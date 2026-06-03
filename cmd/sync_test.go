package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func TestRunSendSettingsRefreshSuccess(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "gmcli.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	client := newFakeSettingsClient(&gmproto.Settings{
		SIMCards: []*gmproto.SIMCard{{
			SIMParticipant: &gmproto.SIMParticipant{ID: "sender-1"},
			SIMData: &gmproto.SIMData{
				SIMPayload: &gmproto.SIMPayload{Two: 1, SIMNumber: 1},
			},
		}},
	})

	res, err := runSendSettingsRefresh(ctx, client, st, zerolog.Nop(), time.Second)
	if err != nil {
		t.Fatalf("refresh send settings: %v", err)
	}
	if !res.RequestedRefresh {
		t.Fatalf("expected refresh request")
	}
	if !res.SettingsReceived {
		t.Fatalf("expected settings received")
	}
	if !res.SendReady {
		t.Fatalf("expected send ready")
	}
	if !res.CachedAfter {
		t.Fatalf("expected settings cached after refresh")
	}
	if res.SIMCount != 1 {
		t.Fatalf("sim count: got %d want 1", res.SIMCount)
	}
	if client.requestCalls != 1 {
		t.Fatalf("request calls: got %d want 1", client.requestCalls)
	}

	cached, err := st.LatestPhoneSettings(ctx)
	if err != nil {
		t.Fatalf("latest phone settings: %v", err)
	}
	if cached.SIMCount != 1 {
		t.Fatalf("cached sim count: got %d want 1", cached.SIMCount)
	}
}

func TestRunSendSettingsRefreshTimeout(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "gmcli.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	res, err := runSendSettingsRefresh(ctx, newFakeSettingsClient(nil), st, zerolog.Nop(), time.Millisecond)
	if err != nil {
		t.Fatalf("timeout should be reported in result, got error: %v", err)
	}
	if !res.RequestedRefresh {
		t.Fatalf("expected refresh request")
	}
	if res.SettingsReceived {
		t.Fatalf("did not expect settings received")
	}
	if res.SendReady {
		t.Fatalf("did not expect send ready")
	}
	if len(res.Issues) == 0 {
		t.Fatalf("expected timeout issue")
	}
	if !strings.Contains(strings.Join(res.Issues, "\n"), "no Settings/SIM metadata received") {
		t.Fatalf("unexpected issues: %#v", res.Issues)
	}
}

func TestSendSettingsRefreshJSONShape(t *testing.T) {
	res := sendSettingsRefreshResult{
		RequestedRefresh: true,
		SettingsReceived: true,
		SendReady:        true,
		CachedBefore:     false,
		CachedAfter:      true,
		SIMCount:         1,
		TimeoutSeconds:   60,
	}

	var buf bytes.Buffer
	if err := output.JSON(&buf, res); err != nil {
		t.Fatalf("json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	for _, key := range []string{
		"requested_refresh",
		"settings_received",
		"send_ready",
		"cached_before",
		"cached_after",
		"sim_count",
		"timeout_seconds",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing json key %q in %s", key, buf.String())
		}
	}
}

type fakeSettingsClient struct {
	settings *gmproto.Settings
	ready    chan struct{}
	once     sync.Once

	handlers     []gm.EventHandler
	requestCalls int
}

func newFakeSettingsClient(settings *gmproto.Settings) *fakeSettingsClient {
	return &fakeSettingsClient{
		settings: settings,
		ready:    make(chan struct{}),
	}
}

func (f *fakeSettingsClient) Subscribe(h gm.EventHandler) {
	f.handlers = append(f.handlers, h)
}

func (f *fakeSettingsClient) Connect() error {
	return nil
}

func (f *fakeSettingsClient) Disconnect() {}

func (f *fakeSettingsClient) WaitForReady(context.Context) error {
	return nil
}

func (f *fakeSettingsClient) RequestUpdates() error {
	f.requestCalls++
	if f.settings != nil {
		for _, h := range f.handlers {
			h(f.settings)
		}
		f.once.Do(func() { close(f.ready) })
	}
	return nil
}

func (f *fakeSettingsClient) WaitForSettings(ctx context.Context) error {
	select {
	case <-f.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
