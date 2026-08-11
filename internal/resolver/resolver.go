/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package resolver maps a free-form agent intent (plus optional capability
// hints) to the set of capability tags the registry indexes.
//
// Week 1 ships a deterministic keyword resolver. Week 3 will replace its
// internals with an embedding-based matcher trained on benchmark traces.
package resolver

import (
	"errors"
	"sort"
	"strings"
	"unicode"
)

// ErrNoCapabilities is returned when neither intent nor capability hints
// produced any capability tags from the keyword table.
var ErrNoCapabilities = errors.New("resolver: no capabilities resolved")

// Resolver turns a request into capability tags.
type Resolver interface {
	Resolve(intent string, hints []string) ([]string, error)
}

// KeywordResolver matches lowercase token substrings of the intent against a
// keyword -> capability index. Capability hints provided by the agent are
// always honored.
type KeywordResolver struct {
	keywords map[string][]string
}

// DefaultKeywordTable is the seeded keyword -> capability index used by the
// Week 1 server. Keep it small and high-signal: every entry should be
// exercised by at least one benchmark scenario.
func DefaultKeywordTable() map[string][]string {
	return map[string][]string{
		"sql":        {"sql"},
		"query":      {"sql"},
		"database":   {"sql", "database"},
		"db":         {"sql", "database"},
		"customer":   {"sql"},
		"slack":      {"messaging", "slack"},
		"message":    {"messaging"},
		"notify":     {"messaging"},
		"audit":      {"audit"},
		"log":        {"audit"},
		"compliance": {"audit"},
		"template":   {"template"},
		"render":     {"template"},
		"report":     {"template"},
		"email":      {"email"},
		"send":       {"email", "messaging"},
		"team":       {"email"},
	}
}

// NewKeywordResolver builds a resolver around the provided keyword table.
// A nil or empty table falls back to DefaultKeywordTable.
func NewKeywordResolver(table map[string][]string) *KeywordResolver {
	if len(table) == 0 {
		table = DefaultKeywordTable()
	}
	normalized := make(map[string][]string, len(table))
	for k, caps := range table {
		normalized[strings.ToLower(strings.TrimSpace(k))] = caps
	}
	return &KeywordResolver{keywords: normalized}
}

// Resolve returns a deterministic, deduplicated list of capability tags. The
// intent is tokenized on Unicode word boundaries; hints are merged in directly.
func (r *KeywordResolver) Resolve(intent string, hints []string) ([]string, error) {
	caps := make(map[string]struct{})

	for _, h := range hints {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			caps[h] = struct{}{}
		}
	}

	for _, tok := range tokenize(intent) {
		if matches, ok := r.keywords[tok]; ok {
			for _, c := range matches {
				caps[strings.ToLower(c)] = struct{}{}
			}
		}
	}

	if len(caps) == 0 {
		return nil, ErrNoCapabilities
	}

	out := make([]string, 0, len(caps))
	for c := range caps {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

func tokenize(s string) []string {
	if s == "" {
		return nil
	}
	tokens := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return tokens
}
