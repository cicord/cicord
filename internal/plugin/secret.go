package plugin

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

const secretPrefix = "cicord-secret:"

type material struct {
	value string
	hosts []string
}

func opSecretHas(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	a, err := decode[struct{ Name string }](args)
	if err != nil {
		return nil, err
	}
	if i.env.Secret == nil {
		return false, nil
	}
	_, _, ok := i.env.Secret(a.Name)
	return ok, nil
}

// The VM sees only a logical handle. Re-evaluate the binding at each fetch so
// rotations take effect without restarting a VM.
func (i *Instance) prepareFetch(rawURL string, a *fetchInit) ([]material, string, error) {
	var used []material
	resolve := func(s string) (string, error) {
		if !strings.Contains(s, secretPrefix) {
			return s, nil
		}
		name, ok := strings.CutPrefix(s, secretPrefix)
		if !ok || i.env.Secret == nil {
			return "", errors.New("invalid secret reference")
		}
		v, hosts, ok := i.env.Secret(name)
		if !ok {
			return "", errors.New("secret is missing or undeclared")
		}
		used = append(used, material{v, hosts})
		return v, nil
	}
	dest, err := resolve(rawURL)
	if err != nil {
		return nil, "", err
	}
	if a.Body != nil && strings.Contains(*a.Body, secretPrefix) {
		return nil, "", errors.New("secret references are forbidden in text bodies")
	}
	if a.Form != nil && a.JSON != nil || a.Body != nil && (a.Form != nil || a.JSON != nil) {
		return nil, "", errors.New("choose one request body encoding")
	}
	if a.Headers == nil {
		a.Headers = map[string]string{}
	}
	for k, v := range a.Headers {
		if strings.Contains(k, secretPrefix) {
			return nil, "", errors.New("invalid header name")
		}
		a.Headers[k], err = resolve(v)
		if err != nil {
			return nil, "", err
		}
	}
	setBody := func(body, kind string) {
		for k := range a.Headers {
			if strings.EqualFold(k, "content-type") {
				delete(a.Headers, k)
			}
		}
		a.Headers["Content-Type"] = kind
		a.Body = &body
	}
	if a.Form != nil {
		fields := url.Values{}
		for k, v := range a.Form {
			if strings.Contains(k, secretPrefix) {
				return nil, "", errors.New("invalid form field name")
			}
			v, err = resolve(v)
			if err != nil {
				return nil, "", err
			}
			fields.Set(k, v)
		}
		setBody(fields.Encode(), "application/x-www-form-urlencoded")
	}
	if a.JSON != nil {
		for k, v := range a.JSON {
			if strings.Contains(k, secretPrefix) {
				return nil, "", errors.New("invalid JSON field name")
			}
			if s, ok := v.(string); ok {
				a.JSON[k], err = resolve(s)
				if err != nil {
					return nil, "", err
				}
			} else {
				b, _ := json.Marshal(v)
				if strings.Contains(string(b), secretPrefix) {
					return nil, "", errors.New("secret must be a direct JSON field value")
				}
			}
		}
		b, err := json.Marshal(a.JSON)
		if err != nil {
			return nil, "", errors.New("invalid JSON request")
		}
		setBody(string(b), "application/json")
	}
	return used, dest, nil
}

func redactResponse(r *fetchResult, materials []material) {
	for _, s := range materials {
		encoded, _ := json.Marshal(s.value)
		for _, v := range []string{s.value, url.QueryEscape(s.value), string(encoded[1 : len(encoded)-1])} {
			if v == "" {
				continue
			}
			r.Body = strings.ReplaceAll(r.Body, v, "[redacted]")
			for k, h := range r.Headers {
				if strings.Contains(k, strings.ToLower(v)) {
					delete(r.Headers, k)
					continue
				}
				r.Headers[k] = strings.ReplaceAll(h, v, "[redacted]")
			}
		}
	}
}
