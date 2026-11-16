package gm

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func TestInspectSessionStates(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		layout := testLayout(t)
		info, err := InspectSession(layout)
		if codeOf(err) != CodeNoSession || info.Exists || info.Paired {
			t.Fatalf("info=%+v err=%v", info, err)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		layout := testLayout(t)
		if err := os.WriteFile(layout.Session, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := InspectSession(layout)
		if codeOf(err) != CodeSessionUnreadable || !info.Exists || info.Paired {
			t.Fatalf("info=%+v err=%v", info, err)
		}
	})
	t.Run("not paired", func(t *testing.T) {
		layout := testLayout(t)
		if err := saveAuth(layout.Session, libgm.NewAuthData()); err != nil {
			t.Fatal(err)
		}
		info, err := InspectSession(layout)
		if codeOf(err) != CodeNotPaired || !info.Exists || info.Paired {
			t.Fatalf("info=%+v err=%v", info, err)
		}
	})
	t.Run("gaia", func(t *testing.T) {
		layout := testLayout(t)
		if err := saveAuth(layout.Session, testGaiaAuth("person@example.com")); err != nil {
			t.Fatal(err)
		}
		info, err := InspectSession(layout)
		if err != nil || !info.Paired || info.Mode != ModeGaia || info.Account != "person@example.com" || info.PhoneID != "" {
			t.Fatalf("info=%+v err=%v", info, err)
		}
		if info.UpdatedAt.IsZero() {
			t.Fatal("missing session timestamp")
		}
	})
	t.Run("legacy qr", func(t *testing.T) {
		layout := testLayout(t)
		auth := libgm.NewAuthData()
		auth.Browser = &gmproto.Device{SourceID: "browser"}
		auth.Mobile = &gmproto.Device{SourceID: "phone-1"}
		if err := saveAuth(layout.Session, auth); err != nil {
			t.Fatal(err)
		}
		info, err := InspectSession(layout)
		if err != nil || info.Mode != ModeLegacyQR || info.PhoneID != "phone-1" || info.Account != "" {
			t.Fatalf("info=%+v err=%v", info, err)
		}
	})
}

func TestValidateSessionKeepsSpecificCode(t *testing.T) {
	layout := testLayout(t)
	if err := saveAuth(layout.Session, testGaiaAuth("person@example.com")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(layout.Session)
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"ok", nil, ""},
		{"logged out", pairErrorf(CodeSessionLoggedOut, "logged out"), CodeSessionLoggedOut},
		{"phone", pairErrorf(CodePhoneUnreachable, "no phone"), CodePhoneUnreachable},
		{"other", errors.New("relay said no"), CodePairingInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeGaiaClient{connectErr: tc.err}
			var gotAuth *libgm.AuthData
			err := validateSession(context.Background(), layout, zerolog.Nop(), fakeGaiaFactory(fake, &gotAuth))
			if codeOf(err) != tc.code || (tc.err == nil) != (err == nil) {
				t.Fatalf("err=%v code=%q want %q", err, codeOf(err), tc.code)
			}
			if !fake.connected || !fake.disconnected {
				t.Fatalf("lifecycle: %+v", fake)
			}
		})
	}
	after, _ := os.ReadFile(layout.Session)
	if string(after) != string(before) {
		t.Fatal("validation rewrote session.json")
	}
	if err := validateSession(context.Background(), testLayout(t), zerolog.Nop(), nil); codeOf(err) != CodeNoSession {
		t.Fatalf("missing session: %v", err)
	}
}

func TestRemoveSession(t *testing.T) {
	t.Run("unpairs the phone then deletes", func(t *testing.T) {
		layout := testLayout(t)
		if err := saveAuth(layout.Session, testGaiaAuth("person@example.com")); err != nil {
			t.Fatal(err)
		}
		fake := &fakeGaiaClient{}
		var gotAuth *libgm.AuthData
		res, err := removeSession(context.Background(), layout, zerolog.Nop(), false, fakeGaiaFactory(fake, &gotAuth))
		if err != nil {
			t.Fatal(err)
		}
		if !fake.connected || !fake.unpaired || !fake.disconnected {
			t.Fatalf("lifecycle: %+v", fake)
		}
		if !res.PhoneUnpaired || res.Account != "person@example.com" || res.Mode != ModeGaia {
			t.Fatalf("result: %+v", res)
		}
		if _, err := os.Stat(layout.Session); !os.IsNotExist(err) {
			t.Fatal("session.json still present")
		}
	})
	t.Run("keeps the session when the phone does not acknowledge", func(t *testing.T) {
		layout := testLayout(t)
		if err := saveAuth(layout.Session, testGaiaAuth("person@example.com")); err != nil {
			t.Fatal(err)
		}
		fake := &fakeGaiaClient{unpairErr: errors.New("relay rejected")}
		var gotAuth *libgm.AuthData
		_, err := removeSession(context.Background(), layout, zerolog.Nop(), false, fakeGaiaFactory(fake, &gotAuth))
		if codeOf(err) != CodeUnpairFailed || !errors.Is(err, fake.unpairErr) {
			t.Fatalf("err=%v", err)
		}
		if _, statErr := os.Stat(layout.Session); statErr != nil {
			t.Fatal("failed unpair deleted session.json")
		}
		if !fake.disconnected {
			t.Fatal("client left connected")
		}
	})
	t.Run("connect failure is unpair_failed unless more specific", func(t *testing.T) {
		layout := testLayout(t)
		if err := saveAuth(layout.Session, testGaiaAuth("person@example.com")); err != nil {
			t.Fatal(err)
		}
		fake := &fakeGaiaClient{connectErr: errors.New("no auth token")}
		var gotAuth *libgm.AuthData
		_, err := removeSession(context.Background(), layout, zerolog.Nop(), false, fakeGaiaFactory(fake, &gotAuth))
		if codeOf(err) != CodeUnpairFailed || fake.unpaired {
			t.Fatalf("err=%v unpaired=%v", err, fake.unpaired)
		}
	})
	t.Run("local only never touches the network", func(t *testing.T) {
		layout := testLayout(t)
		if err := saveAuth(layout.Session, testGaiaAuth("person@example.com")); err != nil {
			t.Fatal(err)
		}
		res, err := removeSession(context.Background(), layout, zerolog.Nop(), true, func(*libgm.AuthData, zerolog.Logger) gaiaPairingClient {
			t.Fatal("client constructed in local-only mode")
			return nil
		})
		if err != nil || res.PhoneUnpaired {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		if _, err := os.Stat(layout.Session); !os.IsNotExist(err) {
			t.Fatal("session.json still present")
		}
	})
	t.Run("unpaired session is deleted without the phone", func(t *testing.T) {
		layout := testLayout(t)
		if err := saveAuth(layout.Session, libgm.NewAuthData()); err != nil {
			t.Fatal(err)
		}
		res, err := removeSession(context.Background(), layout, zerolog.Nop(), false, func(*libgm.AuthData, zerolog.Logger) gaiaPairingClient {
			t.Fatal("client constructed for an unpaired session")
			return nil
		})
		if err != nil || res.PhoneUnpaired {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})
	t.Run("missing session is a coded error", func(t *testing.T) {
		_, err := removeSession(context.Background(), testLayout(t), zerolog.Nop(), true, nil)
		if codeOf(err) != CodeNoSession {
			t.Fatalf("err=%v", err)
		}
	})
}
