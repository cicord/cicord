package typegen

import (
	"strings"
	"testing"
)

const pollsContract = `/** Contract. */
import type { Nope } from "x"; // replaced below
export interface Poll { id: string; "default": number }

export type Status =
  | "open"
  | "closed";

export interface Routes {
  "GET /polls/:id": { response: Poll };
  "POST /polls/:id/votes": { body: { choice: number }; response: { votes: number } };
}
`

func TestGenerateRoutes(t *testing.T) {
	contract := strings.Replace(pollsContract, "import type { Nope } from \"x\"; // replaced below\n", "", 1)
	out, err := GenerateRoutes([]RoutePlugin{
		{Name: "tickets", Version: "1.0.0", Source: "../../plugins/tickets", Routes: []Route{
			{"GET", "/tickets/:id", "moderator"}, {"GET", "/tickets", "moderator"}}},
		{Name: "my-polls", Version: "0.2.0", Source: "acme/polls", Contract: contract, Routes: []Route{
			{"POST", "/polls/:id/votes", "member"}, {"GET", "/polls/:id", "public"}}},
		{Name: "welcome", Version: "0.1.0", Source: "codi/welcome"}, // no routes: left out
		// the same plugin used by two bots
		{Name: "tickets", Version: "1.0.0", Source: "../../plugins/tickets", Routes: []Route{
			{"GET", "/tickets", "moderator"}, {"GET", "/tickets/:id", "moderator"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		RoutesHeader,
		"//   my-polls 0.2.0 (acme/polls): 2 route(s), typed\n",
		"//   tickets 1.0.0 (../../plugins/tickets): 2 route(s), untyped (no spec.routes.types)\n",
		`import type { RouteEntry } from "@codi/client";`,
		"declare namespace codi_my_polls {\n  /** Contract. */\n  export interface Poll",
		"  export type Status =\n    | \"open\"\n",
		`declare module "@codi/client" {`,
		"    \"my-polls\": {\n" +
			"      \"GET /polls/:id\": RouteEntry<\"public\", codi_my_polls.Routes, \"GET /polls/:id\">;\n" +
			"      \"POST /polls/:id/votes\": RouteEntry<\"member\", codi_my_polls.Routes, \"POST /polls/:id/votes\">;\n" +
			"    };\n",
		"    \"tickets\": {\n      \"GET /tickets\": RouteEntry<\"moderator\">;\n      \"GET /tickets/:id\": RouteEntry<\"moderator\">;\n    };\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "welcome") || strings.Count(out, `"tickets": {`) != 1 || strings.Contains(out, "namespace codi_tickets") {
		t.Errorf("unexpected plugins:\n%s", out)
	}
	if strings.Index(out, `"my-polls": {`) > strings.Index(out, `"tickets": {`) {
		t.Error("plugins are not sorted by name")
	}

	// Deterministic.
	again, _ := GenerateRoutes([]RoutePlugin{
		{Name: "my-polls", Version: "0.2.0", Source: "acme/polls", Contract: contract, Routes: []Route{
			{"GET", "/polls/:id", "public"}, {"POST", "/polls/:id/votes", "member"}}},
		{Name: "tickets", Version: "1.0.0", Source: "../../plugins/tickets", Routes: []Route{
			{"GET", "/tickets", "moderator"}, {"GET", "/tickets/:id", "moderator"}}},
	})
	if again != out {
		t.Errorf("output depends on the input order:\n%s\n---\n%s", out, again)
	}
}

func TestGenerateRoutesEmptyAndConflicts(t *testing.T) {
	out, err := GenerateRoutes(nil)
	if err != nil || !strings.HasPrefix(out, RoutesHeader) || !strings.HasSuffix(out, "export {};\n") || strings.Contains(out, "declare module") {
		t.Errorf("empty: %v\n%s", err, out)
	}
	_, err = GenerateRoutes([]RoutePlugin{
		{Name: "xp", Source: "acme/xp", Routes: []Route{{"GET", "/a", "public"}}},
		{Name: "xp", Source: "../../plugins/xp", Routes: []Route{{"GET", "/b", "public"}}},
	})
	if err == nil || !strings.Contains(err.Error(), `two different plugins are named "xp"`) {
		t.Errorf("conflict: %v", err)
	}
	_, err = GenerateRoutes([]RoutePlugin{{Name: "xp", Contract: "export const x = 1;", Routes: []Route{{"GET", "/a", "public"}}}})
	if err == nil || !strings.Contains(err.Error(), "plugin xp: route contract") {
		t.Errorf("bad contract: %v", err)
	}
}

func TestCheckRouteContract(t *testing.T) {
	ok := []string{
		strings.Replace(pollsContract, "import type { Nope } from \"x\"; // replaced below\n", "", 1),
		"export type Routes = { \"GET /a\": { response: string } };",
		"// import x from 'y'\n/* const z = 1 */\ninterface Item { const: string; import: number; default: boolean }\nexport interface Routes { \"GET /a\": { response: Item } }",
		"export interface Routes {\n  \"GET /a\": {\n    query: { q?: string };\n    response: `${string}/x`;\n  };\n}\n",
		"type Map<\n  T,\n> = { [K in keyof T]: string };\nexport interface Routes {}\n",
	}
	for _, src := range ok {
		if err := CheckRouteContract(src); err != nil {
			t.Errorf("%v for\n%s", err, src)
		}
	}
	bad := map[string]string{
		pollsContract: "imports and re-exports are not allowed",
		"export interface Routes {}\nexport { Foo } from \"./foo\";":    "imports and re-exports are not allowed",
		"export interface Routes {}\nexport * from \"./foo\";":          "imports and re-exports are not allowed",
		"export interface Routes {}\nexport const limit = 10;":          "only type declarations",
		"export interface Routes {}\nexport default {};":                "only type declarations",
		"export interface Routes {}\ndeclare global { interface X {} }": "only type declarations",
		"export interface Routes {}\nenum E { A }":                      "only type declarations",
		"export interface Routes {}\nfunction f() {}":                   "only type declarations",
		"export interface Routes {}\nexport = Routes;":                  "only type declarations",
		"interface Routes {}":                                           "must export `interface Routes`",
		"export interface Routes { \"GET /a\": { response: string }":    "unbalanced brackets",
		"export interface Routes {}\n/* never closed":                   "unterminated comment",
		"export interface Routes { \"GET /a: { response: string } }\n":  "unterminated string",
	}
	for src, want := range bad {
		err := CheckRouteContract(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want %q for\n%s", err, want, src)
		}
	}
}
