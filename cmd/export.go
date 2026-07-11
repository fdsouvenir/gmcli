package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/archive"
	"github.com/fdsouvenir/gmcli/internal/output"
)

func exportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "export",
		Short: "Export the local archive to portable formats",
	}
	c.AddCommand(exportJSONCmd())
	return c
}

func exportJSONCmd() *cobra.Command {
	var out string
	var force bool
	c := &cobra.Command{
		Use:   "json",
		Short: "Export conversations, messages, contacts, and aliases as JSON",
		Long: "Writes one portable JSON document containing the complete local archive. " +
			"The export excludes media decryption keys and raw protocol buffers; downloaded media files are not embedded.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return fmt.Errorf("--out is required")
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			result, err := archive.WriteJSON(cmd.Context(), st, out, force)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, result)
			}
			fmt.Fprintf(os.Stderr, "Exported %d conversations, %d messages, %d contacts, and %d aliases to %s\n",
				result.Conversations, result.Messages, result.Contacts, result.Aliases, result.Path)
			return nil
		},
	}
	c.Flags().StringVar(&out, "out", "", "destination JSON file (required)")
	c.Flags().BoolVar(&force, "force", false, "replace an existing output file")
	return c
}
