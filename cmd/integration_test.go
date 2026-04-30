package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fdsouvenir/gmcli/cmd"
	"github.com/fdsouvenir/gmcli/internal/store"
)

// seedStore creates a fresh gmcli store directory with a known dataset and
// returns the directory path.
func seedStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "gmcli.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	must(st.UpsertContact(ctx, store.Contact{
		ParticipantID: "p_alice", Name: "Alice Example", E164: "+15555550100",
		FormattedNumber: "(555) 555-0100",
	}))
	must(st.UpsertContact(ctx, store.Contact{
		ParticipantID: "p_bob", Name: "Bob Sample", E164: "+15555550200",
		FormattedNumber: "(555) 555-0200",
	}))
	must(st.UpsertContact(ctx, store.Contact{
		ParticipantID: "p_me", Name: "Me", IsMe: true,
	}))

	now := time.Now().UnixMilli()
	must(st.UpsertConversation(ctx, store.Conversation{
		ID: "c_alice", Name: "Alice", IsGroup: false,
		ParticipantsJSON:  `[{"id":"p_alice","name":"Alice Example","is_me":false,"formatted_number":"(555) 555-0100"},{"id":"p_me","name":"Me","is_me":true}]`,
		LastMessageTimeMS: now,
		Unread:            true,
	}))
	must(st.UpsertConversation(ctx, store.Conversation{
		ID: "c_bob", Name: "Bob", IsGroup: false,
		ParticipantsJSON:  `[{"id":"p_bob","name":"Bob Sample","is_me":false}]`,
		LastMessageTimeMS: now - 60_000,
	}))

	body := func(s string) *string { return &s }
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m1", ConversationID: "c_alice", SenderID: "p_alice",
		Body: body("Want to grab dinner tonight?"), TimestampMS: now - 4000,
	}))
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m2", ConversationID: "c_alice", SenderID: "p_me",
		Body: body("Sure, dinner sounds great"), TimestampMS: now - 3000, IsFromMe: true,
	}))
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m3", ConversationID: "c_alice", SenderID: "p_alice",
		Body: body("How about 7pm at the usual place"), TimestampMS: now - 2000,
		DecryptionKey: []byte{1, 2, 3},
	}))
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m4", ConversationID: "c_alice", SenderID: "p_me",
		Body: body("See you then"), TimestampMS: now - 1000, IsFromMe: true,
	}))
	must(st.UpsertMessage(ctx, store.Message{
		ID: "mb1", ConversationID: "c_bob", SenderID: "p_bob",
		Body: body("Hey, are we still on for tomorrow?"), TimestampMS: now - 60_000,
	}))
	return dir
}

// runCmd drives cmd.Root with the given args and a fresh stdout buffer.
// Returns trimmed stdout.
func runCmd(t *testing.T, store string, args ...string) string {
	t.Helper()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	root := cmd.Root()
	full := append([]string{"--store", store}, args...)
	root.SetArgs(full)

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	if err := <-errCh; err != nil {
		_ = w.Close()
		t.Fatalf("execute %v: %v", full, err)
	}
	_ = w.Close()
	out, _ := readAll(r)
	return strings.TrimSpace(out)
}

func readAll(r *os.File) (string, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r)
	return buf.String(), err
}

func TestChatsListHumanAndJSON(t *testing.T) {
	dir := seedStore(t)
	human := runCmd(t, dir, "chats", "list")
	if !strings.Contains(human, "c_alice") || !strings.Contains(human, "c_bob") {
		t.Fatalf("human output missing rows: %q", human)
	}
	if !strings.Contains(human, "Alice Example") {
		t.Fatalf("expected participant name, got: %q", human)
	}

	// JSON path. New Root() per call so flags reset.
	jsonOut := runCmd(t, dir, "--json", "chats", "list")
	var got []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, jsonOut)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(got))
	}
	if _, ok := got[0]["conversation_id"]; !ok {
		t.Fatalf("json output missing snake_case conversation_id: %#v", got[0])
	}
}

func TestChatsShowDisplaysMessages(t *testing.T) {
	dir := seedStore(t)
	out := runCmd(t, dir, "chats", "show", "c_alice")
	if !strings.Contains(out, "dinner sounds great") {
		t.Fatalf("expected message body, got: %q", out)
	}
	if !strings.Contains(out, "Alice Example") {
		t.Fatalf("expected participant, got: %q", out)
	}
}

func TestChatsShowLimitUsesMostRecentMessages(t *testing.T) {
	dir := seedStore(t)
	out := runCmd(t, dir, "chats", "show", "c_alice", "--limit", "2")
	if strings.Contains(out, "Want to grab dinner tonight?") {
		t.Fatalf("limit should not include oldest message: %q", out)
	}
	if !strings.Contains(out, "How about 7pm at the usual place") || !strings.Contains(out, "See you then") {
		t.Fatalf("limit should include two most recent messages: %q", out)
	}
}

func TestMessagesSearchFindsMatches(t *testing.T) {
	dir := seedStore(t)
	out := runCmd(t, dir, "messages", "search", "dinner")
	// Snippet should highlight the match with brackets
	if !strings.Contains(out, "[dinner]") && !strings.Contains(out, "dinner") {
		t.Fatalf("expected match in: %q", out)
	}
	if !strings.Contains(out, "m1") || !strings.Contains(out, "m2") {
		t.Fatalf("expected message ids m1,m2 in: %q", out)
	}
}

func TestMessagesShowAndContext(t *testing.T) {
	dir := seedStore(t)
	out := runCmd(t, dir, "messages", "show", "m3")
	if !strings.Contains(out, "How about 7pm at the usual place") {
		t.Fatalf("expected body in: %q", out)
	}

	jsonOut := runCmd(t, dir, "--json", "messages", "show", "m3")
	if strings.Contains(jsonOut, "DecryptionKey") || strings.Contains(jsonOut, "decryption_key") ||
		strings.Contains(jsonOut, "RawProto") || strings.Contains(jsonOut, "raw_proto") {
		t.Fatalf("json leaked internal message fields: %q", jsonOut)
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &msg); err != nil {
		t.Fatalf("json: %v\n%s", err, jsonOut)
	}
	if _, ok := msg["message_id"]; !ok {
		t.Fatalf("json output missing snake_case message_id: %#v", msg)
	}

	ctx := runCmd(t, dir, "messages", "context", "m3", "--before", "1", "--after", "1")
	// before=1 + anchor + after=1 = 3 messages: m2, m3, m4
	for _, want := range []string{"m2", "m3", "m4"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("expected %s in context: %q", want, ctx)
		}
	}
}

func TestContactsSearchAndShow(t *testing.T) {
	dir := seedStore(t)
	hits := runCmd(t, dir, "contacts", "search", "alice")
	if !strings.Contains(hits, "Alice Example") || !strings.Contains(hits, "p_alice") {
		t.Fatalf("expected alice match: %q", hits)
	}

	// Lookup by participant id
	byID := runCmd(t, dir, "contacts", "show", "p_bob")
	if !strings.Contains(byID, "Bob Sample") {
		t.Fatalf("expected Bob: %q", byID)
	}

	// Lookup by phone number
	byNum := runCmd(t, dir, "contacts", "show", "+15555550100")
	if !strings.Contains(byNum, "Alice Example") {
		t.Fatalf("expected Alice via number: %q", byNum)
	}
}

func TestMessagesListFilters(t *testing.T) {
	dir := seedStore(t)
	out := runCmd(t, dir, "--json", "messages", "list", "--conv", "c_alice", "--limit", "10")
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(out), &msgs); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages in c_alice, got %d", len(msgs))
	}
}
