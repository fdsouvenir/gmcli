package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHistoryBackfillResultJSONIsUnambiguous(t *testing.T) {
	res := historyBackfillResult{
		ConversationID:       "198",
		Requests:             2,
		Count:                100,
		FetchedMessages:      150,
		SyncRecordsProcessed: 150,
		MessagesBefore:       301,
		MessagesAfter:        325,
		MessagesAddedForChat: 24,
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(raw)
	for _, want := range []string{
		`"conversation_id":"198"`,
		`"fetched_messages":150`,
		`"sync_records_processed":150`,
		`"messages_before":301`,
		`"messages_after":325`,
		`"messages_added_for_chat":24`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, `"imported"`) {
		t.Fatalf("json should not expose ambiguous imported field: %s", out)
	}
}
