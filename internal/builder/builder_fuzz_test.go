/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package builder

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

func makeFuzzTool(depsCaps []string) registry.Tool {
	return registry.Tool{
		ID:            "fuzz-tool",
		Type:          "http",
		Endpoint:      "http://fuzz",
		Method:        "POST",
		Schema:        map[string]string{"x": "string"},
		Auth:          manifest.AuthPreInjected,
		Capabilities:  []string{"fuzz"},
		DependsOnCaps: depsCaps,
	}
}

// FuzzDependsOnFor asserts that dependsOnFor:
//   - Never panics for arbitrary inputs.
//   - Never includes the action's own ID (no self-cycles).
//   - Returns a deterministic sorted unique list.
//   - Only references action IDs that exist in the available map.
func FuzzDependsOnFor(f *testing.F) {
	seeds := [][]string{
		{"a1", "sql", "audit", "template"},
		{"a2", "sql"},
		{"a3", "sql", "sql"},
	}
	for _, seed := range seeds {
		// pack as comma-joined: "<self>;<dep1>,<dep2>;<availKey>=<id>,<id>"
		caps := strings.Join(seed[1:], ",")
		f.Add(seed[0], caps, "sql=a1,a2;audit=a3;template=a2,a4")
	}

	f.Fuzz(func(t *testing.T, selfID, depsCSV, availSpec string) {
		if !utf8.ValidString(selfID) || !utf8.ValidString(depsCSV) || !utf8.ValidString(availSpec) {
			return
		}
		// Skip degenerate IDs that contain spec separators.
		for _, ch := range []string{",", ";", "="} {
			if strings.Contains(selfID, ch) {
				return
			}
		}

		var deps []string
		for _, d := range strings.Split(depsCSV, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				deps = append(deps, d)
			}
		}

		// Build the available map from the spec.
		available := map[string][]string{}
		for _, group := range strings.Split(availSpec, ";") {
			parts := strings.SplitN(group, "=", 2)
			if len(parts) != 2 {
				continue
			}
			cap := strings.TrimSpace(strings.ToLower(parts[0]))
			if cap == "" {
				continue
			}
			for _, id := range strings.Split(parts[1], ",") {
				id = strings.TrimSpace(id)
				if id == "" {
					continue
				}
				available[cap] = append(available[cap], id)
			}
		}

		tool := makeFuzzTool(deps)
		got := dependsOnFor(selfID, tool, available)

		seen := map[string]struct{}{}
		for i, id := range got {
			if id == selfID {
				t.Fatalf("self-cycle: %q in %v", selfID, got)
			}
			if _, dup := seen[id]; dup {
				t.Fatalf("duplicate %q in %v", id, got)
			}
			seen[id] = struct{}{}
			if i > 0 && got[i-1] >= id {
				t.Fatalf("not sorted: %v", got)
			}
			// Every returned id must come from `available`.
			found := false
			for _, ids := range available {
				for _, a := range ids {
					if a == id {
						found = true
						break
					}
				}
			}
			if !found {
				t.Fatalf("returned id %q not present in available map %v", id, available)
			}
		}
	})
}
