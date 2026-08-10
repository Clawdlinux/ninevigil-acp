/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/internal/resolver"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

type fakeForwarder struct {
	lastSource string
	lastTool   string
	lastArgs   map[string]interface{}
	result     json.RawMessage
	err        error
}

func (f *fakeForwarder) Call(_ context.Context, source, tool string, args map[string]interface{}) (json.RawMessage, error) {
	f.lastSource = source
	f.lastTool = tool
	f.lastArgs = args
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return json.RawMessage(`{"content":[{"type":"text","text":"forwarded"}],"isError":false}`), nil
}

func seedRegistry(t *testing.T) (*registry.MemoryRegistry, []string) {
	t.Helper()
	reg := registry.NewMemoryRegistry()
	tools := []registry.Tool{
		{ID: "github.issues_list", Type: "http", Endpoint: "http://github:8080/tools/call/issues_list",
			Method: "POST", Schema: map[string]string{"owner": "string", "repo": "string"},
			Auth: manifest.AuthPreInjected, Capabilities: []string{"github", "read"}, Egress: []string{"github"}},
		{ID: "github.issues_create", Type: "http", Endpoint: "http://github:8080/tools/call/issues_create",
			Method: "POST", Schema: map[string]string{"owner": "string", "repo": "string", "title": "string", "body": "string?"},
			Auth: manifest.AuthPreInjected, Capabilities: []string{"github", "write"}, Egress: []string{"github"}},
		{ID: "flyctl.apps_list", Type: "http", Endpoint: "http://fly:8080/tools/call/apps_list",
			Method: "POST", Schema: map[string]string{"org": "string?"},
			Auth: manifest.AuthPreInjected, Capabilities: []string{"flyctl", "read"}, Egress: []string{"fly"}},
		{ID: "flyctl.deploy", Type: "http", Endpoint: "http://fly:8080/tools/call/deploy",
			Method: "POST", Schema: map[string]string{"app": "string", "image": "string"},
			Auth: manifest.AuthPreInjected, Capabilities: []string{"flyctl", "write"}, Egress: []string{"fly"}},
		{ID: "db.query", Type: "http", Endpoint: "http://db:5432/tools/call/query",
			Method: "POST", Schema: map[string]string{"sql": "string", "limit": "int?"},
			Auth: manifest.AuthPreInjected, Capabilities: []string{"sql", "database"}, Egress: []string{"db"}},
	}
	allCaps := map[string]struct{}{}
	for _, tool := range tools {
		if err := reg.Register(tool); err != nil {
			t.Fatalf("register %s: %v", tool.ID, err)
		}
		for _, c := range tool.Capabilities {
			allCaps[c] = struct{}{}
		}
	}
	caps := make([]string, 0, len(allCaps))
	for c := range allCaps {
		caps = append(caps, c)
	}
	return reg, caps
}

func newTestBridge(t *testing.T) (*Bridge, *bytes.Buffer) {
	return newTestBridgeWithForwarder(t, nil)
}

func newTestBridgeWithForwarder(t *testing.T, forwarder Forwarder) (*Bridge, *bytes.Buffer) {
	t.Helper()
	reg, allCaps := seedRegistry(t)
	out := &bytes.Buffer{}
	dr := resolver.NewDeferredResolver(resolver.DeferredOptions{
		AllCapabilities: allCaps,
		NarrowThreshold: 3,
		WindowSize:      10,
	})
	b := New(Config{
		Registry: reg,
		Resolver: dr,
		Out:      out,
		Forward:  forwarder,
	})
	// Register tools in the bridge's downstream map.
	for _, tool := range reg.All() {
		parts := strings.SplitN(tool.ID, ".", 2)
		b.RegisterTool(parts[0], parts[1], tool)
	}
	return b, out
}

func sendAndCollect(t *testing.T, b *Bridge, out *bytes.Buffer, msgs ...string) []jsonrpcResponse {
	t.Helper()
	input := strings.Join(msgs, "\n") + "\n"
	if err := b.Serve(strings.NewReader(input)); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var responses []jsonrpcResponse
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Might be a notification; try to decode that.
			var notif jsonrpcNotification
			if err2 := json.Unmarshal([]byte(line), &notif); err2 == nil {
				// Store as a response with method in Error.Message for test assertions.
				responses = append(responses, jsonrpcResponse{
					JSONRPC: "2.0",
					Error:   &jsonrpcError{Code: 0, Message: notif.Method},
				})
				continue
			}
			t.Fatalf("unmarshal response: %v (line: %s)", err, line)
		}
		responses = append(responses, resp)
	}
	return responses
}

func mustJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func TestBridge_Initialize(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}`),
	})

	responses := sendAndCollect(t, b, out, msg)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", resp.Result)
	}
	if result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}

	caps := result["capabilities"].(map[string]interface{})
	tools := caps["tools"].(map[string]interface{})
	if tools["listChanged"] != true {
		t.Fatal("listChanged should be true")
	}
}

func TestBridge_ToolsList_ColdStart_ReturnsAll(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
	})

	responses := sendAndCollect(t, b, out, msg)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	result := responses[0].Result.(map[string]interface{})
	tools := result["tools"].([]interface{})

	// Cold start: all 5 tools should be returned.
	if len(tools) != 5 {
		t.Fatalf("cold start tools count = %d, want 5", len(tools))
	}
}

func TestBridge_ToolsList_CompactSchemas(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
	})

	responses := sendAndCollect(t, b, out, msg)
	result := responses[0].Result.(map[string]interface{})
	tools := result["tools"].([]interface{})

	// Verify schemas are compact (no descriptions in properties).
	for _, raw := range tools {
		tool := raw.(map[string]interface{})
		schema := tool["inputSchema"].(map[string]interface{})
		props := schema["properties"].(map[string]interface{})
		for field, rawProp := range props {
			prop := rawProp.(map[string]interface{})
			if _, hasDesc := prop["description"]; hasDesc {
				t.Fatalf("tool %s field %s has description (should be stripped)", tool["name"], field)
			}
		}
	}
}

func TestBridge_ToolsCall_Success(t *testing.T) {
	t.Parallel()
	forwarder := &fakeForwarder{}
	b, out := newTestBridgeWithForwarder(t, forwarder)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"github.issues_list","arguments":{"owner":"test","repo":"test"}}`),
	})

	responses := sendAndCollect(t, b, out, msg)
	if len(responses) < 1 {
		t.Fatal("expected at least 1 response")
	}

	// First non-notification response.
	var callResp jsonrpcResponse
	for _, r := range responses {
		if r.Error == nil || r.Error.Code != 0 {
			callResp = r
			break
		}
	}

	result := callResp.Result.(map[string]interface{})
	if result["isError"] != false {
		t.Fatal("expected isError=false")
	}
	content := result["content"].([]interface{})
	first := content[0].(map[string]interface{})
	if first["text"] != "forwarded" {
		t.Fatalf("forwarded text = %v, want forwarded", first["text"])
	}
	if forwarder.lastSource != "github" || forwarder.lastTool != "issues_list" {
		t.Fatalf("forwarded to %s.%s, want github.issues_list", forwarder.lastSource, forwarder.lastTool)
	}
	if forwarder.lastArgs["owner"] != "test" {
		t.Fatalf("forwarded args = %#v", forwarder.lastArgs)
	}
}

func TestBridge_ToolsCall_WithoutForwarderUsesPlaceholder(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"github.issues_list","arguments":{"owner":"test","repo":"test"}}`),
	})

	responses := sendAndCollect(t, b, out, msg)
	result := responses[0].Result.(map[string]interface{})
	content := result["content"].([]interface{})
	first := content[0].(map[string]interface{})
	if !strings.Contains(first["text"].(string), "routed to downstream") {
		t.Fatalf("placeholder text = %v", first["text"])
	}
}

func TestBridge_ToolsCall_UnknownTool(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"nonexistent.tool","arguments":{}}`),
	})

	responses := sendAndCollect(t, b, out, msg)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	if responses[0].Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if responses[0].Error.Code != -32602 {
		t.Fatalf("error code = %d, want -32602", responses[0].Error.Code)
	}
}

func TestBridge_Narrowing_EmitsListChanged(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	// First: get the full tools/list so prevToolCount is set.
	listMsg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
	})
	sendAndCollect(t, b, out, listMsg)
	out.Reset()

	// Now: call github tools 3 times to cross the narrow threshold.
	for i := 0; i < 3; i++ {
		out.Reset()
		callMsg := mustJSON(jsonrpcRequest{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`2`),
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"github.issues_list","arguments":{"owner":"test","repo":"test"}}`),
		})
		sendAndCollect(t, b, out, callMsg)
	}

	// Check if any notifications/tools/list_changed was emitted.
	outputLines := strings.Split(strings.TrimSpace(out.String()), "\n")
	foundNotification := false
	for _, line := range outputLines {
		if strings.Contains(line, "notifications/tools/list_changed") {
			foundNotification = true
			break
		}
	}
	if !foundNotification {
		t.Fatal("expected notifications/tools/list_changed after narrowing threshold crossed")
	}
}

func TestBridge_Ping(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "ping",
	})

	responses := sendAndCollect(t, b, out, msg)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("unexpected error: %v", responses[0].Error)
	}
}

func TestBridge_UnknownMethod(t *testing.T) {
	t.Parallel()
	b, out := newTestBridge(t)

	msg := mustJSON(jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "nonexistent/method",
	})

	responses := sendAndCollect(t, b, out, msg)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	if responses[0].Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if responses[0].Error.Code != -32601 {
		t.Fatalf("error code = %d, want -32601", responses[0].Error.Code)
	}
}

func TestCompactToJSONSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		compact string
		want    map[string]interface{}
	}{
		{"string", map[string]interface{}{"type": "string"}},
		{"string?", map[string]interface{}{"type": "string"}},
		{"int", map[string]interface{}{"type": "integer"}},
		{"float", map[string]interface{}{"type": "number"}},
		{"bool", map[string]interface{}{"type": "boolean"}},
		{"json", map[string]interface{}{"type": "object"}},
		{"string[]", map[string]interface{}{
			"type":  "array",
			"items": map[string]interface{}{"type": "string"},
		}},
		{"enum:a|b|c", map[string]interface{}{
			"type": "string",
			"enum": []interface{}{"a", "b", "c"},
		}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.compact, func(t *testing.T) {
			t.Parallel()
			got := compactToJSONSchema(tc.compact)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("compactToJSONSchema(%q) = %s, want %s", tc.compact, gotJSON, wantJSON)
			}
		})
	}
}
