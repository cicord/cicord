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

	"github.com/cicord/cicord/internal/secrets"
)

func init() {
	extraCommands = append(extraCommands, newSecretCmd)
}

// newSecretCmd manages the runtime secret store of a data dir (bot tokens, OAuth client
// secret, git token): the setup wizard writes it, this command rotates or removes them
// afterwards. Values are read from stdin, never from arguments (shell history).
func newSecretCmd() *cobra.Command {
	var dataDir, dbFlag string
	open := func(ctx context.Context) (*secrets.Store, func(), error) {
		return openDataDirSecrets(ctx, dataDir, dbFlag)
	}
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage the runtime secret store (bot tokens, OAuth and git secrets)",
		Long: `Manage the encrypted secrets the runtime keeps in its state store (written by the setup
wizard): bot tokens (token.secretRef), CICORD_DISCORD_CLIENT_SECRET, CICORD_GIT_TOKEN, the
GitHub App (CICORD_GITHUB_APP_ID, CICORD_GITHUB_APP_PRIVATE_KEY, CICORD_GITHUB_APP_INSTALLATION_ID).
The store is the data dir's SQLite database, or the PostgreSQL database of --database-url
(default $CICORD_DATABASE_URL); the encryption key is CICORD_SECRET_KEY or the data dir's
secret.key either way. Environment variables always take precedence over stored secrets.
Values are read from stdin and never printed. The runtime picks a changed bot token up on
its next restart.`,
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", ".cicord-data", "runtime state directory (secret.key; cicord.db unless PostgreSQL is used)")
	cmd.PersistentFlags().StringVar(&dbFlag, "database-url", "", databaseURLFlagHelp)

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
		Long: `Store or rotate a secret. The value is read from stdin: one line (paste, then Enter), or a
whole PEM block (a GitHub App private key: from its BEGIN line through its END line).`,
		Example: "  cicord secret set DISCORD_TOKEN_CICORD --data-dir /data   # paste the token, then Enter\n" +
			"  cicord secret set CICORD_GITHUB_APP_PRIVATE_KEY --data-dir /data < my-app.private-key.pem",
		RunE: func(c *cobra.Command, args []string) error {
			if !secrets.ValidName(args[0]) {
				return fmt.Errorf("invalid secret name %q (letters, digits and _)", args[0])
			}
			value, err := readSecretValue(c.InOrStdin())
			if err != nil {
				return err
			}
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

// openDataDirSecrets opens the secret store of an existing runtime state: the SQLite
// database of dataDir, or the PostgreSQL database of dbFlag / $CICORD_DATABASE_URL, with
// the key of dataDir (or CICORD_SECRET_KEY). It never creates a state or a key.
func openDataDirSecrets(ctx context.Context, dataDir, dbFlag string) (*secrets.Store, func(), error) {
	dbURL := databaseURL(dbFlag)
	if dbURL == "" {
		// SQLite: the database must already exist (never create one in a mistyped dir).
		if _, err := os.Stat(filepath.Join(dataDir, stateDBFile)); err != nil {
			return nil, nil, fmt.Errorf("no runtime state in %s (use --data-dir, or --database-url / %s for PostgreSQL): %w", dataDir, databaseURLEnv, err)
		}
	} else if os.Getenv(secrets.KeyEnv) == "" {
		// PostgreSQL: the encryption key still lives in the data dir (or in
		// CICORD_SECRET_KEY); never generate a new one here, it would not
		// decrypt the stored secrets.
		if _, err := os.Stat(filepath.Join(dataDir, secrets.KeyFile)); err != nil {
			return nil, nil, fmt.Errorf("no %s in %s (use the runtime's --data-dir, or set %s): %w", secrets.KeyFile, dataDir, secrets.KeyEnv, err)
		}
	}
	st, err := openStateStore(ctx, nil, dataDir, dbURL)
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

// maxSecretValue bounds a value read from stdin (a 4096-bit PEM key is ~3.3 KiB).
const maxSecretValue = 64 << 10

// readSecretValue reads a secret value from stdin: the first line, or, when that line
// opens a PEM block ("-----BEGIN …"), every line through the matching "-----END …"
// line (or EOF). The value is trimmed.
func readSecretValue(in io.Reader) (string, error) {
	r := bufio.NewReader(io.LimitReader(in, maxSecretValue+1))
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	first := strings.TrimSpace(line)
	if !strings.HasPrefix(first, "-----BEGIN ") || errors.Is(err, io.EOF) {
		if len(line) > maxSecretValue {
			return "", errors.New("value too large")
		}
		return first, nil
	}
	var b strings.Builder
	b.WriteString(first)
	b.WriteByte('\n')
	for {
		line, err := r.ReadString('\n')
		if t := strings.TrimSpace(line); t != "" {
			b.WriteString(t)
			b.WriteByte('\n')
			if strings.HasPrefix(t, "-----END ") {
				break
			}
		}
		if b.Len() > maxSecretValue {
			return "", errors.New("value too large")
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(b.String()), nil
}
