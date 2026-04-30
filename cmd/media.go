package cmd

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func mediaCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "media",
		Short: "Download and manage attached media",
	}
	c.AddCommand(mediaDownloadCmd())
	return c
}

func mediaDownloadCmd() *cobra.Command {
	var msgID, outPath string
	c := &cobra.Command{
		Use:   "download",
		Short: "Download and decrypt the media attached to a message",
		Long: "Looks up the message by --message, fetches the encrypted bytes " +
			"from Google's CDN, decrypts them with the per-message key stored " +
			"during sync, and writes the result to disk. Defaults to " +
			"$STORE/media/<message_id>.<ext>; override with --out.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if msgID == "" {
				return fmt.Errorf("--message is required")
			}
			return runWithConnectedClient(func(ctx context.Context, c *gm.Client, st *store.Store) error {
				m, err := st.GetMessage(ctx, msgID)
				if err != nil {
					if errors.Is(err, store.ErrNotFound) {
						return fmt.Errorf("no message with id %s in local store; sync first", msgID)
					}
					return err
				}
				if m.MediaID == nil || *m.MediaID == "" {
					return fmt.Errorf("message %s has no attached media", msgID)
				}
				if len(m.DecryptionKey) == 0 {
					return fmt.Errorf("message %s has a media id but no decryption key (was the media stripped during sync?)", msgID)
				}

				bytes, err := c.DownloadMedia(*m.MediaID, m.DecryptionKey)
				if err != nil {
					return fmt.Errorf("download: %w", err)
				}

				path, err := resolveMediaOut(outPath, m, bytes)
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, bytes, 0o600); err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
				if flags.jsonOut {
					return output.JSON(os.Stdout, map[string]any{
						"message_id": m.ID,
						"path":       path,
						"bytes":      len(bytes),
						"mime_type":  derefOrEmpty(m.MimeType),
					})
				}
				fmt.Fprintf(os.Stderr, "Downloaded %d bytes to %s\n", len(bytes), path)
				return nil
			})
		},
	}
	c.Flags().StringVar(&msgID, "message", "", "message_id whose media should be downloaded")
	c.Flags().StringVar(&outPath, "out", "", "output path (default: $STORE/media/<message_id><ext>)")
	return c
}

// resolveMediaOut decides where to write the decrypted bytes. If --out is a
// directory, the file goes inside it with a derived name; if it's a file
// path, that's used verbatim. With no --out, the file lands in the store's
// media/ directory.
func resolveMediaOut(out string, m store.Message, _ []byte) (string, error) {
	layout, err := resolveLayout()
	if err != nil {
		return "", err
	}
	if out == "" {
		ext := extFromMime(derefOrEmpty(m.MimeType))
		return filepath.Join(layout.MediaDir, m.ID+ext), nil
	}
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		ext := extFromMime(derefOrEmpty(m.MimeType))
		return filepath.Join(out, m.ID+ext), nil
	}
	return out, nil
}

func extFromMime(mt string) string {
	if mt == "" {
		return ""
	}
	exts, err := mime.ExtensionsByType(mt)
	if err != nil || len(exts) == 0 {
		return ""
	}
	return exts[0]
}

func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
