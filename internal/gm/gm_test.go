package gm

import (
	"strings"
	"testing"

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
