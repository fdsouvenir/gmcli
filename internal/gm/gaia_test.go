package gm

import (
	"context"
	"errors"
	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"os"
	"strings"
	"testing"
)

func TestParseGaiaCookies(t *testing.T) {
	const valid = "SID=a; HSID=b; OSID=c; SSID=d; APISID=e; SAPISID=f; unrelated=discard"
	cookies, err := ParseGaiaCookies(valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 6 {
		t.Fatal("unneeded cookies retained")
	}
	for _, input := range []string{"SECRET", "SID=SECRET", valid + "\nSECRET"} {
		_, err := ParseGaiaCookies(input)
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unsafe validation error: %v", err)
		}
	}
}

type fakeGaia struct {
	startErr, finishErr error
	stopped             bool
	deadline            bool
}

func (f *fakeGaia) StartGaiaPairing(ctx context.Context) (string, *libgm.PairingSession, error) {
	_, f.deadline = ctx.Deadline()
	return "test-emoji", nil, f.startErr
}
func (f *fakeGaia) FinishGaiaPairing(context.Context, *libgm.PairingSession) (string, error) {
	return "test-phone", f.finishErr
}
func (f *fakeGaia) Disconnect() { f.stopped = true }
func TestGaiaPairingPersistenceAndSanitization(t *testing.T) {
	for _, mode := range []string{"start-fail", "finish-fail", "incomplete", "success"} {
		t.Run(mode, func(t *testing.T) {
			layout, _ := paths.Resolve(t.TempDir())
			if err := os.WriteFile(layout.Session, []byte("prior-session"), 0600); err != nil {
				t.Fatal(err)
			}
			f := &fakeGaia{}
			auth := libgm.NewAuthData()
			if mode == "start-fail" {
				f.startErr = errors.New("SECRET")
			}
			if mode == "finish-fail" {
				f.finishErr = errors.New("SECRET")
			}
			if mode == "success" {
				auth.Browser = &gmproto.Device{}
				auth.PairingID = uuid.New()
			}
			rendered := false
			_, err := pairGaia(context.Background(), layout, auth, f, func(string) { rendered = true })
			if !f.stopped || !f.deadline {
				t.Fatal("missing cleanup or deadline")
			}
			if mode == "start-fail" && rendered {
				t.Fatal("rendered before successful start")
			}
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(layout.Session)
				if info.Mode().Perm() != 0600 {
					t.Fatal("unsafe session permissions")
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "SECRET") {
					t.Fatalf("unsafe error: %v", err)
				}
				data, _ := os.ReadFile(layout.Session)
				if string(data) != "prior-session" {
					t.Fatal("failed pairing replaced existing session")
				}
			}
		})
	}
}
