package cli

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/npjonath/cicord/internal/registry"
)

// Maintainer tooling of the signed official index (ADR-0011): `cicord registry keygen`,
// `cicord registry sign` and `cicord registry verify`. They work on a local checkout of a
// registry repository.

func init() {
	registrySubcommands = append(registrySubcommands, newRegistryKeygenCmd, newRegistrySignCmd, newRegistryVerifyCmd)
}

func newRegistryKeygenCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "keygen --out <file>",
		Short: "Create an ed25519 key pair to sign a registry index",
		Long: `Create a signing key for a registry index. The private key is written to --out (mode
0600, never overwritten); keep it out of any repository. The public key is printed: for
the official registry, it is the value to add to registry.OfficialKeys.`,
		Example: `  cicord registry keygen --out ~/.config/cicord/registry.key`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if out == "" {
				return errors.New("--out is required (where to write the private key)")
			}
			pub, priv, err := registry.GenerateSigningKey()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("%s already exists: refusing to overwrite a signing key", out)
			} else if err != nil {
				return err
			}
			if _, err := f.Write(priv); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Private key written to %s (keep it secret, never commit it).\n", out)
			fmt.Fprintf(w, "Key ID:     %s\n", registry.KeyID(pub))
			fmt.Fprintf(w, "Public key: %s\n", registry.EncodePublicKey(pub))
			fmt.Fprintln(w, "\nFor the official registry, embed the public key in internal/registry (OfficialKeys).")
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "file to write the private key to")
	return cmd
}

// loadSigningKey reads a private key from a file, or from an environment variable with
// "env:NAME" (the key itself is never a flag value: it would leak to ps and shell history).
func loadSigningKey(ref string) (ed25519.PrivateKey, error) {
	var data []byte
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		v, set := os.LookupEnv(name)
		if !set || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("environment variable %s is not set", name)
		}
		data = []byte(v)
	} else {
		b, err := os.ReadFile(ref)
		if err != nil {
			return nil, err
		}
		data = b
	}
	k, err := registry.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	return k, nil
}

// checkIndexForSigning parses index.yaml and refuses what a runtime would ignore or must
// never see in a signed index.
func checkIndexForSigning(data []byte) (*registry.Index, error) {
	idx, err := registry.ParseIndex(data)
	if err != nil {
		return nil, err
	}
	var problems []string
	for n, e := range idx.Plugins {
		switch {
		case !registry.NameRe.MatchString(n):
			problems = append(problems, fmt.Sprintf("%q: invalid plugin name (lowercase letters, digits and dashes)", n))
		case strings.TrimSpace(e.Repo) == "":
			problems = append(problems, fmt.Sprintf("%s: repo is required", n))
		case registry.HasUserinfo(e.Repo):
			problems = append(problems, fmt.Sprintf("%s: the repo URL contains credentials", n))
		case !registry.IsRemoteURL(e.Repo):
			problems = append(problems, fmt.Sprintf("%s: the repo must be an https:// URL", n))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, fmt.Errorf("index.yaml cannot be signed:\n  %s", strings.Join(problems, "\n  "))
	}
	return idx, nil
}

// maxSignatureValidity bounds --expires-in: a stolen old index can be replayed to a
// runtime that never saw a newer one until its signature expires.
const maxSignatureValidity = 180 * 24 * time.Hour

func newRegistrySignCmd() *cobra.Command {
	var keys []string
	var registryURL string
	var serial uint64
	var expiresIn time.Duration
	cmd := &cobra.Command{
		Use:   "sign <registry-dir> --key <file|env:NAME>",
		Short: "Sign the index.yaml of a registry checkout (writes index.yaml.sig)",
		Long: `Sign <registry-dir>/index.yaml and write the signature envelope to
<registry-dir>/index.yaml.sig. The envelope binds the registry URL (--registry-url, default
the official registry of this build), a serial that increases with every signature
(default: the current Unix time, or the previous serial + 1), an expiry (--expires-in,
default 30 days, at most 180) and the SHA-256 of index.yaml; each --key signs it with
ed25519.

Sign again after every change to index.yaml, and at least every couple of weeks (a
scheduled CI job) so that the signature never expires: runtimes refuse an expired
signature, and a serial lower than one they have seen. Commit both files together.
Repeat --key to sign with several keys (key rotation: sign with the old and the new key
until every runtime trusts the new one).

The index is checked first: plugin names, https:// repositories without credentials.`,
		Example: `  cicord registry sign . --key ~/.config/cicord/registry.key --registry-url https://github.com/cicord-dev/registry
  CICORD_REGISTRY_SIGNING_KEY=… cicord registry sign . --key env:CICORD_REGISTRY_SIGNING_KEY`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(keys) == 0 {
				return errors.New("--key is required (a key file or env:NAME)")
			}
			if registryURL == "" {
				off, ok := registry.Official()
				if !ok {
					return errors.New("--registry-url is required (this build has no official registry URL)")
				}
				registryURL = off.URL
			}
			if expiresIn <= 0 || expiresIn > maxSignatureValidity {
				return fmt.Errorf("--expires-in must be between 1s and %s", maxSignatureValidity)
			}
			dir := args[0]
			data, err := os.ReadFile(filepath.Join(dir, registry.IndexFile))
			if err != nil {
				return err
			}
			idx, err := checkIndexForSigning(data)
			if err != nil {
				return err
			}
			sigPath := filepath.Join(dir, registry.SignatureFile)
			prev := uint64(0)
			if old, err := os.ReadFile(sigPath); err == nil {
				if env, err := registry.ParseEnvelope(old); err == nil {
					prev = env.Serial
				}
			}
			switch {
			case serial == 0:
				serial = max(uint64(time.Now().Unix()), prev+1)
			case serial <= prev:
				return fmt.Errorf("--serial %d must be greater than the current serial %d", serial, prev)
			case serial > uint64(time.Now().Add(366*24*time.Hour).Unix()):
				// Runtimes remember the highest serial: a huge one would block every later signature.
				return fmt.Errorf("--serial %d is far above the current Unix time: runtimes would refuse every later signature", serial)
			}
			var privs []ed25519.PrivateKey
			var pubs []string
			for _, ref := range keys {
				k, err := loadSigningKey(ref)
				if err != nil {
					return err
				}
				privs = append(privs, k)
				pubs = append(pubs, registry.EncodePublicKey(k.Public().(ed25519.PublicKey)))
			}
			env := registry.Envelope{Registry: registryURL, Serial: serial, Expires: time.Now().Add(expiresIn)}
			sig, err := registry.SignIndex(data, env, privs...)
			if err != nil {
				return err
			}
			for _, p := range pubs { // self-check: every key's signature verifies
				if _, _, err := registry.VerifyIndex(data, sig, []string{p}, registry.VerifyOptions{Registries: []string{registryURL}}); err != nil {
					return fmt.Errorf("self-check failed: %w", err)
				}
			}
			if err := os.WriteFile(sigPath, sig, 0o644); err != nil {
				return err
			}
			signed, _ := registry.ParseEnvelope(sig)
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Signed %s (%d plugin(s)) → %s\n", filepath.Join(dir, registry.IndexFile), len(idx.Plugins), sigPath)
			fmt.Fprintf(w, "  registry %s, serial %d, expires %s\n", signed.Registry, signed.Serial, signed.Expires.Format(time.RFC3339))
			for _, p := range pubs {
				pub, _ := registry.ParsePublicKey(p)
				official := ""
				if slices.Contains(registry.OfficialKeys, p) {
					official = " (an official key of this build)"
				}
				fmt.Fprintf(w, "  key %s%s\n", registry.KeyID(pub), official)
			}
			fmt.Fprintf(w, "Commit %s and %s together; sign again before %s.\n", registry.IndexFile, registry.SignatureFile,
				signed.Expires.Format(time.DateOnly))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&keys, "key", nil, "private key: a file, or env:NAME for an environment variable (repeatable)")
	cmd.Flags().StringVar(&registryURL, "registry-url", "", "URL of the registry the index is for (default: the official registry)")
	cmd.Flags().Uint64Var(&serial, "serial", 0, "serial of this signature (default: current Unix time, always above the previous one)")
	cmd.Flags().DurationVar(&expiresIn, "expires-in", 30*24*time.Hour, "validity of the signature")
	return cmd
}

func newRegistryVerifyCmd() *cobra.Command {
	var pubs []string
	var registryURL string
	cmd := &cobra.Command{
		Use:   "verify <registry-dir>",
		Short: "Verify the signature of a registry checkout's index.yaml",
		Long: `Check <registry-dir>/index.yaml.sig against index.yaml with the official keys embedded
in this build, or with --public-key (repeatable): a signature by a trusted key, the hash
of index.yaml, the registry URL (--registry-url, default the official registry when this
build has one) and the expiry. Exit 1 otherwise, e.g. in the CI of the registry
repository.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			data, err := os.ReadFile(filepath.Join(dir, registry.IndexFile))
			if err != nil {
				return err
			}
			if _, err := checkIndexForSigning(data); err != nil {
				return err
			}
			sig, err := os.ReadFile(filepath.Join(dir, registry.SignatureFile))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			trusted := pubs
			if len(trusted) == 0 {
				trusted = registry.OfficialKeys
			}
			var urls []string
			if registryURL != "" {
				urls = []string{registryURL}
			} else if off, ok := registry.Official(); ok {
				urls = []string{off.URL}
			}
			env, id, err := registry.VerifyIndex(data, sig, trusted, registry.VerifyOptions{Registries: urls})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "✗ %v\n", err)
				return exitError{1}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ index.yaml is signed by key %s for %s (serial %d, expires %s)\n",
				id, env.Registry, env.Serial, env.Expires.Format(time.RFC3339))
			if len(urls) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "  (registry URL not checked: pass --registry-url)")
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&pubs, "public-key", nil, "trusted public key (base64; default: the official keys of this build)")
	cmd.Flags().StringVar(&registryURL, "registry-url", "", "registry URL the signature must name")
	return cmd
}
