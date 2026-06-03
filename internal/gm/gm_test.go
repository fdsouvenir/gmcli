package gm

import (
	"context"
	"strings"
	"testing"
	"time"

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
