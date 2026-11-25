package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// In-place edits of a JSON document (a portal's package.json): the bytes outside the edited
// member are kept as they are, so key order, indentation and the other fields never change.

// jsonMember is a member of a JSON object, as byte offsets in the document.
type jsonMember struct {
	Key                        string
	KeyStart, ValStart, ValEnd int
}

// jsonObject is a JSON object: the offsets of its braces and its members.
type jsonObject struct {
	Open, Close int
	Members     []jsonMember
}

func (o jsonObject) member(key string) (int, bool) {
	for i, m := range o.Members {
		if m.Key == key {
			return i, true
		}
	}
	return -1, false
}

var errJSONSyntax = errors.New("invalid JSON")

func jsonSkipWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// jsonScanString scans the string starting at b[i] (a quote): it returns the offset after
// the closing quote and the decoded string.
func jsonScanString(b []byte, i int) (int, string, error) {
	if i >= len(b) || b[i] != '"' {
		return 0, "", errJSONSyntax
	}
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			var s string
			if err := json.Unmarshal(b[i:j+1], &s); err != nil {
				return 0, "", err
			}
			return j + 1, s, nil
		}
	}
	return 0, "", errJSONSyntax
}

// jsonScanValue returns the offset after the value starting at b[i].
func jsonScanValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, errJSONSyntax
	}
	switch b[i] {
	case '"':
		end, _, err := jsonScanString(b, i)
		return end, err
	case '{':
		o, err := jsonScanObject(b, i)
		return o.Close + 1, err
	case '[':
		i = jsonSkipWS(b, i+1)
		if i < len(b) && b[i] == ']' {
			return i + 1, nil
		}
		for {
			end, err := jsonScanValue(b, i)
			if err != nil {
				return 0, err
			}
			i = jsonSkipWS(b, end)
			if i >= len(b) {
				return 0, errJSONSyntax
			}
			if b[i] == ']' {
				return i + 1, nil
			}
			if b[i] != ',' {
				return 0, errJSONSyntax
			}
			i = jsonSkipWS(b, i+1)
		}
	default: // number, true, false, null
		j := i
		for j < len(b) && bytes.IndexByte([]byte(",}] \t\r\n"), b[j]) < 0 {
			j++
		}
		if j == i {
			return 0, errJSONSyntax
		}
		return j, nil
	}
}

// jsonScanObject scans the object starting at b[i] (an opening brace).
func jsonScanObject(b []byte, i int) (jsonObject, error) {
	if i >= len(b) || b[i] != '{' {
		return jsonObject{}, errJSONSyntax
	}
	o := jsonObject{Open: i}
	i = jsonSkipWS(b, i+1)
	if i < len(b) && b[i] == '}' {
		o.Close = i
		return o, nil
	}
	for {
		keyEnd, key, err := jsonScanString(b, i)
		if err != nil {
			return o, err
		}
		j := jsonSkipWS(b, keyEnd)
		if j >= len(b) || b[j] != ':' {
			return o, errJSONSyntax
		}
		valStart := jsonSkipWS(b, j+1)
		valEnd, err := jsonScanValue(b, valStart)
		if err != nil {
			return o, err
		}
		o.Members = append(o.Members, jsonMember{Key: key, KeyStart: i, ValStart: valStart, ValEnd: valEnd})
		i = jsonSkipWS(b, valEnd)
		if i >= len(b) {
			return o, errJSONSyntax
		}
		if b[i] == '}' {
			o.Close = i
			return o, nil
		}
		if b[i] != ',' {
			return o, errJSONSyntax
		}
		i = jsonSkipWS(b, i+1)
	}
}

// jsonFindObject returns the object at path (nested keys; none is the document itself).
func jsonFindObject(b []byte, path ...string) (jsonObject, bool, error) {
	o, err := jsonScanObject(b, jsonSkipWS(b, 0))
	if err != nil {
		return o, false, fmt.Errorf("%w: %w", errJSONSyntax, err)
	}
	for _, key := range path {
		k, ok := o.member(key)
		if !ok || b[o.Members[k].ValStart] != '{' {
			return o, false, nil
		}
		if o, err = jsonScanObject(b, o.Members[k].ValStart); err != nil {
			return o, false, err
		}
	}
	return o, true, nil
}

func jsonSplice(b []byte, from, to int, with []byte) []byte {
	out := make([]byte, 0, len(b)-(to-from)+len(with))
	out = append(out, b[:from]...)
	out = append(out, with...)
	return append(out, b[to:]...)
}

// jsonSetString replaces the value of the existing member key of the object at path with
// the string value. It reports whether the member exists.
func jsonSetString(b []byte, path []string, key, value string) ([]byte, bool, error) {
	o, ok, err := jsonFindObject(b, path...)
	if err != nil || !ok {
		return b, false, err
	}
	k, ok := o.member(key)
	if !ok {
		return b, false, nil
	}
	var enc bytes.Buffer
	e := json.NewEncoder(&enc)
	e.SetEscapeHTML(false)
	if err := e.Encode(value); err != nil {
		return b, false, err
	}
	m := o.Members[k]
	return jsonSplice(b, m.ValStart, m.ValEnd, bytes.TrimSpace(enc.Bytes())), true, nil
}

// jsonDeleteMember deletes the member key of the object at path, with its separating comma.
// It reports whether the member existed.
func jsonDeleteMember(b []byte, path []string, key string) ([]byte, bool, error) {
	o, ok, err := jsonFindObject(b, path...)
	if err != nil || !ok {
		return b, false, err
	}
	k, ok := o.member(key)
	if !ok {
		return b, false, nil
	}
	ms := o.Members
	switch {
	case len(ms) == 1:
		return jsonSplice(b, o.Open+1, o.Close, nil), true, nil
	case k < len(ms)-1: // the member, its comma and the space up to the next key
		return jsonSplice(b, ms[k].KeyStart, ms[k+1].KeyStart, nil), true, nil
	default: // the last member: from the end of the previous value
		return jsonSplice(b, ms[k-1].ValEnd, ms[k].ValEnd, nil), true, nil
	}
}
