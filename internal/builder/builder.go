/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package builder constructs ACP Execution Manifests from resolved
// capabilities and a tool source.
//
// The builder is intentionally pure: given the same input it always returns
// the same manifest body (manifest IDs are minted by an injected ID source so
// that tests can be deterministic).
package builder

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

//go:generate ../../bin/mockgen -source=builder.go -destination=mocks_test.go -package=builder

// ToolSource is the consumer-defined view of the registry that the builder
// requires. Defining it here (rather than importing registry.Registry) keeps
// builder tests free of the registry implementation and makes the boundary
// explicit per Go interface conventions.
type ToolSource interface {
	Lookup(capabilities []string) []registry.Tool
}

// IDSource mints opaque, unique manifest IDs.
type IDSource interface {
	NewID() string
}

// Options configures a Builder. Zero values fall back to safe defaults.
type Options struct {
	TTL                string
	FeedbackEndpoint   string
	MaxTokensPerAction int
	AuditLevel         manifest.AuditLevel
}

// DefaultOptions returns the values used by the Week 1 server when the
// caller does not override them.
func DefaultOptions() Options {
	return Options{
		TTL:                "300s",
		FeedbackEndpoint:   "/v1/feedback",
		MaxTokensPerAction: 15000,
		AuditLevel:         manifest.AuditFull,
	}
}

// Builder builds Execution Manifests.
type Builder struct {
	tools ToolSource
	ids   IDSource
	opts  Options
}

// New returns a Builder. Both tools and ids must be non-nil.
func New(tools ToolSource, ids IDSource, opts Options) *Builder {
	if opts.TTL == "" {
		opts.TTL = DefaultOptions().TTL
	}
	if opts.FeedbackEndpoint == "" {
		opts.FeedbackEndpoint = DefaultOptions().FeedbackEndpoint
	}
	if opts.MaxTokensPerAction == 0 {
		opts.MaxTokensPerAction = DefaultOptions().MaxTokensPerAction
	}
	if opts.AuditLevel == "" {
		opts.AuditLevel = DefaultOptions().AuditLevel
	}
	return &Builder{tools: tools, ids: ids, opts: opts}
}

// ErrNoMatchingTools is returned when the resolver produced capabilities but
// the registry has nothing that satisfies them.
var ErrNoMatchingTools = errors.New("builder: no tools match resolved capabilities")

// Build assembles a manifest. It assigns deterministic short action IDs
// (a1, a2, ...) ordered by tool ID, computes depends_on from each tool's
// DependsOnCaps against the capabilities present in this manifest, collects
// the union of egress allow-lists, and lists tools with RequireApprov in
// boundaries.require_approval.
func (b *Builder) Build(req manifest.ContextRequest, capabilities []string) (manifest.ExecutionManifest, error) {
	tools := b.tools.Lookup(capabilities)
	if len(tools) == 0 {
		return manifest.ExecutionManifest{}, ErrNoMatchingTools
	}

	// Stable ordering: by tool ID. The registry already guarantees this but
	// re-sort defensively because a mock may return any order.
	sort.Slice(tools, func(i, j int) bool { return tools[i].ID < tools[j].ID })

	// Index every capability available in this manifest so depends_on can be
	// computed without relying on global registry state.
	availableCaps := make(map[string][]string) // capability -> action IDs that satisfy it
	actionIDs := make([]string, len(tools))
	for i, t := range tools {
		actionIDs[i] = fmt.Sprintf("a%d", i+1)
		for _, c := range t.Capabilities {
			c = strings.ToLower(c)
			availableCaps[c] = append(availableCaps[c], actionIDs[i])
		}
	}

	actions := make([]manifest.Action, 0, len(tools))
	egressSet := make(map[string]struct{})
	approvals := make([]string, 0)

	for i, t := range tools {
		dep := dependsOnFor(actionIDs[i], t, availableCaps)
		actions = append(actions, manifest.Action{
			ID:        actionIDs[i],
			Type:      t.Type,
			Endpoint:  t.Endpoint,
			Method:    t.Method,
			Schema:    t.Schema,
			Auth:      t.Auth,
			Timeout:   t.Timeout,
			DependsOn: dep,
		})
		for _, host := range t.Egress {
			egressSet[host] = struct{}{}
		}
		if t.RequireApprov {
			approvals = append(approvals, actionIDs[i])
		}
	}

	egress := make([]string, 0, len(egressSet))
	for h := range egressSet {
		egress = append(egress, h)
	}
	sort.Strings(egress)

	return manifest.ExecutionManifest{
		ManifestID: b.ids.NewID(),
		Version:    manifest.ProtocolVersion,
		TTL:        b.opts.TTL,
		Actions:    actions,
		Boundaries: manifest.Boundary{
			Egress:             egress,
			MaxTokensPerAction: b.opts.MaxTokensPerAction,
			RequireApproval:    approvals,
			AuditLevel:         b.opts.AuditLevel,
		},
		FeedbackEndpoint: b.opts.FeedbackEndpoint,
	}, nil
}

func dependsOnFor(selfID string, t registry.Tool, available map[string][]string) []string {
	if len(t.DependsOnCaps) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, cap := range t.DependsOnCaps {
		for _, id := range available[strings.ToLower(cap)] {
			if id == selfID {
				continue
			}
			seen[id] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
