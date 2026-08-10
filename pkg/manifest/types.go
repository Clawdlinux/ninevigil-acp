/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*/

// Package manifest defines the public ACP wire types shared between the
// server, the Go SDK (pkg/acp), and adapters.
//
// These types are normative: they mirror SPEC.md §4 and any change here is a
// protocol change.
package manifest

// ProtocolVersion is the manifest schema version emitted by ACP v0.1 servers.
const ProtocolVersion = "acp/v1"

// AuthMode is the auth marker placed on each Action. Servers MUST NOT emit
// raw credentials; the proxy injects them at execution time.
type AuthMode string

const (
	// AuthPreInjected indicates the auth proxy will inject credentials.
	AuthPreInjected AuthMode = "pre-injected"
	// AuthNone indicates the action requires no credentials.
	AuthNone AuthMode = "none"
)

// AuditLevel controls server-side audit verbosity for a manifest.
type AuditLevel string

const (
	AuditNone    AuditLevel = "none"
	AuditSummary AuditLevel = "summary"
	AuditFull    AuditLevel = "full"
)

// OutputFormat controls how verbose the manifest payload should be.
type OutputFormat string

const (
	OutputMinimal OutputFormat = "minimal"
	OutputVerbose OutputFormat = "verbose"
)

// Constraints are the optional caller-supplied execution constraints.
type Constraints struct {
	MaxTokens    int          `json:"max_tokens,omitempty"`
	Timeout      string       `json:"timeout,omitempty"`
	OutputFormat OutputFormat `json:"output_format,omitempty"`
}

// ContextRequest is the request body for POST /v1/context.
type ContextRequest struct {
	Intent       string       `json:"intent"`
	AgentID      string       `json:"agent_id"`
	Capabilities []string     `json:"capabilities,omitempty"`
	Constraints  *Constraints `json:"constraints,omitempty"`
}

// Action describes one executable step in an ACP manifest.
type Action struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Endpoint  string            `json:"endpoint"`
	Method    string            `json:"method,omitempty"`
	Schema    map[string]string `json:"schema"`
	Auth      AuthMode          `json:"auth"`
	Timeout   string            `json:"timeout,omitempty"`
	DependsOn []string          `json:"depends_on,omitempty"`
}

// Boundary captures execution constraints the agent and proxy must honor.
type Boundary struct {
	Egress             []string   `json:"egress"`
	MaxTokensPerAction int        `json:"max_tokens_per_action"`
	RequireApproval    []string   `json:"require_approval,omitempty"`
	AuditLevel         AuditLevel `json:"audit_level"`
}

// ExecutionManifest is the ACP response returned by POST /v1/context.
type ExecutionManifest struct {
	ManifestID       string   `json:"manifest_id"`
	Version          string   `json:"version"`
	TTL              string   `json:"ttl"`
	Actions          []Action `json:"actions"`
	Boundaries       Boundary `json:"boundaries"`
	FeedbackEndpoint string   `json:"feedback_endpoint"`
}

// FeedbackOutcome is the high-level result reported per action.
type FeedbackOutcome string

const (
	FeedbackSuccess FeedbackOutcome = "success"
	FeedbackError   FeedbackOutcome = "error"
	FeedbackSkipped FeedbackOutcome = "skipped"
)

// FeedbackEvent is the request body for POST /v1/feedback.
type FeedbackEvent struct {
	ManifestID string          `json:"manifest_id"`
	ActionID   string          `json:"action_id"`
	Outcome    FeedbackOutcome `json:"outcome"`
	LatencyMS  int64           `json:"latency_ms,omitempty"`
	TokensIn   int             `json:"tokens_in,omitempty"`
	TokensOut  int             `json:"tokens_out,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// ErrorResponse is the wire format for non-2xx ACP responses.
type ErrorResponse struct {
	Error string `json:"error"`
}
