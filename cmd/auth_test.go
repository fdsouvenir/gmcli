package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func TestPairingInvalidatesPriorHealthWithoutNetwork(t *testing.T) {
	ctx := context.Background()
	layout, err := paths.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, kind := range []string{"starting", "data"} {
		if err := st.ObserveHealth(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := invalidatePairingHealth(ctx, layout); err != nil {
		t.Fatal(err)
	}
	h, err := st.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := h.Assessment(time.Now()); status != "unhealthy" || h.Invalidation != "session_changed" {
		t.Fatalf("prior health retained: %+v", h)
	}
}
