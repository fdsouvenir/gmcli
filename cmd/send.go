package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

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
		Short: "Send messages and reactions (requires --read-only=false)",
	}
	c.AddCommand(sendTextCmd())
	c.AddCommand(sendReactCmd())
	return c
}

func sendTextCmd() *cobra.Command {
	var to, message, replyTo string
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
			return runWithConnectedClient(func(ctx context.Context, c *gm.Client, _ *store.Store) error {
				res, err := c.SendText(ctx, to, message, replyTo)
				if err != nil {
					return err
				}

				if flags.jsonOut {
					return output.JSON(os.Stdout, map[string]any{
						"sent":            true,
						"conversation_id": res.ConversationID,
						"message_id":      res.MessageID,
						"tmp_id":          res.TmpID,
					})
				}
				fmt.Fprintf(os.Stderr, "Sent to %s (message_id %s, tmp_id %s)\n",
					res.ConversationID, res.MessageID, res.TmpID)
				return nil
			})
		},
	}
	c.Flags().StringVar(&to, "to", "", "conversation_id (find one via `gmcli chats list`)")
	c.Flags().StringVar(&message, "message", "", "message body")
	c.Flags().StringVar(&replyTo, "reply-to", "", "optional message_id to quote-reply to")
	return c
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

	ready := make(chan error, 1)
	go func() { ready <- client.WaitForReady(ctx) }()

	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	select {
	case err := <-ready:
		if err != nil {
			return fmt.Errorf("wait for ready: %w", err)
		}
	default:
		// Some libgm paths return from Connect after the ready event has
		// already fired. In that case the session is usable.
	}

	return fn(ctx, client, st)
}
