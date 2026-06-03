package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"

	"github.com/fdsouvenir/gmcli/internal/store"
)

func TestPumpPersistsPhoneSettings(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "gmcli.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	settings := testSettings("sender-1")
	New(st, zerolog.Nop()).Handle(settings)

	got, err := st.LatestPhoneSettings(ctx)
	if err != nil {
		t.Fatalf("latest phone settings: %v", err)
	}
	if got.SIMCount != 1 {
		t.Fatalf("sim count: got %d want 1", got.SIMCount)
	}
	decoded := &gmproto.Settings{}
	if err := proto.Unmarshal(got.RawProto, decoded); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !proto.Equal(decoded, settings) {
		t.Fatalf("settings did not round trip")
	}
}

func testSettings(participantID string) *gmproto.Settings {
	return &gmproto.Settings{
		SIMCards: []*gmproto.SIMCard{{
			SIMParticipant: &gmproto.SIMParticipant{ID: participantID},
			SIMData: &gmproto.SIMData{
				SIMPayload: &gmproto.SIMPayload{Two: 1, SIMNumber: 1},
			},
		}},
	}
}
