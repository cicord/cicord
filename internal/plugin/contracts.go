package plugin

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/scanner"

	"github.com/cicord/cicord/internal/config"
)

// Scan the bundled script's tokens, ignoring comments, strings and template text.
// Calculated topics still receive the authoritative check on every publication.
func literalTopics(code string) []string {
	var lex scanner.Scanner
	lex.Init(strings.NewReader(code))
	lex.Mode = scanner.ScanIdents | scanner.ScanStrings | scanner.ScanChars | scanner.ScanRawStrings | scanner.ScanComments | scanner.SkipComments
	// JS strings and regexp literals may contain syntax outside Go's quoted constants.
	lex.Error = func(*scanner.Scanner, string) {}
	var recent [7]string
	var topics []string
	for tok := lex.Scan(); tok != scanner.EOF; tok = lex.Scan() {
		copy(recent[:], recent[1:])
		recent[6] = lex.TokenText()
		if recent[0] != "." || recent[1] != "events" || recent[2] != "." || recent[3] != "publish" || recent[4] != "(" || (recent[6] != "," && recent[6] != ")") {
			continue
		}
		text := recent[5]
		if len(text) < 2 || text[0] != '"' {
			continue
		}
		name, err := strconv.Unquote(text)
		if err == nil {
			topics = append(topics, name)
		}
	}
	return topics
}

func (d *Definition) CheckInterop(spec config.PluginSpec, code string) []string {
	var problems []string
	for _, kind := range []struct {
		what  string
		funcs []string
	}{{"queries", d.Queries}, {"actions", d.Actions}, {"subscriptions", d.Subscriptions}} {
		if spec.Interop == nil {
			if kind.what != "subscriptions" && len(kind.funcs) > 0 {
				problems = append(problems, kind.what+" require spec.interop")
			}
			continue
		}
		var declared []string
		switch kind.what {
		case "queries":
			declared = spec.Interop.Provides.Queries
		case "actions":
			declared = spec.Interop.Provides.Actions
		case "subscriptions":
			for _, s := range spec.Interop.Subscribes {
				declared = append(declared, s.Topic)
			}
		}
		for _, n := range declared {
			if !slices.Contains(kind.funcs, n) {
				problems = append(problems, fmt.Sprintf("declared %s.%s has no handler", kind.what, n))
			}
		}
		for _, n := range kind.funcs {
			if !slices.Contains(declared, n) {
				problems = append(problems, fmt.Sprintf("%s.%s is not declared", kind.what, n))
			}
		}
	}
	if s := spec.Interop; s != nil {
		if spec.Delivery != "at-least-once" {
			problems = append(problems, "spec.interop requires spec.delivery: at-least-once")
		}
		for _, name := range literalTopics(code) {
			if !slices.ContainsFunc(s.Publishes, func(t config.Topic) bool { return t.Topic == name }) {
				problems = append(problems, "undeclared literal event topic "+name)
			}
		}
	}
	return problems
}
