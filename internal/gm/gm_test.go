package gm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/fdsouvenir/gmcli/internal/paths"
)

func TestSendStatusMessage(t *testing.T) {
	tests := []struct {
		name string
		resp *gmproto.SendMessageResponse
		want string
	}{
		{
			name: "default sms app",
			resp: &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_FAILURE_4},
			want: "default SMS app",
		},
		{
			name: "temporary",
			resp: &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_FAILURE_3},
			want: "temporary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sendStatusMessage(tt.resp); !strings.Contains(got, tt.want) {
				t.Fatalf("sendStatusMessage() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestSetSettingsUnblocksWaitForSettingsAndFindsSIM(t *testing.T) {
	c := &Client{}
	c.SetSettings(&gmproto.Settings{
		SIMCards: []*gmproto.SIMCard{{
			SIMParticipant: &gmproto.SIMParticipant{ID: "sender-1"},
			SIMData: &gmproto.SIMData{
				SIMPayload: &gmproto.SIMPayload{Two: 1, SIMNumber: 1},
			},
		}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := c.WaitForSettings(ctx); err != nil {
		t.Fatalf("wait for settings: %v", err)
	}
	sim := c.simForParticipant("sender-1")
	if sim == nil {
		t.Fatalf("expected SIM for sender-1")
	}
	if sim.GetSIMData().GetSIMPayload().GetSIMNumber() != 1 {
		t.Fatalf("unexpected SIM payload: %+v", sim.GetSIMData().GetSIMPayload())
	}
}

func TestWaitForReadyReturnsAfterAlreadyReady(t *testing.T) {
	c := &Client{}
	c.dispatch(&events.ClientReady{})

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := c.WaitForReady(ctx); err != nil {
		t.Fatalf("wait for ready after event: %v", err)
	}
}

func TestBuildSettingsSendTextRequest(t *testing.T) {
	c := &Client{
		getConversationHook: func(conversationID string) (*gmproto.Conversation, error) {
			if conversationID != "conv-1" {
				t.Fatalf("conversation id: got %q want conv-1", conversationID)
			}
			return &gmproto.Conversation{DefaultOutgoingID: "sender-1"}, nil
		},
	}
	c.SetSettings(testSettings("sender-1"))

	req, err := c.buildSettingsSendTextRequest(context.Background(), "conv-1", "hello", "reply-1", "tmp-1")
	if err != nil {
		t.Fatalf("build settings request: %v", err)
	}
	if req.GetSIMPayload() == nil {
		t.Fatalf("expected SIM payload")
	}
	if req.GetMessagePayload().GetParticipantID() != "sender-1" {
		t.Fatalf("participant id: got %q want sender-1", req.GetMessagePayload().GetParticipantID())
	}
	if req.GetMessagePayload().GetMessagePayloadContent() != nil {
		t.Fatalf("settings request should use message_info, not legacy messagePayloadContent")
	}
	if got := req.GetMessagePayload().GetMessageInfo()[0].GetMessageContent().GetContent(); got != "hello" {
		t.Fatalf("message content: got %q want hello", got)
	}
	if req.GetReply().GetMessageID() != "reply-1" {
		t.Fatalf("reply id: got %q want reply-1", req.GetReply().GetMessageID())
	}
}

func TestBuildSettingsSendTextRequestUsesOnlySIMWhenConversationOmitsOutgoingID(t *testing.T) {
	c := &Client{
		getConversationHook: func(conversationID string) (*gmproto.Conversation, error) {
			if conversationID != "conv-1" {
				t.Fatalf("conversation id: got %q want conv-1", conversationID)
			}
			return &gmproto.Conversation{}, nil
		},
	}
	c.SetSettings(testSettings("sender-1"))

	req, err := c.buildSettingsSendTextRequest(context.Background(), "conv-1", "hello", "", "tmp-1")
	if err != nil {
		t.Fatalf("build settings request: %v", err)
	}
	if req.GetSIMPayload() == nil {
		t.Fatalf("expected SIM payload")
	}
	if req.GetMessagePayload().GetParticipantID() != "sender-1" {
		t.Fatalf("participant id: got %q want sender-1", req.GetMessagePayload().GetParticipantID())
	}
	if req.GetMessagePayload().GetMessagePayloadContent() != nil {
		t.Fatalf("settings request should use message_info, not legacy messagePayloadContent")
	}
}

func TestBuildSettingsSendTextRequestForcesRCSForUnknownAutoConversationWithRCSSIM(t *testing.T) {
	c := &Client{
		getConversationHook: func(string) (*gmproto.Conversation, error) {
			return &gmproto.Conversation{
				DefaultOutgoingID: "sender-1",
				Type:              gmproto.ConversationType_UNKNOWN_CONVERSATION_TYPE,
				SendMode:          gmproto.ConversationSendMode_SEND_MODE_AUTO,
			}, nil
		},
	}
	c.SetSettings(testSettingsWithRCS("sender-1"))

	req, err := c.buildSettingsSendTextRequest(context.Background(), "conv-1", "hello", "", "tmp-1")
	if err != nil {
		t.Fatalf("build settings request: %v", err)
	}
	if !req.GetForceRCS() {
		t.Fatalf("expected force RCS for unknown auto conversation with RCS-enabled SIM")
	}
}

func TestBuildSettingsSendTextRequestDoesNotForceRCSForSMSConversation(t *testing.T) {
	c := &Client{
		getConversationHook: func(string) (*gmproto.Conversation, error) {
			return &gmproto.Conversation{
				DefaultOutgoingID: "sender-1",
				Type:              gmproto.ConversationType_SMS,
				SendMode:          gmproto.ConversationSendMode_SEND_MODE_AUTO,
			}, nil
		},
	}
	c.SetSettings(testSettingsWithRCS("sender-1"))

	req, err := c.buildSettingsSendTextRequest(context.Background(), "conv-1", "hello", "", "tmp-1")
	if err != nil {
		t.Fatalf("build settings request: %v", err)
	}
	if req.GetForceRCS() {
		t.Fatalf("did not expect force RCS for SMS conversation")
	}
}

func TestBuildSettingsSendTextRequestRejectsAmbiguousSIMWhenConversationOmitsOutgoingID(t *testing.T) {
	c := &Client{
		getConversationHook: func(string) (*gmproto.Conversation, error) {
			return &gmproto.Conversation{}, nil
		},
	}
	settings := testSettings("sender-1")
	settings.SIMCards = append(settings.GetSIMCards(), testSettings("sender-2").GetSIMCards()[0])
	c.SetSettings(settings)

	if _, err := c.buildSettingsSendTextRequest(context.Background(), "conv-1", "hello", "", "tmp-1"); err == nil {
		t.Fatalf("expected ambiguous SIM error")
	}
}

func TestBuildLegacySendTextRequest(t *testing.T) {
	req := buildLegacySendTextRequest("conv-1", "hello", "reply-1", "tmp-1")
	if req.GetSIMPayload() != nil {
		t.Fatalf("legacy request should omit SIM payload")
	}
	if req.GetMessagePayload().GetParticipantID() != "" {
		t.Fatalf("legacy request should omit participant id")
	}
	if req.GetMessagePayload().GetMessageInfo() != nil {
		t.Fatalf("legacy request should omit message_info")
	}
	if got := req.GetMessagePayload().GetMessagePayloadContent().GetMessageContent().GetContent(); got != "hello" {
		t.Fatalf("message content: got %q want hello", got)
	}
	if req.GetReply().GetMessageID() != "reply-1" {
		t.Fatalf("reply id: got %q want reply-1", req.GetReply().GetMessageID())
	}
}

func TestSendTextUsesSettingsModeWhenAvailable(t *testing.T) {
	c := &Client{
		getConversationHook: func(string) (*gmproto.Conversation, error) {
			return &gmproto.Conversation{DefaultOutgoingID: "sender-1"}, nil
		},
	}
	c.SetSettings(testSettings("sender-1"))
	c.sendMessageHook = func(req *gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
		if req.GetSIMPayload() == nil {
			t.Fatalf("expected SIM payload")
		}
		c.dispatch(&libgm.WrappedMessage{Message: &gmproto.Message{
			MessageID:      "msg-1",
			ConversationID: req.GetConversationID(),
			TmpID:          req.GetTmpID(),
		}})
		return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_SUCCESS}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := c.SendText(ctx, "conv-1", "hello", "")
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if res.SendMode != SendModeSettings {
		t.Fatalf("send mode: got %q want %q", res.SendMode, SendModeSettings)
	}
	if res.MessageID != "msg-1" {
		t.Fatalf("message id: got %q want msg-1", res.MessageID)
	}
}

func TestSendTextFallsBackToLegacyModeWhenSettingsTimeout(t *testing.T) {
	c := &Client{sendMetadataWait: time.Nanosecond}
	c.sendMessageHook = func(req *gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
		if req.GetSIMPayload() != nil {
			t.Fatalf("legacy fallback should omit SIM payload")
		}
		if req.GetMessagePayload().GetMessagePayloadContent() == nil {
			t.Fatalf("legacy fallback should use messagePayloadContent")
		}
		c.dispatch(&libgm.WrappedMessage{Message: &gmproto.Message{
			MessageID:      "msg-legacy",
			ConversationID: req.GetConversationID(),
			TmpID:          req.GetTmpID(),
		}})
		return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_SUCCESS}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := c.SendText(ctx, "conv-1", "hello", "")
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if res.SendMode != SendModeLegacy {
		t.Fatalf("send mode: got %q want %q", res.SendMode, SendModeLegacy)
	}
	if res.MessageID != "msg-legacy" {
		t.Fatalf("message id: got %q want msg-legacy", res.MessageID)
	}
}

func TestSendTextAutoDoesNotFallbackToLegacyWhenSettingsCannotChooseSIM(t *testing.T) {
	c := &Client{
		getConversationHook: func(string) (*gmproto.Conversation, error) {
			return &gmproto.Conversation{}, nil
		},
		sendMessageHook: func(*gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
			t.Fatalf("send should not be attempted without a chosen SIM")
			return nil, nil
		},
	}
	settings := testSettings("sender-1")
	settings.SIMCards = append(settings.GetSIMCards(), testSettings("sender-2").GetSIMCards()[0])
	c.SetSettings(settings)

	if _, err := c.SendText(context.Background(), "conv-1", "hello", ""); err == nil {
		t.Fatalf("expected SIM selection error")
	}
}

func TestSendTextForcedLegacyModeIgnoresAvailableSettings(t *testing.T) {
	c := &Client{}
	c.SetSettings(testSettings("sender-1"))
	c.sendMessageHook = func(req *gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
		if req.GetSIMPayload() != nil {
			t.Fatalf("forced legacy should omit SIM payload")
		}
		if req.GetMessagePayload().GetMessagePayloadContent() == nil {
			t.Fatalf("forced legacy should use messagePayloadContent")
		}
		c.dispatch(&libgm.WrappedMessage{Message: &gmproto.Message{
			MessageID:      "msg-forced-legacy",
			ConversationID: req.GetConversationID(),
			TmpID:          req.GetTmpID(),
		}})
		return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_SUCCESS}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := c.SendTextWithMode(ctx, "conv-1", "hello", "", SendModeLegacy)
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if res.SendMode != SendModeLegacy {
		t.Fatalf("send mode: got %q want %q", res.SendMode, SendModeLegacy)
	}
}

func TestSendTextAutoRetriesLegacyWhenSettingsRejectedUnknown(t *testing.T) {
	c := &Client{
		getConversationHook: func(string) (*gmproto.Conversation, error) {
			return &gmproto.Conversation{DefaultOutgoingID: "sender-1"}, nil
		},
	}
	c.SetSettings(testSettings("sender-1"))
	calls := 0
	c.sendMessageHook = func(req *gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
		calls++
		switch calls {
		case 1:
			if req.GetSIMPayload() == nil {
				t.Fatalf("first auto attempt should use settings request")
			}
			return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_UNKNOWN}, nil
		case 2:
			if req.GetSIMPayload() != nil {
				t.Fatalf("legacy retry should omit SIM payload")
			}
			c.dispatch(&libgm.WrappedMessage{Message: &gmproto.Message{
				MessageID:      "msg-retry",
				ConversationID: req.GetConversationID(),
				TmpID:          req.GetTmpID(),
			}})
			return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_SUCCESS}, nil
		default:
			t.Fatalf("unexpected send call %d", calls)
			return nil, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := c.SendText(ctx, "conv-1", "hello", "")
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if calls != 2 {
		t.Fatalf("send calls: got %d want 2", calls)
	}
	if res.SendMode != SendModeLegacy {
		t.Fatalf("send mode: got %q want %q", res.SendMode, SendModeLegacy)
	}
	if res.MessageID != "msg-retry" {
		t.Fatalf("message id: got %q want msg-retry", res.MessageID)
	}
}

func TestSendTextDoesNotFallbackAfterParentContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sent := false
	c := &Client{
		sendMetadataWait: time.Second,
		sendMessageHook: func(*gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
			sent = true
			return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_SUCCESS}, nil
		},
	}

	if _, err := c.SendText(ctx, "conv-1", "hello", ""); err == nil {
		t.Fatalf("expected canceled context error")
	}
	if sent {
		t.Fatalf("should not send after parent context cancellation")
	}
}

func TestSendTextRequiresEchoInLegacyMode(t *testing.T) {
	c := &Client{
		sendMetadataWait: time.Nanosecond,
		sendMessageHook: func(*gmproto.SendMessageRequest) (*gmproto.SendMessageResponse, error) {
			return &gmproto.SendMessageResponse{Status: gmproto.SendMessageResponse_SUCCESS}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.SendText(ctx, "conv-1", "hello", ""); err == nil {
		t.Fatalf("expected missing echo error")
	}
}

func TestSendTextRejectsUnknownSendMode(t *testing.T) {
	if _, err := (&Client{}).SendTextWithMode(context.Background(), "conv-1", "hello", "", SendMode("bogus")); err == nil {
		t.Fatalf("expected unknown send mode error")
	}
}

type fakeGaiaClient struct {
	configEmail  string
	fetchErr     error
	startErr     error
	finishErr    error
	connectErr   error
	emoji        string
	phoneID      string
	fetched      bool
	started      bool
	finished     bool
	connected    bool
	disconnected bool
}

func (f *fakeGaiaClient) FetchConfig(context.Context) error {
	f.fetched = true
	return f.fetchErr
}

func (f *fakeGaiaClient) ConfigEmail() string { return f.configEmail }

func (f *fakeGaiaClient) StartGaiaPairing(context.Context) (string, *libgm.PairingSession, error) {
	f.started = true
	if f.startErr != nil {
		return "", nil, f.startErr
	}
	return f.emoji, &libgm.PairingSession{}, nil
}

func (f *fakeGaiaClient) FinishGaiaPairing(context.Context, *libgm.PairingSession) (string, error) {
	f.finished = true
	return f.phoneID, f.finishErr
}

func (f *fakeGaiaClient) ValidateConnection(context.Context) error {
	f.connected = true
	return f.connectErr
}

func (f *fakeGaiaClient) Disconnect() { f.disconnected = true }

func fakeGaiaFactory(fake *fakeGaiaClient, gotAuth **libgm.AuthData) gaiaClientFactory {
	return func(auth *libgm.AuthData, _ zerolog.Logger) gaiaPairingClient {
		*gotAuth = auth
		return fake
	}
}

func TestAuthenticateGaiaPairsAndPersistsOnlyAfterConfirmation(t *testing.T) {
	layout := testLayout(t)
	fake := &fakeGaiaClient{configEmail: "person@example.com", emoji: "🦊", phoneID: "phone-1"}
	var gotAuth *libgm.AuthData
	var rendered string
	res, err := authenticateGaia(
		context.Background(), layout, zerolog.Nop(), testCookies(), false,
		func(emoji string) { rendered = emoji }, fakeGaiaFactory(fake, &gotAuth),
	)
	if err != nil {
		t.Fatalf("authenticate Gaia: %v", err)
	}
	if res.Mode != "paired" || res.Account != "person@example.com" || res.PhoneID != "phone-1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if rendered != "🦊" || !fake.fetched || !fake.started || !fake.finished || !fake.disconnected {
		t.Fatalf("unexpected lifecycle: rendered=%q fake=%+v", rendered, fake)
	}
	if gotAuth.Cookies["SID"] != "sid-value" {
		t.Fatalf("cookies not attached to auth data")
	}
	info, err := os.Stat(layout.Session)
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session mode = %04o, want 0600", info.Mode().Perm())
	}
	loaded, err := loadAuth(layout.Session)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.Cookies["SAPISID"] != "sapisid-value" {
		t.Fatalf("persisted cookies missing")
	}
}

func TestAuthenticateGaiaFailurePreservesExistingSession(t *testing.T) {
	layout := testLayout(t)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	before := []byte("existing-session\n")
	if err := os.WriteFile(layout.Session, before, 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGaiaClient{configEmail: "person@example.com", emoji: "🦊", finishErr: libgm.ErrIncorrectEmoji}
	var gotAuth *libgm.AuthData
	_, err := authenticateGaia(
		context.Background(), layout, zerolog.Nop(), testCookies(), true,
		func(string) {}, fakeGaiaFactory(fake, &gotAuth),
	)
	if !errors.Is(err, libgm.ErrIncorrectEmoji) {
		t.Fatalf("error = %v, want incorrect emoji", err)
	}
	after, readErr := os.ReadFile(layout.Session)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("failed auth changed existing session")
	}
}

func TestAuthenticateGaiaReauthenticatesSameAccount(t *testing.T) {
	layout := testLayout(t)
	auth := testGaiaAuth("Person@Example.com")
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := saveAuth(layout.Session, auth); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGaiaClient{configEmail: "person@example.com"}
	var gotAuth *libgm.AuthData
	res, err := authenticateGaia(
		context.Background(), layout, zerolog.Nop(), testCookies(), false,
		func(string) { t.Fatal("reauth should not render an emoji") }, fakeGaiaFactory(fake, &gotAuth),
	)
	if err != nil {
		t.Fatalf("reauthenticate: %v", err)
	}
	if res.Mode != "reauthenticated" || !fake.connected || fake.started {
		t.Fatalf("unexpected reauth lifecycle: result=%+v fake=%+v", res, fake)
	}
	loaded, err := loadAuth(layout.Session)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Cookies["SID"] != "sid-value" {
		t.Fatalf("replacement cookies were not persisted")
	}
}

func TestAuthenticateGaiaRejectsDifferentAccountWithoutChangingSession(t *testing.T) {
	layout := testLayout(t)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := saveAuth(layout.Session, testGaiaAuth("first@example.com")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(layout.Session)
	fake := &fakeGaiaClient{configEmail: "other@example.com"}
	var gotAuth *libgm.AuthData
	_, err := authenticateGaia(
		context.Background(), layout, zerolog.Nop(), testCookies(), false,
		func(string) {}, fakeGaiaFactory(fake, &gotAuth),
	)
	if err == nil || !strings.Contains(err.Error(), "different Google Account") {
		t.Fatalf("unexpected error: %v", err)
	}
	after, _ := os.ReadFile(layout.Session)
	if string(after) != string(before) {
		t.Fatalf("account mismatch changed existing session")
	}
}

func TestAuthenticateGaiaReauthValidationFailurePreservesSession(t *testing.T) {
	layout := testLayout(t)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := saveAuth(layout.Session, testGaiaAuth("person@example.com")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(layout.Session)
	fake := &fakeGaiaClient{
		configEmail: "person@example.com",
		connectErr:  errors.New("pairing revoked"),
	}
	var gotAuth *libgm.AuthData
	_, err := authenticateGaia(
		context.Background(), layout, zerolog.Nop(), testCookies(), false,
		func(string) {}, fakeGaiaFactory(fake, &gotAuth),
	)
	if err == nil || !strings.Contains(err.Error(), "pairing revoked") {
		t.Fatalf("unexpected error: %v", err)
	}
	after, _ := os.ReadFile(layout.Session)
	if string(after) != string(before) {
		t.Fatalf("failed live validation changed existing session")
	}
}

type blockingGaiaClient struct {
	disconnected chan struct{}
}

func (f *blockingGaiaClient) FetchConfig(context.Context) error { return nil }
func (f *blockingGaiaClient) ConfigEmail() string               { return "person@example.com" }
func (f *blockingGaiaClient) StartGaiaPairing(context.Context) (string, *libgm.PairingSession, error) {
	<-f.disconnected
	return "", nil, context.Canceled
}
func (f *blockingGaiaClient) FinishGaiaPairing(context.Context, *libgm.PairingSession) (string, error) {
	return "", nil
}
func (f *blockingGaiaClient) ValidateConnection(context.Context) error { return nil }
func (f *blockingGaiaClient) Disconnect()                              { close(f.disconnected) }

func TestAuthenticateGaiaCancellationBoundsBlockingStart(t *testing.T) {
	layout := testLayout(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &blockingGaiaClient{disconnected: make(chan struct{})}
	var gotAuth *libgm.AuthData
	started := time.Now()
	_, err := authenticateGaia(
		ctx, layout, zerolog.Nop(), testCookies(), false,
		func(string) {}, func(auth *libgm.AuthData, _ zerolog.Logger) gaiaPairingClient {
			gotAuth = auth
			return fake
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %v", elapsed)
	}
	if gotAuth == nil {
		t.Fatal("Gaia client was not created")
	}
}

func testLayout(t *testing.T) paths.Layout {
	t.Helper()
	root := t.TempDir()
	return paths.Layout{
		Root: root, Session: filepath.Join(root, "session.json"),
		Database: filepath.Join(root, "gmcli.db"), MediaDir: filepath.Join(root, "media"),
	}
}

func testCookies() map[string]string {
	return map[string]string{"SID": "sid-value", "SAPISID": "sapisid-value"}
}

func testGaiaAuth(account string) *libgm.AuthData {
	auth := libgm.NewAuthData()
	auth.Browser = &gmproto.Device{SourceID: account}
	auth.Mobile = &gmproto.Device{SourceID: account}
	auth.DestRegID = uuid.New()
	auth.PairingID = uuid.New()
	return auth
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

func testSettingsWithRCS(participantID string) *gmproto.Settings {
	settings := testSettings(participantID)
	settings.SIMCards[0].RCSChats = &gmproto.RCSChats{Enabled: true}
	return settings
}
