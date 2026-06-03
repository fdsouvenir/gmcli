package cmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func TestSeedCachedSendSettings(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "gmcli.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	settings := &gmproto.Settings{
		SIMCards: []*gmproto.SIMCard{{
			SIMParticipant: &gmproto.SIMParticipant{ID: "sender-1"},
			SIMData: &gmproto.SIMData{
				SIMPayload: &gmproto.SIMPayload{Two: 1, SIMNumber: 1},
			},
		}},
	}
	raw, err := proto.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := st.SavePhoneSettings(ctx, raw, len(settings.GetSIMCards())); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	client := &gm.Client{}
	seeded, err := seedCachedSendSettings(ctx, client, st)
	if err != nil {
		t.Fatalf("seed cached settings: %v", err)
	}
	if !seeded {
		t.Fatalf("expected cached settings to seed client")
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if err := client.WaitForSettings(waitCtx); err != nil {
		t.Fatalf("wait for settings after seed: %v", err)
	}
}

func TestSeedCachedSendSettingsMissing(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "gmcli.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seeded, err := seedCachedSendSettings(ctx, &gm.Client{}, st)
	if err != nil {
		t.Fatalf("seed missing settings: %v", err)
	}
	if seeded {
		t.Fatalf("expected missing settings not to seed client")
	}
}

func TestSendTextResultJSONIncludesSendMode(t *testing.T) {
	got := sendTextResultJSON(&gm.SendTextResult{
		MessageID:      "msg-1",
		ConversationID: "conv-1",
		TmpID:          "tmp-1",
		SendMode:       gm.SendModeLegacy,
	})
	if got["sent"] != true {
		t.Fatalf("sent: got %#v want true", got["sent"])
	}
	if got["send_mode"] != gm.SendModeLegacy {
		t.Fatalf("send mode: got %#v want %q", got["send_mode"], gm.SendModeLegacy)
	}
}
