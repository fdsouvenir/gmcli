package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/fdsouvenir/gmcli/internal/store"
)

// authStatusTimeout bounds the optional online check. A healthy phone answers
// in seconds; libgm's own pairing timeout is far longer than anyone wants to
// wait for a status command.
const authStatusTimeout = 45 * time.Second

// authStatusReport is the output of gmcli auth status. Fields mirror
// gm.SessionInfo and add the verdict and the optional online check.
type authStatusReport struct {
	gm.SessionInfo
	SessionAgeSeconds int64             `json:"session_age_seconds,omitempty"`
	Verdict           string            `json:"verdict"`
	Code              string            `json:"code,omitempty"`
	Message           string            `json:"message,omitempty"`
	Online            *authOnlineResult `json:"online,omitempty"`
}

type authOnlineResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Verdicts reported by auth status.
const (
	verdictPaired     = "paired"
	verdictUnpaired   = "unpaired"
	verdictUnreadable = "unreadable"
)

type sessionValidator func(context.Context, paths.Layout, zerolog.Logger) error
type sessionRemover func(context.Context, paths.Layout, zerolog.Logger, bool) (*gm.RemoveResult, error)

func authStatusCmd() *cobra.Command {
	return authStatusCmdWithValidator(gm.ValidateSession)
}

func authStatusCmdWithValidator(validate sessionValidator) *cobra.Command {
	var online bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Report the saved pairing without opening the archive",
		Long: "Read session.json and report whether a paired Google Messages session is saved, " +
			"which account or phone it belongs to, and when it was last written. This is offline " +
			"and does not open or migrate the SQLite store. With --online, also connect and " +
			"require a response from the paired phone, so a logged-out account or an unreachable " +
			"phone is reported with a stable code. Exit status is 0 when the session is paired " +
			"(and, with --online, the phone answered), otherwise 1.",
		Example: "  gmcli auth status\n  gmcli --json auth status\n  gmcli --json auth status --online",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signalContext(cmd.Context())
			defer cancel()
			layout, err := paths.Resolve(flags.storeDir)
			if err != nil {
				return err
			}
			report := buildAuthStatus(ctx, layout, newLogger(), online, validate)
			if flags.jsonOut {
				if err := writeJSONLine(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			} else {
				renderAuthStatus(cmd.OutOrStdout(), report)
			}
			return report.err()
		},
	}
	c.Flags().BoolVar(&online, "online", false, "also connect and require a response from the paired phone")
	return c
}

func buildAuthStatus(ctx context.Context, layout paths.Layout, logger zerolog.Logger, online bool, validate sessionValidator) authStatusReport {
	info, err := gm.InspectSession(layout)
	report := authStatusReport{SessionInfo: info}
	if !info.UpdatedAt.IsZero() {
		report.SessionAgeSeconds = int64(time.Since(info.UpdatedAt).Seconds())
	}
	if err != nil {
		report.Code = errorCode(err)
		report.Message = err.Error()
		if report.Code == gm.CodeSessionUnreadable {
			report.Verdict = verdictUnreadable
		} else {
			report.Verdict = verdictUnpaired
		}
		return report
	}
	report.Verdict = verdictPaired
	if !online {
		return report
	}
	checkCtx, cancel := context.WithTimeout(ctx, authStatusTimeout)
	defer cancel()
	result := &authOnlineResult{OK: true}
	if err := validate(checkCtx, layout, logger); err != nil {
		result.OK = false
		result.Code = errorCode(err)
		result.Message = err.Error()
	}
	report.Online = result
	return report
}

// err converts the report into the command's exit status.
func (r authStatusReport) err() error {
	if r.Verdict != verdictPaired {
		return errors.New(r.Message)
	}
	if r.Online != nil && !r.Online.OK {
		return errors.New(r.Online.Message)
	}
	return nil
}

func renderAuthStatus(w io.Writer, r authStatusReport) {
	fmt.Fprintf(w, "session:   %s\n", r.Path)
	fmt.Fprintf(w, "verdict:   %s\n", r.Verdict)
	if r.Mode != "" {
		fmt.Fprintf(w, "mode:      %s\n", r.Mode)
	}
	if r.Account != "" {
		fmt.Fprintf(w, "account:   %s\n", r.Account)
	}
	if r.PhoneID != "" {
		fmt.Fprintf(w, "phone id:  %s\n", r.PhoneID)
	}
	if !r.UpdatedAt.IsZero() {
		fmt.Fprintf(w, "updated:   %s (%s ago)\n", r.UpdatedAt.Local().Format(time.RFC3339), (time.Duration(r.SessionAgeSeconds) * time.Second).String())
	}
	if r.Message != "" {
		fmt.Fprintf(w, "problem:   %s [%s]\n", r.Message, r.Code)
	}
	if r.Online != nil {
		if r.Online.OK {
			fmt.Fprintln(w, "online:    phone answered")
		} else {
			fmt.Fprintf(w, "online:    failed: %s [%s]\n", r.Online.Message, r.Online.Code)
		}
	}
}

func authRemoveCmd() *cobra.Command {
	return authRemoveCmdWithRemover(gm.RemoveSession)
}

func authRemoveCmdWithRemover(remove sessionRemover) *cobra.Command {
	var localOnly bool
	c := &cobra.Command{
		Use:   "remove",
		Short: "Sign out: unpair the phone and delete the saved session",
		Long: "Ask the paired phone to drop this device, then delete session.json and confirm it is gone. " +
			"The local archive is kept. If the phone does not acknowledge, session.json is left in place " +
			"and the error carries code unpair_failed; pass --local-only to delete the session without " +
			"contacting the phone, for example when the pairing was already revoked from the phone. " +
			"With --json, the result or error is one JSON object.",
		Example: "  gmcli auth remove\n  gmcli auth remove --local-only\n  gmcli --json auth remove",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signalContext(cmd.Context())
			defer cancel()
			layout, err := paths.Resolve(flags.storeDir)
			if err != nil {
				return err
			}
			res, err := remove(ctx, layout, newLogger(), localOnly)
			if err != nil {
				if flags.jsonOut {
					_ = writeJSONLine(cmd.OutOrStdout(), authEvent{Event: authEventError, Code: errorCode(err), Message: err.Error()})
				}
				return err
			}
			// A removed pairing invalidates any recorded connection health, but
			// only when an archive already exists; never create one here.
			if _, statErr := os.Stat(layout.Database); statErr == nil {
				if err := invalidateRemovedPairingHealth(ctx, layout); err != nil {
					return err
				}
			}
			if flags.jsonOut {
				return writeJSONLine(cmd.OutOrStdout(), struct {
					Event string `json:"event"`
					*gm.RemoveResult
				}{Event: "removed", RemoveResult: res})
			}
			who := res.Account
			if who == "" {
				who = res.PhoneID
			}
			switch {
			case res.PhoneUnpaired:
				fmt.Fprintf(cmd.OutOrStdout(), "Unpaired the phone and removed the session for %s. The local archive is kept.\n", who)
			case who != "":
				fmt.Fprintf(cmd.OutOrStdout(), "Removed the session for %s without contacting the phone. Also remove this device under Google Messages > Device pairing. The local archive is kept.\n", who)
			default:
				fmt.Fprintln(cmd.OutOrStdout(), "Removed the unpaired session file. The local archive is kept.")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&localOnly, "local-only", false, "delete session.json without asking the phone to unpair")
	return c
}

func invalidateRemovedPairingHealth(ctx context.Context, layout paths.Layout) error {
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.ObserveHealth(ctx, "session_changed")
}

func writeJSONLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
