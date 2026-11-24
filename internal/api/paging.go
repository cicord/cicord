package api

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Server-side pagination, sorting and search for admin data tables (https://cicord.dev/docs/reference/http-api/authentication/):
// ?q=<words>&sort=<key>&order=asc|desc&offset=<n>&limit=<n>.

type pageMeta struct {
	Total  int    `json:"total"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Sort   string `json:"sort"`
	Order  string `json:"order"`
}

type pageQuery struct {
	q             []string
	sort, order   string
	offset, limit int
	limited       bool
}

const maxPageLimit = 200

// parsePage reads the paging parameters. Without limit, every item is returned (older
// clients); with one it is capped at maxPageLimit.
func parsePage(r *http.Request, sortKeys []string, defaultSort string) (pageQuery, error) {
	v := r.URL.Query()
	p := pageQuery{sort: defaultSort, order: "asc", q: strings.Fields(strings.ToLower(v.Get("q")))}
	if s := v.Get("sort"); s != "" {
		if !slices.Contains(sortKeys, s) {
			return p, httpErr(http.StatusBadRequest, "invalid", "sort must be one of: %s", strings.Join(sortKeys, ", "))
		}
		p.sort = s
	}
	switch o := v.Get("order"); o {
	case "", "asc":
	case "desc":
		p.order = "desc"
	default:
		return p, httpErr(http.StatusBadRequest, "invalid", "order must be asc or desc")
	}
	if s := v.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return p, httpErr(http.StatusBadRequest, "invalid", "offset must be a non-negative integer")
		}
		p.offset = n
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return p, httpErr(http.StatusBadRequest, "invalid", "limit must be a positive integer")
		}
		p.limit, p.limited = min(n, maxPageLimit), true
	}
	return p, nil
}

// page filters items whose text contains every query word, sorts them by key (ties by
// tie) and returns the requested window.
func page[T any](items []T, p pageQuery, text func(T) string, key func(T, string) string, tie func(T) string) ([]T, pageMeta) {
	out := make([]T, 0, len(items))
	for _, it := range items {
		hay := strings.ToLower(text(it))
		ok := true
		for _, w := range p.q {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	slices.SortStableFunc(out, func(a, b T) int {
		c := cmpNatural(key(a, p.sort), key(b, p.sort))
		if c == 0 {
			c = cmp.Compare(tie(a), tie(b))
		}
		if p.order == "desc" {
			return -c
		}
		return c
	})
	meta := pageMeta{Total: len(out), Offset: p.offset, Limit: len(out), Sort: p.sort, Order: p.order}
	if p.limited {
		meta.Limit = p.limit
	}
	start := min(p.offset, len(out))
	end := len(out)
	if p.limited {
		end = min(start+p.limit, len(out))
	}
	return out[start:end], meta
}

// cmpNatural compares case-insensitively, numbers by value ("2" < "10").
func cmpNatural(a, b string) int {
	if x, err1 := strconv.ParseFloat(a, 64); err1 == nil {
		if y, err2 := strconv.ParseFloat(b, 64); err2 == nil {
			return cmp.Compare(x, y)
		}
	}
	return cmp.Compare(strings.ToLower(a), strings.ToLower(b))
}
