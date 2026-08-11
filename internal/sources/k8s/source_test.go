/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package k8s

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

func ann(kvs ...string) map[string]string {
	if len(kvs)%2 != 0 {
		panic("ann requires key/value pairs")
	}
	out := make(map[string]string, len(kvs)/2)
	for i := 0; i < len(kvs); i += 2 {
		out[AnnotationPrefix+kvs[i]] = kvs[i+1]
	}
	return out
}

func TestImporter_RegisterAll_HappyPath(t *testing.T) {
	t.Parallel()

	svcs := []Service{
		{
			Namespace: "billing", Name: "billing-api", Port: 8080,
			Annotations: ann(
				"expose", "true",
				"tool-id", "billing.query",
				"capabilities", "sql, billing",
				"endpoint-path", "/v1/query",
				"method", "post",
				"schema", "sql:string, limit:int?",
				"timeout", "10s",
			),
		},
		{
			// Not opted in; should be skipped.
			Namespace: "ops", Name: "monitoring",
			Annotations: ann("tool-id", "metrics.read"),
		},
		{
			Namespace: "vault", Name: "vault-api", Port: 8200,
			Annotations: ann(
				"expose", "TRUE",
				"tool-id", "vault.fetch",
				"capabilities", "secret",
				"require-approval", "true",
			),
		},
	}

	reg := registry.NewMemoryRegistry()
	imp := NewImporter(reg)
	n, err := imp.RegisterAll(svcs)
	if err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if n != 2 {
		t.Fatalf("registered %d, want 2", n)
	}

	got, err := reg.Get("billing.query")
	if err != nil {
		t.Fatalf("billing.query: %v", err)
	}
	if got.Endpoint != "http://billing-api.billing.svc.cluster.local:8080/v1/query" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
	if got.Method != "POST" {
		t.Fatalf("method = %q", got.Method)
	}
	if got.Auth != manifest.AuthPreInjected {
		t.Fatalf("auth = %q", got.Auth)
	}
	if got.Timeout != "10s" {
		t.Fatalf("timeout = %q", got.Timeout)
	}
	wantCaps := []string{"billing", "sql"}
	caps := append([]string(nil), got.Capabilities...)
	sort.Strings(caps)
	if !reflect.DeepEqual(caps, wantCaps) {
		t.Fatalf("capabilities = %v, want %v", caps, wantCaps)
	}
	wantSchema := map[string]string{"sql": "string", "limit": "int?"}
	if !reflect.DeepEqual(got.Schema, wantSchema) {
		t.Fatalf("schema = %v, want %v", got.Schema, wantSchema)
	}

	approval, err := reg.Get("vault.fetch")
	if err != nil {
		t.Fatalf("vault.fetch: %v", err)
	}
	if !approval.RequireApprov {
		t.Fatal("RequireApprov must be true")
	}
}

func TestImporter_RegisterAll_DefaultsApplied(t *testing.T) {
	t.Parallel()

	svcs := []Service{
		{
			Namespace: "team-a", Name: "echo",
			Annotations: ann(
				"expose", "true",
				"tool-id", "echo.say",
				"capabilities", "echo",
			),
		},
	}
	reg := registry.NewMemoryRegistry()
	if _, err := NewImporter(reg).RegisterAll(svcs); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	got, err := reg.Get("echo.say")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Method != "POST" {
		t.Fatalf("default method = %q", got.Method)
	}
	if got.Timeout != "30s" {
		t.Fatalf("default timeout = %q", got.Timeout)
	}
	// Default endpoint path is "/", port falls back to 80, host falls back to
	// the Kubernetes DNS form.
	want := "http://echo.team-a.svc.cluster.local:80/"
	if got.Endpoint != want {
		t.Fatalf("default endpoint = %q, want %q", got.Endpoint, want)
	}
}

func TestImporter_RegisterAll_StopsOnFirstError(t *testing.T) {
	t.Parallel()

	svcs := []Service{
		{
			Namespace: "ok", Name: "good",
			Annotations: ann(
				"expose", "true",
				"tool-id", "good.tool",
				"capabilities", "x",
			),
		},
		{
			Namespace: "bad", Name: "missing-id",
			Annotations: ann(
				"expose", "true",
				// No tool-id.
				"capabilities", "x",
			),
		},
		{
			Namespace: "after", Name: "should-not-register",
			Annotations: ann(
				"expose", "true",
				"tool-id", "after.tool",
				"capabilities", "x",
			),
		},
	}

	reg := registry.NewMemoryRegistry()
	n, err := NewImporter(reg).RegisterAll(svcs)
	if err == nil {
		t.Fatalf("expected error, got nil (n=%d)", n)
	}
	if n != 1 {
		t.Fatalf("expected partial progress n=1, got %d", n)
	}
	if _, err := reg.Get("good.tool"); err != nil {
		t.Fatalf("good.tool should be registered: %v", err)
	}
	if _, err := reg.Get("after.tool"); err == nil {
		t.Fatal("after.tool must NOT be registered after the failure")
	}
}

func TestImporter_RegisterAll_RejectsMissingCapabilities(t *testing.T) {
	t.Parallel()

	svcs := []Service{
		{
			Namespace: "x", Name: "y",
			Annotations: ann(
				"expose", "true",
				"tool-id", "y.id",
				// missing capabilities
			),
		},
	}
	if _, err := NewImporter(registry.NewMemoryRegistry()).RegisterAll(svcs); err == nil {
		t.Fatal("expected error for missing capabilities annotation")
	}
}

func TestImporter_RegisterAll_HonorsClusterHostOverride(t *testing.T) {
	t.Parallel()

	svcs := []Service{
		{
			Namespace: "n", Name: "s", ClusterHost: "external.example.com", Port: 443,
			Annotations: ann(
				"expose", "true",
				"tool-id", "ext.tool",
				"capabilities", "ext",
				"endpoint-path", "v1/x", // missing leading slash should be normalized
			),
		},
	}
	reg := registry.NewMemoryRegistry()
	if _, err := NewImporter(reg).RegisterAll(svcs); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	got, err := reg.Get("ext.tool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Endpoint != "http://external.example.com:443/v1/x" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
	if len(got.Egress) != 1 || got.Egress[0] != "external.example.com" {
		t.Fatalf("egress = %v, want [external.example.com]", got.Egress)
	}
}

func TestParseCompactSchema_TolerantToWhitespaceAndDupes(t *testing.T) {
	t.Parallel()
	got := parseCompactSchema("  sql : string  ,  limit:int?  ,  ,malformed,name:type")
	want := map[string]string{
		"sql":   "string",
		"limit": "int?",
		"name":  "type",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema = %v, want %v", got, want)
	}
}

func TestSplitCSV_LowercasesAndDedupes(t *testing.T) {
	t.Parallel()
	got := splitCSV(" SQL , audit, sql ,, AUDIT")
	want := []string{"audit", "sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitCSV = %v, want %v", got, want)
	}
}
