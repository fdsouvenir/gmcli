package sync

import (
	"context"
	"errors"
	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/store"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"path/filepath"
	"testing"
	"time"
)

func TestHealthEventLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := New(st, zerolog.Nop())
	for _, evt := range []any{&gm.ConnectionStarting{}, &gm.TransportRequested{}, &events.AuthTokenRefreshed{}} {
		p.Handle(evt)
	}
	h, _ := st.Health(ctx)
	if h.PhoneResponse != 0 {
		t.Fatal("relay auth fabricated phone response")
	}
	if status, _ := h.Assessment(time.Now()); status != "unknown" {
		t.Fatal(status)
	}
	p.Handle(&gmproto.Settings{})
	h, _ = st.Health(ctx)
	if status, _ := h.Assessment(time.Now()); status != "unknown" {
		t.Fatal("settings alone verified archive")
	}
	p.Handle(&gmproto.Conversation{ConversationID: "test-conversation"})
	h, _ = st.Health(ctx)
	if status, _ := h.Assessment(time.Now()); status != "recently_verified" {
		t.Fatal(status)
	}
	p.Handle(&events.PhoneNotResponding{})
	h, _ = st.Health(ctx)
	if status, _ := h.Assessment(time.Now()); status != "unhealthy" {
		t.Fatal(status)
	}
	p.Handle(&events.PhoneRespondingAgain{})
	p.Handle(&gmproto.Conversation{ConversationID: "test-conversation"})
	h, _ = st.Health(ctx)
	if status, _ := h.Assessment(time.Now()); status != "recently_verified" {
		t.Fatal(status)
	}
	p.Handle(&events.ListenTemporaryError{})
	p.Handle(&events.ListenRecovered{})
	h, _ = st.Health(ctx)
	if status, _ := h.Assessment(time.Now()); status != "unknown" {
		t.Fatal("transport recovery reused old phone state")
	}
	p.Handle(&events.GaiaLoggedOut{})
	p.Handle(&gm.ConnectionStopped{})
	// Late evidence and shutdown must not erase a terminal auth failure.
	p.Handle(&events.PhoneRespondingAgain{})
	h, _ = st.Health(ctx)
	if h.Invalidation != "auth_invalid" {
		t.Fatalf("lost auth invalidation: %+v", h)
	}
	if status, _ := h.Assessment(time.Now()); status != "unhealthy" {
		t.Fatal(status)
	}
	select {
	case <-p.Fatal():
	default:
		t.Fatal("missing fatal notification")
	}
	p.Handle(&gm.ConnectionStarting{})
	p.Handle(&events.ListenFatalError{Error: errors.New("test")})
	h, _ = st.Health(ctx)
	if h.Invalidation != "fatal" || h.PhoneResponse != 0 {
		t.Fatal(h)
	}
}

func TestHealthInvalidEventsCannotVerifyData(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := New(st, zerolog.Nop())
	p.Handle(&gm.ConnectionStarting{})
	for _, evt := range []any{nil, (*events.ClientReady)(nil), (*events.PhoneRespondingAgain)(nil), (*events.ListenFatalError)(nil), (*gmproto.Conversation)(nil), &gmproto.Conversation{}, (*libgm.WrappedMessage)(nil), &libgm.WrappedMessage{}, &libgm.WrappedMessage{Message: &gmproto.Message{MessageID: "no-conversation"}}, (*gmproto.Settings)(nil)} {
		p.Handle(evt)
	}
	h, _ := st.Health(ctx)
	if h.DataEvidence != 0 || h.PhoneResponse != 0 {
		t.Fatalf("invalid event manufactured proof: %+v", h)
	}
}
