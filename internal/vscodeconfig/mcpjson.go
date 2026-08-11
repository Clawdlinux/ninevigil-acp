/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package vscodeconfig reads MCP server configs from common IDE locations.
package vscodeconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Source is the bridge-ready view of one MCP server config.
type Source struct {
	Name    string
	Type    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
}

type mcpFile struct {
	Servers    map[string]serverConfig `json:"servers"`
	MCPServers map[string]serverConfig `json:"mcpServers"`
}

type serverConfig struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
}

// DefaultUserMCPPath returns the common VS Code user MCP config path.
func DefaultUserMCPPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", "mcp.json")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Code", "User", "mcp.json")
	default:
		return filepath.Join(home, ".config", "Code", "User", "mcp.json")
	}
}

// Load reads one MCP JSON config and returns usable sources. Entries with
// unresolved VS Code input prompts are skipped because acp-bridge is headless.
func Load(path string) ([]Source, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("vscodeconfig: read %s: %w", path, err)
	}

	var body mcpFile
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, nil, fmt.Errorf("vscodeconfig: parse %s: %w", path, err)
	}

	servers := body.Servers
	if len(servers) == 0 {
		servers = body.MCPServers
	}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	sources := make([]Source, 0, len(names))
	skipped := make([]string, 0)
	for _, name := range names {
		server := servers[name]
		if isACPBridge(name, server) {
			skipped = append(skipped, name)
			continue
		}
		if containsInputPrompt(server) {
			skipped = append(skipped, name)
			continue
		}
		source := Source{
			Name:    name,
			Type:    normalizeType(server.Type, server),
			Command: server.Command,
			Args:    append([]string(nil), server.Args...),
			Env:     copyEnv(server.Env),
			URL:     server.URL,
		}
		if source.Type == "stdio" && strings.TrimSpace(source.Command) == "" {
			skipped = append(skipped, name)
			continue
		}
		if source.Type == "http" {
			skipped = append(skipped, name)
			continue
		}
		sources = append(sources, source)
	}
	return sources, skipped, nil
}

func isACPBridge(name string, server serverConfig) bool {
	if strings.Contains(strings.ToLower(name), "acp-bridge") {
		return true
	}
	base := strings.ToLower(filepath.Base(server.Command))
	return base == "acp-bridge" || base == "acp-bridge.exe"
}

func normalizeType(raw string, server serverConfig) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "http", "sse", "streamable-http":
		return "http"
	case "stdio":
		return "stdio"
	default:
		if strings.TrimSpace(server.URL) != "" {
			return "http"
		}
		return "stdio"
	}
}

func containsInputPrompt(server serverConfig) bool {
	if strings.Contains(server.Command, "${input:") || strings.Contains(server.URL, "${input:") {
		return true
	}
	for _, arg := range server.Args {
		if strings.Contains(arg, "${input:") {
			return true
		}
	}
	for _, value := range server.Env {
		if strings.Contains(value, "${input:") {
			return true
		}
	}
	return false
}

func copyEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
