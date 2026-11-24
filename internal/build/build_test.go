package build

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grafana/sobek"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// run evaluates a bundle in a fresh Sobek runtime with a stub require and returns the
// plugin exports object.
func run(t *testing.T, code string, sdk map[string]any) (*sobek.Runtime, *sobek.Object) {
	t.Helper()
	rt := sobek.New()
	rt.SetFieldNameMapper(sobek.UncapFieldNameMapper())
	_ = rt.Set("require", func(name string) any {
		if name != SDKModule {
			panic(rt.NewTypeError("unknown module " + name))
		}
		return sdk
	})
	if _, err := rt.RunScript("plugin.bundle.js", code); err != nil {
		t.Fatalf("run bundle: %v", err)
	}
	exp := rt.Get(GlobalName)
	if exp == nil || sobek.IsUndefined(exp) {
		t.Fatalf("bundle did not define %s", GlobalName)
	}
	return rt, exp.ToObject(rt)
}

func TestBundleWelcomeExample(t *testing.T) {
	res, err := Bundle("../../examples/basic/plugins/welcome", "./src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Code, `__require("@cicord/sdk")`) {
		t.Fatalf("@cicord/sdk must stay external (tsconfig paths must not inline the .d.ts):\n%s", res.Code)
	}
	if !strings.Contains(res.Code, "var "+GlobalName+" = ") {
		t.Fatal("bundle does not assign the global name")
	}
	lines := strings.Split(strings.TrimRight(res.Code, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "//# sourceMappingURL=data:application/json;base64,") {
		t.Fatalf("last line is not an inline source map: %.80s", last)
	}
	if !strings.Contains(string(res.SourceMap), `"src/index.ts"`) {
		t.Fatalf("source map sources should be relative to the plugin dir: %.200s", res.SourceMap)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}

	var defined map[string]any
	sdk := map[string]any{
		"definePlugin": func(d map[string]any) map[string]any { defined = d; return d },
		"command":      func(d map[string]any) map[string]any { return d },
	}
	_, exp := run(t, res.Code, sdk)
	if exp.Get("default") == nil || defined == nil {
		t.Fatal("definePlugin was not called / no default export")
	}
	cmds, _ := defined["commands"].(map[string]any)
	if _, ok := cmds["welcome-preview"]; !ok {
		t.Fatalf("commands not defined: %v", defined)
	}
}

// TestTargetFeatures bundles modern TypeScript and runs it in Sobek, proving the chosen
// esbuild target only emits syntax Sobek supports (async generators are lowered).
func TestTargetFeatures(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"src/util.ts": `
export class Counter {
  #n = 0;
  static created = 0;
  static { Counter.created = 0; }
  constructor(public readonly label: string) { Counter.created++; }
  inc(by = 1): this { this.#n += by; return this; }
  get value() { return this.#n; }
}
export enum Color { Red = "red", Blue = "blue" }
export async function* gen(n: number) { for (let i = 0; i < n; i++) { await null; yield i; } }
`,
		"src/index.ts": `
import { Counter, Color, gen } from "./util";
import data from "./data.json";
type Opt = { a?: { b?: number } };
export async function features(): Promise<Record<string, unknown>> {
  const o: Opt = { a: {} };
  let x: number | undefined;
  x ??= 5;
  const parts: number[] = [];
  for await (const v of gen(3)) parts.push(v);
  const c = new Counter("c").inc().inc(2);
  const { a, ...rest } = { a: 1, b: 2, c: 3 };
  return {
    optional: o.a?.b?.toFixed() ?? "none",
    nullishAssign: x,
    template: ` + "`${c.label}:${c.value}`" + `,
    replaceAll: "a-b-c".replaceAll("-", "+"),
    asyncGen: parts.join(","),
    rest: Object.keys(rest).join(","),
    enumValue: Color.Blue,
    json: data.name,
    at: [1, 2, 3].at(-1),
    hasOwn: Object.hasOwn({ k: 1 }, "k"),
    bigint: (2n ** 10n).toString(),
    regex: /(?<year>\d{4})/.exec("in 2024")?.groups?.year,
    created: Counter.created,
  };
}
export default features;
`,
		"src/data.json": `{"name": "cicord"}`,
	})
	res, err := Bundle(dir, "src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Code, "async function*") {
		t.Fatal("async generators must be lowered")
	}
	rt, exp := run(t, res.Code, map[string]any{})
	fn, ok := sobek.AssertFunction(exp.Get("features"))
	if !ok {
		t.Fatal("features is not a function")
	}
	v, err := fn(sobek.Undefined())
	if err != nil {
		t.Fatal(err)
	}
	p, ok := v.Export().(*sobek.Promise)
	if !ok {
		t.Fatalf("expected a promise, got %T", v.Export())
	}
	if p.State() != sobek.PromiseStateFulfilled {
		t.Fatalf("promise not fulfilled: %v %v", p.State(), p.Result())
	}
	got := p.Result().ToObject(rt).Export().(map[string]any)
	want := map[string]any{
		"optional": "none", "nullishAssign": int64(5), "template": "c:3", "replaceAll": "a+b+c",
		"asyncGen": "0,1,2", "rest": "b,c", "enumValue": "blue", "json": "cicord", "at": int64(3),
		"hasOwn": true, "bigint": "1024", "regex": "2024", "created": int64(1),
	}
	for k, w := range want {
		if g := got[k]; g != w {
			t.Errorf("%s = %#v (%T), want %#v", k, g, g, w)
		}
	}
}

func TestSourceMapMapsStackTraces(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"src/index.ts": `// line 1
interface Unused { a: string }
type Alias = number;

export function boom(): never {
  throw new Error("kaboom"); // line 6
}
`,
	})
	res, err := Bundle(dir, "src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, exp := run(t, res.Code, nil)
	fn, _ := sobek.AssertFunction(exp.Get("boom"))
	_, err = fn(sobek.Undefined())
	var ex *sobek.Exception
	if !errors.As(err, &ex) {
		t.Fatalf("expected exception, got %v", err)
	}
	if !strings.Contains(ex.String(), "(src/index.ts:6:") {
		t.Fatalf("stack is not mapped to the TypeScript source:\n%s", ex.String())
	}
}

func TestBuildErrorsAreReadable(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  []string
	}{
		"syntax": {
			files: map[string]string{"src/index.ts": "export const a = 1;\nexport const b = ;\n"},
			want:  []string{"src/index.ts:2:"},
		},
		"missing import": {
			files: map[string]string{"src/index.ts": `import x from "./nope";` + "\nexport default x;\n"},
			want:  []string{"src/index.ts:1:", `"./nope"`},
		},
		"node builtin": {
			files: map[string]string{"src/index.ts": `import { readFileSync } from "fs";` + "\nexport default readFileSync;\n"},
			want:  []string{"src/index.ts:1:", "Node.js built-in"},
		},
		"node: scheme": {
			files: map[string]string{"src/index.ts": `import * as p from "node:path";` + "\nexport default p;\n"},
			want:  []string{"Node.js built-in"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeFiles(t, tc.files)
			_, err := Bundle(dir, "src/index.ts")
			var be *Error
			if !errors.As(err, &be) {
				t.Fatalf("expected *build.Error, got %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
}

func TestMissingEntry(t *testing.T) {
	_, err := Bundle(t.TempDir(), "src/index.ts")
	if err == nil || !strings.Contains(err.Error(), "entry point not found") {
		t.Fatalf("got %v", err)
	}
}
