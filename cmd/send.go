package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
	gmsync "github.com/fdsouvenir/gmcli/internal/sync"
)

// readyTimeout is how long send/react wait for the libgm session to come up
// before giving up. ClientReady normally lands within 1–3 seconds.
const readyTimeout = 30 * time.Second

func sendCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "send",
		Short: "Send messages, reactions, and inspect send readiness",
	}
	c.AddCommand(sendTextCmd())
	c.AddCommand(sendReactCmd())
	c.AddCommand(sendPreflightCmd())
	return c
}

type sendPreflightResult struct {
	Connected                bool      `json:"connected"`
	RequestedActiveSession   bool      `json:"requested_active_session"`
	PhoneDefaultSMSApp       bool      `json:"phone_default_sms_app"`
	PhoneDefaultSMSAppProbed bool      `json:"phone_default_sms_app_probed"`
	SendSettingsCached       bool      `json:"send_settings_cached"`
	SendSettingsSIMCount     int       `json:"send_settings_sim_count,omitempty"`
	SendSettingsUpdated      time.Time `json:"send_settings_updated_at,omitempty"`
	CachedSettingsDefaultSMS *bool     `json:"cached_settings_default_sms_app,omitempty"`
	SendReady                bool      `json:"send_ready"`
	Issues                   []string  `json:"issues,omitempty"`
}

func sendTextCmd() *cobra.Command {
	var to, message, replyTo, sendMode string
	c := &cobra.Command{
		Use:   "text",
		Short: "Send a text message into a conversation",
		Long: "Sends `--message` to the conversation identified by `--to` " +
			"(a conversation_id; find one with `gmcli chats list`). " +
			"Optionally `--reply-to <message_id>` to render the message as a " +
			"quoted reply. Requires `--read-only=false` to be passed at the " +
			"root since gmcli is read-only by default.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" || message == "" {
				return fmt.Errorf("--to and --message are required")
			}
			if err := requireWritable(); err != nil {
				return err
			}
			return runWithConnectedClient(func(ctx context.Context, c *gm.Client, st *store.Store) error {
				cached, err := seedCachedSendSettings(ctx, c, st)
				if err != nil {
					return err
				}
				if !cached {
					if err := c.RequestUpdates(); err != nil {
						return fmt.Errorf("request phone send settings refresh: %w", err)
					}
				}
				res, err := c.SendTextWithMode(ctx, to, message, replyTo, gm.SendMode(sendMode))
				if err != nil {
					return err
				}

				if flags.jsonOut {
					return output.JSON(os.Stdout, sendTextResultJSON(res))
				}
				fmt.Fprintf(os.Stderr, "Sent to %s (message_id %s, tmp_id %s, mode %s)\n",
					res.ConversationID, res.MessageID, res.TmpID, res.SendMode)
				return nil
			})
		},
	}
	c.Flags().StringVar(&to, "to", "", "conversation_id (find one via `gmcli chats list`)")
	c.Flags().StringVar(&message, "message", "", "message body")
	c.Flags().StringVar(&replyTo, "reply-to", "", "optional message_id to quote-reply to")
	c.Flags().StringVar(&sendMode, "send-mode", string(gm.SendModeAuto), "request shape: auto, settings, or legacy")
	return c
}

func sendTextResultJSON(res *gm.SendTextResult) map[string]any {
	return map[string]any{
		"sent":            true,
		"conversation_id": res.ConversationID,
		"message_id":      res.MessageID,
		"tmp_id":          res.TmpID,
		"send_mode":       res.SendMode,
	}
}

func sendPreflightCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "preflight",
		Short: "Check live phone state needed before sending",
		Long: "Open the paired Google Messages session and check live send readiness " +
			"without sending SMS. This command is read-only; it may refresh local " +
			"Settings/SIM metadata in the gmcli store.",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := runSendPreflight()
			if flags.jsonOut {
				if renderErr := output.JSON(os.Stdout, res); renderErr != nil {
					return renderErr
				}
			} else {
				renderSendPreflight(res)
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
}

func runSendPreflight() (sendPreflightResult, error) {
	layout, err := resolveLayout()
	if err != nil {
		return sendPreflightResult{}, err
	}
	logger := newLogger()

	ctx, cancel := signalContext(context.Background())
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 2*readyTimeout)
	defer cancelTimeout()

	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		return sendPreflightResult{}, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	res := sendPreflightResult{}
	if cached, err := st.LatestPhoneSettings(ctx); err == nil {
		res.SendSettingsCached = true
		res.SendSettingsSIMCount = cached.SIMCount
		res.SendSettingsUpdated = cached.UpdatedAt
		if cachedDefault, ok := cachedDefaultSMSApp(cached.RawProto); ok {
			res.CachedSettingsDefaultSMS = &cachedDefault
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return res, fmt.Errorf("read cached send settings: %w", err)
	}

	client, err := gm.Open(layout, logger)
	if err != nil {
		return res, err
	}
	pump := gmsync.New(st, logger)
	client.Subscribe(pump.Handle)

	if err := client.Connect(); err != nil {
		return res, fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()
	if err := waitForConnected(ctx, client); err != nil {
		return res, err
	}
	res.Connected = true
	if err := client.RequestUpdates(); err != nil {
		return res, fmt.Errorf("set active Google Messages session: %w", err)
	}
	res.RequestedActiveSession = true
	res.SendReady = true
	defaultSMS, err := client.IsDefaultSMSApp()
	if err != nil {
		res.Issues = append(res.Issues, fmt.Sprintf("default SMS app probe failed: %v", err))
	} else {
		res.PhoneDefaultSMSApp = defaultSMS
		res.PhoneDefaultSMSAppProbed = true
		if !defaultSMS {
			res.Issues = append(res.Issues, "default SMS app probe returned false; cached settings and phone UI may still show Google Messages as default")
		}
	}
	return res, nil
}

func cachedDefaultSMSApp(raw []byte) (bool, bool) {
	settings := &gmproto.Settings{}
	if err := proto.Unmarshal(raw, settings); err != nil {
		return false, false
	}
	if settings.GetRCSSettings() == nil {
		return false, false
	}
	return settings.GetRCSSettings().GetIsDefaultSMSApp(), true
}

func renderSendPreflight(res sendPreflightResult) {
	fmt.Println("gmcli send preflight")
	fmt.Println("====================")
	fmt.Printf("  connected:              %v\n", res.Connected)
	fmt.Printf("  requested active:       %v\n", res.RequestedActiveSession)
	if res.PhoneDefaultSMSAppProbed {
		fmt.Printf("  phone default SMS app:  %v\n", res.PhoneDefaultSMSApp)
	} else {
		fmt.Println("  phone default SMS app:  unknown")
	}
	fmt.Printf("  send settings cached:   %v\n", res.SendSettingsCached)
	if res.SendSettingsCached {
		fmt.Printf("  send settings SIMs:     %d\n", res.SendSettingsSIMCount)
		if res.CachedSettingsDefaultSMS != nil {
			fmt.Printf("  cached default SMS app: %v\n", *res.CachedSettingsDefaultSMS)
		}
		if res.SendSettingsUpdated.UnixMilli() > 0 {
			fmt.Printf("  send settings updated:  %s\n", res.SendSettingsUpdated.Format(time.RFC3339))
		}
	}
	fmt.Printf("  send ready:             %v\n", res.SendReady)
	if len(res.Issues) > 0 {
		fmt.Println()
		fmt.Println("Issues:")
		for _, issue := range res.Issues {
			fmt.Printf("  - %s\n", issue)
		}
	}
}

func seedCachedSendSettings(ctx context.Context, c *gm.Client, st *store.Store) (bool, error) {
	cached, err := st.LatestPhoneSettings(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load cached phone send settings: %w", err)
	}
	if cached.SIMCount <= 0 {
		return false, nil
	}
	settings := &gmproto.Settings{}
	if err := proto.Unmarshal(cached.RawProto, settings); err != nil {
		return false, fmt.Errorf("decode cached phone send settings: %w", err)
	}
	if len(settings.GetSIMCards()) == 0 {
		return false, nil
	}
	c.SetSettings(settings)
	return true, nil
}

func sendReactCmd() *cobra.Command {
	var msgID, emoji string
	var remove, switchAct bool
	c := &cobra.Command{
		Use:   "react",
		Short: "Add, remove, or switch a reaction on a message",
		RunE: func(cmd *cobra.Command, args []string) error {
			if msgID == "" || emoji == "" {
				return fmt.Errorf("--message and --emoji are required")
			}
			if remove && switchAct {
				return fmt.Errorf("--remove and --switch are mutually exclusive")
			}
			if err := requireWritable(); err != nil {
				return err
			}
			action := gm.ReactionAdd
			switch {
			case remove:
				action = gm.ReactionRemove
			case switchAct:
				action = gm.ReactionSwitch
			}
			return runWithConnectedClient(func(ctx context.Context, c *gm.Client, _ *store.Store) error {
				if err := c.SendReaction(msgID, emoji, action); err != nil {
					return err
				}
				if flags.jsonOut {
					return output.JSON(os.Stdout, map[string]any{
						"reacted":    true,
						"message_id": msgID,
						"emoji":      emoji,
					})
				}
				fmt.Fprintf(os.Stderr, "Reacted %s on %s\n", emoji, msgID)
				return nil
			})
		},
	}
	c.Flags().StringVar(&msgID, "message", "", "target message_id")
	c.Flags().StringVar(&emoji, "emoji", "", "unicode emoji to react with")
	c.Flags().BoolVar(&remove, "remove", false, "remove the reaction instead of adding it")
	c.Flags().BoolVar(&switchAct, "switch", false, "switch an existing reaction to a new emoji")
	return c
}

// runWithConnectedClient opens the store + libgm session, registers the
// sync pump (so events resulting from this operation update the DB), and
// invokes fn with both. Disconnects on return. Bounds the overall operation
// at twice readyTimeout — enough for WaitForReady plus the actual write.
func runWithConnectedClient(fn func(ctx context.Context, c *gm.Client, st *store.Store) error) error {
	layout, err := resolveLayout()
	if err != nil {
		return err
	}
	logger := newLogger()

	ctx, cancel := signalContext(context.Background())
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 2*readyTimeout)
	defer cancelTimeout()

	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		return err
	}
	defer st.Close()

	client, err := gm.Open(layout, logger)
	if err != nil {
		return err
	}

	pump := gmsync.New(st, logger)
	client.Subscribe(pump.Handle)

	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	if err := waitForConnected(ctx, client); err != nil {
		return err
	}
	if err := client.RequestUpdates(); err != nil {
		return fmt.Errorf("set active Google Messages session: %w", err)
	}
	defaultSMS, err := client.IsDefaultSMSApp()
	if err != nil {
		logger.Warn().Err(err).Msg("Default SMS app probe failed; continuing send")
	} else if !defaultSMS {
		logger.Warn().Msg("Default SMS app probe returned false; continuing send")
	}

	return fn(ctx, client, st)
}

func waitForConnected(ctx context.Context, client *gm.Client) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if client.IsConnected() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Google Messages long-poll connection: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
