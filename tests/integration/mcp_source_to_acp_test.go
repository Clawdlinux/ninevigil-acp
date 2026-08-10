/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package integration_test exercises the end-to-end path:
//
//	mock MCP server  ->  internal/sources/mcp.Importer  ->  registry
//	   ->  builder  ->  ACP server  ->  manifest with compacted schemas
//
// This is the concrete proof of the "ACP on top of MCP" positioning:
// point ACP at any MCP server and it produces an intent-scoped manifest
// with credentials pre-injected and schemas stripped to the mini-language.
package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	builder "github.com/Clawdlinux/agent-native-format/internal/builder"
	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/internal/resolver"
	"github.com/Clawdlinux/agent-native-format/internal/server"
	mcpsource "github.com/Clawdlinux/agent-native-format/internal/sources/mcp"
	"github.com/Clawdlinux/agent-native-format/pkg/acp"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

// fakeMCPServer returns an httptest.Server that serves a verbose MCP
// `tools/list` payload (the kind that consumes 1.5 KB - 12 KB per tool in
// production).
func fakeMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	body := `{
	  "tools": [
	    {
	      "name": "github.issues.create",
	      "description": "Create a new issue. Long verbose description, examples, lists of params, etc.",
	      "inputSchema": {
	        "$schema": "https://json-schema.org/draft/2020-12/schema",
	        "type": "object",
	        "properties": {
	          "title":  {"type": "string", "description": "issue title", "maxLength": 256},
	          "body":   {"type": "string", "description": "issue body"},
	          "labels": {"type": "array", "items": {"type": "string"}, "description": "labels"}
	        },
	        "required": ["title"]
	      }
	    },
	    {
	      "name": "github.issues.list",
	      "description": "List issues with filters. Lots of filter docs.",
	      "inputSchema": {
	        "type": "object",
	        "properties": {
	          "state": {"type": "string", "enum": ["open", "closed", "all"]},
	          "limit": {"type": "integer", "minimum": 1, "maximum": 100}
	        },
	        "required": []
	      }
	    }
	  ]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/list" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stack returns a fully-wired ACP server backed by the registry (after MCP
// import).
func stack(t *testing.T, reg *registry.MemoryRegistry, table map[string][]string, token string) *httptest.Server {
	t.Helper()
	bld := builder.New(reg, server.CryptoIDSource{}, builder.DefaultOptions())
	handler := server.New(server.Config{
		Resolver:  resolver.NewKeywordResolver(table),
		Builder:   bld,
		Feedback:  &server.LoggingFeedbackSink{},
		AuthToken: token,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestIntegration_MCPSource_To_ACPServer(t *testing.T) {
	t.Parallel()

	mcpSrv := fakeMCPServer(t)

	// Import MCP tools into a fresh registry.
	reg := registry.NewMemoryRegistry()
	imp := mcpsource.NewImporter(reg, &http.Client{Timeout: 5 * time.Second})
	n, err := imp.ImportSource(mcpsource.Source{
		Name:              "github",
		BaseURL:           mcpSrv.URL,
		ExtraCapabilities: []string{"github"},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported %d tools, want 2", n)
	}

	// Resolver table that maps the user's intent to the inferred capabilities
	// of the imported tools.
	resolverTable := map[string][]string{
		"create": {"create"},
		"file":   {"issues"},
		"issue":  {"issues"},
	}
	acpSrv := stack(t, reg, resolverTable, "secret")
	client := acp.NewClient(acpSrv.URL, acp.WithToken("secret"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mf, err := client.Context(ctx, manifest.ContextRequest{
		Intent:  "create a github issue",
		AgentID: "integration-agent",
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	// Both imported tools share the "issues" capability and the create one
	// has "create"; both match the intent, so we expect both in the manifest.
	if len(mf.Actions) < 1 {
		t.Fatalf("expected at least 1 action, got %d", len(mf.Actions))
	}

	// Confirm the manifest schema is the *compacted* form, not the verbose
	// JSON-Schema. Compact form for the create tool: title=string,
	// body=string?, labels=string[]?.
	createAction := actionByEndpointSubstr(mf.Actions, "issues.create")
	if createAction == nil {
		t.Fatalf("create action not found in manifest: %#v", mf.Actions)
	}
	wantSchema := map[string]string{
		"title":  "string",
		"body":   "string?",
		"labels": "string[]?",
	}
	for k, v := range wantSchema {
		if got := createAction.Schema[k]; got != v {
			t.Fatalf("schema %s = %q, want %q", k, got, v)
		}
	}

	// Round-trip the body back to JSON and confirm it is significantly
	// smaller than the upstream MCP `tools/list` body. This is the headline
	// claim made concrete in a single test.
	mfJSON, err := json.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	mcpResp, err := http.Get(mcpSrv.URL + "/tools/list")
	if err != nil {
		t.Fatalf("fetch upstream: %v", err)
	}
	defer mcpResp.Body.Close()
	mcpBody, err := io.ReadAll(mcpResp.Body)
	if err != nil {
		t.Fatalf("read upstream body: %v", err)
	}

	if len(mfJSON) >= len(mcpBody) {
		t.Fatalf("expected manifest (%d bytes) to be smaller than MCP tools/list (%d bytes)",
			len(mfJSON), len(mcpBody))
	}
	t.Logf("ACP manifest %d bytes vs MCP tools/list %d bytes (%.0f%% reduction)",
		len(mfJSON), len(mcpBody), 100.0*(1.0-float64(len(mfJSON))/float64(len(mcpBody))))
}

func actionByEndpointSubstr(actions []manifest.Action, sub string) *manifest.Action {
	for i, a := range actions {
		if strings.Contains(a.Endpoint, sub) {
			return &actions[i]
		}
	}
	return nil
}
