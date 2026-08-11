// Package gm wraps go.mau.fi/mautrix-gmessages/pkg/libgm with the conventions
// gmcli needs: filesystem-backed AuthData persistence, an event subscriber
// model on top of libgm's single SetEventHandler, and helpers for the Google
// Account (Gaia) pairing flow.
//
// Two entry points cover the lifecycle:
//
//	AuthenticateGaia(ctx, ...)       // first run/reauth: produces session.json
//	Open(layout, logger) -> *Client  // subsequent runs: ready to Connect()
//
// The wrapper does not own a goroutine of its own; libgm runs the long-poll.
// Subscribers must not block in their handlers.
package gm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"

	"github.com/fdsouvenir/gmcli/internal/paths"
)

// PairTimeout is the upper bound on a Google Account pairing attempt. Google's
// relay drops unfinished emoji confirmations after a few minutes.
const PairTimeout = 5 * time.Minute

// sendMetadataTimeout bounds how long a write waits for the phone settings
// event that carries preferred SIM metadata before falling back to the legacy
// send request shape.
const sendMetadataTimeout = 10 * time.Second

// EventHandler is invoked for each event delivered by libgm. The argument
// type is one of the concrete types in pkg/libgm/events or pkg/libgm/gmproto.
type EventHandler func(evt any)

// SendMode identifies which Google Messages request shape was used.
type SendMode string

const (
	SendModeAuto     SendMode = "auto"
	SendModeSettings SendMode = "settings"
	SendModeLegacy   SendMode = "legacy"
)

// Client is a thin wrapper around *libgm.Client adding fan-out event
// subscription and persistence on AuthTokenRefreshed.
type Client struct {
	libgm  *libgm.Client
	auth   *libgm.AuthData
	layout paths.Layout
	logger zerolog.Logger

	mu          sync.RWMutex
	subscribers []EventHandler
	settings    *gmproto.Settings
	ready       bool

	sendMessageHook     func(*gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error)
	getConversationHook func(string) (*gmproto.Conversation, error)
	sendMetadataWait    time.Duration
}

// Open loads session.json and returns a connected-but-not-yet-Connect()'d
// Client. Returns an error if no session exists — the caller must run Pair
// first.
func Open(layout paths.Layout, logger zerolog.Logger) (*Client, error) {
	auth, err := loadAuth(layout.Session)
	if err != nil {
		return nil, err
	}
	if auth.Browser == nil {
		return nil, fmt.Errorf("session %s has no paired device; run `gmcli auth` first", layout.Session)
	}
	c := &Client{
		auth:   auth,
		layout: layout,
		logger: logger,
	}
	c.libgm = libgm.NewClient(auth, nil, logger)
	c.libgm.SetEventHandler(c.dispatch)
	return c, nil
}

// Subscribe registers a handler. Multiple subscribers receive each event in
// the order they were registered. Handlers must not block.
func (c *Client) Subscribe(h EventHandler) {
	c.mu.Lock()
	c.subscribers = append(c.subscribers, h)
	c.mu.Unlock()
}

// Connect opens the long-poll connection. Events flow to subscribers
// immediately. Returns when the initial sync completes; the connection
// continues running in a background goroutine inside libgm.
func (c *Client) Connect() error {
	c.mu.Lock()
	c.ready = false
	c.mu.Unlock()
	return c.libgm.Connect()
}

// Disconnect closes the long-poll. Safe to call multiple times.
func (c *Client) Disconnect() {
	c.libgm.Disconnect()
	c.mu.Lock()
	c.ready = false
	c.mu.Unlock()
}

// IsConnected reports whether the long-poll is currently active.
func (c *Client) IsConnected() bool {
	return c.libgm.IsConnected()
}

// WaitForReady blocks until the libgm client emits *events.ClientReady or
// the context is cancelled. SendMessage and SendReaction need an established
// session before they can round-trip a response; ClientReady is the earliest
// signal that the session is up. The handler is removed before returning.
//
// Subscribe(c.WaitForReady...) is not the right idiom — this method
// installs and removes a single-fire subscriber for you.
func (c *Client) WaitForReady(ctx context.Context) error {
	c.mu.RLock()
	isReady := c.ready
	c.mu.RUnlock()
	if isReady {
		return nil
	}
	ready := make(chan struct{}, 1)
	var fired sync.Once

	c.mu.Lock()
	idx := len(c.subscribers)
	c.subscribers = append(c.subscribers, func(evt any) {
		if _, ok := evt.(*events.ClientReady); ok {
			fired.Do(func() { close(ready) })
		}
	})
	if c.ready {
		fired.Do(func() { close(ready) })
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		// Remove only our subscriber; preserve any added concurrently.
		if idx < len(c.subscribers) {
			c.subscribers = append(c.subscribers[:idx], c.subscribers[idx+1:]...)
		}
		c.mu.Unlock()
	}()

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Underlying returns the wrapped *libgm.Client for callers that need access
// to libgm methods we haven't surfaced yet (ListContacts, FetchMessages,
// etc.). Higher-level operations should prefer the typed wrappers below.
func (c *Client) Underlying() *libgm.Client { return c.libgm }

// SetSettings seeds the send metadata cache from persisted phone settings.
func (c *Client) SetSettings(settings *gmproto.Settings) {
	if settings == nil {
		return
	}
	cloned, ok := proto.Clone(settings).(*gmproto.Settings)
	if !ok {
		return
	}
	c.mu.Lock()
	c.settings = cloned
	c.mu.Unlock()
}

// RequestUpdates asks the phone for a fresh GET_UPDATES payload.
func (c *Client) RequestUpdates() error {
	return c.libgm.SetActiveSession()
}

// IsDefaultSMSApp asks the phone whether Google Messages is the default SMS app.
func (c *Client) IsDefaultSMSApp() (bool, error) {
	resp, err := c.libgm.IsBugleDefault()
	if err != nil {
		return false, err
	}
	return resp.GetSuccess(), nil
}

// SendTextResult describes a successful send.
type SendTextResult struct {
	MessageID      string
	ConversationID string
	TmpID          string
	SendMode       SendMode
}

// SendText sends a text message into the given conversation. ReplyToID is
// optional; when set, the new message is rendered as a quoted reply by the
// recipient's client. The libgm long-poll must be Connected; call
// WaitForReady first for fresh sessions.
func (c *Client) SendText(ctx context.Context, conversationID, body, replyToID string) (*SendTextResult, error) {
	return c.SendTextWithMode(ctx, conversationID, body, replyToID, SendModeAuto)
}

// SendTextWithMode is SendText with an explicit request-shape selection.
// SendModeAuto prefers Settings/SIM metadata, falls back to legacy when
// Settings are unavailable, and retries legacy when the phone rejects a
// settings-mode attempt with UNKNOWN.
func (c *Client) SendTextWithMode(ctx context.Context, conversationID, body, replyToID string, requested SendMode) (*SendTextResult, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if body == "" {
		return nil, fmt.Errorf("message body is required")
	}
	if !validRequestedSendMode(requested) {
		return nil, fmt.Errorf("unknown send mode %q", requested)
	}
	tmpID := uuid.NewString()
	req, mode, err := c.buildSendTextRequest(ctx, conversationID, body, replyToID, tmpID, requested)
	if err != nil {
		return nil, err
	}
	res, err := c.sendBuiltText(ctx, req, mode)
	if err == nil {
		return res, nil
	}
	var rejected sendRejectedError
	if requested == SendModeAuto && mode == SendModeSettings && errors.As(err, &rejected) && rejected.status == gmproto.SendMessageResponse_UNKNOWN {
		legacyReq := buildLegacySendTextRequest(conversationID, body, replyToID, uuid.NewString())
		legacyRes, legacyErr := c.sendBuiltText(ctx, legacyReq, SendModeLegacy)
		if legacyErr != nil {
			return nil, fmt.Errorf("%w; legacy fallback failed: %w", err, legacyErr)
		}
		return legacyRes, nil
	}
	return nil, err
}

func (c *Client) sendBuiltText(ctx context.Context, req *gmproto.SendMessageRequest, mode SendMode) (*SendTextResult, error) {
	waitEcho, unsubscribe := c.watchMessageEcho(req.GetTmpID())
	defer unsubscribe()

	resp, err := c.sendMessage(req)
	if err != nil {
		return nil, fmt.Errorf("libgm send: %w", err)
	}
	if resp.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
		return nil, sendRejectedError{
			status:  resp.GetStatus(),
			message: sendStatusMessage(resp),
		}
	}

	echo, err := waitEcho(ctx)
	if err != nil {
		return nil, fmt.Errorf("send accepted by phone, but no sent-message echo arrived for tmp_id %s: %w", req.GetTmpID(), err)
	}
	return &SendTextResult{
		MessageID:      echo.Message.GetMessageID(),
		ConversationID: echo.Message.GetConversationID(),
		TmpID:          req.GetTmpID(),
		SendMode:       mode,
	}, nil
}

type sendRejectedError struct {
	status  gmproto.SendMessageResponse_Status
	message string
}

func (e sendRejectedError) Error() string {
	return fmt.Sprintf("send rejected by phone: %s", e.message)
}

func validRequestedSendMode(mode SendMode) bool {
	switch mode {
	case SendModeAuto, SendModeSettings, SendModeLegacy:
		return true
	default:
		return false
	}
}

func (c *Client) buildSendTextRequest(ctx context.Context, conversationID, body, replyToID, tmpID string, requested SendMode) (*gmproto.SendMessageRequest, SendMode, error) {
	if requested == SendModeLegacy {
		return buildLegacySendTextRequest(conversationID, body, replyToID, tmpID), SendModeLegacy, nil
	}

	settingsCtx, cancel := context.WithTimeout(ctx, c.sendMetadataWaitDuration())
	defer cancel()
	if err := c.WaitForSettings(settingsCtx); err != nil {
		if ctx.Err() != nil {
			return nil, "", fmt.Errorf("wait for phone send settings: %w", err)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			if requested == SendModeSettings {
				return nil, "", fmt.Errorf("wait for phone send settings: %w", err)
			}
			return buildLegacySendTextRequest(conversationID, body, replyToID, tmpID), SendModeLegacy, nil
		}
		return nil, "", fmt.Errorf("wait for phone send settings: %w", err)
	}

	req, err := c.buildSettingsSendTextRequest(conversationID, body, replyToID, tmpID)
	if err != nil {
		if requested == SendModeSettings {
			return nil, "", err
		}
		return nil, "", err
	}
	return req, SendModeSettings, nil
}

func (c *Client) buildSettingsSendTextRequest(conversationID, body, replyToID, tmpID string) (*gmproto.SendMessageRequest, error) {
	conv, err := c.getConversation(conversationID)
	if err != nil {
		return nil, fmt.Errorf("get conversation %s before send: %w", conversationID, err)
	}
	outgoingID := conv.GetDefaultOutgoingID()
	var sim *gmproto.SIMCard
	if outgoingID == "" {
		var err error
		outgoingID, sim, err = c.onlyUsableSIM()
		if err != nil {
			return nil, fmt.Errorf("conversation %s has no default outgoing participant: %w", conversationID, err)
		}
	} else {
		sim = c.simForParticipant(outgoingID)
		if sim == nil {
			return nil, fmt.Errorf("conversation %s uses outgoing participant %s, but no matching SIM metadata was received", conversationID, outgoingID)
		}
	}
	req := &gmproto.SendMessageRequest{
		ConversationID: conversationID,
		TmpID:          tmpID,
		ForceRCS:       forceRCSForConversation(conv, sim),
		MessagePayload: &gmproto.MessagePayload{
			ConversationID: conversationID,
			ParticipantID:  outgoingID,
			TmpID:          tmpID,
			TmpID2:         tmpID,
			MessageInfo: []*gmproto.MessageInfo{{
				Data: &gmproto.MessageInfo_MessageContent{
					MessageContent: &gmproto.MessageContent{Content: body},
				},
			}},
		},
	}
	req.SIMPayload = sim.GetSIMData().GetSIMPayload()
	if replyToID != "" {
		req.Reply = &gmproto.ReplyPayload{MessageID: replyToID}
	}
	return req, nil
}

func forceRCSForConversation(conv *gmproto.Conversation, sim *gmproto.SIMCard) bool {
	if !sim.GetRCSChats().GetEnabled() || conv.GetSendMode() != gmproto.ConversationSendMode_SEND_MODE_AUTO {
		return false
	}
	switch conv.GetType() {
	case gmproto.ConversationType_RCS, gmproto.ConversationType_UNKNOWN_CONVERSATION_TYPE:
		return true
	default:
		return false
	}
}

func buildLegacySendTextRequest(conversationID, body, replyToID, tmpID string) *gmproto.SendMessageRequest {
	req := &gmproto.SendMessageRequest{
		ConversationID: conversationID,
		TmpID:          tmpID,
		MessagePayload: &gmproto.MessagePayload{
			ConversationID: conversationID,
			TmpID:          tmpID,
			TmpID2:         tmpID,
			MessagePayloadContent: &gmproto.MessagePayloadContent{
				MessageContent: &gmproto.MessageContent{Content: body},
			},
		},
	}
	if replyToID != "" {
		req.Reply = &gmproto.ReplyPayload{MessageID: replyToID}
	}
	return req
}

func (c *Client) sendMessage(req *gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
	if c.sendMessageHook != nil {
		return c.sendMessageHook(req)
	}
	return c.libgm.SendMessage(req)
}

func (c *Client) getConversation(conversationID string) (*gmproto.Conversation, error) {
	if c.getConversationHook != nil {
		return c.getConversationHook(conversationID)
	}
	return c.libgm.GetConversation(conversationID)
}

func (c *Client) sendMetadataWaitDuration() time.Duration {
	if c.sendMetadataWait > 0 {
		return c.sendMetadataWait
	}
	return sendMetadataTimeout
}

// WaitForSettings blocks until libgm emits the phone settings event. Send
// requests prefer its SIM metadata to match the browser client shape.
func (c *Client) WaitForSettings(ctx context.Context) error {
	c.mu.RLock()
	hasSettings := c.settings != nil
	c.mu.RUnlock()
	if hasSettings {
		return nil
	}

	ready := make(chan struct{}, 1)
	var fired sync.Once
	c.mu.Lock()
	idx := len(c.subscribers)
	c.subscribers = append(c.subscribers, func(evt any) {
		if _, ok := evt.(*gmproto.Settings); ok {
			fired.Do(func() { close(ready) })
		}
	})
	if c.settings != nil {
		fired.Do(func() { close(ready) })
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if idx < len(c.subscribers) {
			c.subscribers = append(c.subscribers[:idx], c.subscribers[idx+1:]...)
		}
		c.mu.Unlock()
	}()

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) simForParticipant(participantID string) *gmproto.SIMCard {
	c.mu.RLock()
	settings := c.settings
	c.mu.RUnlock()
	if settings == nil {
		return nil
	}
	for _, sim := range settings.GetSIMCards() {
		if sim.GetSIMParticipant().GetID() == participantID {
			return sim
		}
	}
	return nil
}

func (c *Client) onlyUsableSIM() (string, *gmproto.SIMCard, error) {
	c.mu.RLock()
	settings := c.settings
	c.mu.RUnlock()
	if settings == nil {
		return "", nil, fmt.Errorf("no phone send settings are cached")
	}

	var only *gmproto.SIMCard
	for _, sim := range settings.GetSIMCards() {
		if sim.GetSIMParticipant().GetID() == "" || sim.GetSIMData().GetSIMPayload() == nil {
			continue
		}
		if only != nil {
			return "", nil, fmt.Errorf("multiple usable SIMs were received; cannot choose sender/SIM")
		}
		only = sim
	}
	if only == nil {
		return "", nil, fmt.Errorf("no usable SIM metadata was received")
	}
	return only.GetSIMParticipant().GetID(), only, nil
}

func (c *Client) watchMessageEcho(tmpID string) (func(context.Context) (*libgm.WrappedMessage, error), func()) {
	echo := make(chan *libgm.WrappedMessage, 1)
	c.mu.Lock()
	idx := len(c.subscribers)
	c.subscribers = append(c.subscribers, func(evt any) {
		w, ok := evt.(*libgm.WrappedMessage)
		if !ok || w.Message.GetTmpID() != tmpID {
			return
		}
		select {
		case echo <- w:
		default:
		}
	})
	c.mu.Unlock()

	unsubscribe := func() {
		c.mu.Lock()
		if idx < len(c.subscribers) {
			c.subscribers = append(c.subscribers[:idx], c.subscribers[idx+1:]...)
		}
		c.mu.Unlock()
	}
	wait := func(ctx context.Context) (*libgm.WrappedMessage, error) {
		select {
		case w := <-echo:
			return w, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return wait, unsubscribe
}

func sendStatusMessage(resp *gmproto.SendMessageResponse) string {
	switch resp.GetStatus() {
	case gmproto.SendMessageResponse_UNKNOWN:
		if resp.GetGoogleAccountSwitch() != nil {
			return "re-run `gmcli auth` with Google Account cookies before sending messages"
		}
		return "unknown status"
	case gmproto.SendMessageResponse_FAILURE_2:
		return "unknown permanent error"
	case gmproto.SendMessageResponse_FAILURE_3:
		return "unknown temporary error"
	case gmproto.SendMessageResponse_FAILURE_4:
		return "Google Messages is not your default SMS app"
	default:
		return resp.GetStatus().String()
	}
}

// ReactionAction selects ADD / REMOVE / SWITCH semantics on SendReaction.
type ReactionAction int

const (
	ReactionAdd ReactionAction = iota
	ReactionRemove
	ReactionSwitch
)

// SendReaction adds, removes, or switches a unicode reaction on a message.
func (c *Client) SendReaction(messageID, emoji string, action ReactionAction) error {
	if messageID == "" {
		return fmt.Errorf("message id is required")
	}
	if emoji == "" {
		return fmt.Errorf("emoji is required")
	}
	var act gmproto.SendReactionRequest_Action
	switch action {
	case ReactionAdd:
		act = gmproto.SendReactionRequest_ADD
	case ReactionRemove:
		act = gmproto.SendReactionRequest_REMOVE
	case ReactionSwitch:
		act = gmproto.SendReactionRequest_SWITCH
	default:
		return fmt.Errorf("unknown reaction action %v", action)
	}
	_, err := c.libgm.SendReaction(&gmproto.SendReactionRequest{
		MessageID:    messageID,
		Action:       act,
		ReactionData: &gmproto.ReactionData{Unicode: emoji},
	})
	if err != nil {
		return fmt.Errorf("libgm reaction: %w", err)
	}
	return nil
}

// DownloadMedia retrieves and decrypts the bytes for an attachment.
// The connection does not need to be in long-poll mode — DownloadMedia
// uses authenticated HTTP — but the AuthData's TachyonAuthToken must be
// fresh. Call Connect once before this if the session has been idle.
func (c *Client) DownloadMedia(mediaID string, key []byte) ([]byte, error) {
	if mediaID == "" {
		return nil, fmt.Errorf("media id is required")
	}
	return c.libgm.DownloadMedia(mediaID, key)
}

// AuthSnapshot returns a deep copy of the current AuthData by JSON
// round-trip. Useful for diagnostics; do not modify.
func (c *Client) AuthSnapshot() (*libgm.AuthData, error) {
	b, err := json.Marshal(c.auth)
	if err != nil {
		return nil, err
	}
	var out libgm.AuthData
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// dispatch is the single libgm callback. It persists on token refresh and
// then fans out to subscribers.
func (c *Client) dispatch(evt any) {
	switch e := evt.(type) {
	case *events.AuthTokenRefreshed, *events.PairSuccessful:
		if err := saveAuth(c.layout.Session, c.auth); err != nil {
			c.logger.Error().Err(err).Msg("Failed to persist refreshed auth data")
		}
	case *events.ClientReady:
		c.mu.Lock()
		c.ready = true
		c.mu.Unlock()
	case *gmproto.Settings:
		c.mu.Lock()
		c.settings = e
		c.mu.Unlock()
	}
	c.mu.RLock()
	subs := append([]EventHandler(nil), c.subscribers...)
	c.mu.RUnlock()
	for _, h := range subs {
		h(evt)
	}
}

// PairResult is returned by AuthenticateGaia on success. PhoneID identifies
// a newly paired phone when the protocol returns one, Account is the validated
// Google Account, and SessionPath is where the persisted AuthData lives.
type PairResult struct {
	Mode        string `json:"mode"`
	PhoneID     string `json:"phone_id,omitempty"`
	Account     string `json:"account,omitempty"`
	SessionPath string `json:"session_path"`
}

// EmojiRenderer is invoked once Gaia pairing has selected the emoji that the
// user must tap in Google Messages on their phone.
type EmojiRenderer func(emoji string)

type gaiaPairingClient interface {
	FetchConfig(context.Context) error
	ConfigEmail() string
	StartGaiaPairing(context.Context) (string, *libgm.PairingSession, error)
	FinishGaiaPairing(context.Context, *libgm.PairingSession) (string, error)
	ValidateConnection(context.Context) error
	Disconnect()
}

type libgmGaiaClient struct {
	*libgm.Client
}

func (c *libgmGaiaClient) ConfigEmail() string {
	return c.Config.GetDeviceInfo().GetEmail()
}

// ValidateConnection requires an RPC response from the paired phone. Connect
// alone only starts libgm's long poll and can return before a revoked pairing
// is rejected by the relay.
func (c *libgmGaiaClient) ValidateConnection(ctx context.Context) error {
	fatal := make(chan error, 1)
	c.SetEventHandler(func(evt any) {
		var err error
		switch e := evt.(type) {
		case *events.ListenFatalError:
			err = e.Error
		case *events.GaiaLoggedOut:
			err = errors.New("Google Account session was logged out")
		case *events.PhoneNotResponding:
			err = errors.New("paired phone is not responding")
		case *events.PingFailed:
			err = e.Error
		}
		if err != nil {
			select {
			case fatal <- err:
			default:
			}
		}
	})
	if err := c.Connect(); err != nil {
		return err
	}

	roundTrip := make(chan error, 1)
	go func() {
		_, err := c.IsBugleDefault()
		roundTrip <- err
	}()
	select {
	case err := <-roundTrip:
		return err
	case err := <-fatal:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type gaiaClientFactory func(*libgm.AuthData, zerolog.Logger) gaiaPairingClient

func newGaiaClient(auth *libgm.AuthData, logger zerolog.Logger) gaiaPairingClient {
	return &libgmGaiaClient{Client: libgm.NewClient(auth, nil, logger)}
}

// AuthenticateGaia pairs with Google Messages using Google Account cookies
// and phone-side emoji confirmation. If a Gaia session already exists and
// forceNew is false, it refreshes that session's cookies after verifying that
// they belong to the same account. session.json is replaced only after the
// complete operation succeeds.
func AuthenticateGaia(
	ctx context.Context,
	layout paths.Layout,
	logger zerolog.Logger,
	cookies map[string]string,
	forceNew bool,
	render EmojiRenderer,
) (*PairResult, error) {
	return authenticateGaia(ctx, layout, logger, cookies, forceNew, render, newGaiaClient)
}

func authenticateGaia(
	ctx context.Context,
	layout paths.Layout,
	logger zerolog.Logger,
	cookies map[string]string,
	forceNew bool,
	render EmojiRenderer,
	newClient gaiaClientFactory,
) (*PairResult, error) {
	if err := layout.EnsureDirs(); err != nil {
		return nil, err
	}

	pairCtx, cancel := context.WithTimeout(ctx, PairTimeout)
	defer cancel()

	if !forceNew {
		if auth, err := loadAuth(layout.Session); err == nil && auth.IsGoogleAccount() {
			return reauthenticateGaia(pairCtx, layout, logger, auth, cookies, newClient)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	auth := libgm.NewAuthData()
	auth.SetCookies(cloneCookies(cookies))
	cli := newClient(auth, logger)
	defer cli.Disconnect()

	if err := cli.FetchConfig(pairCtx); err != nil {
		return nil, fmt.Errorf("validate Google Account cookies: %w", err)
	}
	account := cli.ConfigEmail()
	if account == "" {
		return nil, errors.New("Google Messages config did not identify an account")
	}
	emoji, pairing, err := startGaiaPairingWithContext(pairCtx, cli)
	if err != nil {
		return nil, describeGaiaPairingError("start", err)
	}
	render(emoji)
	phoneID, err := cli.FinishGaiaPairing(pairCtx, pairing)
	if err != nil {
		return nil, describeGaiaPairingError("finish", err)
	}
	if err := saveAuth(layout.Session, auth); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}
	return &PairResult{
		Mode:        "paired",
		PhoneID:     phoneID,
		Account:     account,
		SessionPath: layout.Session,
	}, nil
}

type gaiaStartResult struct {
	emoji   string
	pairing *libgm.PairingSession
	err     error
}

// libgm v0.2605.0 waits for its initial long-poll callback without selecting
// on the caller's context. Keep that dependency call behind a selectable
// boundary so the CLI can honor its deadline and SIGINT. The caller's deferred
// Disconnect closes an established poll before control returns.
func startGaiaPairingWithContext(ctx context.Context, cli gaiaPairingClient) (string, *libgm.PairingSession, error) {
	result := make(chan gaiaStartResult, 1)
	go func() {
		emoji, pairing, err := cli.StartGaiaPairing(ctx)
		result <- gaiaStartResult{emoji: emoji, pairing: pairing, err: err}
	}()
	select {
	case res := <-result:
		return res.emoji, res.pairing, res.err
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
}

func reauthenticateGaia(
	ctx context.Context,
	layout paths.Layout,
	logger zerolog.Logger,
	auth *libgm.AuthData,
	cookies map[string]string,
	newClient gaiaClientFactory,
) (*PairResult, error) {
	previousAccount := auth.Mobile.GetSourceID()
	auth.SetCookies(cloneCookies(cookies))
	cli := newClient(auth, logger)
	defer cli.Disconnect()

	if err := cli.FetchConfig(ctx); err != nil {
		return nil, fmt.Errorf("validate replacement Google Account cookies: %w", err)
	}
	account := cli.ConfigEmail()
	if previousAccount == "" || account == "" || !strings.EqualFold(previousAccount, account) {
		return nil, fmt.Errorf("cookies belong to a different Google Account; use the original account or pass --new")
	}
	if err := cli.ValidateConnection(ctx); err != nil {
		return nil, fmt.Errorf("validate existing Google Messages pairing: %w", err)
	}
	if err := saveAuth(layout.Session, auth); err != nil {
		return nil, fmt.Errorf("persist refreshed session: %w", err)
	}
	return &PairResult{
		Mode:        "reauthenticated",
		Account:     account,
		SessionPath: layout.Session,
	}, nil
}

func cloneCookies(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for name, value := range in {
		out[name] = value
	}
	return out
}

func describeGaiaPairingError(stage string, err error) error {
	switch {
	case errors.Is(err, libgm.ErrNoCookies):
		return fmt.Errorf("%s Gaia pairing: required Google Account cookies are missing: %w", stage, err)
	case errors.Is(err, libgm.ErrNoDevicesFound):
		return fmt.Errorf("%s Gaia pairing: no phone has Google Account pairing enabled; open Google Messages > Device pairing on the phone: %w", stage, err)
	case errors.Is(err, libgm.ErrPairingInitTimeout):
		return fmt.Errorf("%s Gaia pairing: phone did not respond; keep Google Messages open and disable battery optimization temporarily: %w", stage, err)
	case errors.Is(err, libgm.ErrIncorrectEmoji):
		return fmt.Errorf("%s Gaia pairing: the wrong emoji was selected on the phone: %w", stage, err)
	case errors.Is(err, libgm.ErrPairingCancelled):
		return fmt.Errorf("%s Gaia pairing: confirmation was cancelled on the phone: %w", stage, err)
	case errors.Is(err, libgm.ErrPairingTimeout), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s Gaia pairing: confirmation timed out; run `gmcli auth` again: %w", stage, err)
	default:
		return fmt.Errorf("%s Gaia pairing: %w", stage, err)
	}
}

func loadAuth(path string) (*libgm.AuthData, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no session at %s; run `gmcli auth` first: %w", path, os.ErrNotExist)
		}
		return nil, fmt.Errorf("open session %s: %w", path, err)
	}
	defer f.Close()
	var auth libgm.AuthData
	if err := json.NewDecoder(f).Decode(&auth); err != nil {
		return nil, fmt.Errorf("decode session %s: %w", path, err)
	}
	return &auth, nil
}

func saveAuth(path string, auth *libgm.AuthData) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(auth); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("encode session: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
