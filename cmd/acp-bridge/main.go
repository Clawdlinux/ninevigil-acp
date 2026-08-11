/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Command acp-bridge runs an MCP stdio server that wraps multiple downstream
// MCP servers behind ACP's deferred-intent resolver with schema compaction.
//
// Usage in VS Code mcp.json:
//
//	{
//	  "servers": {
//	    "acp-bridge": {
//	      "type": "stdio",
//	      "command": "acp-bridge",
//	      "args": ["--config", "/path/to/bridge.json"]
//	    }
//	  }
//	}
//
// The bridge reads downstream MCP server configurations from a JSON config
// file, imports and compacts their tools, then serves a dynamic tools/list
// via JSON-RPC over stdin/stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/Clawdlinux/agent-native-format/internal/bridge"
	"github.com/Clawdlinux/agent-native-format/internal/mcpclient"
	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/internal/resolver"
	mcpsource "github.com/Clawdlinux/agent-native-format/internal/sources/mcp"
	"github.com/Clawdlinux/agent-native-format/internal/vscodeconfig"
)

var version = "0.2.0-dev"

// BridgeConfig is the JSON config file format for the bridge.
type BridgeConfig struct {
	// Sources lists downstream MCP servers to import tools from.
	Sources []SourceConfig `json:"sources"`

	// NarrowThreshold is the number of tool-call observations before
	// narrowing. Default: 3.
	NarrowThreshold int `json:"narrow_threshold,omitempty"`

	// WindowSize is the sliding window of recent observations. Default: 10.
	WindowSize int `json:"window_size,omitempty"`
}

// SourceConfig describes one downstream MCP server.
type SourceConfig struct {
	// Name is a stable label for this source (e.g. "github", "flyctl").
	Name string `json:"name"`

	// Type is "stdio" or "http". Empty infers from command/url.
	Type string `json:"type,omitempty"`

	// URL is the HTTP base URL where tools/list is served.
	URL string `json:"url"`

	// Command, Args, and Env describe a local stdio MCP server.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// Auth is sent as the Authorization header. Use env var references.
	Auth string `json:"auth,omitempty"`

	// Capabilities are extra capability tags applied to all tools from
	// this source.
	Capabilities []string `json:"capabilities,omitempty"`
}

func main() {
	configPath := flag.String("config", "", "path to bridge config JSON file")
	importVSCode := flag.Bool("import-vscode", false, "import MCP servers from VS Code user mcp.json")
	vscodeConfigPath := flag.String("vscode-mcp-config", "", "path to VS Code-style mcp.json; implies --import-vscode")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}

	// Logger writes to stderr so stdout stays clean for JSON-RPC.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var cfg BridgeConfig
	if *configPath != "" {
		data, err := os.ReadFile(*configPath)
		if err != nil {
			logger.Error("read config", "error", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.Error("parse config", "error", err)
			os.Exit(1)
		}
	}
	if *importVSCode || *vscodeConfigPath != "" {
		path := *vscodeConfigPath
		if path == "" {
			path = vscodeconfig.DefaultUserMCPPath()
		}
		if path == "" {
			logger.Error("could not resolve VS Code mcp.json path")
			os.Exit(1)
		}
		sources, skipped, err := vscodeconfig.Load(path)
		if err != nil {
			logger.Error("import VS Code MCP config", "path", path, "error", err)
			os.Exit(1)
		}
		for _, skippedName := range skipped {
			logger.Warn("skipping MCP server with unresolved inputs or missing command/url", "server", skippedName)
		}
		for _, source := range sources {
			cfg.Sources = append(cfg.Sources, SourceConfig{
				Name:    source.Name,
				Type:    source.Type,
				URL:     source.URL,
				Command: source.Command,
				Args:    source.Args,
				Env:     source.Env,
			})
		}
		logger.Info("imported VS Code MCP config", "path", path, "sources", len(sources), "skipped", len(skipped))
	}

	if len(cfg.Sources) == 0 {
		logger.Info("no sources configured; bridge will serve an empty tool surface")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Import tools from all downstream MCP servers.
	reg := registry.NewMemoryRegistry()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	importer := mcpsource.NewImporter(reg, httpClient)
	manager := mcpclient.NewManager(30 * time.Second)
	defer manager.Close()

	for _, src := range cfg.Sources {
		name := safeSourceName(src.Name)
		if name == "" {
			logger.Error("source name is required")
			continue
		}
		n, err := importSource(ctx, importer, manager, SourceConfig{
			Name:         name,
			Type:         src.Type,
			URL:          expandEnv(src.URL),
			Command:      expandEnv(src.Command),
			Args:         expandSlice(src.Args),
			Env:          expandMap(src.Env),
			Auth:         expandEnv(src.Auth),
			Capabilities: src.Capabilities,
		})
		if err != nil {
			logger.Error("import source", "source", name, "error", err)
			continue
		}
		logger.Info("imported source", "source", name, "tools", n)
	}

	// Collect all capability tags from imported tools.
	allTools := reg.All()
	capSet := make(map[string]struct{})
	for _, t := range allTools {
		for _, c := range t.Capabilities {
			capSet[c] = struct{}{}
		}
	}
	allCaps := make([]string, 0, len(capSet))
	for c := range capSet {
		allCaps = append(allCaps, c)
	}
	sort.Strings(allCaps)

	// Build deferred resolver.
	dr := resolver.NewDeferredResolver(resolver.DeferredOptions{
		AllCapabilities: allCaps,
		NarrowThreshold: cfg.NarrowThreshold,
		WindowSize:      cfg.WindowSize,
	})

	// Build bridge and register downstream tool mappings.
	var forward bridge.Forwarder
	if manager.Count() > 0 {
		forward = manager
	}
	b := bridge.New(bridge.Config{
		Registry: reg,
		Resolver: dr,
		Logger:   logger,
		Out:      os.Stdout,
		Forward:  forward,
	})

	for _, t := range allTools {
		// Tool ID format: "sourceName.toolName"
		source := t.ID
		toolName := t.ID
		for i, c := range t.ID {
			if c == '.' {
				source = t.ID[:i]
				toolName = t.ID[i+1:]
				break
			}
		}
		b.RegisterTool(source, toolName, t)
	}

	logger.Info("acp-bridge starting",
		"version", version,
		"tools", len(allTools),
		"capabilities", len(allCaps),
	)

	// Serve JSON-RPC over stdin/stdout.
	if err := b.Serve(os.Stdin); err != nil {
		logger.Error("serve", "error", err)
		os.Exit(1)
	}
}

func importSource(ctx context.Context, importer *mcpsource.Importer, manager *mcpclient.Manager, src SourceConfig) (int, error) {
	switch sourceType(src) {
	case "stdio":
		client, err := mcpclient.Spawn(ctx, mcpclient.StdioConfig{Command: src.Command, Args: src.Args, Env: src.Env})
		if err != nil {
			return 0, err
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := client.Initialize(callCtx); err != nil {
			client.Close()
			return 0, err
		}
		descriptors, err := client.ListTools(callCtx)
		if err != nil {
			client.Close()
			return 0, err
		}
		if err := manager.Register(src.Name, client); err != nil {
			client.Close()
			return 0, err
		}
		return importer.RegisterAll(mcpsource.Source{
			Name:              src.Name,
			Type:              "stdio",
			ExtraCapabilities: src.Capabilities,
		}, descriptors)
	case "http":
		return importer.ImportSource(mcpsource.Source{
			Name:              src.Name,
			BaseURL:           src.URL,
			Auth:              src.Auth,
			ExtraCapabilities: src.Capabilities,
		})
	default:
		return 0, fmt.Errorf("unsupported source type %q", src.Type)
	}
}

func sourceType(src SourceConfig) string {
	t := strings.ToLower(strings.TrimSpace(src.Type))
	if t == "" {
		if strings.TrimSpace(src.URL) != "" {
			return "http"
		}
		return "stdio"
	}
	if t == "streamable-http" || t == "sse" {
		return "http"
	}
	return t
}

// expandEnv replaces ${VAR} and $VAR references in s with environment
// variable values. This lets bridge.json reference secrets without
// hardcoding them.
func expandEnv(s string) string {
	var out strings.Builder
	for {
		start := strings.Index(s, "${env:")
		if start < 0 {
			out.WriteString(os.ExpandEnv(s))
			return out.String()
		}
		out.WriteString(os.ExpandEnv(s[:start]))
		rest := s[start+len("${env:"):]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			out.WriteString(os.ExpandEnv(s[start:]))
			return out.String()
		}
		out.WriteString(os.Getenv(rest[:end]))
		s = rest[end+1:]
	}
}

func expandSlice(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = expandEnv(value)
	}
	return out
}

func expandMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = expandEnv(value)
	}
	return out
}

func safeSourceName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		allowed := unicode.IsLetter(r) || unicode.IsDigit(r)
		if allowed {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
