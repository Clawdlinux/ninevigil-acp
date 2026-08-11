/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// DeferredResolver implements [Resolver] for interactive environments where
// intent is unknown at session start and emerges over time from observed
// tool calls.
//
// The resolver operates in three phases:
//
//  1. Cold start — no observations yet. Returns all registered capabilities
//     so the host sees the full (compacted) tool surface.
//  2. Warm — observations accumulate. Returns the union of recently-observed
//     capability domains plus a configurable margin of related capabilities
//     from the fallback resolver.
//  3. Narrowed — high-confidence observations within the sliding window.
//     Returns only the observed domain capabilities plus hints.
//
// The Observe method is goroutine-safe and may be called from a feedback
// handler concurrent with Resolve calls.
//
// DeferredResolver satisfies §4.8 of the ACP v0.2 specification (Deferred
// Intent Mode).
type DeferredResolver struct {
	mu              sync.RWMutex
	allCapabilities []string
	fallback        Resolver
	window          []observation
	windowSize      int
	narrowThreshold int
}

type observation struct {
	capability string
	ts         time.Time
}

// DeferredOptions configures a [DeferredResolver].
type DeferredOptions struct {
	// AllCapabilities is the full set of capability tags across all registered
	// tools. Returned verbatim during the cold-start phase.
	AllCapabilities []string

	// Fallback is consulted when intent text is provided alongside
	// observations. If nil, defaults to a [KeywordResolver].
	Fallback Resolver

	// WindowSize is the number of recent observations to consider.
	// Default: 10.
	WindowSize int

	// NarrowThreshold is the minimum number of observations before the
	// resolver starts narrowing. Default: 3.
	NarrowThreshold int
}

// NewDeferredResolver builds a resolver for interactive/deferred-intent
// environments.
func NewDeferredResolver(opts DeferredOptions) *DeferredResolver {
	if opts.Fallback == nil {
		opts.Fallback = NewKeywordResolver(nil)
	}
	ws := opts.WindowSize
	if ws <= 0 {
		ws = 10
	}
	nt := opts.NarrowThreshold
	if nt <= 0 {
		nt = 3
	}
	sorted := make([]string, len(opts.AllCapabilities))
	copy(sorted, opts.AllCapabilities)
	sort.Strings(sorted)

	return &DeferredResolver{
		allCapabilities: sorted,
		fallback:        opts.Fallback,
		windowSize:      ws,
		narrowThreshold: nt,
	}
}

// Observe records that a tool with the given capability was invoked. This
// is the primary signal for progressive narrowing. Goroutine-safe.
func (d *DeferredResolver) Observe(capability string) {
	cap := strings.ToLower(strings.TrimSpace(capability))
	if cap == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = append(d.window, observation{capability: cap, ts: time.Now()})
	if len(d.window) > d.windowSize {
		d.window = d.window[len(d.window)-d.windowSize:]
	}
}

// ObservationCount returns the current number of observations in the sliding
// window. Useful for testing.
func (d *DeferredResolver) ObservationCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.window)
}

// Reset clears all observations, returning the resolver to cold-start phase.
// Goroutine-safe.
func (d *DeferredResolver) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = d.window[:0]
}

// Resolve returns capabilities scoped to the current observation state.
//
// Below threshold (cold start + warming): returns all capabilities.
// At or above threshold (narrowed): returns only observed capability domains
// plus hints and any fallback-resolved intent signal.
func (d *DeferredResolver) Resolve(intent string, hints []string) ([]string, error) {
	caps := make(map[string]struct{})

	// Hints are always honored.
	for _, h := range hints {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			caps[h] = struct{}{}
		}
	}

	d.mu.RLock()
	obsCount := len(d.window)
	observed := d.observedCaps()
	d.mu.RUnlock()

	if obsCount < d.narrowThreshold {
		// Below threshold: not enough signal to narrow. Return all capabilities.
		for _, c := range d.allCapabilities {
			caps[c] = struct{}{}
		}
	} else {
		// Narrowed: only observed domains + hints.
		for c := range observed {
			caps[c] = struct{}{}
		}
		// If intent provides additional signal, add it but don't broaden to all.
		if intent != "" {
			if resolved, err := d.fallback.Resolve(intent, nil); err == nil {
				for _, c := range resolved {
					caps[c] = struct{}{}
				}
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

// observedCaps returns the unique capability set from the current window.
// Caller must hold at least d.mu.RLock.
func (d *DeferredResolver) observedCaps() map[string]struct{} {
	caps := make(map[string]struct{}, len(d.window))
	for _, o := range d.window {
		caps[o.capability] = struct{}{}
	}
	return caps
}
