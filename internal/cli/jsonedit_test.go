package cli

import "testing"

func TestJSONEdit(t *testing.T) {
	doc := "{\n  \"a\": 1,\n  \"o\": {\n    \"x\": \"1\",\n    \"y\": [1, {\"z\": \"}\"}],\n    \"w\": \"\\\"q\\\"\"\n  },\n  \"b\": true\n}\n"
	for _, c := range []struct {
		name string
		edit func([]byte) ([]byte, bool, error)
		want string
	}{
		{"set", func(b []byte) ([]byte, bool, error) { return jsonSetString(b, []string{"o"}, "x", "<2>") },
			"{\n  \"a\": 1,\n  \"o\": {\n    \"x\": \"<2>\",\n    \"y\": [1, {\"z\": \"}\"}],\n    \"w\": \"\\\"q\\\"\"\n  },\n  \"b\": true\n}\n"},
		{"delete first", func(b []byte) ([]byte, bool, error) { return jsonDeleteMember(b, []string{"o"}, "x") },
			"{\n  \"a\": 1,\n  \"o\": {\n    \"y\": [1, {\"z\": \"}\"}],\n    \"w\": \"\\\"q\\\"\"\n  },\n  \"b\": true\n}\n"},
		{"delete middle", func(b []byte) ([]byte, bool, error) { return jsonDeleteMember(b, []string{"o"}, "y") },
			"{\n  \"a\": 1,\n  \"o\": {\n    \"x\": \"1\",\n    \"w\": \"\\\"q\\\"\"\n  },\n  \"b\": true\n}\n"},
		{"delete last", func(b []byte) ([]byte, bool, error) { return jsonDeleteMember(b, nil, "b") },
			"{\n  \"a\": 1,\n  \"o\": {\n    \"x\": \"1\",\n    \"y\": [1, {\"z\": \"}\"}],\n    \"w\": \"\\\"q\\\"\"\n  }\n}\n"},
		{"missing", func(b []byte) ([]byte, bool, error) { return jsonDeleteMember(b, []string{"nope"}, "x") }, doc},
	} {
		got, _, err := c.edit([]byte(doc))
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %v\n%s\nwant:\n%s", c.name, err, got, c.want)
		}
	}
	got, ok, err := jsonDeleteMember([]byte(`{"o": {"only": 1}}`), []string{"o"}, "only")
	if !ok || err != nil || string(got) != `{"o": {}}` {
		t.Errorf("delete only = %s %v %v", got, ok, err)
	}
	if _, _, err := jsonFindObject([]byte(`{"a": }`)); err == nil {
		t.Error("invalid JSON accepted")
	}
}
