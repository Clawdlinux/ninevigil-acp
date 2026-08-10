/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package k8s implements a Kubernetes source adapter for the ACP
// registry.
//
// The adapter scans Kubernetes Services in one or more namespaces and
// registers each annotated Service as an ACP Tool. This is the
// Kubernetes-native analog of internal/sources/mcp: instead of GETting
// /tools/list off an MCP server, it list-watches Services and converts
// the ones that opt in via annotations.
//
// Annotation contract (all under prefix `acp.clawdlinux.org/`):
//
//	acp.clawdlinux.org/expose          = "true"            (required opt-in)
//	acp.clawdlinux.org/tool-id         = "billing.query"   (required: ACP tool ID)
//	acp.clawdlinux.org/capabilities    = "sql,billing"     (required: comma-separated)
//	acp.clawdlinux.org/endpoint-path   = "/v1/query"       (default: /)
//	acp.clawdlinux.org/method          = "POST"            (default: POST)
//	acp.clawdlinux.org/schema          = "sql:string,limit:int?"  (compact form)
//	acp.clawdlinux.org/timeout         = "30s"             (default: 30s)
//	acp.clawdlinux.org/require-approval = "true"           (default: false)
//
// The adapter does NOT depend on client-go: it accepts a slice of
// `Service` objects (a tiny mirror struct) so callers can either build
// it themselves from the apimachinery types or hand-fill from YAML in
// tests. This keeps the ACP server's dependency surface minimal; a
// separate repo (or the agentic-operator) can do the actual list-watch
// and feed Service slices into Importer.RegisterAll.
package k8s

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

// AnnotationPrefix is the common prefix for all ACP-relevant Service
// annotations.
const AnnotationPrefix = "acp.clawdlinux.org/"

// annotation keys (without the prefix).
const (
	keyExpose          = "expose"
	keyToolID          = "tool-id"
	keyCapabilities    = "capabilities"
	keyEndpointPath    = "endpoint-path"
	keyMethod          = "method"
	keySchema          = "schema"
	keyTimeout         = "timeout"
	keyRequireApproval = "require-approval"
)

// Service is a tiny mirror of the bits of a Kubernetes Service the
// adapter cares about. Callers building from real client-go objects
// just populate these fields.
type Service struct {
	Namespace   string
	Name        string
	ClusterHost string            // e.g. "billing.team-a.svc.cluster.local"
	Port        int               // primary HTTP port
	Annotations map[string]string // ACP-prefixed annotations only or all; the adapter filters
}

// Importer registers ACP tools derived from a stream of annotated
// Services.
type Importer struct {
	registry *registry.MemoryRegistry
}

// NewImporter constructs an Importer.
func NewImporter(reg *registry.MemoryRegistry) *Importer {
	return &Importer{registry: reg}
}

// RegisterAll walks the supplied Services, converts the ones that opt
// in via the `acp.clawdlinux.org/expose=true` annotation, and
// registers each in the ACP registry. Returns the number of
// successfully registered tools and any error encountered. A
// malformed service stops iteration; partial progress is preserved.
func (i *Importer) RegisterAll(services []Service) (int, error) {
	count := 0
	for _, svc := range services {
		if !optedIn(svc) {
			continue
		}
		tool, err := convert(svc)
		if err != nil {
			return count, fmt.Errorf("k8s: convert %s/%s: %w", svc.Namespace, svc.Name, err)
		}
		if err := i.registry.Register(tool); err != nil {
			return count, fmt.Errorf("k8s: register %s: %w", tool.ID, err)
		}
		count++
	}
	return count, nil
}

func optedIn(svc Service) bool {
	v, ok := svc.Annotations[AnnotationPrefix+keyExpose]
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

func convert(svc Service) (registry.Tool, error) {
	a := svc.Annotations
	id := strings.TrimSpace(a[AnnotationPrefix+keyToolID])
	if id == "" {
		return registry.Tool{}, errors.New("missing acp.clawdlinux.org/tool-id annotation")
	}
	caps := splitCSV(a[AnnotationPrefix+keyCapabilities])
	if len(caps) == 0 {
		return registry.Tool{}, errors.New("missing acp.clawdlinux.org/capabilities annotation")
	}

	host := svc.ClusterHost
	if host == "" {
		// Fall back to the Kubernetes DNS form.
		host = fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace)
	}
	port := svc.Port
	if port <= 0 {
		port = 80
	}
	path := strings.TrimSpace(a[AnnotationPrefix+keyEndpointPath])
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	method := strings.ToUpper(strings.TrimSpace(a[AnnotationPrefix+keyMethod]))
	if method == "" {
		method = "POST"
	}

	timeout := strings.TrimSpace(a[AnnotationPrefix+keyTimeout])
	if timeout == "" {
		timeout = "30s"
	}

	schema := parseCompactSchema(a[AnnotationPrefix+keySchema])

	requireApproval := strings.EqualFold(
		strings.TrimSpace(a[AnnotationPrefix+keyRequireApproval]), "true")

	endpoint := fmt.Sprintf("http://%s:%d%s", host, port, path)

	return registry.Tool{
		ID:            id,
		Type:          "http",
		Endpoint:      endpoint,
		Method:        method,
		Schema:        schema,
		Auth:          manifest.AuthPreInjected,
		Timeout:       timeout,
		Capabilities:  caps,
		Egress:        []string{host},
		RequireApprov: requireApproval,
	}, nil
}

// splitCSV splits "a, b , ,c" -> [a b c] (lowercased, deduped, sorted).
func splitCSV(raw string) []string {
	seen := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p == "" {
			continue
		}
		seen[p] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// parseCompactSchema reads "sql:string,limit:int?" -> {sql: string, limit: int?}.
// Whitespace is tolerated. Empty input yields an empty (non-nil) map.
func parseCompactSchema(raw string) map[string]string {
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		colon := strings.IndexByte(pair, ':')
		if colon <= 0 || colon == len(pair)-1 {
			continue
		}
		name := strings.TrimSpace(pair[:colon])
		typ := strings.TrimSpace(pair[colon+1:])
		if name == "" || typ == "" {
			continue
		}
		out[name] = typ
	}
	return out
}
