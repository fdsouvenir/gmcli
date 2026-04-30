package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func chatsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "chats",
		Short: "List and inspect conversations",
	}
	c.AddCommand(chatsListCmd())
	c.AddCommand(chatsShowCmd())
	return c
}

func chatsListCmd() *cobra.Command {
	var (
		limit      int
		unreadOnly bool
		pinned     bool
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List conversations, most recently active first",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			convs, err := st.ListConversations(context.Background(), store.ListConversationOpts{
				Limit:      limit,
				UnreadOnly: unreadOnly,
				Pinned:     pinned,
			})
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, convs)
			}
			if len(convs) == 0 {
				fmt.Fprintln(os.Stderr, "(no conversations)")
				return nil
			}
			rows := make([][]string, 0, len(convs))
			for _, c := range convs {
				kind := "1:1"
				if c.IsGroup {
					kind = "grp"
				}
				rows = append(rows, []string{
					output.FormatTime(c.LastMessageTimeMS),
					kind,
					boolMark(c.Unread, "*"),
					boolMark(c.Pinned, "P"),
					participantSummary(c.ParticipantsJSON),
					truncate(c.Name, 40),
					c.ID,
				})
			}
			return output.Table(os.Stdout,
				[]string{"last_msg", "kind", "unread", "pin", "participants", "name", "conv_id"},
				rows)
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "max rows")
	c.Flags().BoolVar(&unreadOnly, "unread-only", false, "only conversations with unread messages")
	c.Flags().BoolVar(&pinned, "pinned", false, "only pinned conversations")
	return c
}

func chatsShowCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "show <conversation-id>",
		Short: "Show a conversation header and its most recent messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			conv, err := st.GetConversation(ctx, args[0])
			if err != nil {
				if errors.Is(err, store.ErrNotFound) || err.Error() == "sql: no rows in result set" {
					return fmt.Errorf("no conversation with id %s", args[0])
				}
				return err
			}
			msgs, err := st.ListMessages(ctx, store.ListMessageOpts{
				ConversationID: args[0],
				Limit:          limit,
				Order:          "desc",
			})
			if err != nil {
				return err
			}
			reverseMessages(msgs)
			if flags.jsonOut {
				return output.JSON(os.Stdout, struct {
					Conversation store.Conversation `json:"conversation"`
					Messages     []store.Message    `json:"messages"`
				}{conv, msgs})
			}
			renderChatDetail(conv, msgs)
			return nil
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "max recent messages to display")
	return c
}

func reverseMessages(msgs []store.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

func renderChatDetail(c store.Conversation, msgs []store.Message) {
	fmt.Printf("conversation_id: %s\n", c.ID)
	fmt.Printf("name:            %s\n", c.Name)
	fmt.Printf("group:           %v\n", c.IsGroup)
	fmt.Printf("unread:          %v\n", c.Unread)
	fmt.Printf("pinned:          %v\n", c.Pinned)
	fmt.Printf("last_message:    %s\n", output.FormatTime(c.LastMessageTimeMS))
	fmt.Printf("participants:    %s\n", participantSummary(c.ParticipantsJSON))
	fmt.Println()
	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "(no messages)")
		return
	}
	_ = renderMessages(msgs)
}

// boolMark returns mark when b is true, else empty.
func boolMark(b bool, mark string) string {
	if b {
		return mark
	}
	return ""
}

// participantSummary parses the participants_json blob and returns a comma
// separated list of names (preferring formatted_number when name is empty).
// Best-effort — malformed JSON returns "?".
func participantSummary(js string) string {
	if js == "" || js == "[]" {
		return ""
	}
	var parts []map[string]any
	if err := json.Unmarshal([]byte(js), &parts); err != nil {
		return "?"
	}
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if v, _ := p["is_me"].(bool); v {
			continue
		}
		if name, _ := p["name"].(string); name != "" {
			names = append(names, name)
			continue
		}
		if n, _ := p["formatted_number"].(string); n != "" {
			names = append(names, n)
			continue
		}
		if n, _ := p["e164"].(string); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return truncate(out, 40)
}
