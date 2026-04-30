package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/store"
	gmsync "github.com/fdsouvenir/gmcli/internal/sync"
)

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
			select {
			case <-ctx.Done():
				fmt.Fprintln(os.Stderr, "Disconnecting...")
				return nil
			case err := <-pump.Fatal():
				return err
			}
		},
	}
	c.Flags().BoolVar(&follow, "follow", false, "stay connected and stream events until interrupted")
	return c
}
