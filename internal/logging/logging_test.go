package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestLibGMPayloadWarningsAreSuppressed(t *testing.T) {
	var buf bytes.Buffer
	logger, err := New(&buf, "trace", true)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	logger.Warn().
		Str("evt_data", "private-payload").
		Str("decrypted_data", "private-decrypted-payload").
		Msg("Got unknown event type")
	if buf.Len() != 0 {
		t.Fatalf("expected upstream payload warning to be suppressed, got %q", buf.String())
	}

	logger.Warn().Msg("ordinary warning")
	if !strings.Contains(buf.String(), "ordinary warning") {
		t.Fatalf("expected ordinary warning to be logged, got %q", buf.String())
	}
}
