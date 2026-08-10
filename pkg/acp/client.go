/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package acp provides a Go client for the ACP HTTP API.
//
// Example:
//
//	client := acp.NewClient("http://localhost:8080", acp.WithToken("dev"))
//	mf, err := client.Context(ctx, manifest.ContextRequest{
//	    Intent:  "query the customer db",
//	    AgentID: "agent-01",
//	})
package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

//go:generate ../../bin/mockgen -source=client.go -destination=mock_httpdoer_test.go -package=acp

// HTTPDoer abstracts the HTTP transport for testability. The standard
// *http.Client satisfies this interface.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a strongly typed HTTP client for the ACP API.
type Client struct {
	baseURL string
	token   string
	doer    HTTPDoer
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithToken sets the bearer token used for /v1/* requests.
func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// WithDoer overrides the default HTTP transport (useful for tests).
func WithDoer(doer HTTPDoer) Option {
	return func(c *Client) {
		if doer != nil {
			c.doer = doer
		}
	}
}

// NewClient builds a Client. The base URL must be the ACP server root
// (without /v1).
func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		doer:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Context calls POST /v1/context.
func (c *Client) Context(ctx context.Context, req manifest.ContextRequest) (*manifest.ExecutionManifest, error) {
	var out manifest.ExecutionManifest
	if err := c.do(ctx, http.MethodPost, "/v1/context", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Feedback calls POST /v1/feedback.
func (c *Client) Feedback(ctx context.Context, event manifest.FeedbackEvent) error {
	return c.do(ctx, http.MethodPost, "/v1/feedback", event, nil)
}

// Healthz calls GET /healthz.
func (c *Client) Healthz(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil)
}

// APIError is returned by the client when the server returns a non-2xx status.
type APIError struct {
	StatusCode int
	Message    string
}

// Error implements error.
func (e *APIError) Error() string {
	return fmt.Sprintf("acp: status %d: %s", e.StatusCode, e.Message)
}

// IsAPIError reports whether err is an *APIError.
func IsAPIError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("acp: marshal request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("acp: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return fmt.Errorf("acp: transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		var er manifest.ErrorResponse
		_ = json.Unmarshal(raw, &er)
		msg := er.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("acp: decode response: %w", err)
	}
	return nil
}
