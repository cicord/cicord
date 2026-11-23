package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// sdkPackage is the npm name of the plugin SDK.
const sdkPackage = "@codi/sdk"

// errNoSDKTarball means the binary embeds no @codi/sdk tarball (built without `make ui`).
var errNoSDKTarball = errors.New("this codi binary embeds no @codi/sdk package (build it with `make ui build`)")

// scaffoldPluginTests writes the unit test setup of a new plugin: package.json (test
// script: node --test), test/<name>.test.ts using @codi/sdk/testing, .gitignore, and
// the embedded @codi/sdk tarball in vendor/ (a file: dependency: no npm publication
// needed, works outside the CODI repository), or "^<version>" of the npm registry with
// fromNPM. It returns the written files.
func scaffoldPluginTests(dir, name string, force, fromNPM bool) ([]string, error) {
	kit, err := portalKit()
	if err != nil {
		return nil, errNoSDKTarball
	}
	pkgs, err := readKitPackages(kit)
	if err != nil {
		return nil, err
	}
	var sdk *kitPackage
	for i := range pkgs {
		if pkgs[i].Name == sdkPackage {
			sdk = &pkgs[i]
		}
	}
	if sdk == nil {
		return nil, errNoSDKTarball
	}
	vendored := filepath.Join(dir, "vendor", sdk.File)
	spec := "file:vendor/" + sdk.File
	if fromNPM {
		spec = "^" + sdk.Version // the npm registry: same version as the embedded kit
	} else if _, err := os.Stat(vendored); err == nil && !force {
		return nil, fmt.Errorf("%s already exists (use --force to overwrite)", vendored)
	}
	files, err := renderTree("plugin-test", dir, map[string]any{
		"Name":    name,
		"SDKSpec": spec,
	}, func(rel string) string {
		switch rel {
		case "gitignore":
			return ".gitignore"
		case "test/plugin.test.ts":
			return "test/" + name + ".test.ts"
		}
		return rel
	}, force)
	if err != nil {
		return nil, err
	}
	if fromNPM {
		return files, nil
	}
	if err := os.MkdirAll(filepath.Dir(vendored), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(vendored, sdk.Data, 0o644); err != nil {
		return nil, err
	}
	return append(files, vendored), nil
}
