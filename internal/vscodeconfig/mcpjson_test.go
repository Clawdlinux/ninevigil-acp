/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package vscodeconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ReadsVSCodeServersAndSkipsInputs(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `{
  "servers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
      "env": {"NODE_ENV": "production"}
    },
    "github": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@github/github-mcp-server"],
      "env": {"GITHUB_TOKEN": "${input:github-token}"}
    },
    "remote": {
      "type": "http",
      "url": "http://127.0.0.1:9000"
		},
		"acp-bridge": {
			"type": "stdio",
			"command": "acp-bridge",
			"args": ["--import-vscode"]
    }
  }
}`)

	sources, skipped, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %#v, want 1", sources)
	}
	if sources[0].Name != "filesystem" || sources[0].Type != "stdio" || sources[0].Command != "npx" {
		t.Fatalf("first source = %#v", sources[0])
	}
	if len(skipped) != 3 || skipped[0] != "acp-bridge" || skipped[1] != "github" || skipped[2] != "remote" {
		t.Fatalf("skipped = %#v, want acp-bridge, github, and remote", skipped)
	}
}

func TestLoad_ReadsMCPServersShape(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `{
  "mcpServers": {
    "memory": {
      "command": "uvx",
      "args": ["mcp-memory"]
    }
  }
}`)

	sources, skipped, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want none", skipped)
	}
	if len(sources) != 1 || sources[0].Name != "memory" || sources[0].Type != "stdio" {
		t.Fatalf("sources = %#v", sources)
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
