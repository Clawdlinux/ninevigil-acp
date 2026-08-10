/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package mcp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
)

func sampleToolsListJSON() string {
	return `{
	  "tools": [
	    {
	      "name": "issues.create",
	      "description": "Create a GitHub issue.",
	      "inputSchema": {
	        "type": "object",
	        "properties": {
	          "title": {"type": "string"},
	          "body": {"type": "string"},
	          "labels": {"type": "array", "items": {"type": "string"}}
	        },
	        "required": ["title"]
	      }
	    },
	    {
	      "name": "issues.list",
	      "description": "List GitHub issues.",
	      "inputSchema": {
	        "type": "object",
	        "properties": {
	          "state": {"type": "string", "enum": ["open", "closed", "all"]},
	          "limit": {"type": "integer"}
	        },
	        "required": []
	      }
	    }
	  ]
	}`
}

func responseFromString(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
}

func TestImporter_ImportSource_RegistersTools(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)

	doer.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if req.URL.String() != "https://gh.example.com/tools/list" {
			t.Fatalf("url = %s", req.URL.String())
		}
		if req.Header.Get("Authorization") != "Bearer xyz" {
			t.Fatalf("authorization header missing or wrong: %q", req.Header.Get("Authorization"))
		}
		return responseFromString(http.StatusOK, sampleToolsListJSON()), nil
	})

	reg := registry.NewMemoryRegistry()
	imp := NewImporter(reg, doer)
	n, err := imp.ImportSource(Source{
		Name:              "github",
		BaseURL:           "https://gh.example.com",
		Auth:              "Bearer xyz",
		ExtraCapabilities: []string{"github"},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Fatalf("registered %d tools, want 2", n)
	}

	create, err := reg.Get("github.issues.create")
	if err != nil {
		t.Fatalf("get create: %v", err)
	}
	wantSchema := map[string]string{
		"title":  "string",
		"body":   "string?",
		"labels": "string[]?",
	}
	if !reflect.DeepEqual(create.Schema, wantSchema) {
		t.Fatalf("compact schema = %v, want %v", create.Schema, wantSchema)
	}

	caps := append([]string(nil), create.Capabilities...)
	sort.Strings(caps)
	for _, must := range []string{"github", "issues", "create", "write"} {
		found := false
		for _, c := range caps {
			if c == must {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected capability %q in %v", must, caps)
		}
	}

	if create.Endpoint != "https://gh.example.com/tools/call/issues.create" {
		t.Fatalf("endpoint = %q", create.Endpoint)
	}
	if len(create.Egress) != 1 || create.Egress[0] != "gh.example.com" {
		t.Fatalf("egress = %v, want [gh.example.com]", create.Egress)
	}

	list, err := reg.Get("github.issues.list")
	if err != nil {
		t.Fatalf("get list: %v", err)
	}
	if list.Schema["state"] != "enum:open|closed|all?" {
		t.Fatalf("enum schema = %q", list.Schema["state"])
	}
	if list.Schema["limit"] != "int?" {
		t.Fatalf("int? schema = %q", list.Schema["limit"])
	}
}

func TestImporter_ImportSource_HTTPErrorPropagated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)
	doer.EXPECT().Do(gomock.Any()).Return(nil, errors.New("dial tcp: refused"))

	reg := registry.NewMemoryRegistry()
	imp := NewImporter(reg, doer)
	_, err := imp.ImportSource(Source{Name: "x", BaseURL: "https://x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestImporter_ImportSource_NonOKResponse(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)
	doer.EXPECT().Do(gomock.Any()).Return(responseFromString(http.StatusUnauthorized, `{}`), nil)

	reg := registry.NewMemoryRegistry()
	imp := NewImporter(reg, doer)
	_, err := imp.ImportSource(Source{Name: "x", BaseURL: "https://x"})
	if err == nil || !contains(err.Error(), "status 401") {
		t.Fatalf("expected status 401 error, got %v", err)
	}
}

func TestImporter_ImportSource_RejectsEmptyName(t *testing.T) {
	t.Parallel()

	imp := NewImporter(registry.NewMemoryRegistry(), nil)
	if _, err := imp.ImportSource(Source{Name: " ", BaseURL: "https://x"}); err == nil {
		t.Fatal("expected error for empty name")
	}
	if _, err := imp.ImportSource(Source{Name: "x", BaseURL: " "}); err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestRegisterAll_RejectsEmptyDescriptorName(t *testing.T) {
	t.Parallel()

	imp := NewImporter(registry.NewMemoryRegistry(), nil)
	_, err := imp.RegisterAll(Source{Name: "x", BaseURL: "https://x"}, []ToolDescriptor{
		{Name: "", Description: "no name"},
	})
	if err == nil {
		t.Fatal("expected error for empty descriptor name")
	}
}

func TestRegisterAll_StdioSourceRegistersStdioTools(t *testing.T) {
	t.Parallel()

	reg := registry.NewMemoryRegistry()
	imp := NewImporter(reg, nil)
	_, err := imp.RegisterAll(Source{Name: "files", Type: "stdio", ExtraCapabilities: []string{"filesystem"}}, []ToolDescriptor{
		{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"path"},
			},
		},
	})
	if err != nil {
		t.Fatalf("register stdio tools: %v", err)
	}

	tool, err := reg.Get("files.read_file")
	if err != nil {
		t.Fatalf("get stdio tool: %v", err)
	}
	if tool.Type != "stdio" {
		t.Fatalf("type = %q, want stdio", tool.Type)
	}
	if tool.Endpoint != "stdio://files/read_file" {
		t.Fatalf("endpoint = %q, want stdio://files/read_file", tool.Endpoint)
	}
	if len(tool.Egress) != 0 {
		t.Fatalf("egress = %v, want empty", tool.Egress)
	}
}

func TestCompactSchema_OmittedFieldDefaults(t *testing.T) {
	t.Parallel()

	out := compactSchema(map[string]interface{}{
		"properties": map[string]interface{}{
			"opaque": map[string]interface{}{}, // no "type"
		},
		"required": []interface{}{"opaque"},
	})
	if out["opaque"] != "string" {
		t.Fatalf("default-type field = %q, want string", out["opaque"])
	}
}

func TestInferCapabilities_FallsBackToSourceName(t *testing.T) {
	t.Parallel()

	caps := inferCapabilities("a", []string{}) // single-char tokens are dropped
	if len(caps) != 0 {
		t.Fatalf("single-char token should produce no caps, got %v", caps)
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
