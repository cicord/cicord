package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/secrets"
	"github.com/npjonath/codi/internal/state/sqlite"
)

func init() {
	extraCommands = append(extraCommands, newSecretCmd)
}

// newSecretCmd manages the runtime secret store of a data dir (bot tokens, OAuth client
// secret, git token): the setup wizard writes it, this command rotates or removes them
// afterwards. Values are read from stdin, never from arguments (shell history).
func newSecretCmd() *cobra.Command {
	var dataDir string
	open := func(ctx context.Context) (*secrets.Store, func(), error) {
		db := filepath.Join(dataDir, "codi.db")
		if _, err := os.Stat(db); err != nil {
			return nil, nil, fmt.Errorf("no runtime state in %s (use --data-dir): %w", dataDir, err)
		}
		st, err := sqlite.Open(db)
		if err != nil {
			return nil, nil, err
		}
		s, err := openSecrets(ctx, st, dataDir, false)
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		return s, func() { st.Close() }, nil
	}
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage the runtime secret store (bot tokens, OAuth and git secrets)",
		Long: `Manage the encrypted secrets the runtime keeps in its data dir (written by the setup
wizard): bot tokens (token.secretRef), CODI_DISCORD_CLIENT_SECRET, CODI_GIT_TOKEN.
Environment variables always take precedence over stored secrets. Values are read from
stdin and never printed. The runtime picks a changed bot token up on its next restart.`,
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", ".codi-data", "runtime state directory")

	cmd.AddCommand(&cobra.Command{
		Use: "ls", Short: "List stored secrets (names only)", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			s, done, err := open(c.Context())
			if err != nil {
				return err
			}
			defer done()
			ms, err := s.List(c.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tUPDATED")
			for _, m := range ms {
				fmt.Fprintf(tw, "%s\t%s\n", m.Name, m.UpdatedAt.Local().Format(time.DateTime))
			}
			return tw.Flush()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "set NAME", Short: "Store or rotate a secret (value read from stdin)", Args: cobra.ExactArgs(1),
		Example: "  codi secret set DISCORD_TOKEN_CODI --data-dir /data   # paste the token, then Enter",
		RunE: func(c *cobra.Command, args []string) error {
			if !secrets.ValidName(args[0]) {
				return fmt.Errorf("invalid secret name %q (letters, digits and _)", args[0])
			}
			line, err := bufio.NewReader(c.InOrStdin()).ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			value := strings.TrimSpace(line)
			if value == "" {
				return errors.New("empty value on stdin")
			}
			s, done, err := open(c.Context())
			if err != nil {
				return err
			}
			defer done()
			if err := s.Set(c.Context(), args[0], value); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Stored %s.\n", args[0])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "rm NAME", Short: "Delete a stored secret", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			s, done, err := open(c.Context())
			if err != nil {
				return err
			}
			defer done()
			if err := s.Delete(c.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Deleted %s.\n", args[0])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "rotate-key", Short: "Re-encrypt every secret with a new key (key file only)", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if v := os.Getenv(secrets.KeyEnv); v != "" {
				return fmt.Errorf("%s is set: rotate it yourself (re-encryption with a key you provide is not supported yet)", secrets.KeyEnv)
			}
			s, done, err := open(c.Context())
			if err != nil {
				return err
			}
			defer done()
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return err
			}
			p := filepath.Join(dataDir, secrets.KeyFile)
			tmp := p + ".new"
			if err := os.WriteFile(tmp, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
				return err
			}
			if err := s.RotateKey(c.Context(), key); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			if err := os.Rename(tmp, p); err != nil {
				return fmt.Errorf("secrets re-encrypted but the new key could not replace %s (it is in %s): %w", p, tmp, err)
			}
			fmt.Fprintln(c.OutOrStdout(), "Secrets re-encrypted with a new key.")
			return nil
		},
	})
	return cmd
}
