/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzDeferredResolver_Resolve(f *testing.F) {
	seeds := []string{
		"", "query", "query the database", "send email to team",
		"slack notify audit log compliance",
		"\x00\x01invalid\xff",
		strings.Repeat("a", 4096),
	}
	for _, s := range seeds {
		f.Add(s, "sql,audit", 3)
	}

	allCaps := []string{"audit", "database", "email", "messaging", "sql", "template"}

	f.Fuzz(func(t *testing.T, intent, hintsCSV string, obsCount int) {
		if !utf8.ValidString(intent) || !utf8.ValidString(hintsCSV) {
			return
		}
		if obsCount < 0 {
			obsCount = 0
		}
		if obsCount > 50 {
			obsCount = 50
		}

		r := NewDeferredResolver(DeferredOptions{
			AllCapabilities: allCaps,
			WindowSize:      10,
			NarrowThreshold: 3,
		})

		// Simulate observations.
		for i := 0; i < obsCount; i++ {
			r.Observe(allCaps[i%len(allCaps)])
		}

		var hints []string
		if hintsCSV != "" {
			hints = strings.Split(hintsCSV, ",")
		}

		got, err := r.Resolve(intent, hints)

		// Invariant 1: no panics (implicit).
		// Invariant 2: result is sorted if non-nil.
		if got != nil && !sort.StringsAreSorted(got) {
			t.Fatalf("result not sorted: %v", got)
		}
		// Invariant 3: no duplicates.
		if got != nil {
			seen := make(map[string]bool, len(got))
			for _, c := range got {
				if seen[c] {
					t.Fatalf("duplicate capability: %q in %v", c, got)
				}
				seen[c] = true
			}
		}
		// Invariant 4: error only when result is nil.
		if err != nil && got != nil {
			t.Fatalf("got error %v with non-nil result %v", err, got)
		}
	})
}
