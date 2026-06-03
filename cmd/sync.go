package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
	gmsync "github.com/fdsouvenir/gmcli/internal/sync"
)

const syncHeartbeatInterval = 5 * time.Minute
const sendSettingsRefreshTimeout = 60 * time.Second

type sendSettingsRefreshClient interface {
	Subscribe(gm.EventHandler)
	Connect() error
	Disconnect()
	IsConnected() bool
	RequestUpdates() error
	WaitForSettings(context.Context) error
}

type sendSettingsRefreshResult struct {
	RequestedRefresh bool      `json:"requested_refresh"`
	SettingsReceived bool      `json:"settings_received"`
	SendReady        bool      `json:"send_ready"`
	CachedBefore     bool      `json:"cached_before"`
	CachedAfter      bool      `json:"cached_after"`
	SIMCount         int       `json:"sim_count,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
	TimeoutSeconds   int       `json:"timeout_seconds"`
	Issues           []string  `json:"issues,omitempty"`
}

func syncCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "sync",
		Short: "Connect to Google Messages and write events into the local store",
		Long: "Open the long-poll connection to your paired phone and persist incoming " +
			"conversations, messages, and contacts into the SQLite store. With --follow, " +
			"the connection stays open until interrupted; without it, the command runs the " +
			"initial-sync pass and exits.",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			logger := newLogger()

			ctx, cancel := signalContext(context.Background())
			defer cancel()

			st, err := store.Open(ctx, layout.Database)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer st.Close()

			client, err := gm.Open(layout, logger)
			if err != nil {
				return err
			}

			pump := gmsync.New(st, logger)
			client.Subscribe(pump.Handle)

			fmt.Fprintln(os.Stderr, "Connecting to Google Messages relay...")
			if err := client.Connect(); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer client.Disconnect()

			if resp, err := client.Underlying().ListContacts(); err != nil {
				logger.Warn().Err(err).Msg("Contact import failed")
			} else {
				imported := pump.ImportContacts(ctx, resp.GetContacts())
				logger.Info().Int("contacts", imported).Msg("Imported contacts")
			}

			if resp, err := client.Underlying().ListConversations(50, gmproto.ListConversationsRequest_INBOX); err != nil {
				logger.Warn().Err(err).Msg("Conversation import failed")
			} else {
				convs, msgs := 0, 0
				for _, conv := range resp.GetConversations() {
					if conv == nil || conv.GetConversationID() == "" {
						continue
					}
					pump.Handle(conv)
					convs++
					if history, err := client.Underlying().FetchMessages(conv.GetConversationID(), 10, nil); err != nil {
						logger.Debug().Err(err).Str("conversation_id", conv.GetConversationID()).Msg("Recent message import failed")
					} else {
						msgs += pump.ImportMessages(ctx, history.GetMessages())
					}
				}
				logger.Info().Int("conversations", convs).Int("messages", msgs).Msg("Imported recent conversation history")
			}

			if !follow {
				select {
				case err := <-pump.Fatal():
					return err
				default:
				}
				fmt.Fprintln(os.Stderr, "Initial sync complete. Pass --follow to stay connected.")
				return nil
			}

			fmt.Fprintln(os.Stderr, "Connected. Streaming events. Ctrl-C to stop.")
			heartbeat := time.NewTicker(syncHeartbeatInterval)
			defer heartbeat.Stop()
			for {
				select {
				case <-ctx.Done():
					fmt.Fprintln(os.Stderr, "Disconnecting...")
					return nil
				case err := <-pump.Fatal():
					return err
				case <-heartbeat.C:
					if err := st.TouchSync(ctx); err != nil {
						logger.Debug().Err(err).Msg("sync heartbeat failed")
					}
				}
			}
		},
	}
	c.Flags().BoolVar(&follow, "follow", false, "stay connected and stream events until interrupted")
	c.AddCommand(syncSendSettingsCmd())
	return c
}

func syncSendSettingsCmd() *cobra.Command {
	timeout := sendSettingsRefreshTimeout
	c := &cobra.Command{
		Use:   "send-settings",
		Short: "Request phone send settings and report send readiness",
		Long: "Open the paired Google Messages session, request a send-settings refresh " +
			"from the phone, and wait for real Settings/SIM metadata. This is a read-only " +
			"network diagnostic: it updates only gmcli's local cache and never sends SMS.",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			logger := newLogger()

			ctx, cancel := signalContext(context.Background())
			defer cancel()

			st, err := store.Open(ctx, layout.Database)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer st.Close()

			client, err := gm.Open(layout, logger)
			if err != nil {
				return err
			}

			res, err := runSendSettingsRefresh(ctx, client, st, logger, timeout)
			if flags.jsonOut {
				if renderErr := output.JSON(os.Stdout, res); renderErr != nil {
					return renderErr
				}
			} else {
				renderSendSettingsRefresh(res)
			}
			if err != nil {
				return err
			}
			if !res.SendReady {
				return fmt.Errorf("send settings/SIM metadata not ready")
			}
			return nil
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", sendSettingsRefreshTimeout, "maximum time to wait for Settings/SIM metadata")
	return c
}

func runSendSettingsRefresh(ctx context.Context, client sendSettingsRefreshClient, st *store.Store, logger zerolog.Logger, timeout time.Duration) (sendSettingsRefreshResult, error) {
	res := sendSettingsRefreshResult{
		TimeoutSeconds: int(timeout.Round(time.Second) / time.Second),
	}
	if cached, err := st.LatestPhoneSettings(ctx); err == nil {
		res.CachedBefore = true
		res.SIMCount = cached.SIMCount
		res.UpdatedAt = cached.UpdatedAt
	} else if !errors.Is(err, store.ErrNotFound) {
		return res, fmt.Errorf("read cached send settings: %w", err)
	}

	pump := gmsync.New(st, logger)
	client.Subscribe(pump.Handle)

	if err := client.Connect(); err != nil {
		return res, fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	if err := waitForRefreshClientConnected(ctx, client); err != nil {
		return res, err
	}

	if err := client.RequestUpdates(); err != nil {
		return res, fmt.Errorf("request phone send settings refresh: %w", err)
	}
	res.RequestedRefresh = true

	waitCtx, cancelWait := context.WithTimeout(ctx, timeout)
	err := client.WaitForSettings(waitCtx)
	cancelWait()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			res.Issues = append(res.Issues, fmt.Sprintf("no Settings/SIM metadata received within %s", timeout))
		} else {
			return res, fmt.Errorf("wait for phone send settings: %w", err)
		}
	} else {
		res.SettingsReceived = true
	}

	if settings, err := st.LatestPhoneSettings(ctx); err == nil {
		res.CachedAfter = true
		res.SIMCount = settings.SIMCount
		res.UpdatedAt = settings.UpdatedAt
		res.SendReady = settings.SIMCount > 0
		if res.SettingsReceived && !res.SendReady {
			res.Issues = append(res.Issues, "Settings received, but it contained no SIM cards")
		}
	} else if errors.Is(err, store.ErrNotFound) {
		res.Issues = append(res.Issues, "no cached send settings/SIM metadata available")
	} else {
		return res, fmt.Errorf("read refreshed send settings: %w", err)
	}

	select {
	case err := <-pump.Fatal():
		return res, err
	default:
	}
	return res, nil
}

func waitForRefreshClientConnected(ctx context.Context, client sendSettingsRefreshClient) error {
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

func renderSendSettingsRefresh(res sendSettingsRefreshResult) {
	fmt.Println("gmcli sync send-settings")
	fmt.Println("========================")
	fmt.Printf("  requested refresh: %v\n", res.RequestedRefresh)
	fmt.Printf("  settings received: %v\n", res.SettingsReceived)
	fmt.Printf("  send ready:        %v\n", res.SendReady)
	fmt.Printf("  cached before:     %v\n", res.CachedBefore)
	fmt.Printf("  cached after:      %v\n", res.CachedAfter)
	fmt.Printf("  SIM count:         %d\n", res.SIMCount)
	if res.UpdatedAt.UnixMilli() > 0 {
		fmt.Printf("  updated:           %s\n", res.UpdatedAt.Format(time.RFC3339))
	}
	if len(res.Issues) > 0 {
		fmt.Println()
		fmt.Println("Issues:")
		for _, issue := range res.Issues {
			fmt.Printf("  - %s\n", issue)
		}
	}
}
