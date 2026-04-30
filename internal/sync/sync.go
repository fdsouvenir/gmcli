// Package sync converts libgm events into store rows. The Pump is meant to
// be registered as an EventHandler on gm.Client. All persistence work runs
// inline on the libgm callback goroutine — SQLite writes against a local
// file are fast enough that this hasn't been measured to be a bottleneck.
// If it ever becomes one, swap Handle for a buffered-channel pump without
// changing the public interface.
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/fdsouvenir/gmcli/internal/store"
)

// Pump owns the conversion of libgm events into store writes.
type Pump struct {
	store    *store.Store
	logger   zerolog.Logger
	platform string
	fatal    chan error
}

// New constructs a Pump that writes into st.
func New(st *store.Store, logger zerolog.Logger) *Pump {
	return &Pump{store: st, logger: logger, platform: "gm", fatal: make(chan error, 1)}
}

// Fatal returns a channel that receives terminal connection/auth errors.
// Long-running commands should select on this and exit non-zero so supervisors
// can restart or alert instead of silently idling after libgm gives up.
func (p *Pump) Fatal() <-chan error {
	return p.fatal
}

// Handle is the entry point registered with gm.Client.Subscribe. It must
// not block; errors are logged but do not propagate.
func (p *Pump) Handle(evt any) {
	ctx := context.Background()
	switch e := evt.(type) {
	case *events.ClientReady:
		p.onClientReady(ctx, e)
	case *events.AuthTokenRefreshed:
		p.touch(ctx)
	case *gmproto.Conversation:
		p.onConversation(ctx, e)
	case *libgm.WrappedMessage:
		p.onMessage(ctx, e)
	case *events.PhoneNotResponding:
		p.logger.Warn().Msg("Phone not responding — messages will queue until it reconnects")
	case *events.PhoneRespondingAgain:
		p.logger.Info().Msg("Phone responding again")
	case *events.GaiaLoggedOut:
		err := fmt.Errorf("phone reports logged-out state; re-run `gmcli auth`")
		p.logger.Error().Err(err).Msg("libgm auth fatal error")
		p.fail(err)
	case *events.ListenFatalError:
		err := fmt.Errorf("libgm listen fatal error: %w", e.Error)
		p.logger.Error().Err(e.Error).Msg("libgm listen fatal error")
		p.fail(err)
	case *events.ListenTemporaryError:
		p.logger.Warn().Err(e.Error).Msg("libgm listen temporary error")
	case *events.ListenRecovered:
		p.logger.Info().Msg("libgm listen recovered")
	case *events.PairSuccessful, *events.BrowserActive:
		// Lifecycle events; nothing to persist beyond the AuthData write
		// the gm wrapper handles.
	default:
		p.logger.Trace().Type("event", evt).Msg("Unhandled libgm event")
	}
}

func (p *Pump) fail(err error) {
	select {
	case p.fatal <- err:
	default:
	}
}

// ImportContacts persists a full contact-list response from libgm.
func (p *Pump) ImportContacts(ctx context.Context, contacts []*gmproto.Contact) int {
	imported := 0
	for _, c := range contacts {
		if c == nil || c.GetParticipantID() == "" {
			continue
		}
		num := c.GetNumber()
		row := store.Contact{
			ParticipantID:   c.GetParticipantID(),
			SourcePlatform:  p.platform,
			ContactID:       c.GetContactID(),
			Name:            c.GetName(),
			E164:            firstNonEmpty(num.GetNumber(), num.GetNumber2()),
			FormattedNumber: num.GetFormattedNumber(),
			AvatarColor:     c.GetAvatarHexColor(),
		}
		if err := p.store.UpsertContact(ctx, row); err != nil {
			p.logger.Error().Err(err).Str("participant_id", row.ParticipantID).Msg("Upsert contact failed")
			continue
		}
		imported++
	}
	return imported
}

// ImportMessages persists message-history rows fetched on demand. These are
// marked old so they don't advance the live-event freshness timestamp.
func (p *Pump) ImportMessages(ctx context.Context, messages []*gmproto.Message) int {
	imported := 0
	for _, m := range messages {
		if m == nil || m.GetMessageID() == "" {
			continue
		}
		p.onMessage(ctx, &libgm.WrappedMessage{Message: m, IsOld: true})
		imported++
	}
	return imported
}

func (p *Pump) onClientReady(ctx context.Context, e *events.ClientReady) {
	p.logger.Info().
		Str("session_id", e.SessionID).
		Int("conversations", len(e.Conversations)).
		Msg("Client ready — applying initial conversation snapshot")
	for _, c := range e.Conversations {
		if c == nil {
			continue
		}
		p.onConversation(ctx, c)
	}
	p.touch(ctx)
}

func (p *Pump) onConversation(ctx context.Context, c *gmproto.Conversation) {
	if c.GetConversationID() == "" {
		return
	}
	row := store.Conversation{
		ID:                c.GetConversationID(),
		SourcePlatform:    p.platform,
		Name:              c.GetName(),
		IsGroup:           c.GetIsGroupChat(),
		ParticipantsJSON:  participantsJSON(c.GetParticipants()),
		LastMessageTimeMS: normalizeTimestampMS(c.GetLastMessageTimestamp()),
		Unread:            c.GetUnread(),
		Pinned:            c.GetPinned(),
	}
	if err := p.store.UpsertConversation(ctx, row); err != nil {
		p.logger.Error().Err(err).Str("conv_id", row.ID).Msg("Upsert conversation failed")
		return
	}
	// Also walk participants and write any embedded Contact-like data.
	for _, part := range c.GetParticipants() {
		if part == nil {
			continue
		}
		p.upsertParticipant(ctx, part)
	}
}

func (p *Pump) upsertParticipant(ctx context.Context, part *gmproto.Participant) {
	pid := part.GetID().GetParticipantID()
	if pid == "" {
		return
	}
	name := part.GetFullName()
	if name == "" {
		name = part.GetFirstName()
	}
	row := store.Contact{
		ParticipantID:   pid,
		SourcePlatform:  p.platform,
		ContactID:       part.GetContactID(),
		Name:            name,
		E164:            part.GetID().GetNumber(),
		FormattedNumber: part.GetFormattedNumber(),
		AvatarColor:     part.GetAvatarHexColor(),
		IsMe:            part.GetIsMe(),
	}
	if err := p.store.UpsertContact(ctx, row); err != nil {
		p.logger.Error().Err(err).Str("participant_id", pid).Msg("Upsert participant failed")
	}
}

func (p *Pump) onMessage(ctx context.Context, w *libgm.WrappedMessage) {
	m := w.Message
	if m == nil || m.GetMessageID() == "" {
		return
	}
	body := messageBody(m)
	media := primaryMedia(m)
	row := store.Message{
		ID:             m.GetMessageID(),
		ConversationID: m.GetConversationID(),
		SourcePlatform: p.platform,
		SenderID:       m.GetParticipantID(),
		TimestampMS:    normalizeTimestampMS(m.GetTimestamp()),
		Status:         int64(m.GetMessageStatus().GetStatus()),
		IsFromMe:       isFromMe(m),
		ReactionsJSON:  reactionsJSON(m.GetReactions()),
		ReplyToID:      replyToID(m),
		RawProto:       w.Data,
	}
	if body != "" {
		row.Body = &body
	}
	if media != nil {
		mid := media.GetMediaID()
		mt := media.GetMimeType()
		if mid != "" {
			row.MediaID = &mid
		}
		if mt != "" {
			row.MimeType = &mt
		}
		row.DecryptionKey = media.GetDecryptionKey()
	}
	if err := p.store.UpsertMessage(ctx, row); err != nil {
		p.logger.Error().Err(err).Str("msg_id", row.ID).Msg("Upsert message failed")
		return
	}
	if !w.IsOld {
		_ = p.store.MarkSync(ctx, time.UnixMilli(row.TimestampMS), time.Now())
	}
}

func (p *Pump) touch(ctx context.Context) {
	if err := p.store.MarkSync(ctx, time.Time{}, time.Now()); err != nil {
		p.logger.Debug().Err(err).Msg("MarkSync failed")
	}
}

// messageBody concatenates all MessageContent bodies in MessageInfo. A
// single message can carry multiple MessageInfo entries (e.g. text +
// attachment), each with its own oneof.
func messageBody(m *gmproto.Message) string {
	var parts []string
	for _, info := range m.GetMessageInfo() {
		if mc := info.GetMessageContent(); mc != nil && mc.GetContent() != "" {
			parts = append(parts, mc.GetContent())
		}
	}
	return strings.Join(parts, "\n")
}

// primaryMedia returns the first MediaContent attachment on a message,
// preferring full media over thumbnails.
func primaryMedia(m *gmproto.Message) *gmproto.MediaContent {
	for _, info := range m.GetMessageInfo() {
		if mc := info.GetMediaContent(); mc != nil {
			return mc
		}
	}
	return nil
}

func isFromMe(m *gmproto.Message) bool {
	// Google's protocol does not expose a direct is_from_me bit on Message.
	// MessageStatus.Status carries OUTGOING_* values for sent messages; the
	// concrete enum values shift between versions, so use the string form
	// as a cheap heuristic and refine in Phase 3 once we have ground truth.
	s := m.GetMessageStatus().GetStatus().String()
	return strings.Contains(s, "OUTGOING") || strings.Contains(s, "SENT_BY_ME")
}

func reactionsJSON(reactions []*gmproto.ReactionEntry) *string {
	if len(reactions) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(reactions))
	for _, r := range reactions {
		if r == nil {
			continue
		}
		out = append(out, map[string]any{
			"emoji":        r.GetData().GetUnicode(),
			"participants": r.GetParticipantIDs(),
		})
	}
	if len(out) == 0 {
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

func replyToID(m *gmproto.Message) *string {
	if r := m.GetReplyMessage(); r != nil {
		if id := r.GetMessageID(); id != "" {
			return &id
		}
	}
	return nil
}

// participantsJSON produces a compact JSON array of participant summaries
// embedded into the conversation row, so a query layer can answer "who's in
// this thread" without a join.
func participantsJSON(parts []*gmproto.Participant) string {
	if len(parts) == 0 {
		return "[]"
	}
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		if p == nil {
			continue
		}
		name := p.GetFullName()
		if name == "" {
			name = p.GetFirstName()
		}
		out = append(out, map[string]any{
			"id":               p.GetID().GetParticipantID(),
			"name":             name,
			"e164":             p.GetID().GetNumber(),
			"formatted_number": p.GetFormattedNumber(),
			"is_me":            p.GetIsMe(),
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeTimestampMS(ts int64) int64 {
	// libgm/gmproto timestamps have appeared as both millis and micros across
	// event types. Current Unix milliseconds are ~1e12; microseconds are ~1e15.
	if ts > 100_000_000_000_000 {
		return ts / 1000
	}
	return ts
}
