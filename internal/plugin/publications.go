package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/js"

	"github.com/cicord/cicord/internal/config"
)

// The host also validates every publication. The builder catches mistakes in
// statically known calls without interpreting comments, regexps or strings as code.
func checkPublications(code string, topics []config.Topic) []string {
	ast, err := js.Parse(parse.NewInputString(code), js.Options{})
	if err != nil {
		return []string{fmt.Sprintf("parse event publications: %v", err)}
	}
	checker := publicationChecker{topics: topics}
	js.Walk(&checker, ast)
	return checker.problems
}

type publicationChecker struct {
	topics   []config.Topic
	problems []string
}

func (c *publicationChecker) Enter(node js.INode) js.IVisitor {
	call, ok := node.(*js.CallExpr)
	if !ok || len(call.Args.List) == 0 || call.Args.List[0].Rest {
		return c
	}
	events, method := staticMember(call.X)
	_, service := staticMember(events)
	if method != "publish" || service != "events" {
		return c
	}
	topic, known := stringValue(call.Args.List[0].Value)
	if !known {
		return c
	}
	if !slices.ContainsFunc(c.topics, func(t config.Topic) bool { return t.Topic == topic }) {
		c.problems = append(c.problems, "undeclared literal event topic "+topic)
		return c
	}
	version, known := publicationVersion(call.Args.List)
	if known && !slices.ContainsFunc(c.topics, func(t config.Topic) bool { return t.Topic == topic && t.V() == version }) {
		c.problems = append(c.problems, fmt.Sprintf("undeclared literal event version %s v%d", topic, version))
	}
	return c
}
func (*publicationChecker) Exit(js.INode) {}

func staticMember(expr js.IExpr) (js.IExpr, string) {
	switch e := expr.(type) {
	case *js.DotExpr:
		return e.X, e.Y.String()
	case *js.IndexExpr:
		name, _ := stringValue(e.Y)
		return e.X, name
	case *js.GroupExpr:
		return staticMember(e.X)
	}
	return nil, ""
}
func stringValue(expr js.IExpr) (string, bool) {
	switch x := expr.(type) {
	case *js.GroupExpr:
		return stringValue(x.X)
	case *js.TemplateExpr:
		if x.Tag == nil && len(x.List) == 0 && len(x.Tail) >= 2 && !bytes.ContainsRune(x.Tail, '\\') {
			return string(x.Tail[1 : len(x.Tail)-1]), true
		}
	case *js.LiteralExpr:
		if x.TokenType != js.StringToken {
			return "", false
		}
		var b bytes.Buffer
		if x.JSON(&b) != nil {
			return "", false
		}
		var s string
		if json.Unmarshal(b.Bytes(), &s) == nil {
			return s, true
		}
	}
	return "", false
}
func publicationVersion(args []js.Arg) (int, bool) {
	for i := range min(3, len(args)) {
		if args[i].Rest {
			return 0, false
		}
	}
	if len(args) < 3 {
		return 1, true
	}
	opts, ok := args[2].Value.(*js.ObjectExpr)
	if !ok {
		return 0, false
	}
	// Apply normal object override order. A later spread or computed key can
	// replace an earlier literal; in that case only the runtime knows the version.
	for i := len(opts.List) - 1; i >= 0; i-- {
		prop := opts.List[i]
		if prop.Spread || prop.Name == nil {
			return 0, false
		}
		name := string(prop.Name.Literal.Data)
		if prop.Name.Computed != nil {
			var known bool
			name, known = stringValue(prop.Name.Computed)
			if !known {
				return 0, false
			}
		} else if prop.Name.Literal.TokenType == js.StringToken {
			name, _ = stringValue(&prop.Name.Literal)
		}
		if name == "version" {
			return numberVersion(prop.Value)
		}
	}
	return 1, true
}
func numberVersion(expr js.IExpr) (int, bool) {
	switch e := expr.(type) {
	case *js.GroupExpr:
		return numberVersion(e.X)
	case *js.UnaryExpr:
		if e.Op == js.NegToken || e.Op == js.PosToken {
			n, ok := numberVersion(e.X)
			if e.Op == js.NegToken {
				n = -n
			}
			return n, ok
		}
	case *js.LiteralExpr:
		if !js.IsNumeric(e.TokenType) {
			return 0, true
		}
		raw := string(e.Data)
		if n, err := strconv.ParseInt(raw, 0, 32); err == nil {
			return int(n), true
		}
		n, err := strconv.ParseFloat(raw, 64)
		if err == nil && math.Trunc(n) == n && n > 0 && n <= math.MaxInt32 {
			return int(n), true
		}
		return 0, true
	}
	return 0, false
}
