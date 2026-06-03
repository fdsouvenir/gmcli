package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/store"
)

type doctorReport struct {
	StoreRoot            string    `json:"store_root"`
	SessionExists        bool      `json:"session_exists"`
	SessionPath          string    `json:"session_path"`
	Paired               bool      `json:"paired"`
	PhoneID              string    `json:"phone_id,omitempty"`
	StoreOpens           bool      `json:"store_opens"`
	SchemaVersion        int       `json:"schema_version,omitempty"`
	Conversations        int       `json:"conversations,omitempty"`
	Messages             int       `json:"messages,omitempty"`
	Contacts             int       `json:"contacts,omitempty"`
	LastEventTime        time.Time `json:"last_event_time,omitempty"`
	LastConnectTime      time.Time `json:"last_connect_time,omitempty"`
	LastSyncActivityTime time.Time `json:"last_sync_activity_time,omitempty"`
	SendSettingsCached   bool      `json:"send_settings_cached"`
	SendSettingsSIMCount int       `json:"send_settings_sim_count,omitempty"`
	SendSettingsUpdated  time.Time `json:"send_settings_updated_at,omitempty"`
	SendSettingsDefault  *bool     `json:"send_settings_cached_default_sms_app,omitempty"`
	Issues               []string  `json:"issues,omitempty"`
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Inspect session, store, and freshness state",
		Long: "Run a non-network self-check: does the session file exist and contain " +
			"a paired device? Does the SQLite store open and report a healthy schema? " +
			"How fresh is the last sync activity?",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signalContext(context.Background())
			defer cancel()

			report := runDoctor(ctx)
			if flags.jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			renderDoctor(report)
			if len(report.Issues) > 0 {
				return fmt.Errorf("%d issue(s) detected", len(report.Issues))
			}
			return nil
		},
	}
}

func runDoctor(ctx context.Context) doctorReport {
	r := doctorReport{}
	layout, err := resolveLayout()
	if err != nil {
		r.Issues = append(r.Issues, fmt.Sprintf("resolve layout: %v", err))
		return r
	}
	r.StoreRoot = layout.Root
	r.SessionPath = layout.Session

	if _, err := os.Stat(layout.Session); err == nil {
		r.SessionExists = true
		client, err := gm.Open(layout, newLogger())
		if err != nil {
			r.Issues = append(r.Issues, fmt.Sprintf("open session: %v", err))
		} else {
			snap, err := client.AuthSnapshot()
			if err == nil && snap != nil && snap.Browser != nil {
				r.Paired = true
				r.PhoneID = snap.Mobile.GetSourceID()
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		r.Issues = append(r.Issues, "no session.json — run `gmcli auth` to pair")
	} else {
		r.Issues = append(r.Issues, fmt.Sprintf("stat session: %v", err))
	}

	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		r.Issues = append(r.Issues, fmt.Sprintf("open store: %v", err))
		return r
	}
	defer st.Close()
	r.StoreOpens = true
	if v, err := st.SchemaVersion(ctx); err == nil {
		r.SchemaVersion = v
	}

	if n, err := st.CountConversations(ctx); err == nil {
		r.Conversations = n
	}
	if n, err := st.CountMessages(ctx); err == nil {
		r.Messages = n
	}
	if n, err := st.CountContacts(ctx); err == nil {
		r.Contacts = n
	}
	if state, err := st.SyncState(ctx); err == nil {
		r.LastEventTime = state.LastEventTime
		r.LastConnectTime = state.LastConnectTime
		r.LastSyncActivityTime = state.UpdatedAt
	}
	settings, err := st.LatestPhoneSettings(ctx)
	switch {
	case err == nil:
		r.SendSettingsCached = true
		r.SendSettingsSIMCount = settings.SIMCount
		r.SendSettingsUpdated = settings.UpdatedAt
		decoded := &gmproto.Settings{}
		if err := proto.Unmarshal(settings.RawProto, decoded); err == nil && decoded.GetRCSSettings() != nil {
			isDefault := decoded.GetRCSSettings().GetIsDefaultSMSApp()
			r.SendSettingsDefault = &isDefault
		}
	case errors.Is(err, store.ErrNotFound):
		// Missing Settings/SIM metadata is not a hard send blocker; send text
		// can fall back to the legacy request shape with echo confirmation.
	case err != nil:
		r.Issues = append(r.Issues, fmt.Sprintf("read cached send settings: %v", err))
	}
	return r
}

func renderDoctor(r doctorReport) {
	fmt.Println("gmcli doctor")
	fmt.Println("============")
	fmt.Printf("  store root:       %s\n", r.StoreRoot)
	fmt.Printf("  session present:  %v\n", r.SessionExists)
	fmt.Printf("  paired:           %v\n", r.Paired)
	if r.PhoneID != "" {
		fmt.Printf("  phone id:         %s\n", r.PhoneID)
	}
	fmt.Printf("  store opens:      %v\n", r.StoreOpens)
	if r.StoreOpens {
		fmt.Printf("  schema version:   %d\n", r.SchemaVersion)
		fmt.Printf("  conversations:    %d\n", r.Conversations)
		fmt.Printf("  messages:         %d\n", r.Messages)
		fmt.Printf("  contacts:         %d\n", r.Contacts)
	}
	if r.LastEventTime.UnixMilli() > 0 {
		fmt.Printf("  last event:       %s\n", r.LastEventTime.Format(time.RFC3339))
	} else {
		fmt.Printf("  last event:       (none yet)\n")
	}
	if r.LastConnectTime.UnixMilli() > 0 {
		fmt.Printf("  last connect:     %s\n", r.LastConnectTime.Format(time.RFC3339))
	} else {
		fmt.Printf("  last connect:     (none yet)\n")
	}
	if r.LastSyncActivityTime.UnixMilli() > 0 {
		fmt.Printf("  last sync activity: %s\n", r.LastSyncActivityTime.Format(time.RFC3339))
	} else {
		fmt.Printf("  last sync activity: (none yet)\n")
	}
	fmt.Printf("  send settings cached: %v\n", r.SendSettingsCached)
	if r.SendSettingsCached {
		fmt.Printf("  send settings SIMs:   %d\n", r.SendSettingsSIMCount)
		if r.SendSettingsDefault != nil {
			fmt.Printf("  cached default SMS app: %v\n", *r.SendSettingsDefault)
		}
		if r.SendSettingsUpdated.UnixMilli() > 0 {
			fmt.Printf("  send settings updated: %s\n", r.SendSettingsUpdated.Format(time.RFC3339))
		}
	}
	if len(r.Issues) > 0 {
		fmt.Println()
		fmt.Println("Issues:")
		for _, i := range r.Issues {
			fmt.Printf("  - %s\n", i)
		}
	}
}
