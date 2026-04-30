package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func contactsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "contacts",
		Short: "Look up contacts in the local archive",
	}
	c.AddCommand(contactsSearchCmd())
	c.AddCommand(contactsShowCmd())
	c.AddCommand(contactsAliasCmd())
	return c
}

func contactsAliasCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "alias",
		Short: "Manage local-only aliases (display names) for contacts",
		Long: "Aliases override the libgm-supplied contact name in CLI output. " +
			"They are stored in the local SQLite database and are never sent to " +
			"Google. Useful for renaming contacts that arrived from your phone " +
			"with awkward names, or for labelling unsaved numbers.",
	}
	c.AddCommand(contactsAliasSetCmd())
	c.AddCommand(contactsAliasRmCmd())
	c.AddCommand(contactsAliasListCmd())
	return c
}

func contactsAliasSetCmd() *cobra.Command {
	var id, alias string
	c := &cobra.Command{
		Use:   "set",
		Short: "Set or update a contact alias",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" || alias == "" {
				return fmt.Errorf("--id and --alias are required")
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			// Allow setting an alias even if the contact has not been
			// imported yet — the user may know a participant_id from a
			// chats listing before the contact row arrives.
			if err := st.SetAlias(ctx, store.AliasContact, id, alias); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Set alias %q for %s\n", alias, id)
			return nil
		},
	}
	c.Flags().StringVar(&id, "id", "", "participant id (find via `contacts search`)")
	c.Flags().StringVar(&alias, "alias", "", "alias to display in place of the contact's name")
	return c
}

func contactsAliasRmCmd() *cobra.Command {
	var id string
	c := &cobra.Command{
		Use:   "rm",
		Short: "Remove a contact alias",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			err = st.RemoveAlias(context.Background(), store.AliasContact, id)
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("no alias set for %s", id)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Removed alias for %s\n", id)
			return nil
		},
	}
	c.Flags().StringVar(&id, "id", "", "participant id")
	return c
}

func contactsAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all local aliases",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			aliases, err := st.ListAliases(context.Background())
			if err != nil {
				return err
			}
			// Filter to contacts only since this is the contacts subtree.
			contactsOnly := make([]store.Alias, 0, len(aliases))
			for _, a := range aliases {
				if a.TargetType == store.AliasContact {
					contactsOnly = append(contactsOnly, a)
				}
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, contactsOnly)
			}
			if len(contactsOnly) == 0 {
				fmt.Fprintln(os.Stderr, "(no aliases set)")
				return nil
			}
			rows := make([][]string, 0, len(contactsOnly))
			for _, a := range contactsOnly {
				rows = append(rows, []string{a.Alias, a.TargetID, output.FormatTime(a.UpdatedAt.UnixMilli())})
			}
			return output.Table(os.Stdout, []string{"alias", "participant_id", "updated"}, rows)
		},
	}
}

func contactsSearchCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "search [query]",
		Short: "Search contacts by name or number (substring, case-insensitive)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			query := joinArgs(args)
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			hits, err := st.SearchContacts(context.Background(), query, limit)
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
				name := h.DisplayName
				if h.Alias != "" && h.Alias != h.Name {
					name = h.Alias + " (" + h.Name + ")"
				}
				rows = append(rows, []string{
					name,
					h.FormattedNumber,
					h.E164,
					h.ParticipantID,
				})
			}
			return output.Table(os.Stdout, []string{"name", "number", "e164", "participant_id"}, rows)
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "max rows")
	return c
}

func contactsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <participant-id-or-number>",
		Short: "Show one contact's full record (lookup by participant_id or phone number)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()

			c, err := st.GetContact(ctx, args[0])
			if errors.Is(err, store.ErrNotFound) {
				c, err = st.GetContactByNumber(ctx, args[0])
			}
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("no contact matching %q", args[0])
				}
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, c)
			}
			renderContactDetail(c)
			return nil
		},
	}
}

func renderContactDetail(c store.Contact) {
	fmt.Printf("name:             %s\n", c.Name)
	if c.Alias != "" {
		fmt.Printf("alias:            %s\n", c.Alias)
	}
	fmt.Printf("participant_id:   %s\n", c.ParticipantID)
	if c.ContactID != "" {
		fmt.Printf("contact_id:       %s\n", c.ContactID)
	}
	if c.E164 != "" {
		fmt.Printf("e164:             %s\n", c.E164)
	}
	if c.FormattedNumber != "" {
		fmt.Printf("formatted_number: %s\n", c.FormattedNumber)
	}
	if c.AvatarColor != "" {
		fmt.Printf("avatar_color:     %s\n", c.AvatarColor)
	}
	if c.IsMe {
		fmt.Println("self:             true")
	}
}
