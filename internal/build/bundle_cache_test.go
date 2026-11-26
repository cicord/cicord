package build

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBundleSnapshotParityAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"type":"module"}`)
	write("tsconfig.json", `{"compilerOptions":{"paths":{"dep":["./src/dep.ts"]}}}`)
	write("src/index.ts", `export {default} from "dep";`)
	write("src/dep.ts", `export default "first value";`)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	sources := packageFS{root}
	snap, key := snapshotForBundle(sources, "src/index.ts")
	if snap == nil {
		t.Fatal("fixture not captured")
	}
	engine, err := prepareCompiler()
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := runCompiler(t.Context(), engine, sources, "src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	cached, err := Bundle(dir, "src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cached, uncached) {
		t.Fatal("cache changed compiler bytes, source map or diagnostics")
	}
	cached.SourceMap[0] ^= 0xff
	hit, ok := bundles.get(key)
	if !ok || !bytes.Equal(hit.SourceMap, uncached.SourceMap) {
		t.Fatal("caller mutated cached output")
	}
	write("src/dep.ts", `export default "other value";`)
	changed, newKey := snapshotForBundle(sources, "src/index.ts")
	if changed == nil || newKey == key {
		t.Fatal("dependency did not invalidate bundle")
	}
	old, err := runCompiler(t.Context(), engine, snap, "src/index.ts")
	if err != nil || old.Code != uncached.Code {
		t.Fatal("compile observed a file from outside the frozen snapshot")
	}
	next, err := Bundle(dir, "src/index.ts")
	if err != nil || next.Code == old.Code {
		t.Fatal("stale result after dependency edit")
	}
	write("src/other.ts", `export default "different entry";`)
	_, entryKey := snapshotForBundle(sources, "src/other.ts")
	_, mainKey := snapshotForBundle(sources, "src/index.ts")
	if entryKey == mainKey {
		t.Fatal("entry absent from key")
	}
	write(".cicord/metadata", "ignored")
	if snapshot, _ := snapshotForBundle(sources, "src/index.ts"); snapshot != nil {
		t.Fatal("restricted package metadata entered snapshot")
	}
}
