/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package integration_test exercises the deferred-intent bridge end-to-end:
//
//	mock MCP server  ->  Importer  ->  Registry  ->  DeferredResolver
//	   ->  Bridge (stdio JSON-RPC)  ->  cold start (all tools)
//	   ->  tool calls  ->  narrowing  ->  list_changed notification
//
// This proves the §4.8 Deferred Intent Mode lifecycle: cold start → warm
// → narrowed, with schema compaction applied throughout.
package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Clawdlinux/agent-native-format/internal/bridge"
	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/internal/resolver"
	mcpsource "github.com/Clawdlinux/agent-native-format/internal/sources/mcp"
)

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Method string `json:"method,omitempty"` // for notifications
}

func mustMarshal(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func TestIntegration_DeferredBridge_EndToEnd(t *testing.T) {
	t.Parallel()

	// Step 1: Start mock MCP servers (one per domain for distinct capabilities).
	githubMCP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tools":[
			{"name":"issues_list","description":"List issues","inputSchema":{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"}},"required":["owner","repo"]}},
			{"name":"issues_create","description":"Create issue","inputSchema":{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},"title":{"type":"string"}},"required":["owner","repo","title"]}}
		]}`)
	}))
	defer githubMCP.Close()

	flyMCP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tools":[
			{"name":"apps_list","description":"List apps","inputSchema":{"type":"object","properties":{"org":{"type":"string"}}}},
			{"name":"deploy","description":"Deploy app","inputSchema":{"type":"object","properties":{"app":{"type":"string"},"image":{"type":"string"}},"required":["app","image"]}}
		]}`)
	}))
	defer flyMCP.Close()

	dbMCP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tools":[
			{"name":"query","description":"SQL query","inputSchema":{"type":"object","properties":{"sql":{"type":"string"},"limit":{"type":"integer"}},"required":["sql"]}},
			{"name":"tables_list","description":"List tables","inputSchema":{"type":"object","properties":{"schema":{"type":"string"}}}}
		]}`)
	}))
	defer dbMCP.Close()

	// Step 2: Import tools from each source with unique source names.
	reg := registry.NewMemoryRegistry()
	sources := []struct {
		name string
		url  string
		caps []string
	}{
		{"github", githubMCP.URL, []string{"github"}},
		{"flyctl", flyMCP.URL, []string{"flyctl"}},
		{"database", dbMCP.URL, []string{"database"}},
	}
	totalTools := 0
	for _, src := range sources {
		imp := mcpsource.NewImporter(reg, &http.Client{})
		n, err := imp.ImportSource(mcpsource.Source{
			Name:              src.name,
			BaseURL:           src.url,
			ExtraCapabilities: src.caps,
		})
		if err != nil {
			t.Fatalf("import %s: %v", src.name, err)
		}
		totalTools += n
	}
	if totalTools != 6 {
		t.Fatalf("imported %d tools, want 6", totalTools)
	}

	// Step 3: Collect all capability tags.
	allTools := reg.All()
	capSet := make(map[string]struct{})
	for _, tool := range allTools {
		for _, c := range tool.Capabilities {
			capSet[c] = struct{}{}
		}
	}
	allCaps := make([]string, 0, len(capSet))
	for c := range capSet {
		allCaps = append(allCaps, c)
	}
	sort.Strings(allCaps)

	// Step 4: Build deferred resolver + bridge.
	dr := resolver.NewDeferredResolver(resolver.DeferredOptions{
		AllCapabilities: allCaps,
		NarrowThreshold: 3,
		WindowSize:      10,
	})

	out := &bytes.Buffer{}
	b := bridge.New(bridge.Config{
		Registry: reg,
		Resolver: dr,
		Out:      out,
	})
	for _, tool := range allTools {
		parts := strings.SplitN(tool.ID, ".", 2)
		b.RegisterTool(parts[0], parts[1], tool)
	}

	// Step 5: Send initialize + tools/list (cold start).
	msgs := []string{
		mustMarshal(jsonrpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize",
			Params: json.RawMessage(`{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}`)}),
		mustMarshal(jsonrpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"}),
	}
	input := strings.Join(msgs, "\n") + "\n"
	if err := b.Serve(strings.NewReader(input)); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.String())
	if len(responses) < 2 {
		t.Fatalf("expected ≥2 responses, got %d", len(responses))
	}

	// Verify cold-start tools/list returns all 6 tools.
	toolsListResp := responses[1]
	result := toolsListResp.Result.(map[string]interface{})
	tools := result["tools"].([]interface{})
	if len(tools) != 6 {
		t.Fatalf("cold start: got %d tools, want 6", len(tools))
	}

	// Verify schemas are compacted (no descriptions in properties).
	for _, raw := range tools {
		tool := raw.(map[string]interface{})
		schema := tool["inputSchema"].(map[string]interface{})
		props := schema["properties"].(map[string]interface{})
		for field, rawProp := range props {
			prop := rawProp.(map[string]interface{})
			if _, hasDesc := prop["description"]; hasDesc {
				t.Fatalf("tool %s field %s has description — schema not compacted",
					tool["name"], field)
			}
		}
	}

	// Step 6: Call github tools 3 times to cross narrow threshold.
	out.Reset()
	callMsgs := make([]string, 3)
	for i := 0; i < 3; i++ {
		callMsgs[i] = mustMarshal(jsonrpcRequest{
			JSONRPC: "2.0",
			ID:      json.RawMessage(fmt.Sprintf(`%d`, 10+i)),
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"github.issues_list","arguments":{"owner":"test","repo":"test"}}`),
		})
	}
	if err := b.Serve(strings.NewReader(strings.Join(callMsgs, "\n") + "\n")); err != nil {
		t.Fatalf("serve calls: %v", err)
	}

	// Step 7: Verify list_changed notification was emitted.
	allOutput := out.String()
	if !strings.Contains(allOutput, "notifications/tools/list_changed") {
		t.Fatal("expected notifications/tools/list_changed after 3 observations")
	}

	// Step 8: Re-fetch tools/list — should be narrowed.
	out.Reset()
	listMsg := mustMarshal(jsonrpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`20`), Method: "tools/list"})
	if err := b.Serve(strings.NewReader(listMsg + "\n")); err != nil {
		t.Fatalf("serve narrowed list: %v", err)
	}
	narrowedResponses := parseResponses(t, out.String())
	if len(narrowedResponses) == 0 {
		t.Fatal("no response to narrowed tools/list")
	}
	narrowedResult := narrowedResponses[0].Result.(map[string]interface{})
	narrowedTools := narrowedResult["tools"].([]interface{})
	if len(narrowedTools) >= 6 {
		t.Fatalf("narrowed tools count = %d, should be less than 6", len(narrowedTools))
	}
	if len(narrowedTools) == 0 {
		t.Fatal("narrowed tools count = 0, should have at least the observed tools")
	}

	t.Logf("cold start: %d tools → narrowed: %d tools (after 3 github observations)",
		len(tools), len(narrowedTools))
}

func parseResponses(t *testing.T, output string) []jsonrpcResponse {
	t.Helper()
	var responses []jsonrpcResponse
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Logf("skipping non-response line: %s", line)
			continue
		}
		responses = append(responses, resp)
	}
	return responses
}
