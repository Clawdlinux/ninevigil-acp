/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzCompactSchema asserts that compactSchema:
//   - Never panics on arbitrary JSON-Schema-shaped input.
//   - Always returns a non-nil map.
//   - Each value uses only legal mini-language tokens (string, int, float,
//     bool, json, bytes, array suffix [], optional suffix ?, enum:, ref:).
func FuzzCompactSchema(f *testing.F) {
	seeds := []string{
		`{"properties":{"a":{"type":"string"}},"required":["a"]}`,
		`{"properties":{"x":{"type":"integer"},"y":{"type":"number"}},"required":[]}`,
		`{"properties":{"items":{"type":"array","items":{"type":"string"}}}}`,
		`{"properties":{"deep":{"type":"array","items":{"type":"array","items":{"type":"object"}}}}}`,
		`{"properties":{"e":{"type":"string","enum":["a","b","c"]}},"required":["e"]}`,
		`{"properties":{"weird":{}},"required":["weird"]}`,
		`{}`,
		`null`,
		`{"required":["a","b"]}`,
		`{"properties":null}`,
		`{"properties":{"x":{"type":"unknown-type"}}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	allowedAtoms := map[string]struct{}{
		"string": {}, "int": {}, "float": {}, "bool": {}, "json": {}, "bytes": {},
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if !utf8.ValidString(raw) {
			return
		}
		var doc map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return
		}

		out := compactSchema(doc)
		if out == nil {
			t.Fatalf("compactSchema returned nil for %q", raw)
		}

		for field, compact := range out {
			if compact == "" {
				t.Fatalf("empty compact for field %q", field)
			}
			// Strip optional `?` and any number of `[]` suffixes.
			body := strings.TrimSuffix(compact, "?")
			for strings.HasSuffix(body, "[]") {
				body = body[:len(body)-2]
			}
			switch {
			case strings.HasPrefix(body, "enum:"):
				options := strings.Split(body[len("enum:"):], "|")
				if len(options) == 0 || options[0] == "" {
					t.Fatalf("malformed enum %q for field %q", compact, field)
				}
			case strings.HasPrefix(body, "ref:"):
				if len(body) <= len("ref:") {
					t.Fatalf("empty ref kind in %q", compact)
				}
			default:
				if _, ok := allowedAtoms[body]; !ok {
					t.Fatalf("unknown atom %q (full=%q) for field %q", body, compact, field)
				}
			}
		}
	})
}

// FuzzInferCapabilities ensures capability inference never panics, returns a
// sorted unique lowercase list, and never includes single-character tokens.
func FuzzInferCapabilities(f *testing.F) {
	seeds := []string{"a", "issues.create", "github_repo_get", "Slack-Send-Message",
		"a.b/c:d e_f-g.h", "", strings.Repeat("x", 256)}
	for _, s := range seeds {
		f.Add(s, "alpha,beta")
	}

	f.Fuzz(func(t *testing.T, name, extras string) {
		if !utf8.ValidString(name) || !utf8.ValidString(extras) {
			return
		}
		var ex []string
		for _, e := range strings.Split(extras, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				ex = append(ex, e)
			}
		}

		caps := inferCapabilities(name, ex)
		seen := map[string]struct{}{}
		for i, c := range caps {
			if c != strings.ToLower(c) {
				t.Fatalf("non-lowercase capability %q", c)
			}
			if len(c) < 2 {
				t.Fatalf("single-char capability %q must be filtered", c)
			}
			if _, dup := seen[c]; dup {
				t.Fatalf("duplicate capability %q in %v", c, caps)
			}
			seen[c] = struct{}{}
			if i > 0 && caps[i-1] >= c {
				t.Fatalf("not sorted: %v", caps)
			}
		}
	})
}
