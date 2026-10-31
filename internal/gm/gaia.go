package gm

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"

	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// ParseGaiaCookies accepts a browser Cookie header, never returning its contents
// in errors. This is the cookie contract used by the pinned upstream connector.
func ParseGaiaCookies(input string) (map[string]string, error) {
	cookies := map[string]string{}
	allowed := map[string]bool{"SID": true, "HSID": true, "OSID": true, "SSID": true, "APISID": true, "SAPISID": true, "__Secure-1PSIDTS": true}
	for _, part := range strings.Split(input, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.ContainsAny(value, "\r\n\x00") {
			return nil, errors.New("invalid cookie header")
		}
		if allowed[name] {
			for _, b := range []byte(value) {
				if b < 0x21 || b > 0x7e || b == '"' || b == '\\' {
					return nil, errors.New("invalid cookie value")
				}
			}
			cookies[name] = value
		}
	}
	for _, name := range []string{"SID", "HSID", "OSID", "SSID", "APISID", "SAPISID"} {
		if cookies[name] == "" {
			return nil, fmt.Errorf("missing required cookie %s", name)
		}
	}
	return cookies, nil
}

type gaiaPairer interface {
	StartGaiaPairing(context.Context) (string, *libgm.PairingSession, error)
	FinishGaiaPairing(context.Context, *libgm.PairingSession) (string, error)
	Disconnect()
}

// PairGaia runs upstream's supported two-phase flow without DoGaiaPairing's
// automatic reconnect. render must target only a private human-owned terminal.
// All upstream logging and error payloads are suppressed across this boundary.
func PairGaia(ctx context.Context, layout paths.Layout, cookies map[string]string, render func(string)) (*PairResult, error) {
	auth := libgm.NewAuthData()
	auth.SetCookies(cookies)
	quiet := zerolog.Nop()
	cli := libgm.NewClient(auth, nil, quiet)
	ctx = quiet.WithContext(ctx)
	return pairGaia(ctx, layout, auth, cli, render)
}

func pairGaia(ctx context.Context, layout paths.Layout, auth *libgm.AuthData, cli gaiaPairer, render func(string)) (*PairResult, error) {
	ctx, cancel := context.WithTimeout(ctx, PairTimeout)
	defer cancel()
	defer cli.Disconnect()
	emoji, ps, err := cli.StartGaiaPairing(ctx)
	if err != nil {
		return nil, errors.New("Google-account pairing could not start; check browser sign-in, cookies and phone availability")
	}
	render(emoji)
	phone, err := cli.FinishGaiaPairing(ctx, ps)
	if err != nil {
		return nil, errors.New("Google-account pairing was not completed; retry in the private terminal")
	}
	if auth.Browser == nil || auth.PairingID == uuid.Nil {
		return nil, errors.New("pairing returned incomplete session data; existing session preserved")
	}
	if err := layout.EnsureDirs(); err != nil {
		return nil, err
	}
	if err := saveAuth(layout.Session, auth); err != nil {
		return nil, errors.New("could not persist paired session")
	}
	return &PairResult{PhoneID: phone, SessionPath: layout.Session}, nil
}
