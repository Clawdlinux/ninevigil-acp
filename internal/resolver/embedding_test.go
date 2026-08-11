/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestEmbeddingResolver_KnownPhrasesMatchExpectedCapability(t *testing.T) {
	t.Parallel()
	r := NewEmbeddingResolver(DefaultExamples(), EmbeddingOptions{})

	cases := []struct {
		intent string
		must   string
	}{
		{"query the customer database", "sql"},
		{"send an email to the team", "email"},
		{"render the weekly report", "template"},
		{"post a slack notification", "messaging"},
		{"log this for compliance audit", "audit"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.intent, func(t *testing.T) {
			t.Parallel()
			got, err := r.Resolve(tc.intent, nil)
			if err != nil {
				t.Fatalf("Resolve err = %v", err)
			}
			found := false
			for _, c := range got {
				if c == tc.must {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected %q in resolved caps for %q, got %v", tc.must, tc.intent, got)
			}
		})
	}
}

func TestEmbeddingResolver_GeneralizesBeyondKeywordTable(t *testing.T) {
	t.Parallel()
	// Phrases that don't contain any DefaultKeywordTable() literal but are
	// semantically obvious. Embedding resolver should pick them up where
	// the keyword resolver would fall through.
	r := NewEmbeddingResolver(DefaultExamples(), EmbeddingOptions{})
	kw := NewKeywordResolver(nil)

	cases := []struct {
		intent string
		want   string
	}{
		{"pull last week's revenue numbers", "sql"},
		{"deliver the report by email", "email"},
		{"fill in the template with these variables", "template"},
		{"drop a message in the channel", "messaging"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.intent, func(t *testing.T) {
			t.Parallel()
			got, err := r.Resolve(tc.intent, nil)
			if err != nil {
				t.Fatalf("embedding Resolve err = %v", err)
			}
			found := false
			for _, c := range got {
				if c == tc.want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("embedding missed %q for %q (got %v)", tc.want, tc.intent, got)
			}
			// Best-effort check that the keyword resolver alone is weaker.
			kwGot, kwErr := kw.Resolve(tc.intent, nil)
			t.Logf("keyword baseline: %v (err=%v)", kwGot, kwErr)
		})
	}
}

func TestEmbeddingResolver_DeterministicOutput(t *testing.T) {
	t.Parallel()
	r := NewEmbeddingResolver(DefaultExamples(), EmbeddingOptions{})

	first, err := r.Resolve("query customer data and email the team", nil)
	if err != nil {
		t.Fatalf("Resolve err = %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := r.Resolve("query customer data and email the team", nil)
		if err != nil {
			t.Fatalf("non-deterministic err on iter %d: %v", i, err)
		}
		if !reflect.DeepEqual(sortedCopy(got), sortedCopy(first)) {
			t.Fatalf("iter %d differs: %v vs %v", i, got, first)
		}
	}
}

func TestEmbeddingResolver_HintsAreAlwaysIncluded(t *testing.T) {
	t.Parallel()
	r := NewEmbeddingResolver(DefaultExamples(), EmbeddingOptions{})
	got, err := r.Resolve("query the database", []string{"audit"})
	if err != nil {
		t.Fatalf("Resolve err = %v", err)
	}
	hasAudit := false
	for _, c := range got {
		if c == "audit" {
			hasAudit = true
			break
		}
	}
	if !hasAudit {
		t.Fatalf("hint 'audit' missing from output: %v", got)
	}
}

func TestEmbeddingResolver_FallsBackToKeywordOnUnknownIntent(t *testing.T) {
	t.Parallel()
	r := NewEmbeddingResolver(DefaultExamples(), EmbeddingOptions{
		Threshold: 0.95, // ridiculously strict so nothing passes
	})
	// "audit" is in the keyword table; the embedding centroid won't pass
	// at threshold=0.95 so the fallback should fire and find it.
	got, err := r.Resolve("audit log entry", nil)
	if err != nil {
		t.Fatalf("Resolve err = %v", err)
	}
	found := false
	for _, c := range got {
		if c == "audit" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fallback to keyword resolver did not find 'audit' in %v", got)
	}
}

func TestEmbeddingResolver_NoFallbackReturnsNoCapabilities(t *testing.T) {
	t.Parallel()
	// Configure with no examples and a keyword fallback that can't match either.
	r := NewEmbeddingResolver(nil, EmbeddingOptions{
		Fallback: NewKeywordResolver(map[string][]string{}),
	})
	_, err := r.Resolve("complete gibberish unrelated content", nil)
	if !errors.Is(err, ErrNoCapabilities) {
		t.Fatalf("expected ErrNoCapabilities, got %v", err)
	}
}

func TestEmbeddingResolver_HandlesEmptyInputs(t *testing.T) {
	t.Parallel()
	r := NewEmbeddingResolver(DefaultExamples(), EmbeddingOptions{})

	if _, err := r.Resolve("", nil); !errors.Is(err, ErrNoCapabilities) {
		t.Fatalf("expected ErrNoCapabilities for empty input, got %v", err)
	}
	got, err := r.Resolve("", []string{"sql"})
	if err != nil {
		t.Fatalf("Resolve hint-only err = %v", err)
	}
	if len(got) != 1 || got[0] != "sql" {
		t.Fatalf("hint-only result = %v, want [sql]", got)
	}
}

func TestEmbeddingResolver_DimAndThresholdDefaults(t *testing.T) {
	t.Parallel()
	r := NewEmbeddingResolver(DefaultExamples(), EmbeddingOptions{Dim: 0, Threshold: 0})
	if r.dim != 256 {
		t.Fatalf("dim default = %d, want 256", r.dim)
	}
	if r.threshold != 0.20 {
		t.Fatalf("threshold default = %v, want 0.20", r.threshold)
	}
}

func TestEmbeddingResolver_SkipsMalformedExamples(t *testing.T) {
	t.Parallel()
	r := NewEmbeddingResolver([]CapabilityExample{
		{"  ", "phrase"},
		{"sql", ""},
		{"sql", "query the customer database"},
	}, EmbeddingOptions{})

	if len(r.centroids) != 1 {
		t.Fatalf("centroids = %v, want only sql", r.centroids)
	}
	if _, ok := r.centroids["sql"]; !ok {
		t.Fatalf("sql centroid missing: %v", r.centroids)
	}
}
