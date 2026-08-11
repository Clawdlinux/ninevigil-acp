/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"errors"
	"reflect"
	"testing"
)

func TestKeywordResolver_Resolve(t *testing.T) {
	t.Parallel()

	r := NewKeywordResolver(nil)

	cases := []struct {
		name   string
		intent string
		hints  []string
		want   []string
	}{
		{
			name:   "single keyword",
			intent: "query the database",
			want:   []string{"database", "sql"},
		},
		{
			name:   "multi-step workflow",
			intent: "query customer data, render a report, email the team",
			want:   []string{"email", "sql", "template"},
		},
		{
			name:   "case insensitive",
			intent: "SLACK NOTIFY",
			want:   []string{"messaging", "slack"},
		},
		{
			name:   "punctuation tokenized",
			intent: "sql; audit; template.render",
			want:   []string{"audit", "sql", "template"},
		},
		{
			name:   "hints merged with intent",
			intent: "send to team",
			hints:  []string{"audit"},
			want:   []string{"audit", "email", "messaging"},
		},
		{
			name:   "hints only",
			intent: "",
			hints:  []string{"sql"},
			want:   []string{"sql"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Resolve(tc.intent, tc.hints)
			if err != nil {
				t.Fatalf("Resolve err = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Resolve = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKeywordResolver_NoCapabilities(t *testing.T) {
	t.Parallel()

	r := NewKeywordResolver(nil)
	_, err := r.Resolve("", nil)
	if !errors.Is(err, ErrNoCapabilities) {
		t.Fatalf("expected ErrNoCapabilities, got %v", err)
	}

	_, err = r.Resolve("nothing meaningful here", nil)
	if !errors.Is(err, ErrNoCapabilities) {
		t.Fatalf("expected ErrNoCapabilities for unknown words, got %v", err)
	}
}

func TestKeywordResolver_CustomTable(t *testing.T) {
	t.Parallel()

	r := NewKeywordResolver(map[string][]string{
		"foo": {"alpha"},
		"bar": {"beta", "gamma"},
	})
	got, err := r.Resolve("foo bar", nil)
	if err != nil {
		t.Fatalf("Resolve err = %v", err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve = %v, want %v", got, want)
	}
}
