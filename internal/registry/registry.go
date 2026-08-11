/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package registry holds the in-memory tool registry used by the ACP server
// to compute Execution Manifests.
package registry

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

// ErrNotFound is returned when a tool is not registered.
var ErrNotFound = errors.New("registry: tool not found")

// Tool is the registry's internal representation of a callable backend.
//
// Capabilities are the lowercase tags the resolver emits (e.g. "sql",
// "messaging", "audit"). Egress is the host(s) the auth proxy must allow.
// DependsOnCaps declares what capabilities, if present in the same manifest,
// must execute before this tool.
type Tool struct {
	ID            string
	Type          string
	Endpoint      string
	Method        string
	Schema        map[string]string
	Auth          manifest.AuthMode
	Timeout       string
	Capabilities  []string
	Egress        []string
	DependsOnCaps []string
	RequireApprov bool
}

// Registry is the read/write surface implemented by MemoryRegistry.
type Registry interface {
	Register(t Tool) error
	Get(id string) (Tool, error)
	Lookup(capabilities []string) []Tool
	All() []Tool
}

// MemoryRegistry is a goroutine-safe in-memory Registry implementation.
type MemoryRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewMemoryRegistry constructs an empty registry.
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{tools: make(map[string]Tool)}
}

// Register inserts or replaces a tool. Returns an error for empty IDs or
// missing capability tags so misconfiguration fails loudly.
func (r *MemoryRegistry) Register(t Tool) error {
	if strings.TrimSpace(t.ID) == "" {
		return errors.New("registry: tool ID is required")
	}
	if len(t.Capabilities) == 0 {
		return errors.New("registry: tool capabilities are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.ID] = t
	return nil
}

// Get returns a tool by ID.
func (r *MemoryRegistry) Get(id string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[id]
	if !ok {
		return Tool{}, ErrNotFound
	}
	return t, nil
}

// Lookup returns the tools whose capability tags intersect the requested
// capabilities. Results are returned in deterministic order (by tool ID) so
// that downstream manifest IDs and depends_on chains are reproducible.
func (r *MemoryRegistry) Lookup(capabilities []string) []Tool {
	want := normalizeSet(capabilities)
	if len(want) == 0 {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]Tool, 0)
	for _, t := range r.tools {
		for _, c := range t.Capabilities {
			if _, ok := want[strings.ToLower(c)]; ok {
				matched = append(matched, t)
				break
			}
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	return matched
}

// All returns every registered tool, sorted by ID.
func (r *MemoryRegistry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func normalizeSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			out[s] = struct{}{}
		}
	}
	return out
}
