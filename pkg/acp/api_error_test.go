/*
Copyright 2026 Clawdlinux.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package acp

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAPIError_FormatsStatusAndMessage(t *testing.T) {
	t.Parallel()

	err := &APIError{StatusCode: http.StatusUnauthorized, Message: "missing bearer token"}
	got := err.Error()
	if !strings.Contains(got, "401") {
		t.Fatalf("error message missing status: %q", got)
	}
	if !strings.Contains(got, "missing bearer token") {
		t.Fatalf("error message missing body: %q", got)
	}
}

func TestAPIError_IsError(t *testing.T) {
	t.Parallel()

	var err error = &APIError{StatusCode: 500, Message: "boom"}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As should match *APIError")
	}
	if apiErr.StatusCode != 500 {
		t.Fatalf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
}

func TestIsAPIError_FalseForNonAPI(t *testing.T) {
	t.Parallel()
	if IsAPIError(nil) {
		t.Fatal("nil should not be APIError")
	}
	if IsAPIError(errors.New("boom")) {
		t.Fatal("plain error should not be APIError")
	}
}
