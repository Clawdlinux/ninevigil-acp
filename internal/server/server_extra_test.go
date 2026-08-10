/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

// errorSink always fails on Record so we can hit handleFeedback's 500 path.
type errorSink struct{}

func (errorSink) Record(manifest.FeedbackEvent) error { return errors.New("sink down") }

func TestServer_Feedback_RejectsBadJSON(t *testing.T) {
	t.Parallel()
	srv := newServer(t, Config{Feedback: &LoggingFeedbackSink{}})
	resp, err := http.Post(srv.URL+"/v1/feedback", "application/json", strings.NewReader("not-json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_Feedback_RejectsGet(t *testing.T) {
	t.Parallel()
	srv := newServer(t, Config{Feedback: &LoggingFeedbackSink{}})
	resp, err := http.Get(srv.URL + "/v1/feedback")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_Feedback_PropagatesSinkError(t *testing.T) {
	t.Parallel()
	srv := newServer(t, Config{Feedback: errorSink{}})
	resp := postJSON(t, srv.URL+"/v1/feedback", manifest.FeedbackEvent{
		ManifestID: "m-1", ActionID: "a1", Outcome: manifest.FeedbackSuccess,
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestCryptoIDSource_HighEntropy(t *testing.T) {
	t.Parallel()
	// 1000 IDs should all be unique with overwhelming probability.
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := CryptoIDSource{}.NewID()
		if !strings.HasPrefix(id, "m-") {
			t.Fatalf("missing prefix: %s", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("collision after %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

// freePort returns a TCP port the test can bind without races.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

func TestListenAndServe_HandlesContextCancel(t *testing.T) {
	t.Parallel()

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	ctx, cancel := context.WithCancel(context.Background())

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	done := make(chan error, 1)
	go func() {
		done <- ListenAndServe(ctx, addr, handler)
	}()

	// Wait for the listener to come up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Cancel and assert ListenAndServe returns cleanly.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("ListenAndServe did not return within 5s of cancel")
	}
}

func TestListenAndServe_ReturnsErrorOnBadAddr(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Port 1 requires privileges; ListenAndServe should surface the bind error.
	err := ListenAndServe(ctx, "127.0.0.1:1", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if err == nil {
		t.Fatal("expected bind error on privileged port")
	}
}

// TestServer_AuthHeader_Missing_HasJSONEnvelope ensures unauthorized
// responses use the same JSON envelope as everything else (no plain-text
// leak from net/http defaults).
func TestServer_AuthHeader_Missing_HasJSONEnvelope(t *testing.T) {
	t.Parallel()
	srv := newServer(t, Config{AuthToken: "secret", Resolver: nil, Builder: nil})
	resp, err := http.Post(srv.URL+"/v1/context", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type = %q, want JSON", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("body missing error field: %s", body)
	}
}
