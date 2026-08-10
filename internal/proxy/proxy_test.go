/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

// fakeUpstream returns an httptest.Server that records the last request it
// received so tests can assert on auth-injection behaviour.
type recordedRequest struct {
	authHeader string
	xKeyHeader string
	body       string
	path       string
}

func newUpstream(t *testing.T) (string, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.authHeader = r.Header.Get("Authorization")
		rec.xKeyHeader = r.Header.Get("X-Api-Key")
		rec.path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		rec.body = string(body)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, rec
}

func upstreamHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	host := u.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host
}

func newManifest(actionID, endpoint string, requireApproval bool, egress []string) *manifest.ExecutionManifest {
	mf := &manifest.ExecutionManifest{
		ManifestID: "m-test",
		Version:    manifest.ProtocolVersion,
		Actions: []manifest.Action{{
			ID:       actionID,
			Type:     "http",
			Endpoint: endpoint,
			Method:   "POST",
			Schema:   map[string]string{"x": "string"},
			Auth:     manifest.AuthPreInjected,
		}},
		Boundaries: manifest.Boundary{
			Egress:     egress,
			AuditLevel: manifest.AuditFull,
		},
	}
	if requireApproval {
		mf.Boundaries.RequireApproval = []string{actionID}
	}
	return mf
}

func TestProxy_HappyPath_InjectsCredsAndStripsAuthorization(t *testing.T) {
	t.Parallel()

	upstreamURL, rec := newUpstream(t)
	mf := newManifest("a1", upstreamURL+"/query", false, []string{upstreamHost(t, upstreamURL)})

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)

	store.EXPECT().Get("m-test").Return(mf, true)
	injector.EXPECT().Inject(gomock.Any(), "m-test", gomock.Any()).Return(http.Header{
		"X-Api-Key": []string{"server-side-key"},
	}, nil)

	srv := httptest.NewServer(New(Config{Store: store, Injector: injector, Approval: AlwaysApprove{}}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/exec/m-test/a1", strings.NewReader(`{"sql":"select 1"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer agent-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	if rec.authHeader != "" {
		t.Fatalf("upstream saw Authorization header %q (should be stripped)", rec.authHeader)
	}
	if rec.xKeyHeader != "server-side-key" {
		t.Fatalf("upstream saw X-Api-Key %q, want server-side-key", rec.xKeyHeader)
	}
	if rec.body != `{"sql":"select 1"}` {
		t.Fatalf("upstream body = %q", rec.body)
	}
	if rec.path != "/query" {
		t.Fatalf("upstream path = %q, want /query", rec.path)
	}
}

func TestProxy_404_ManifestNotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)
	store.EXPECT().Get("m-missing").Return(nil, false)

	srv := httptest.NewServer(New(Config{Store: store, Injector: injector}))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/exec/m-missing/a1", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestProxy_404_ActionNotFound(t *testing.T) {
	t.Parallel()

	upstreamURL, _ := newUpstream(t)
	mf := newManifest("a1", upstreamURL+"/x", false, []string{upstreamHost(t, upstreamURL)})

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)
	store.EXPECT().Get("m-test").Return(mf, true)

	srv := httptest.NewServer(New(Config{Store: store, Injector: injector}))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/exec/m-test/a99", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestProxy_403_ApprovalRequired(t *testing.T) {
	t.Parallel()

	upstreamURL, _ := newUpstream(t)
	mf := newManifest("a1", upstreamURL+"/x", true /* requireApproval */, []string{upstreamHost(t, upstreamURL)})

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)
	store.EXPECT().Get("m-test").Return(mf, true)

	srv := httptest.NewServer(New(Config{Store: store, Injector: injector})) // default AlwaysDeny
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/exec/m-test/a1", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if got := resp.Header.Get("X-ACP-Approval-Required"); got != "true" {
		t.Fatalf("missing X-ACP-Approval-Required header (got %q)", got)
	}
}

func TestProxy_403_EgressNotAllowed(t *testing.T) {
	t.Parallel()

	upstreamURL, _ := newUpstream(t)
	// Egress allow-list has the wrong host.
	mf := newManifest("a1", upstreamURL+"/x", false, []string{"some-other-host"})

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)
	store.EXPECT().Get("m-test").Return(mf, true)

	srv := httptest.NewServer(New(Config{Store: store, Injector: injector}))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/exec/m-test/a1", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestProxy_502_InjectorError(t *testing.T) {
	t.Parallel()

	upstreamURL, _ := newUpstream(t)
	mf := newManifest("a1", upstreamURL+"/x", false, []string{upstreamHost(t, upstreamURL)})

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)

	store.EXPECT().Get("m-test").Return(mf, true)
	injector.EXPECT().Inject(gomock.Any(), "m-test", gomock.Any()).Return(nil, errors.New("vault timeout"))

	srv := httptest.NewServer(New(Config{Store: store, Injector: injector, Approval: AlwaysApprove{}}))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/exec/m-test/a1", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestProxy_400_MalformedPath(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)

	srv := httptest.NewServer(New(Config{Store: store, Injector: injector}))
	t.Cleanup(srv.Close)

	cases := []struct {
		name string
		path string
		want int
	}{
		{"missing action", "/v1/exec/m-test", http.StatusBadRequest},
		{"unknown route", "/wrong", http.StatusNotFound},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+tc.path, "application/json", bytes.NewReader([]byte(`{}`)))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestMapInjector_PerActionTakesPriority(t *testing.T) {
	t.Parallel()

	inj := &MapInjector{
		PerAction:   map[string]http.Header{"m1|a1": {"X-Api-Key": []string{"per-manifest"}}},
		PerActionID: map[string]http.Header{"a1": {"X-Api-Key": []string{"fallback"}}},
	}
	got, err := inj.Inject(context.Background(), "m1", manifest.Action{ID: "a1"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if got.Get("X-Api-Key") != "per-manifest" {
		t.Fatalf("per-manifest entry should win, got %q", got.Get("X-Api-Key"))
	}

	got, err = inj.Inject(context.Background(), "m2", manifest.Action{ID: "a1"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if got.Get("X-Api-Key") != "fallback" {
		t.Fatalf("fallback should win, got %q", got.Get("X-Api-Key"))
	}

	got, err = inj.Inject(context.Background(), "m3", manifest.Action{ID: "unknown"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unknown action should yield empty headers, got %v", got)
	}
}

func TestMemoryStore_PutGet(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if _, ok := s.Get("missing"); ok {
		t.Fatalf("expected miss")
	}
	s.Put(&manifest.ExecutionManifest{ManifestID: "m"})
	mf, ok := s.Get("m")
	if !ok || mf.ManifestID != "m" {
		t.Fatalf("get: %+v %v", mf, ok)
	}
	// Nil and empty-id puts are no-ops.
	s.Put(nil)
	s.Put(&manifest.ExecutionManifest{})
	if _, ok := s.Get(""); ok {
		t.Fatalf("empty id should not be stored")
	}
}

func TestProxy_StripsProxyAuthorization(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Errorf("upstream saw Proxy-Authorization header (should be stripped)")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	upstreamURL := srv.URL
	host := upstreamHost(t, upstreamURL)

	mf := newManifest("a1", upstreamURL+"/x", false, []string{host})

	ctrl := gomock.NewController(t)
	store := NewMockManifestStore(ctrl)
	injector := NewMockInjector(ctrl)
	store.EXPECT().Get("m-test").Return(mf, true)
	injector.EXPECT().Inject(gomock.Any(), "m-test", gomock.Any()).Return(http.Header{}, nil)

	proxySrv := httptest.NewServer(New(Config{Store: store, Injector: injector, Approval: AlwaysApprove{}}))
	t.Cleanup(proxySrv.Close)

	req, _ := http.NewRequest(http.MethodPost, proxySrv.URL+"/v1/exec/m-test/a1", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Proxy-Authorization", "Bearer something")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
