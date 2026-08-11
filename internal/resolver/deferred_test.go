/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"reflect"
	"testing"
)

func TestDeferredResolver_Resolve(t *testing.T) {
	t.Parallel()

	allCaps := []string{"audit", "database", "email", "messaging", "sql", "template"}

	cases := []struct {
		name    string
		intent  string
		hints   []string
		observe []string // capabilities to observe before Resolve
		want    []string
		wantErr bool
	}{
		{
			name:   "cold start returns all capabilities",
			intent: "",
			hints:  nil,
			want:   allCaps,
		},
		{
			name:   "cold start with hints returns all plus hints sorted",
			intent: "",
			hints:  []string{"custom"},
			want:   []string{"audit", "custom", "database", "email", "messaging", "sql", "template"},
		},
		{
			name:    "cold start with intent still returns all",
			intent:  "query the database",
			hints:   nil,
			observe: nil,
			want:    allCaps, // 0 obs < threshold → all caps
		},
		{
			name:    "warm phase still returns all below threshold",
			intent:  "query the database",
			hints:   nil,
			observe: []string{"email"}, // 1 obs < threshold(3)
			want:    allCaps,
		},
		{
			name:    "warm phase no intent returns all",
			intent:  "",
			hints:   nil,
			observe: []string{"email"},
			want:    allCaps, // < threshold → all
		},
		{
			name:    "narrowed phase returns only observed domains",
			intent:  "",
			hints:   nil,
			observe: []string{"sql", "sql", "sql"}, // 3 obs >= threshold(3)
			want:    []string{"sql"},
		},
		{
			name:    "narrowed phase with hints merges",
			intent:  "",
			hints:   []string{"audit"},
			observe: []string{"sql", "sql", "sql"},
			want:    []string{"audit", "sql"},
		},
		{
			name:    "narrowed phase with intent adds fallback signal",
			intent:  "send email",
			hints:   nil,
			observe: []string{"sql", "sql", "sql"},
			want:    []string{"email", "messaging", "sql"},
		},
		{
			name:    "narrowed with multiple observed domains",
			intent:  "",
			hints:   nil,
			observe: []string{"sql", "email", "sql"},
			want:    []string{"email", "sql"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := NewDeferredResolver(DeferredOptions{
				AllCapabilities: allCaps,
				NarrowThreshold: 3,
				WindowSize:      10,
			})

			for _, cap := range tc.observe {
				r.Observe(cap)
			}

			got, err := r.Resolve(tc.intent, tc.hints)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeferredResolver_Observe(t *testing.T) {
	t.Parallel()

	r := NewDeferredResolver(DeferredOptions{
		AllCapabilities: []string{"sql", "email"},
		WindowSize:      3,
	})

	r.Observe("sql")
	r.Observe("email")
	r.Observe("sql")
	if got := r.ObservationCount(); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}

	// Window overflow: oldest drops.
	r.Observe("audit")
	if got := r.ObservationCount(); got != 3 {
		t.Fatalf("count after overflow = %d, want 3", got)
	}
}

func TestDeferredResolver_Reset(t *testing.T) {
	t.Parallel()

	allCaps := []string{"email", "sql"}
	r := NewDeferredResolver(DeferredOptions{
		AllCapabilities: allCaps,
		NarrowThreshold: 1,
	})

	r.Observe("sql")
	r.Observe("sql")
	r.Observe("sql")

	// Narrowed.
	got, _ := r.Resolve("", nil)
	if reflect.DeepEqual(got, allCaps) {
		t.Fatal("should be narrowed, got all caps")
	}

	r.Reset()

	// Back to cold start.
	got, _ = r.Resolve("", nil)
	if !reflect.DeepEqual(got, allCaps) {
		t.Fatalf("after reset got %v, want %v", got, allCaps)
	}
}

func TestDeferredResolver_HintsAlwaysHonored(t *testing.T) {
	t.Parallel()

	r := NewDeferredResolver(DeferredOptions{
		AllCapabilities: []string{"sql"},
		NarrowThreshold: 1,
	})

	// Narrowed to sql only.
	r.Observe("sql")
	r.Observe("sql")

	got, err := r.Resolve("", []string{"custom-hint"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, c := range got {
		if c == "custom-hint" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hint not in result: %v", got)
	}
}

func TestDeferredResolver_EmptyObserveIgnored(t *testing.T) {
	t.Parallel()

	r := NewDeferredResolver(DeferredOptions{
		AllCapabilities: []string{"sql"},
	})

	r.Observe("")
	r.Observe("  ")

	if got := r.ObservationCount(); got != 0 {
		t.Fatalf("count = %d, want 0 (empty obs should be ignored)", got)
	}
}
