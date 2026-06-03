package gm

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
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

	req, err := c.buildSettingsSendTextRequest("conv-1", "hello", "reply-1", "tmp-1")
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

	req, err := c.buildSettingsSendTextRequest("conv-1", "hello", "", "tmp-1")
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

	req, err := c.buildSettingsSendTextRequest("conv-1", "hello", "", "tmp-1")
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

	req, err := c.buildSettingsSendTextRequest("conv-1", "hello", "", "tmp-1")
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

	if _, err := c.buildSettingsSendTextRequest("conv-1", "hello", "", "tmp-1"); err == nil {
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
