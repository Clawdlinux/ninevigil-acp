/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

func mustResponse(status int, body any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(raw)),
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
}

func TestClient_Context_HappyPath(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)

	doer.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if !strings.HasSuffix(req.URL.Path, "/v1/context") {
			t.Fatalf("path = %s", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer dev" {
			t.Fatalf("auth header = %q", got)
		}
		var body manifest.ContextRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if body.AgentID != "agent-1" {
			t.Fatalf("agent_id = %q", body.AgentID)
		}
		return mustResponse(http.StatusOK, manifest.ExecutionManifest{
			ManifestID: "m-test",
			Version:    manifest.ProtocolVersion,
			Actions:    []manifest.Action{{ID: "a1"}},
		}), nil
	})

	c := NewClient("http://example", WithToken("dev"), WithDoer(doer))
	got, err := c.Context(context.Background(), manifest.ContextRequest{
		Intent: "x", AgentID: "agent-1",
	})
	if err != nil {
		t.Fatalf("Context err = %v", err)
	}
	if got.ManifestID != "m-test" || len(got.Actions) != 1 {
		t.Fatalf("unexpected manifest: %#v", got)
	}
}

func TestClient_Context_APIError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)
	doer.EXPECT().Do(gomock.Any()).Return(mustResponse(http.StatusUnauthorized, manifest.ErrorResponse{Error: "missing bearer token"}), nil)

	c := NewClient("http://example", WithDoer(doer))
	_, err := c.Context(context.Background(), manifest.ContextRequest{Intent: "x", AgentID: "a"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsAPIError(err) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As failed for %v", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "missing bearer token" {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestClient_Feedback(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)
	doer.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/v1/feedback") {
			t.Fatalf("path = %s", req.URL.Path)
		}
		var ev manifest.FeedbackEvent
		if err := json.NewDecoder(req.Body).Decode(&ev); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ev.Outcome != manifest.FeedbackSuccess {
			t.Fatalf("outcome = %q", ev.Outcome)
		}
		return mustResponse(http.StatusAccepted, map[string]string{"status": "accepted"}), nil
	})

	c := NewClient("http://example", WithDoer(doer))
	if err := c.Feedback(context.Background(), manifest.FeedbackEvent{
		ManifestID: "m-1", ActionID: "a1", Outcome: manifest.FeedbackSuccess,
	}); err != nil {
		t.Fatalf("Feedback err = %v", err)
	}
}

func TestClient_Healthz(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)
	doer.EXPECT().Do(gomock.Any()).Return(mustResponse(http.StatusOK, map[string]string{"status": "ok"}), nil)

	c := NewClient("http://example", WithDoer(doer))
	if err := c.Healthz(context.Background()); err != nil {
		t.Fatalf("Healthz err = %v", err)
	}
}

func TestClient_TransportError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	doer := NewMockHTTPDoer(ctrl)
	doer.EXPECT().Do(gomock.Any()).Return(nil, errors.New("connect refused"))

	c := NewClient("http://example", WithDoer(doer))
	_, err := c.Context(context.Background(), manifest.ContextRequest{Intent: "x", AgentID: "a"})
	if err == nil {
		t.Fatal("expected error")
	}
	if IsAPIError(err) {
		t.Fatalf("transport error should not be APIError: %v", err)
	}
}

func TestClient_TrimsBaseURL(t *testing.T) {
	t.Parallel()

	c := NewClient("http://example.com/")
	if c.baseURL != "http://example.com" {
		t.Fatalf("baseURL = %q", c.baseURL)
	}
}
