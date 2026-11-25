package catalog

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
)

// Updating bundled plugins: the Store copies a catalog plugin to plugins/<name>/ of the
// repository (a local plugin). A newer binary may bundle a newer version; the copy can be
// updated when it was not modified locally since it was copied.
//
// The binary only holds the current version of each plugin. A copy is known unmodified
// when its content hash is the one of a released version of that plugin (released, below:
// every version ever bundled, kept by TestReleasedIntegrity), or when it only differs from
// the current version by the version field of plugin.yaml.

// released maps plugin → version → integrities (registry.Integrity of PluginFiles) of
// every version bundled in a Cicord build. When the files of a bundled plugin change, add
// the new integrity to its version (keep the old ones: copies made by earlier builds stay
// recognized); TestReleasedIntegrity fails and prints the value until it is recorded.
var released = map[string]map[string][]string{
	"welcome": {"0.1.0": {
		"sha256-Vc5USPwpH2u+lnQSZpc7phHFxtp6xb5jmy/BFXbVPJ8=",
		"sha256-IhSR6HcCZguQ2/K/IBs/QDYn80hXjSlOw9ylu8ySBT0=", // rename to Cicord (@cicord/sdk imports)
		"sha256-Qu8O4XciIsmOm++fjfDAgTVtduweA/ZZ0+tiv8jPG1c=", // lint fixes (oxlint, knip), lib ES2023
	}, "0.1.1": {
		"sha256-5XF4vJJotjSWOCOStlDuA5Dcfj/tFaSGQ9OZLKlsjxo=", // English DM, button and /welcome-stats reply; strict tsconfig like the registry
	}},
	"onboarding": {"0.1.0": {
		"sha256-MKVQjq2L+jA2skWMdzZCUozF1n3y59TwnxOVjEPuqNg=",
		"sha256-LS5IHZLOt50r8S/bK9NvuqPLHf/xuzGchTXQMDRx/0A=", // rename to Cicord (@cicord/sdk imports)
		"sha256-KRr1yFLgezY7ukj+ajgG+IpGRNRjM0eWd07vyK1r3A8=", // lint fixes (oxlint, knip), lib ES2023
	}},
	"tickets": {"0.1.0": {
		"sha256-L5+NgFlgHE/P0n5n8GyrW+DumWbmKl+n5jRpVWNrOP8=",
		"sha256-UzOujz9ctRAfYIJ3JtqJCqRC3A6lgzhX35cCk0eW3Ao=", // rename to Cicord (@cicord/sdk imports)
		"sha256-/cNsyw6flhPR1uZWzMn9E8jhud2cSnwSbEfR/7x5J5Y=", // lint fixes (oxlint, knip), lib ES2023
	}, "0.2.0": {
		"sha256-M4KXV5edLR6OyUjOJnpOAIWTVZrIPjbfyAOJx9RYqAI=", // the registry's 0.2.0: member and staff portal routes, labels
	}, "0.2.1": {
		"sha256-WdDIfvxP78ZUa1AyWc9KNJxPgnq3xJZMBvx1zPtLNus=", // lint fixes (toSorted)
	}},
	"polls": {"0.1.0": {
		"sha256-J2By/eEG8diCTxf6xekJyQyBzyn+qX9wskGmRmVptBs=",
		"sha256-innGAtvVzpodj1AtwyVMX5tTMbc4AM0WSw8g/khOXoA=", // route contract (src/routes.ts)
		"sha256-5F37/61i51+mE/n9ViqJwZdGiBpxquwWt/xEIYp1nes=", // rename to Cicord (@cicord/sdk imports)
		"sha256-dxKi3tKzTsaGZpeLiiC9fex04g9EZ7qcrgg3gXNvl/Y=", // lint fixes (oxlint, knip), lib ES2023
		"sha256-aZzFGaEkov/ESA89DumEb0g8mPk7ojxp9ZPtJ5/usVA=", // channel.message(), /polls close|results (subcommands, autocomplete)
	}, "0.1.1": {
		"sha256-gJyGVv248zITLY9WHCxWWGM4fIbSXTFL6wqFkCeIH40=", // ping: non-mentionable roles for managers only, allowedMentions limited to the pinged member or role
	}},
	"levels": {"0.1.0": {
		"sha256-FrdXmmfH7j5m0hN6XwwyttgOVU1cBvNlLmXW4vZrBO0=",
		"sha256-Phlqcqh1igaLpy2gCf7Jd/uSblJxNSU+G8b1JpchAUY=", // route contract (src/routes.ts)
		"sha256-bkc96UvDhOJ0R1Kq3oZHn7jS4adb3meFfvi2WIaPTvk=", // rename to Cicord (@cicord/sdk imports)
		"sha256-KKOyIRgrLuUFfztVK/QkgqRC0jzGA6iDMus5eAblp8Y=", // lint fixes (oxlint, knip), lib ES2023
	}},
	"moderation": {"0.1.0": {
		"sha256-JYpd9sY6f38cc9ZI3APfJX6LYab0MGuBUcURYXyjx2A=",
		"sha256-ciiRRkOU3+n/3+Cs1xlgz4LV4QsiA/1uVlhvo/G7RJ0=", // publishes moderation.case
		"sha256-D1hAQQTYTdf9hUkDmK6Xph51WWwD9gsmZPN6PYN4jFM=", // rename to Cicord (@cicord/sdk imports)
		"sha256-FxEiSXHt/uierLV2QinTEQyoAKklCyw+9PZNs8V+V8c=", // lint fixes (oxlint, knip), lib ES2023
	}, "0.1.1": {
		"sha256-A6Dxp2+J6UqtZzXahnH63CqMc5Ck9chchv9MCmMBAIA=", // invoker's role hierarchy, owner and staff checks
	}},
	"reaction-roles": {"0.1.0": {
		"sha256-QdfT6KQQqvWVZqz8jX1DVgDkm37ZCkG5BVzECErUDCw=",
		"sha256-LYyxabLe8FGcnQMgyAmsAnMbyPYicoJDvZSG7KHeiNY=", // rename to Cicord (@cicord/sdk imports)
		"sha256-Gs1W7MLFa0O9He5W6DMMOBInDtQQ01Ihel0V8qxXj/Q=", // lint fixes (oxlint, knip), lib ES2023
		"sha256-8X7CpCN3vtquk22aCr4v8kMeLS2fRAGguYFsiBSrySo=", // channel.message()
	}, "0.1.1": {
		"sha256-nANu7psYPMYoORK1gMGvnZMvMjkdKRyAQvPFjNoAKRA=", // same code as 0.1.0 above (channel.message()), released under 0.1.1 to match the registry
	}},
	"audit-log": {"0.1.0": {
		"sha256-CqUKC4C6sA9vNJBI0tClq77TpE8X7PgJGsq0Jyz1YjQ=",
		"sha256-/DS7OjfqTtK3XZ58P7BVQ0CUMV1c2mfCBOp8Jb6Bo8M=", // rename to Cicord (@cicord/sdk imports)
		"sha256-5jcvRwAWq2fFzlElZFvsy8l0tVCXZkH4lHYYb75BDf4=", // lint fixes (oxlint, knip), lib ES2023
	}},
	"announcements": {"0.1.0": {
		"sha256-2FUTA2TWaOuUgM7FFhDbaYT41VFR1QW5LLJdXNWDuJk=",
		"sha256-NozhyJgHRpaiIgOR/UK4XR/sbuUX2DBrz5oIF6OnWo0=", // rename to Cicord (@cicord/sdk imports)
		"sha256-DrtE6XadcqWmQCFnNizgQoMsgjJIlGo6l/yFJjpv8Zo=", // lint fixes (oxlint, knip), lib ES2023
	}},
	"voice-rooms": {"0.1.0": {
		"sha256-uL6pZTiV73przMhhxvze+RBz3/K9t6nGCK9/vanjXV4=", // join-to-create voice rooms (voiceStateUpdate, editChannel, voiceMembers, moveTo)
	}},
}

// PluginFiles returns the files of a catalog plugin relative to its directory (what an
// install writes under plugins/<name>/).
func (c *Catalog) PluginFiles(name string) (map[string][]byte, error) {
	e, ok := c.Get(name)
	if !ok {
		return nil, errors.New("unknown catalog plugin " + name)
	}
	return c.files(e.dir)
}

// Integrity is the content hash of the current version of a catalog plugin.
func (c *Catalog) Integrity(name string) (string, error) {
	files, err := c.PluginFiles(name)
	if err != nil {
		return "", err
	}
	return registry.Integrity(files), nil
}

// CopyStatus describes a copy of a catalog plugin in a repository.
type CopyStatus struct {
	// Version is the version in the copy's plugin.yaml.
	Version string
	// Modified is true when the copy is neither a released version of the plugin nor the
	// current one with another version number: updating it would overwrite local changes
	// (or changes that cannot be told apart from them).
	Modified bool
	// Changed lists the files the update rewrites, Added the files it creates and Removed
	// the files of the copy it deletes (plugin-relative, sorted).
	Changed, Added, Removed []string
}

// CheckCopy compares the copy of plugin name in dir (plugins/<name> of a checkout) with
// the current catalog version.
func (c *Catalog) CheckCopy(name, dir string) (CopyStatus, error) {
	cur, err := c.PluginFiles(name)
	if err != nil {
		return CopyStatus{}, err
	}
	copied, err := ReadDir(dir)
	if err != nil {
		return CopyStatus{}, err
	}
	st := CopyStatus{Version: manifestVersion(copied["plugin.yaml"])}
	for p, data := range cur {
		old, ok := copied[p]
		switch {
		case !ok:
			st.Added = append(st.Added, p)
		case !bytes.Equal(old, data):
			st.Changed = append(st.Changed, p)
		}
	}
	for p := range copied {
		if _, ok := cur[p]; !ok {
			st.Removed = append(st.Removed, p)
		}
	}
	slices.Sort(st.Added)
	slices.Sort(st.Changed)
	slices.Sort(st.Removed)
	st.Modified = !isReleased(name, st.Version, registry.Integrity(copied)) && !onlyVersionDiffers(copied, cur)
	return st, nil
}

func isReleased(name, version, integrity string) bool {
	return slices.Contains(released[name][version], integrity)
}

// onlyVersionDiffers: same files, same content except metadata.version of plugin.yaml.
func onlyVersionDiffers(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for p, da := range a {
		db, ok := b[p]
		if !ok {
			return false
		}
		if p == "plugin.yaml" {
			da, db = versionRe.ReplaceAll(da, []byte("${1}"+"x")), versionRe.ReplaceAll(db, []byte("${1}"+"x"))
		}
		if !bytes.Equal(da, db) {
			return false
		}
	}
	return true
}

// versionRe matches metadata.version in a plugin.yaml (a two-space indented key).
var versionRe = regexp.MustCompile(`(?m)^(  version:[ \t]*)\S.*$`)

func manifestVersion(data []byte) string {
	var m struct {
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if len(data) == 0 || config.DecodeYAML(data, &m) != nil {
		return ""
	}
	return m.Metadata.Version
}

// ReadDir reads the regular files under dir (slash-separated relative paths). A missing
// directory gives an empty map.
func ReadDir(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || (d.Name() == ".git" && p != dir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p) //nolint:gosec // G122: reads a plugin directory of the user repository, not a tree shared with an untrusted writer
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	return out, err
}
