/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package builder

import (
	"errors"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

// fixedID is a tiny IDSource for deterministic tests.
type fixedID struct{ id string }

func (f fixedID) NewID() string { return f.id }

func tool(id string, caps []string, depsOn []string, host string, approve bool) registry.Tool {
	return registry.Tool{
		ID:            id,
		Type:          "http",
		Endpoint:      "http://" + id,
		Method:        "POST",
		Schema:        map[string]string{"x": "string"},
		Auth:          manifest.AuthPreInjected,
		Timeout:       "10s",
		Capabilities:  caps,
		Egress:        []string{host},
		DependsOnCaps: depsOn,
		RequireApprov: approve,
	}
}

func TestBuilder_Build_AssignsActionIDsAndOrdering(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	src := NewMockToolSource(ctrl)

	caps := []string{"sql", "template"}
	src.EXPECT().Lookup(caps).Return([]registry.Tool{
		tool("template.render", []string{"template"}, []string{"sql"}, "template-svc", false),
		tool("db.query", []string{"sql"}, nil, "db-proxy", false),
	})

	b := New(src, fixedID{id: "m-fixed"}, DefaultOptions())
	got, err := b.Build(manifest.ContextRequest{Intent: "ignored", AgentID: "agent"}, caps)
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}

	if got.ManifestID != "m-fixed" {
		t.Fatalf("ManifestID = %q, want m-fixed", got.ManifestID)
	}
	if got.Version != manifest.ProtocolVersion {
		t.Fatalf("Version = %q, want %q", got.Version, manifest.ProtocolVersion)
	}
	if len(got.Actions) != 2 {
		t.Fatalf("Actions len = %d, want 2", len(got.Actions))
	}
	// Sorted by tool ID: db.query => a1, template.render => a2.
	if got.Actions[0].ID != "a1" || got.Actions[0].Endpoint != "http://db.query" {
		t.Fatalf("a1 wrong: %#v", got.Actions[0])
	}
	if got.Actions[1].ID != "a2" || got.Actions[1].Endpoint != "http://template.render" {
		t.Fatalf("a2 wrong: %#v", got.Actions[1])
	}
	// template.render depends on sql -> a1.
	if !reflect.DeepEqual(got.Actions[1].DependsOn, []string{"a1"}) {
		t.Fatalf("DependsOn = %v, want [a1]", got.Actions[1].DependsOn)
	}
	// db.query has no deps.
	if len(got.Actions[0].DependsOn) != 0 {
		t.Fatalf("expected no deps for a1, got %v", got.Actions[0].DependsOn)
	}
}

func TestBuilder_Build_AggregatesEgressAndApprovals(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	src := NewMockToolSource(ctrl)

	caps := []string{"sql", "email"}
	src.EXPECT().Lookup(caps).Return([]registry.Tool{
		tool("db.query", []string{"sql"}, nil, "db-proxy", false),
		tool("email.send", []string{"email"}, []string{"sql"}, "email-gw", true),
	})

	b := New(src, fixedID{id: "m-2"}, DefaultOptions())
	got, err := b.Build(manifest.ContextRequest{Intent: "any", AgentID: "agent"}, caps)
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}

	wantEgress := []string{"db-proxy", "email-gw"}
	if !reflect.DeepEqual(got.Boundaries.Egress, wantEgress) {
		t.Fatalf("Egress = %v, want %v", got.Boundaries.Egress, wantEgress)
	}
	if !reflect.DeepEqual(got.Boundaries.RequireApproval, []string{"a2"}) {
		t.Fatalf("RequireApproval = %v, want [a2]", got.Boundaries.RequireApproval)
	}
	if got.Boundaries.AuditLevel != manifest.AuditFull {
		t.Fatalf("AuditLevel = %q, want %q", got.Boundaries.AuditLevel, manifest.AuditFull)
	}
}

func TestBuilder_Build_NoMatchingTools(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	src := NewMockToolSource(ctrl)
	src.EXPECT().Lookup([]string{"unknown"}).Return(nil)

	b := New(src, fixedID{id: "m-3"}, DefaultOptions())
	_, err := b.Build(manifest.ContextRequest{Intent: "x", AgentID: "agent"}, []string{"unknown"})
	if !errors.Is(err, ErrNoMatchingTools) {
		t.Fatalf("expected ErrNoMatchingTools, got %v", err)
	}
}

func TestBuilder_Build_DeterministicSchemas(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	src := NewMockToolSource(ctrl)

	called := tool("db.query", []string{"sql"}, nil, "db-proxy", false)
	src.EXPECT().Lookup([]string{"sql"}).Return([]registry.Tool{called})

	b := New(src, fixedID{id: "m-4"}, DefaultOptions())
	got, err := b.Build(manifest.ContextRequest{Intent: "x", AgentID: "agent"}, []string{"sql"})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}

	// Manifest schema should be the registry schema verbatim (compact mini-language already).
	if !reflect.DeepEqual(got.Actions[0].Schema, called.Schema) {
		t.Fatalf("Schema = %v, want %v", got.Actions[0].Schema, called.Schema)
	}
	if got.Actions[0].Auth != manifest.AuthPreInjected {
		t.Fatalf("Auth = %q, want %q", got.Actions[0].Auth, manifest.AuthPreInjected)
	}
}

func TestBuilder_Build_OptionsDefaults(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	src := NewMockToolSource(ctrl)
	src.EXPECT().Lookup([]string{"sql"}).Return([]registry.Tool{
		tool("db.query", []string{"sql"}, nil, "db-proxy", false),
	})

	// Pass zero Options to confirm defaults are applied.
	b := New(src, fixedID{id: "m-5"}, Options{})
	got, err := b.Build(manifest.ContextRequest{Intent: "x", AgentID: "agent"}, []string{"sql"})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if got.TTL != "300s" {
		t.Fatalf("TTL = %q, want 300s", got.TTL)
	}
	if got.FeedbackEndpoint != "/v1/feedback" {
		t.Fatalf("FeedbackEndpoint = %q, want /v1/feedback", got.FeedbackEndpoint)
	}
	if got.Boundaries.MaxTokensPerAction != 15000 {
		t.Fatalf("MaxTokensPerAction = %d, want 15000", got.Boundaries.MaxTokensPerAction)
	}
}
