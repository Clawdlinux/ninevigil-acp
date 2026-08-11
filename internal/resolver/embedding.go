/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"errors"
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

// EmbeddingResolver implements [Resolver] using a deterministic
// hash-based TF-IDF embedding.
//
// Each capability is represented by a centroid vector built from
// example phrases supplied at construction time. At query time the
// intent is vectorized the same way and we keep capabilities whose
// cosine similarity to the centroid is at or above [Threshold].
//
// The implementation is intentionally pure-Go and dependency-free:
//
//   - No model downloads, no Python, no GPU.
//   - Same vector for the same input on every machine.
//   - Tiny memory footprint (default 256 hash buckets).
//
// This is not a frontier-quality embedding model. The point is to be
// noticeably better than substring matching on real-world intent
// variation while staying deterministic and offline. When you outgrow
// it, swap the implementation behind the [Resolver] interface; nothing
// upstream changes.
//
// EmbeddingResolver is goroutine-safe after construction.
type EmbeddingResolver struct {
	dim       int
	threshold float64
	keywords  *KeywordResolver     // fallback when no centroid passes
	centroids map[string][]float64 // capability -> centroid
}

// CapabilityExample is one labeled training phrase used to build a
// capability's centroid.
type CapabilityExample struct {
	Capability string
	Phrase     string
}

// EmbeddingOptions configures [NewEmbeddingResolver].
type EmbeddingOptions struct {
	// Dim is the dimensionality of the hashed-bag-of-words vector.
	// Must be > 0; default 256.
	Dim int
	// Threshold is the minimum cosine similarity (0..1) for a
	// capability to be returned. Default 0.20.
	Threshold float64
	// Fallback is consulted with the original intent + hints when no
	// centroid passes the threshold. nil disables fallback.
	Fallback Resolver
}

// DefaultExamples returns the seeded capability examples used by the
// reference implementation. They cover the same five capabilities as
// the keyword table (sql, audit, template, email, messaging) plus a
// few aliases that the keyword resolver misses.
func DefaultExamples() []CapabilityExample {
	return []CapabilityExample{
		// SQL / database
		{"sql", "query the customer database"},
		{"sql", "select rows from the warehouse"},
		{"sql", "look up records in postgres"},
		{"sql", "fetch user accounts"},
		{"sql", "run a database query"},
		{"sql", "pull last week's revenue numbers"},
		// Audit
		{"audit", "log this action for compliance"},
		{"audit", "record an audit event"},
		{"audit", "write to the audit trail"},
		{"audit", "compliance log entry"},
		// Template
		{"template", "render the weekly report template"},
		{"template", "generate a report from the data"},
		{"template", "produce the formatted document"},
		{"template", "fill in the template with these variables"},
		// Email
		{"email", "send the team an email"},
		{"email", "email the finance group the summary"},
		{"email", "deliver the report by email"},
		{"email", "mail the customers"},
		// Messaging
		{"messaging", "post a slack notification"},
		{"messaging", "send a chat message"},
		{"messaging", "notify the team in slack"},
		{"messaging", "drop a message in the channel"},
		{"slack", "post in slack"},
		{"slack", "ping slack"},
	}
}

// NewEmbeddingResolver builds an EmbeddingResolver from the given
// examples. Examples with empty Capability or Phrase are skipped.
func NewEmbeddingResolver(examples []CapabilityExample, opts EmbeddingOptions) *EmbeddingResolver {
	if opts.Dim <= 0 {
		opts.Dim = 256
	}
	if opts.Threshold == 0 {
		opts.Threshold = 0.20
	}
	if opts.Fallback == nil {
		opts.Fallback = NewKeywordResolver(nil)
	}

	// Group examples by capability.
	byCap := make(map[string][][]string)
	for _, ex := range examples {
		cap := strings.ToLower(strings.TrimSpace(ex.Capability))
		phrase := strings.TrimSpace(ex.Phrase)
		if cap == "" || phrase == "" {
			continue
		}
		byCap[cap] = append(byCap[cap], tokenize(phrase))
	}

	// Compute IDF-style weights: terms that appear in many capability
	// example sets get down-weighted.
	docFreq := make(map[string]int)
	totalDocs := 0
	for _, docs := range byCap {
		for _, doc := range docs {
			totalDocs++
			seen := make(map[string]struct{}, len(doc))
			for _, tok := range doc {
				if _, ok := seen[tok]; ok {
					continue
				}
				seen[tok] = struct{}{}
				docFreq[tok]++
			}
		}
	}
	idf := make(map[string]float64, len(docFreq))
	for tok, df := range docFreq {
		// Smoothed log idf so common tokens still contribute a little.
		idf[tok] = math.Log(float64(1+totalDocs) / float64(1+df))
	}

	// Build the capability centroid by averaging TF-IDF vectors of
	// all phrases for that capability and normalizing.
	centroids := make(map[string][]float64, len(byCap))
	for cap, docs := range byCap {
		centroid := make([]float64, opts.Dim)
		for _, doc := range docs {
			vec := tfidfVector(doc, opts.Dim, idf)
			for i, v := range vec {
				centroid[i] += v
			}
		}
		l2Normalize(centroid)
		centroids[cap] = centroid
	}

	return &EmbeddingResolver{
		dim:       opts.Dim,
		threshold: opts.Threshold,
		keywords:  opts.Fallback.(*KeywordResolver),
		centroids: centroids,
	}
}

// Resolve implements [Resolver]. It returns capabilities whose
// centroid cosine similarity is at or above the threshold, plus any
// hints (lowercased and deduplicated). Falls back to the configured
// fallback resolver when no centroid passes.
func (r *EmbeddingResolver) Resolve(intent string, hints []string) ([]string, error) {
	caps := make(map[string]struct{})

	for _, h := range hints {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			caps[h] = struct{}{}
		}
	}

	tokens := tokenize(intent)
	if len(tokens) > 0 {
		idf := make(map[string]float64) // empty -> behaves like raw TF
		vec := tfidfVector(tokens, r.dim, idf)
		l2Normalize(vec)

		for cap, centroid := range r.centroids {
			sim := cosine(vec, centroid)
			if sim >= r.threshold {
				caps[cap] = struct{}{}
			}
		}
	}

	if len(caps) == 0 {
		if r.keywords == nil {
			return nil, ErrNoCapabilities
		}
		return r.keywords.Resolve(intent, hints)
	}

	out := make([]string, 0, len(caps))
	for c := range caps {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// tfidfVector hashes each token into [0, dim) and accumulates its IDF
// weight. Tokens missing from idf get weight 1.0 (raw TF).
func tfidfVector(tokens []string, dim int, idf map[string]float64) []float64 {
	v := make([]float64, dim)
	for _, t := range tokens {
		w := 1.0
		if x, ok := idf[t]; ok && x > 0 {
			w = x
		}
		idx := int(hashBucket(t, uint32(dim)))
		v[idx] += w
	}
	return v
}

func hashBucket(s string, dim uint32) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32() % dim
}

func l2Normalize(v []float64) {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	n := math.Sqrt(sum)
	if n == 0 {
		return
	}
	for i := range v {
		v[i] /= n
	}
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	// a and b are already L2-normalized so dot is cosine.
	return dot
}

// ErrEmbedding is returned for embedding-resolver-specific failures
// that cannot be expressed with [ErrNoCapabilities].
var ErrEmbedding = errors.New("resolver: embedding error")
