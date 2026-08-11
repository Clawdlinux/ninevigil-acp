/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package mcp implements an MCP source adapter for the ACP registry.
//
// An MCP source reads `tools/list` responses (per the MCP 2024-11 spec) from
// any number of upstream MCP servers and registers each tool as an
// `registry.Tool` with capabilities derived from the tool name and
// description. This is the concrete implementation of the "ACP sits on top
// of MCP" positioning: existing MCP servers become a supply chain for ACP
// manifests instead of a competitor.
//
// Capability inference is deliberately simple in v0.1:
//   - The MCP tool name is split on `.`, `_`, `-`, and `/`. Each token
//     becomes a capability.
//   - A small synonym table maps common MCP names to ACP capability tags
//     (e.g. "github_create_issue" -> capabilities {github, issue, write}).
//
// The adapter does NOT preserve verbose MCP schemas verbatim - it converts
// them into the ACP compact mini-language (string, int?, string[], etc.).
// This is what produces the token reduction.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

//go:generate ../../../bin/mockgen -source=source.go -destination=mocks_test.go -package=mcp

// HTTPDoer is the minimal HTTP transport interface (mirrors pkg/acp).
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ToolDescriptor mirrors the on-the-wire shape of a single entry in an MCP
// `tools/list` response.
type ToolDescriptor struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ToolsListResponse is the body of an MCP `tools/list` response.
type ToolsListResponse struct {
	Tools []ToolDescriptor `json:"tools"`
}

// Source represents one upstream MCP server.
type Source struct {
	// Name is a stable label used as a prefix on imported tool IDs and as
	// the egress allow-list host.
	Name string
	// BaseURL is the MCP server's HTTP root.
	BaseURL string
	// Type is the downstream transport. Empty means HTTP for backwards
	// compatibility. Set to "stdio" for acp-bridge child processes.
	Type string
	// Auth, if non-empty, is sent as `Authorization` to the MCP server. The
	// proxy strips this before the agent ever sees it.
	Auth string
	// ExtraCapabilities are added to every tool imported from this source.
	// Useful for tagging an entire server (e.g. {"github"} for the GitHub MCP
	// server).
	ExtraCapabilities []string
}

// Importer fetches MCP `tools/list` responses and registers them in an ACP
// registry as `registry.Tool` entries with compact ACP schemas.
type Importer struct {
	registry *registry.MemoryRegistry
	doer     HTTPDoer
}

// NewImporter constructs an Importer. A nil doer falls back to a
// 30-second http.Client.
func NewImporter(reg *registry.MemoryRegistry, doer HTTPDoer) *Importer {
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}
	return &Importer{registry: reg, doer: doer}
}

// ImportSource fetches `<baseURL>/tools/list` from the given source and
// registers every tool. Returns the number of tools imported.
func (i *Importer) ImportSource(src Source) (int, error) {
	if strings.TrimSpace(src.Name) == "" {
		return 0, errors.New("mcp: source name is required")
	}
	if strings.TrimSpace(src.BaseURL) == "" {
		return 0, errors.New("mcp: source base URL is required")
	}

	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(src.BaseURL, "/")+"/tools/list", nil)
	if err != nil {
		return 0, fmt.Errorf("mcp: build request: %w", err)
	}
	if src.Auth != "" {
		req.Header.Set("Authorization", src.Auth)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := i.doer.Do(req)
	if err != nil {
		return 0, fmt.Errorf("mcp: fetch %s: %w", src.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("mcp: %s returned status %d", src.Name, resp.StatusCode)
	}

	var body ToolsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("mcp: decode %s: %w", src.Name, err)
	}

	return i.RegisterAll(src, body.Tools)
}

// RegisterAll registers every descriptor under the given source. Useful for
// tests or for environments that already have the tools/list payload in
// hand.
func (i *Importer) RegisterAll(src Source, descriptors []ToolDescriptor) (int, error) {
	count := 0
	for _, d := range descriptors {
		tool, err := convert(src, d)
		if err != nil {
			return count, err
		}
		if err := i.registry.Register(tool); err != nil {
			return count, fmt.Errorf("mcp: register %s: %w", tool.ID, err)
		}
		count++
	}
	return count, nil
}

// convert renders an MCP descriptor into an ACP registry.Tool with a
// compact schema.
func convert(src Source, d ToolDescriptor) (registry.Tool, error) {
	if strings.TrimSpace(d.Name) == "" {
		return registry.Tool{}, errors.New("mcp: descriptor name is required")
	}
	caps := inferCapabilities(d.Name, src.ExtraCapabilities)
	if len(caps) == 0 {
		caps = []string{src.Name}
	}
	schema := compactSchema(d.InputSchema)

	if strings.EqualFold(src.Type, "stdio") {
		return registry.Tool{
			ID:           src.Name + "." + d.Name,
			Type:         "stdio",
			Endpoint:     "stdio://" + src.Name + "/" + d.Name,
			Method:       "tools/call",
			Schema:       schema,
			Auth:         manifest.AuthNone,
			Timeout:      "30s",
			Capabilities: caps,
		}, nil
	}

	return registry.Tool{
		ID:           src.Name + "." + d.Name,
		Type:         "http",
		Endpoint:     strings.TrimRight(src.BaseURL, "/") + "/tools/call/" + d.Name,
		Method:       http.MethodPost,
		Schema:       schema,
		Auth:         manifest.AuthPreInjected,
		Timeout:      "30s",
		Capabilities: caps,
		Egress:       []string{hostOf(src.BaseURL)},
	}, nil
}

// inferCapabilities derives capability tags from an MCP tool name plus any
// source-level extras. Splits on common separators and adds verb synonyms
// for read/write semantics.
func inferCapabilities(name string, extras []string) []string {
	seen := make(map[string]struct{})
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || len(s) < 2 {
			return
		}
		seen[s] = struct{}{}
	}
	for _, e := range extras {
		add(e)
	}
	for _, tok := range tokenize(name) {
		add(tok)
		// Verb synonyms.
		switch tok {
		case "create", "post", "put", "send", "publish":
			add("write")
		case "get", "list", "read", "fetch", "search", "describe":
			add("read")
		case "delete", "destroy", "remove":
			add("write")
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func tokenize(s string) []string {
	out := make([]string, 0, 4)
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		switch r {
		case '.', '_', '-', '/', ' ', ':':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// compactSchema converts a JSON-Schema object into the ACP mini-language.
// Only the top-level `properties` map is examined - that's what ACP
// manifests publish.
func compactSchema(jsonSchema map[string]interface{}) map[string]string {
	out := make(map[string]string)
	if jsonSchema == nil {
		return out
	}
	props, _ := jsonSchema["properties"].(map[string]interface{})
	required := requiredSet(jsonSchema)
	for name, raw := range props {
		field, _ := raw.(map[string]interface{})
		compact := compactField(field)
		if _, isRequired := required[name]; !isRequired {
			compact += "?"
		}
		out[name] = compact
	}
	return out
}

func requiredSet(jsonSchema map[string]interface{}) map[string]struct{} {
	set := make(map[string]struct{})
	raw, ok := jsonSchema["required"].([]interface{})
	if !ok {
		return set
	}
	for _, r := range raw {
		if s, ok := r.(string); ok {
			set[s] = struct{}{}
		}
	}
	return set
}

func compactField(field map[string]interface{}) string {
	if field == nil {
		return "string"
	}
	if enum, ok := field["enum"].([]interface{}); ok && len(enum) > 0 {
		parts := make([]string, 0, len(enum))
		for _, v := range enum {
			parts = append(parts, fmt.Sprint(v))
		}
		return "enum:" + strings.Join(parts, "|")
	}
	t, _ := field["type"].(string)
	switch t {
	case "string":
		return "string"
	case "integer":
		return "int"
	case "number":
		return "float"
	case "boolean":
		return "bool"
	case "object":
		return "json"
	case "array":
		items, _ := field["items"].(map[string]interface{})
		return compactField(items) + "[]"
	default:
		return "string"
	}
}

func hostOf(rawURL string) string {
	s := rawURL
	for _, prefix := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	if i := strings.IndexAny(s, "/:?"); i >= 0 {
		s = s[:i]
	}
	return s
}
