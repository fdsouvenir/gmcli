package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
	gmsync "github.com/fdsouvenir/gmcli/internal/sync"
)

type historyBackfillResult struct {
	ConversationID string `json:"conversation_id"`
	Requests       int    `json:"requests"`
	Count          int64  `json:"count"`
	Imported       int    `json:"imported"`
}

func historyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "history",
		Short: "Best-effort message history backfill",
		Long: "Fetch older messages for a conversation through the paired phone. " +
			"Like wacli, this is best-effort: Google may return partial history, " +
			"and the phone must be online.",
	}
	c.AddCommand(historyBackfillCmd())
	return c
}

func historyBackfillCmd() *cobra.Command {
	var chat string
	var requests int
	var count int64
	c := &cobra.Command{
		Use:   "backfill",
		Short: "Fetch older messages for one conversation",
		RunE: func(cmd *cobra.Command, args []string) error {
			if chat == "" {
				return fmt.Errorf("--chat is required")
			}
			if requests <= 0 {
				requests = 10
			}
			if count <= 0 {
				count = 50
			}
			res, err := runHistoryBackfill(chat, requests, count)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, res)
			}
			fmt.Fprintf(os.Stderr, "Backfilled %d message(s) for %s using %d request(s)\n",
				res.Imported, res.ConversationID, res.Requests)
			return nil
		},
	}
	c.Flags().StringVar(&chat, "chat", "", "conversation_id to backfill")
	c.Flags().IntVar(&requests, "requests", 10, "max history requests to make")
	c.Flags().Int64Var(&count, "count", 50, "messages to request per round")
	return c
}

func runHistoryBackfill(chat string, requests int, count int64) (historyBackfillResult, error) {
	layout, err := resolveLayout()
	if err != nil {
		return historyBackfillResult{}, err
	}
	logger := newLogger()
	ctx, cancel := signalContext(context.Background())
	defer cancel()

	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		return historyBackfillResult{}, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	client, err := gm.Open(layout, logger)
	if err != nil {
		return historyBackfillResult{}, err
	}

	pump := gmsync.New(st, logger)
	client.Subscribe(pump.Handle)

	if err := client.Connect(); err != nil {
		return historyBackfillResult{}, fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	if conv, err := client.Underlying().GetConversation(chat); err == nil && conv != nil {
		pump.Handle(conv)
	} else if _, localErr := st.GetConversation(ctx, chat); localErr != nil {
		if err != nil {
			return historyBackfillResult{}, fmt.Errorf("get conversation %s: %w", chat, err)
		}
		return historyBackfillResult{}, fmt.Errorf("conversation %s is not in the local store; run `gmcli sync` first", chat)
	}

	cursor, err := oldestCursor(ctx, st, chat)
	if err != nil {
		return historyBackfillResult{}, err
	}

	res := historyBackfillResult{ConversationID: chat, Count: count}
	for i := 0; i < requests; i++ {
		resp, err := client.Underlying().FetchMessages(chat, count, cursor)
		if err != nil {
			return res, fmt.Errorf("fetch messages: %w", err)
		}
		res.Requests++
		msgs := resp.GetMessages()
		res.Imported += pump.ImportMessages(ctx, msgs)
		next := resp.GetCursor()
		if len(msgs) == 0 || sameCursor(cursor, next) {
			break
		}
		cursor = next
	}
	return res, nil
}

func oldestCursor(ctx context.Context, st *store.Store, chat string) (*gmproto.Cursor, error) {
	msgs, err := st.ListMessages(ctx, store.ListMessageOpts{
		ConversationID: chat,
		Limit:          1,
		Order:          "asc",
	})
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return &gmproto.Cursor{
		LastItemID:        msgs[0].ID,
		LastItemTimestamp: msgs[0].TimestampMS,
	}, nil
}

func sameCursor(a, b *gmproto.Cursor) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.GetLastItemID() == b.GetLastItemID() &&
		a.GetLastItemTimestamp() == b.GetLastItemTimestamp()
}
