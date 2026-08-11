/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package main

import (
	"os"
	"strings"
	"testing"
)

func TestSafeSourceName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Azure Mcp server":              "azure_mcp_server",
		"com.apify/apify-mcp-server":    "com_apify_apify_mcp_server",
		" io.github/github.mcp-server ": "io_github_github_mcp_server",
	}
	for input, want := range cases {
		if got := safeSourceName(input); got != want {
			t.Fatalf("safeSourceName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSourceType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  SourceConfig
		want string
	}{
		{name: "explicit stdio", src: SourceConfig{Type: "stdio"}, want: "stdio"},
		{name: "explicit sse maps to http", src: SourceConfig{Type: "sse"}, want: "http"},
		{name: "url infers http", src: SourceConfig{URL: "http://127.0.0.1:9000"}, want: "http"},
		{name: "command infers stdio", src: SourceConfig{Command: "npx"}, want: "stdio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sourceType(tc.src); got != tc.want {
				t.Fatalf("sourceType(%#v) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestExpandEnvSupportsVSCodeEnvReferences(t *testing.T) {
	t.Setenv("ACP_TEST_TOKEN", "abc$DO_NOT_EXPAND")
	t.Setenv("ACP_TEST_HOME", "/tmp/acp")

	got := expandEnv("Bearer ${env:ACP_TEST_TOKEN} at $ACP_TEST_HOME")
	if got != "Bearer abc$DO_NOT_EXPAND at /tmp/acp" {
		t.Fatalf("expandEnv = %q", got)
	}
	if strings.Contains(got, os.Getenv("DO_NOT_EXPAND")) && os.Getenv("DO_NOT_EXPAND") != "" {
		t.Fatalf("expandEnv expanded a dollar sequence inside the resolved env value: %q", got)
	}
}
