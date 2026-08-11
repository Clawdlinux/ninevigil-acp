/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzKeywordResolver_Resolve hammers the resolver with arbitrary intents and
// hints to confirm:
//   - Resolve never panics.
//   - The output is always sorted, deduplicated, lowercase.
//   - When non-empty, every returned tag is either a hint or originates from a
//     known keyword in the table.
func FuzzKeywordResolver_Resolve(f *testing.F) {
	seeds := []string{
		"",
		"query",
		"QUERY THE database",
		"sql; audit; template.render",
		"\x00\x01invalid\xff",
		"slack notify",
		strings.Repeat("a", 4096),
		"send to team",
	}
	for _, s := range seeds {
		f.Add(s, "sql,audit")
	}

	tableValues := make(map[string]struct{})
	r := NewKeywordResolver(nil)
	for _, caps := range DefaultKeywordTable() {
		for _, c := range caps {
			tableValues[strings.ToLower(c)] = struct{}{}
		}
	}

	f.Fuzz(func(t *testing.T, intent, hintsCSV string) {
		if !utf8.ValidString(intent) || !utf8.ValidString(hintsCSV) {
			return
		}
		var hints []string
		for _, h := range strings.Split(hintsCSV, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				hints = append(hints, h)
			}
		}

		got, err := r.Resolve(intent, hints)
		if err != nil {
			// ErrNoCapabilities is the only valid error.
			if got != nil {
				t.Fatalf("Resolve returned both result %v and err %v", got, err)
			}
			return
		}

		// Sorted + unique + lowercase invariants.
		seen := make(map[string]struct{})
		for i, c := range got {
			if c != strings.ToLower(c) {
				t.Fatalf("non-lowercase capability %q", c)
			}
			if _, dup := seen[c]; dup {
				t.Fatalf("duplicate capability %q in %v", c, got)
			}
			seen[c] = struct{}{}
			if i > 0 && got[i-1] >= c {
				t.Fatalf("not sorted: %v", got)
			}
		}

		// Every output cap must come from a hint (lowercased) or the table.
		hintSet := make(map[string]struct{}, len(hints))
		for _, h := range hints {
			hintSet[strings.ToLower(h)] = struct{}{}
		}
		for _, c := range got {
			if _, ok := hintSet[c]; ok {
				continue
			}
			if _, ok := tableValues[c]; ok {
				continue
			}
			t.Fatalf("unexpected capability %q (not in hints or table)", c)
		}
	})
}
