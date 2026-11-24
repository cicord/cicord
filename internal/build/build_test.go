package build

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/plugin/qjs"
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

// harness is a minimal prelude for running bundles in the plugin engine (QuickJS-ng): a
// stub require("@cicord/sdk") and helpers to inspect the exports.
const harness = `(function (host) {
  let defined, settled;
  globalThis.require = (n) => {
    if (n !== "@cicord/sdk") throw new TypeError("unknown module " + n);
    return { definePlugin: (d) => (defined = d), command: (d) => d };
  };
  const exp = () => globalThis.` + GlobalName + `;
  return {
    errInfo(e) { return JSON.stringify({ message: String(e && e.message), stack: String(e && e.stack) }); },
    hasDefault() { return String(exp() !== undefined && exp().default !== undefined); },
    commands() { return JSON.stringify(Object.keys((defined && defined.commands) || {})); },
    start(fn) { exp()[fn]().then((v) => { settled = JSON.stringify(v); }, (e) => { settled = "error: " + e; }); },
    settled() { return settled; },
  };
})`

// run evaluates a bundle in a fresh VM of the plugin engine.
func run(t *testing.T, code string) *qjs.VM {
	t.Helper()
	ctx := context.Background()
	vm, err := qjs.New(ctx, qjs.Config{}, func(int32, string, string, float64) qjs.Reply { return qjs.Reply{Undefined: true} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vm.Close)
	if err := vm.Prelude(ctx, "harness.js", harness); err != nil {
		t.Fatal(err)
	}
	if err := vm.Eval(ctx, "plugin.bundle.js", code); err != nil {
		t.Fatalf("run bundle: %v", err)
	}
	return vm
}

func call(t *testing.T, vm *qjs.VM, fn string, args ...string) string {
	t.Helper()
	r, _, err := vm.Call(context.Background(), fn, args...)
	if err != nil {
		t.Fatalf("%s: %v", fn, err)
	}
	return r
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

	vm := run(t, res.Code)
	if call(t, vm, "hasDefault") != "true" {
		t.Fatal("no default export")
	}
	if cmds := call(t, vm, "commands"); !strings.Contains(cmds, `"welcome-preview"`) {
		t.Fatalf("commands not defined: %s", cmds)
	}
}

// TestTargetFeatures bundles modern TypeScript and runs it in the plugin engine
// (QuickJS-ng), proving the chosen esbuild target only emits syntax it supports.
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
export const last = (a: number[]) => a.findLast((x) => x > 1);
export async function* gen(n: number) { for (let i = 0; i < n; i++) { await null; yield i; } }
`,
		"src/index.ts": `
import { Counter, Color, gen, last } from "./util";
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
    findLast: last([1, 2, 3, 0]),
    toSorted: [3, 1, 2].toSorted().join(","),
    withResolvers: typeof Promise.withResolvers,
    groupBy: Object.keys(Object.groupBy([1, 2, 3], (n: number) => (n % 2 ? "odd" : "even"))).join(","),
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
	if !strings.Contains(res.Code, "async function*") {
		t.Fatal("async generators are supported by the engine and should not be lowered")
	}
	vm := run(t, res.Code)
	call(t, vm, "start", "features")
	if err := vm.RunJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	settled := call(t, vm, "settled")
	var got map[string]any
	if err := json.Unmarshal([]byte(settled), &got); err != nil {
		t.Fatalf("features did not settle with an object: %q", settled)
	}
	want := map[string]any{
		"optional": "none", "nullishAssign": 5.0, "template": "c:3", "replaceAll": "a+b+c",
		"asyncGen": "0,1,2", "rest": "b,c", "enumValue": "blue", "json": "cicord", "at": 3.0,
		"hasOwn": true, "bigint": "1024", "regex": "2024", "created": 1.0,
		"findLast": 3.0, "toSorted": "1,2,3", "withResolvers": "function", "groupBy": "odd,even",
	}
	for k, w := range want {
		if g := got[k]; g != w {
			t.Errorf("%s = %#v (%T), want %#v", k, g, g, w)
		}
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
