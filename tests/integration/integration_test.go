/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package integration_test exercises the Go SDK against a real ACP server
// running in-process. It validates the full happy path without touching the
// network or shelling out to curl.
package integration_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	builder "github.com/Clawdlinux/agent-native-format/internal/builder"
	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/internal/resolver"
	"github.com/Clawdlinux/agent-native-format/internal/server"
	"github.com/Clawdlinux/agent-native-format/pkg/acp"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

// liveStack returns an httptest.Server hosting a fully-wired ACP stack.
func liveStack(t *testing.T, token string) *httptest.Server {
	t.Helper()
	reg := registry.NewMemoryRegistry()
	if err := registry.Seed(reg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bld := builder.New(reg, server.CryptoIDSource{}, builder.DefaultOptions())
	handler := server.New(server.Config{
		Resolver:  resolver.NewKeywordResolver(nil),
		Builder:   bld,
		Feedback:  &server.LoggingFeedbackSink{},
		AuthToken: token,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestIntegration_SDKEndToEnd_HappyPath(t *testing.T) {
	t.Parallel()

	srv := liveStack(t, "secret")
	client := acp.NewClient(srv.URL, acp.WithToken("secret"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Healthz(ctx); err != nil {
		t.Fatalf("Healthz: %v", err)
	}

	mf, err := client.Context(ctx, manifest.ContextRequest{
		Intent:  "query the customer database, render a report, email the team",
		AgentID: "integration-agent",
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	// Manifest invariants.
	if mf.ManifestID == "" || mf.Version != manifest.ProtocolVersion {
		t.Fatalf("bad manifest envelope: %#v", mf)
	}
	if len(mf.Actions) < 3 {
		t.Fatalf("expected >=3 actions for full DAG, got %d", len(mf.Actions))
	}

	// At least one action should declare a depends_on chain (template or email).
	var sawDeps, sawApproval bool
	for _, a := range mf.Actions {
		if len(a.DependsOn) > 0 {
			sawDeps = true
		}
		if a.Auth != manifest.AuthPreInjected {
			t.Fatalf("action %s has unexpected auth %q", a.ID, a.Auth)
		}
	}
	if !sawDeps {
		t.Fatalf("no action declared depends_on; manifest = %#v", mf)
	}
	for _, id := range mf.Boundaries.RequireApproval {
		if id != "" {
			sawApproval = true
		}
	}
	if !sawApproval {
		t.Fatalf("expected at least one approval gate (email.send), got none")
	}

	// Feedback round-trip.
	if err := client.Feedback(ctx, manifest.FeedbackEvent{
		ManifestID: mf.ManifestID,
		ActionID:   mf.Actions[0].ID,
		Outcome:    manifest.FeedbackSuccess,
		LatencyMS:  42,
	}); err != nil {
		t.Fatalf("Feedback: %v", err)
	}
}

func TestIntegration_SDKEndToEnd_AuthMissing(t *testing.T) {
	t.Parallel()

	srv := liveStack(t, "secret")
	client := acp.NewClient(srv.URL) // no token

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Context(ctx, manifest.ContextRequest{Intent: "query db", AgentID: "agent"})
	if err == nil {
		t.Fatal("expected error without bearer token")
	}
	if !acp.IsAPIError(err) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
}

func TestIntegration_SDKEndToEnd_NoCapabilitiesMatched(t *testing.T) {
	t.Parallel()

	srv := liveStack(t, "")
	client := acp.NewClient(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Context(ctx, manifest.ContextRequest{
		Intent:  "complete gibberish unrelated content",
		AgentID: "agent",
	})
	if err == nil {
		t.Fatal("expected error for unmatched intent")
	}
	if !acp.IsAPIError(err) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
}
