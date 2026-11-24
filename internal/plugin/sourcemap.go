package plugin

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// sourceMap is a decoded source map (version 3, as produced by esbuild): enough to map a
// generated line:column back to a source position.
type sourceMap struct {
	sources []string
	lines   [][]mapping // per generated line (0-based), sorted by column
}

type mapping struct {
	genCol, src, line, col int
}

const inlineMapPrefix = "//# sourceMappingURL=data:application/json;base64,"

// parseInlineSourceMap decodes the inline source map at the end of a bundle. It returns
// an empty map (positions unchanged) when there is none or it is invalid.
func parseInlineSourceMap(code string) *sourceMap {
	i := strings.LastIndex(code, inlineMapPrefix)
	if i < 0 {
		return &sourceMap{}
	}
	data := code[i+len(inlineMapPrefix):]
	if j := strings.IndexAny(data, "\r\n"); j >= 0 {
		data = data[:j]
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	if err != nil {
		return &sourceMap{}
	}
	return decodeSourceMap(raw)
}

func decodeSourceMap(raw []byte) *sourceMap {
	var m struct {
		Version    int      `json:"version"`
		Sources    []string `json:"sources"`
		SourceRoot string   `json:"sourceRoot"`
		Mappings   string   `json:"mappings"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Version != 3 {
		return &sourceMap{}
	}
	sm := &sourceMap{sources: make([]string, len(m.Sources))}
	for i, s := range m.Sources {
		sm.sources[i] = m.SourceRoot + s
	}
	var src, line, col int
	for _, l := range strings.Split(m.Mappings, ";") {
		var segs []mapping
		genCol := 0
		for _, seg := range strings.Split(l, ",") {
			if seg == "" {
				continue
			}
			f, ok := decodeVLQ(seg)
			if !ok || len(f) == 0 {
				return &sourceMap{}
			}
			genCol += f[0]
			if len(f) < 4 {
				continue
			}
			src, line, col = src+f[1], line+f[2], col+f[3]
			if src >= 0 && src < len(sm.sources) {
				segs = append(segs, mapping{genCol: genCol, src: src, line: line, col: col})
			}
		}
		sm.lines = append(sm.lines, segs)
	}
	return sm
}

const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func decodeVLQ(s string) ([]int, bool) {
	var out []int
	val, shift := 0, 0
	for i := 0; i < len(s); i++ {
		d := strings.IndexByte(b64chars, s[i])
		if d < 0 || shift > 30 {
			return nil, false
		}
		val |= (d & 31) << shift
		if d&32 != 0 {
			shift += 5
			continue
		}
		n := val >> 1
		if val&1 != 0 {
			n = -n
		}
		out = append(out, n)
		val, shift = 0, 0
	}
	return out, shift == 0
}

// lookup maps a generated position (1-based line, 1-based column) to a source position
// (1-based line and column).
func (m *sourceMap) lookup(line, col int) (string, int, int, bool) {
	if line < 1 || line > len(m.lines) {
		return "", 0, 0, false
	}
	segs := m.lines[line-1]
	i := sort.Search(len(segs), func(i int) bool { return segs[i].genCol > col-1 }) - 1
	if i < 0 {
		if len(segs) == 0 {
			return "", 0, 0, false
		}
		i = 0
	}
	s := segs[i]
	return m.sources[s.src], s.line + 1, s.col + 1, true
}

var posRe = regexp.MustCompile(`([A-Za-z0-9_.\-]+):(\d+)(?::(\d+))?`)

// rewrite replaces "<file>:line[:col]" occurrences in a stack trace by source positions.
func (m *sourceMap) rewrite(stack, file string) string {
	if len(m.lines) == 0 {
		return stack
	}
	return posRe.ReplaceAllStringFunc(stack, func(s string) string {
		sub := posRe.FindStringSubmatch(s)
		if sub[1] != file {
			return s
		}
		line, _ := strconv.Atoi(sub[2])
		col := 1
		if sub[3] != "" {
			col, _ = strconv.Atoi(sub[3])
		}
		src, l, c, ok := m.lookup(line, col)
		if !ok {
			return s
		}
		return src + ":" + strconv.Itoa(l) + ":" + strconv.Itoa(c)
	})
}
