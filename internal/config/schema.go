package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/npjonath/codi/spec"
)

var printer = message.NewPrinter(language.English)

var (
	schemasOnce sync.Once
	schemas     map[string]*jsonschema.Schema // by kind
	schemasErr  error
)

// kindSchemas compiles the embedded CODI schemas once.
func kindSchemas() (map[string]*jsonschema.Schema, error) {
	schemasOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		entries, err := fs.ReadDir(spec.Schemas, "schemas")
		if err != nil {
			schemasErr = err
			return
		}
		for _, e := range entries {
			data, err := fs.ReadFile(spec.Schemas, "schemas/"+e.Name())
			if err != nil {
				schemasErr = err
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				schemasErr = fmt.Errorf("%s: %w", e.Name(), err)
				return
			}
			if err := c.AddResource(spec.SchemaBaseURL+e.Name(), doc); err != nil {
				schemasErr = err
				return
			}
		}
		schemas = map[string]*jsonschema.Schema{}
		for kind, file := range map[string]string{
			"Project": "project.schema.json",
			"Server":  "server.schema.json",
			"Bot":     "bot.schema.json",
			"Plugin":  "plugin.schema.json",
		} {
			s, err := c.Compile(spec.SchemaBaseURL + file)
			if err != nil {
				schemasErr = fmt.Errorf("compile %s: %w", file, err)
				return
			}
			schemas[kind] = s
		}
	})
	return schemas, schemasErr
}

// toJSONValue normalizes arbitrary Go values (e.g. from YAML or merge patches) into the
// representation the validator expects.
func toJSONValue(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(data))
}

// validateValue validates v and appends one diagnostic per leaf error.
func validateValue(sink *diagSink, doc *document, s *jsonschema.Schema, v any, pathPrefix string) bool {
	jv, err := toJSONValue(v)
	if err != nil {
		sink.errorf(doc.file, 0, pathPrefix, "%v", err)
		return false
	}
	err = s.Validate(jv)
	if err == nil {
		return true
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		sink.errorf(doc.file, 0, pathPrefix, "%v", err)
		return false
	}
	type leaf struct{ loc, msg string }
	seen := map[leaf]bool{}
	var leaves []leaf
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		l := leaf{pathPrefix + jsonPointer(e.InstanceLocation...), e.ErrorKind.LocalizedString(printer)}
		if !seen[l] {
			seen[l] = true
			leaves = append(leaves, l)
		}
	}
	walk(ve)
	if len(leaves) == 0 {
		leaves = append(leaves, leaf{pathPrefix, ve.Error()})
	}
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].loc < leaves[j].loc })
	for _, l := range leaves {
		line := 0
		if doc.root != nil && strings.HasPrefix(l.loc, "/") {
			line = doc.lineOf(l.loc)
		}
		sink.errorf(doc.file, line, l.loc, "%s", l.msg)
	}
	return false
}

// compilePluginConfigSchema compiles a plugin's config.schema.json. CODI extension
// keywords (x-codi-*) are ignored by the validator and read separately.
func compilePluginConfigSchema(url string, raw []byte) (*jsonschema.Schema, map[string]any, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("config schema must be a JSON object")
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(url, doc); err != nil {
		return nil, nil, err
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, nil, err
	}
	return s, m, nil
}
