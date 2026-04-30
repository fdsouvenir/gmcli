package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func messagesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "messages",
		Short: "Query messages in the local archive",
	}
	c.AddCommand(messagesListCmd())
	c.AddCommand(messagesSearchCmd())
	c.AddCommand(messagesShowCmd())
	c.AddCommand(messagesContextCmd())
	return c
}

func messagesListCmd() *cobra.Command {
	var (
		convID, sender     string
		sinceStr, untilStr string
		limit              int
		order              string
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List messages, most recent first by default",
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := parseFlagTime(sinceStr)
			if err != nil {
				return fmt.Errorf("--since: %w", err)
			}
			until, err := parseFlagTime(untilStr)
			if err != nil {
				return fmt.Errorf("--until: %w", err)
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			msgs, err := st.ListMessages(context.Background(), store.ListMessageOpts{
				ConversationID: convID,
				SenderID:       sender,
				Since:          since,
				Until:          until,
				Limit:          limit,
				Order:          order,
			})
			if err != nil {
				return err
			}
			return renderMessages(msgs)
		},
	}
	c.Flags().StringVar(&convID, "conv", "", "filter by conversation id")
	c.Flags().StringVar(&sender, "from", "", "filter by sender participant id")
	c.Flags().StringVar(&sinceStr, "since", "", "lower time bound (YYYY-MM-DD or RFC3339)")
	c.Flags().StringVar(&untilStr, "until", "", "upper time bound (YYYY-MM-DD or RFC3339)")
	c.Flags().IntVar(&limit, "limit", 50, "max rows")
	c.Flags().StringVar(&order, "order", "desc", "asc or desc")
	return c
}

func messagesSearchCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search messages (FTS5 syntax)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := joinArgs(args)
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			hits, err := st.SearchMessages(context.Background(), query, limit)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, hits)
			}
			if len(hits) == 0 {
				fmt.Fprintln(os.Stderr, "(no matches)")
				return nil
			}
			rows := make([][]string, 0, len(hits))
			for _, h := range hits {
				dir := "<-"
				if h.IsFromMe {
					dir = "->"
				}
				rows = append(rows, []string{
					output.FormatTime(h.TimestampMS),
					dir,
					h.MessageID,
					h.ConversationID,
					truncate(h.Snippet, 80),
				})
			}
			return output.Table(os.Stdout, []string{"time", "dir", "msg_id", "conv_id", "snippet"}, rows)
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "max rows")
	return c
}

func messagesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <message-id>",
		Short: "Show a single message in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			m, err := st.GetMessage(context.Background(), args[0])
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("no message with id %s", args[0])
				}
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, m)
			}
			renderMessageDetail(m)
			return nil
		},
	}
}

func messagesContextCmd() *cobra.Command {
	var before, after int
	c := &cobra.Command{
		Use:   "context <message-id>",
		Short: "Show neighboring messages around a message id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			msgs, err := st.GetMessageContext(context.Background(), args[0], before, after)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("no message with id %s", args[0])
				}
				return err
			}
			return renderMessages(msgs)
		},
	}
	c.Flags().IntVar(&before, "before", 5, "messages before the anchor")
	c.Flags().IntVar(&after, "after", 5, "messages after the anchor")
	return c
}

// renderMessages prints a message slice as a table or JSON.
func renderMessages(msgs []store.Message) error {
	if flags.jsonOut {
		return output.JSON(os.Stdout, msgs)
	}
	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "(no messages)")
		return nil
	}
	rows := make([][]string, 0, len(msgs))
	for _, m := range msgs {
		dir := "<-"
		if m.IsFromMe {
			dir = "->"
		}
		body := ""
		if m.Body != nil {
			body = *m.Body
		} else if m.MimeType != nil {
			body = "[" + *m.MimeType + "]"
		}
		rows = append(rows, []string{
			output.FormatTime(m.TimestampMS),
			dir,
			m.SenderID,
			m.ID,
			truncate(body, 80),
		})
	}
	return output.Table(os.Stdout, []string{"time", "dir", "sender", "msg_id", "body"}, rows)
}

func renderMessageDetail(m store.Message) {
	fmt.Printf("message_id:      %s\n", m.ID)
	fmt.Printf("conversation_id: %s\n", m.ConversationID)
	fmt.Printf("sender:          %s\n", m.SenderID)
	fmt.Printf("from_me:         %v\n", m.IsFromMe)
	fmt.Printf("timestamp:       %s\n", output.FormatTime(m.TimestampMS))
	fmt.Printf("status:          %d\n", m.Status)
	if m.ReplyToID != nil {
		fmt.Printf("reply_to:        %s\n", *m.ReplyToID)
	}
	if m.MediaID != nil {
		fmt.Printf("media_id:        %s\n", *m.MediaID)
		if m.MimeType != nil {
			fmt.Printf("mime_type:       %s\n", *m.MimeType)
		}
	}
	if m.ReactionsJSON != nil {
		fmt.Printf("reactions:       %s\n", *m.ReactionsJSON)
	}
	fmt.Println()
	if m.Body != nil {
		fmt.Println(*m.Body)
	} else {
		fmt.Println("(no text body)")
	}
}

// parseFlagTime accepts "" (zero), a YYYY-MM-DD date, or any RFC3339 datetime
// in the local timezone for date-only inputs.
func parseFlagTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q (try YYYY-MM-DD or RFC3339)", s)
}

// joinArgs concatenates trailing positional args with spaces — handy when
// a user types `gmcli messages search dinner tonight` instead of quoting.
func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
