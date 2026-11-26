package state

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const IndexQuota = 20 << 20

type IndexDocument struct {
	Title string          `json:"title,omitempty"`
	Text  string          `json:"text"`
	Tags  []string        `json:"tags,omitempty"`
	Meta  json.RawMessage `json:"meta,omitempty"`
}
type IndexResult struct {
	ID      string          `json:"id"`
	Score   float64         `json:"score"`
	Snippet string          `json:"snippet"`
	Meta    json.RawMessage `json:"meta"`
}
type IndexQuery struct {
	Limit int      `json:"limit"`
	Tags  []string `json:"tags"`
}
type FullTextIndex interface {
	Upsert(context.Context, string, string, string, IndexDocument) error
	Remove(context.Context, string, string) error
	Search(context.Context, string, string, string, IndexQuery) ([]IndexResult, error)
	Clear(context.Context, string) error
}

var indexName = regexp.MustCompile("^[A-Za-z0-9_-]{1,80}$")
var word = regexp.MustCompile(`[\pL\pN]+`)

func ValidateIndex(name, lang string) error {
	if !indexName.MatchString(name) || (lang != "en" && lang != "fr") {
		return errors.New("invalid index name or language (en or fr)")
	}
	return nil
}
func ValidateDoc(id string, d IndexDocument) (int, error) {
	if id == "" || len(id) > 512 || strings.ContainsRune(id, 0) || d.Text == "" || len(d.Tags) > 32 {
		return 0, errors.New("invalid index document")
	}
	for _, t := range d.Tags {
		if t == "" || len(t) > 128 {
			return 0, errors.New("invalid index tag")
		}
	}
	if strings.ContainsRune(d.Title+d.Text, 0) {
		return 0, errors.New("invalid text in index document")
	}
	b, err := json.Marshal(d)
	if err != nil || len(b) > 256<<10 {
		return 0, errors.New("index document exceeds 256 KiB or contains invalid JSON")
	}
	return len(b) + len(id) + 128, nil
}
func IndexTerms(q string, opts *IndexQuery) ([]string, error) {
	if opts.Limit == 0 {
		opts.Limit = 10
	}
	if opts.Limit < 1 || opts.Limit > 50 || len(opts.Tags) > 32 || len(q) > 2048 {
		return nil, errors.New("invalid index query; limit must be 1 to 50")
	}
	return word.FindAllString(q, 32), nil
}

// SubjectIndex permits privacy cleanup of explicitly member-tagged documents
// in one installation. Empty name and id select only that subject's tagged
// documents across this installation's indexes.
type SubjectIndex interface {
	EraseSubject(ctx context.Context, subject, name, id string) error
}

var ErrUnboundIndexDocument = errors.New("index document has no binding to the member being erased")
