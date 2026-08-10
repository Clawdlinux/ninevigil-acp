/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package manifest

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestExecutionManifest_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := ExecutionManifest{
		ManifestID: "m-test",
		Version:    ProtocolVersion,
		TTL:        "300s",
		Actions: []Action{
			{
				ID:       "a1",
				Type:     "http",
				Endpoint: "grpc://db-proxy.svc:50051/query",
				Method:   "POST",
				Schema:   map[string]string{"sql": "string", "limit": "int?"},
				Auth:     AuthPreInjected,
				Timeout:  "30s",
			},
			{
				ID:        "a2",
				Type:      "http",
				Endpoint:  "https://email-gw.svc:443/send",
				Method:    "POST",
				Schema:    map[string]string{"to": "string[]", "subject": "string", "body": "string"},
				Auth:      AuthPreInjected,
				DependsOn: []string{"a1"},
			},
		},
		Boundaries: Boundary{
			Egress:             []string{"db-proxy.svc", "email-gw.svc"},
			MaxTokensPerAction: 15000,
			RequireApproval:    []string{"a2"},
			AuditLevel:         AuditFull,
		},
		FeedbackEndpoint: "http://acp-feedback.svc.cluster.local/v1/feedback",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ExecutionManifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\n  want %#v\n  got  %#v", original, decoded)
	}
}

func TestContextRequest_JSONOmitsZero(t *testing.T) {
	t.Parallel()

	req := ContextRequest{Intent: "query db", AgentID: "agent-1"}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := string(raw)
	want := `{"intent":"query db","agent_id":"agent-1"}`
	if got != want {
		t.Fatalf("unexpected JSON:\n  want %s\n  got  %s", want, got)
	}
}

func TestFeedbackEvent_OutcomeValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		o    FeedbackOutcome
		want string
	}{
		{"success", FeedbackSuccess, "success"},
		{"error", FeedbackError, "error"},
		{"skipped", FeedbackSkipped, "skipped"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if string(tc.o) != tc.want {
				t.Fatalf("outcome %q != %q", tc.o, tc.want)
			}
		})
	}
}
