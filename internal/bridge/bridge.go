/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package bridge implements an MCP stdio server that sits on top of ACP's
// registry and resolver, presenting a dynamic, intent-narrowed tool surface
// to MCP clients like VS Code.
//
// The bridge operates in ACP's deferred-intent mode (SPEC §4.8):
//  1. On initialize: register downstream MCP servers, compact schemas.
//  2. On tools/list: return the current scoped tool set (all initially).
//  3. On tools/call: proxy to the downstream server and observe the
//     capability for progressive narrowing.
//  4. When observations cross the narrow threshold, emit
//     notifications/tools/list_changed so the client re-fetches.
//
// The bridge speaks JSON-RPC 2.0 over stdin/stdout per the MCP stdio
// transport specification.
package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/internal/resolver"
)

// Forwarder routes a bridge tools/call to the original downstream MCP server.
type Forwarder interface {
	Call(ctx context.Context, source, tool string, args map[string]interface{}) (json.RawMessage, error)
}

// jsonrpcRequest is a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // may be number, string, or null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// jsonrpcNotification is a JSON-RPC 2.0 notification (no id).
type jsonrpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// DownstreamTool tracks the mapping from MCP tool name back to its origin.
type DownstreamTool struct {
	SourceName string            // which downstream server
	ToolName   string            // original MCP tool name
	Tool       registry.Tool     // ACP registry representation
	RawSchema  map[string]string // compact schema for tools/list
}

// Bridge is an MCP stdio server backed by ACP's deferred-intent resolver.
type Bridge struct {
	mu       sync.RWMutex
	reg      *registry.MemoryRegistry
	resolver *resolver.DeferredResolver
	tools    map[string]DownstreamTool // MCP tool name -> downstream mapping
	logger   *slog.Logger
	out      io.Writer
	forward  Forwarder

	// prevToolCount tracks the last emitted tools/list size so we know
	// when to send list_changed.
	prevToolCount int
}

// Config configures a Bridge.
type Config struct {
	Registry *registry.MemoryRegistry
	Resolver *resolver.DeferredResolver
	Logger   *slog.Logger
	Out      io.Writer // stdout, for JSON-RPC responses
	Forward  Forwarder
}

// New creates a Bridge ready to serve.
func New(cfg Config) *Bridge {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Bridge{
		reg:      cfg.Registry,
		resolver: cfg.Resolver,
		tools:    make(map[string]DownstreamTool),
		logger:   cfg.Logger,
		out:      cfg.Out,
		forward:  cfg.Forward,
	}
}

// RegisterTool adds a tool that came from a downstream MCP server (already
// imported and compacted). This is called during startup after importing
// from each downstream source.
func (b *Bridge) RegisterTool(sourceName, mcpToolName string, tool registry.Tool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools[tool.ID] = DownstreamTool{
		SourceName: sourceName,
		ToolName:   mcpToolName,
		Tool:       tool,
		RawSchema:  tool.Schema,
	}
}

// Serve reads JSON-RPC messages from r (stdin) and writes responses to
// the configured Out (stdout). Blocks until r is closed or an error occurs.
func (b *Bridge) Serve(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	// MCP messages can be large; allow up to 10 MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			b.logger.Warn("malformed JSON-RPC", "error", err)
			continue
		}

		b.handleMessage(req)
	}
	return scanner.Err()
}

func (b *Bridge) handleMessage(req jsonrpcRequest) {
	switch req.Method {
	case "initialize":
		b.handleInitialize(req)
	case "notifications/initialized":
		// Client acknowledgement; no response needed.
	case "tools/list":
		b.handleToolsList(req)
	case "tools/call":
		b.handleToolsCall(req)
	case "ping":
		b.sendResult(req.ID, map[string]interface{}{})
	default:
		if req.ID != nil {
			b.sendError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
		}
	}
}

func (b *Bridge) handleInitialize(req jsonrpcRequest) {
	result := map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{
				"listChanged": true,
			},
		},
		"serverInfo": map[string]interface{}{
			"name":    "acp-bridge",
			"version": "0.2.0",
		},
	}
	b.sendResult(req.ID, result)
}

func (b *Bridge) handleToolsList(req jsonrpcRequest) {
	// Resolve current capabilities using deferred resolver.
	caps, err := b.resolver.Resolve("", nil)
	if err != nil {
		// If resolver has nothing, return empty list.
		b.sendResult(req.ID, map[string]interface{}{
			"tools": []interface{}{},
		})
		return
	}

	// Lookup matching tools from registry.
	matched := b.reg.Lookup(caps)

	// Convert to MCP tools/list response format with compact schemas.
	tools := make([]map[string]interface{}, 0, len(matched))
	for _, t := range matched {
		tool := b.toolToMCP(t)
		if tool != nil {
			tools = append(tools, tool)
		}
	}

	b.mu.Lock()
	b.prevToolCount = len(tools)
	b.mu.Unlock()

	b.sendResult(req.ID, map[string]interface{}{
		"tools": tools,
	})
}

type toolsCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func (b *Bridge) handleToolsCall(req jsonrpcRequest) {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		b.sendError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	b.mu.RLock()
	downstream, ok := b.tools[params.Name]
	b.mu.RUnlock()

	if !ok {
		b.sendError(req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
		return
	}

	// Observe the capability for deferred narrowing.
	for _, cap := range downstream.Tool.Capabilities {
		b.resolver.Observe(cap)
	}

	// Check if tool surface changed after observation.
	b.maybeNotifyListChanged()

	if b.forward != nil {
		result, err := b.forward.Call(context.Background(), downstream.SourceName, downstream.ToolName, params.Arguments)
		if err != nil {
			b.sendResult(req.ID, map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": err.Error(),
					},
				},
				"isError": true,
			})
			return
		}
		b.sendResult(req.ID, json.RawMessage(result))
		return
	}

	// Return a placeholder result indicating the tool was routed.
	// This remains only for config/test modes that do not provide a Forwarder.
	b.sendResult(req.ID, map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fmt.Sprintf("Tool %s routed to downstream %s.%s",
					params.Name, downstream.SourceName, downstream.ToolName),
			},
		},
		"isError": false,
	})
}

// maybeNotifyListChanged checks if the resolved tool surface has changed
// and sends notifications/tools/list_changed if so.
func (b *Bridge) maybeNotifyListChanged() {
	caps, err := b.resolver.Resolve("", nil)
	if err != nil {
		return
	}
	matched := b.reg.Lookup(caps)

	b.mu.RLock()
	prev := b.prevToolCount
	b.mu.RUnlock()

	if len(matched) != prev && prev > 0 {
		b.sendNotification("notifications/tools/list_changed")
	}
}

// toolToMCP converts an ACP registry.Tool into MCP tools/list format with
// compact schemas. Descriptions are stripped to save tokens.
func (b *Bridge) toolToMCP(t registry.Tool) map[string]interface{} {
	// Build minimal JSON Schema from compact ACP schema.
	properties := make(map[string]interface{})
	required := make([]string, 0)

	for field, typeStr := range t.Schema {
		prop := compactToJSONSchema(typeStr)
		isOptional := strings.HasSuffix(typeStr, "?")
		if !isOptional {
			required = append(required, field)
		}
		properties[field] = prop
	}

	inputSchema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		inputSchema["required"] = required
	}

	return map[string]interface{}{
		"name":        t.ID,
		"description": fmt.Sprintf("[ACP-compacted] %s", t.ID),
		"inputSchema": inputSchema,
	}
}

// compactToJSONSchema converts an ACP mini-language type to a minimal
// JSON Schema property. This is the inverse of the MCP source compaction.
func compactToJSONSchema(compact string) map[string]interface{} {
	compact = strings.TrimSuffix(compact, "?")

	if strings.HasPrefix(compact, "enum:") {
		vals := strings.Split(strings.TrimPrefix(compact, "enum:"), "|")
		ivals := make([]interface{}, len(vals))
		for i, v := range vals {
			ivals[i] = v
		}
		return map[string]interface{}{"type": "string", "enum": ivals}
	}

	if strings.HasSuffix(compact, "[]") {
		itemType := strings.TrimSuffix(compact, "[]")
		return map[string]interface{}{
			"type":  "array",
			"items": compactToJSONSchema(itemType),
		}
	}

	switch compact {
	case "string":
		return map[string]interface{}{"type": "string"}
	case "int":
		return map[string]interface{}{"type": "integer"}
	case "float":
		return map[string]interface{}{"type": "number"}
	case "bool":
		return map[string]interface{}{"type": "boolean"}
	case "json":
		return map[string]interface{}{"type": "object"}
	case "bytes":
		return map[string]interface{}{"type": "string", "format": "byte"}
	default:
		return map[string]interface{}{"type": "string"}
	}
}

func (b *Bridge) sendResult(id json.RawMessage, result interface{}) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	b.writeJSON(resp)
}

func (b *Bridge) sendError(id json.RawMessage, code int, message string) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message},
	}
	b.writeJSON(resp)
}

func (b *Bridge) sendNotification(method string) {
	n := jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  method,
	}
	b.writeJSON(n)
}

func (b *Bridge) writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		b.logger.Error("marshal JSON-RPC", "error", err)
		return
	}
	// MCP stdio: messages are newline-delimited.
	data = append(data, '\n')
	if _, err := b.out.Write(data); err != nil {
		b.logger.Error("write JSON-RPC", "error", err)
	}
}
