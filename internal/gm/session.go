package gm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/fdsouvenir/gmcli/internal/paths"
)

// Session error codes used by InspectSession, ValidateSession and
// RemoveSession.
const (
	CodeNoSession         = "no_session"         // session.json does not exist
	CodeSessionUnreadable = "session_unreadable" // session.json exists but cannot be parsed
	CodeNotPaired         = "not_paired"         // session.json has no paired device
	CodeSessionLoggedOut  = "session_logged_out" // Google reports the account session is gone
	CodeUnpairFailed      = "unpair_failed"      // the phone did not acknowledge the unpair
)

// Pairing modes reported by InspectSession.
const (
	ModeGaia     = "gaia"
	ModeLegacyQR = "legacy_qr"
)

// SessionInfo describes session.json without any network access.
type SessionInfo struct {
	Path      string    `json:"session_path"`
	Exists    bool      `json:"session_exists"`
	Paired    bool      `json:"paired"`
	Mode      string    `json:"pairing_mode,omitempty"`
	Account   string    `json:"account,omitempty"`
	PhoneID   string    `json:"phone_id,omitempty"`
	UpdatedAt time.Time `json:"session_updated_at,omitzero"`
}

// InspectSession reads session.json and reports what it contains. A missing
// or unreadable file is returned as a coded error alongside the partial info.
func InspectSession(layout paths.Layout) (SessionInfo, error) {
	info := SessionInfo{Path: layout.Session}
	stat, err := os.Stat(layout.Session)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return info, pairErrorf(CodeNoSession, "no session at %s; run `gmcli auth` first", layout.Session)
		}
		return info, pairErrorf(CodeSessionUnreadable, "stat session %s: %w", layout.Session, err)
	}
	info.Exists = true
	info.UpdatedAt = stat.ModTime()
	auth, err := loadAuth(layout.Session)
	if err != nil {
		return info, pairErrorf(CodeSessionUnreadable, "%w", err)
	}
	if auth.Browser == nil {
		return info, pairErrorf(CodeNotPaired, "session %s has no paired device; run `gmcli auth`", layout.Session)
	}
	info.Paired = true
	if auth.IsGoogleAccount() {
		info.Mode = ModeGaia
		info.Account = auth.Mobile.GetSourceID()
	} else {
		info.Mode = ModeLegacyQR
		info.PhoneID = auth.Mobile.GetSourceID()
	}
	return info, nil
}

// ValidateSession connects with the saved session and requires a response
// from the paired phone. It never writes session.json. Failures carry a
// PairError code: session_logged_out, phone_unreachable, or pairing_invalid.
func ValidateSession(ctx context.Context, layout paths.Layout, logger zerolog.Logger) error {
	return validateSession(ctx, layout, logger, newGaiaClient)
}

func validateSession(ctx context.Context, layout paths.Layout, logger zerolog.Logger, newClient gaiaClientFactory) error {
	if _, err := InspectSession(layout); err != nil {
		return err
	}
	auth, err := loadAuth(layout.Session)
	if err != nil {
		return pairErrorf(CodeSessionUnreadable, "%w", err)
	}
	cli := newClient(auth, logger)
	defer cli.Disconnect()
	if err := cli.ValidateConnection(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return keepPairCode(CodePairingInvalid, "validate Google Messages pairing: %w", err)
	}
	return nil
}

// RemoveResult reports what RemoveSession did.
type RemoveResult struct {
	SessionPath   string `json:"session_path"`
	Mode          string `json:"pairing_mode,omitempty"`
	Account       string `json:"account,omitempty"`
	PhoneID       string `json:"phone_id,omitempty"`
	PhoneUnpaired bool   `json:"phone_unpaired"`
}

// RemoveSession signs out: unless localOnly, it asks the phone to drop this
// pairing, then deletes session.json and confirms the file is gone. If the
// phone does not acknowledge, session.json is kept and the error carries
// code unpair_failed so the caller can retry or pass localOnly.
func RemoveSession(ctx context.Context, layout paths.Layout, logger zerolog.Logger, localOnly bool) (*RemoveResult, error) {
	return removeSession(ctx, layout, logger, localOnly, newGaiaClient)
}

func removeSession(ctx context.Context, layout paths.Layout, logger zerolog.Logger, localOnly bool, newClient gaiaClientFactory) (*RemoveResult, error) {
	info, err := InspectSession(layout)
	if err != nil && !isPairCode(err, CodeNotPaired) {
		return nil, err
	}
	res := &RemoveResult{SessionPath: layout.Session, Mode: info.Mode, Account: info.Account, PhoneID: info.PhoneID}
	if info.Paired && !localOnly {
		auth, err := loadAuth(layout.Session)
		if err != nil {
			return nil, pairErrorf(CodeSessionUnreadable, "%w", err)
		}
		cli := newClient(auth, logger)
		if err := cli.Connect(); err != nil {
			cli.Disconnect()
			return nil, keepPairCode(CodeUnpairFailed, "connect before unpairing (pass --local-only to delete the session without telling the phone): %w", err)
		}
		err = cli.Unpair(ctx)
		cli.Disconnect()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, keepPairCode(CodeUnpairFailed, "unpair on the phone (pass --local-only to delete the session without telling the phone): %w", err)
		}
		res.PhoneUnpaired = true
	}
	if err := os.Remove(layout.Session); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove session %s: %w", layout.Session, err)
	}
	if _, err := os.Stat(layout.Session); !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("session %s still exists after removal", layout.Session)
	}
	return res, nil
}

// keepPairCode wraps err with a message while preserving an inner PairError
// code when one exists, so the most specific code survives wrapping.
func keepPairCode(fallback string, format string, err error) error {
	var inner *PairError
	code := fallback
	if errors.As(err, &inner) {
		code = inner.code
	}
	return &PairError{code: code, err: fmt.Errorf(format, err)}
}

func isPairCode(err error, code string) bool {
	var pe *PairError
	return errors.As(err, &pe) && pe.code == code
}
