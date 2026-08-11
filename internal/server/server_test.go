/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

func newServer(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(cfg))
	t.Cleanup(srv.Close)
	return srv
}

func decode(t *testing.T, body io.Reader, into any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(into); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func postJSON(t *testing.T, url string, payload any, headers map[string]string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestServer_Healthz(t *testing.T) {
	t.Parallel()

	srv := newServer(t, Config{})
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	decode(t, resp.Body, &body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestServer_Healthz_RejectsPost(t *testing.T) {
	t.Parallel()
	srv := newServer(t, Config{})
	resp, err := http.Post(srv.URL+"/healthz", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want JSON", ct)
	}
}

func TestServer_Context_HappyPath(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	res := NewMockResolver(ctrl)
	bld := NewMockBuilder(ctrl)

	res.EXPECT().Resolve("query the customer db", []string(nil)).Return([]string{"sql"}, nil)
	bld.EXPECT().Build(gomock.Any(), []string{"sql"}).Return(manifest.ExecutionManifest{
		ManifestID: "m-test",
		Version:    manifest.ProtocolVersion,
		TTL:        "300s",
		Actions: []manifest.Action{{
			ID: "a1", Type: "http", Endpoint: "http://db", Method: "POST",
			Schema: map[string]string{"sql": "string"}, Auth: manifest.AuthPreInjected,
		}},
		Boundaries:       manifest.Boundary{Egress: []string{"db"}, MaxTokensPerAction: 15000, AuditLevel: manifest.AuditFull},
		FeedbackEndpoint: "/v1/feedback",
	}, nil)

	srv := newServer(t, Config{Resolver: res, Builder: bld})
	resp := postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{
		Intent: "query the customer db", AgentID: "agent-1",
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var got manifest.ExecutionManifest
	decode(t, resp.Body, &got)
	if got.ManifestID != "m-test" {
		t.Fatalf("ManifestID = %q", got.ManifestID)
	}
	if len(got.Actions) != 1 || got.Actions[0].ID != "a1" {
		t.Fatalf("actions = %#v", got.Actions)
	}
}

func TestServer_Context_Validation(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	cfg := Config{Resolver: NewMockResolver(ctrl), Builder: NewMockBuilder(ctrl)}
	srv := newServer(t, cfg)

	cases := []struct {
		name       string
		body       any
		wantStatus int
	}{
		{"empty body", manifest.ContextRequest{}, http.StatusBadRequest},
		{"missing agent_id", manifest.ContextRequest{Intent: "query db"}, http.StatusBadRequest},
		{"missing intent and capabilities", manifest.ContextRequest{AgentID: "a"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, srv.URL+"/v1/context", tc.body, nil)
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			var body manifest.ErrorResponse
			decode(t, resp.Body, &body)
			if body.Error == "" {
				t.Fatalf("expected error message")
			}
		})
	}
}

func TestServer_Context_RejectsBadJSON(t *testing.T) {
	t.Parallel()

	srv := newServer(t, Config{Resolver: nil, Builder: nil})
	resp, err := http.Post(srv.URL+"/v1/context", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_Context_PropagatesResolverError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	res := NewMockResolver(ctrl)
	bld := NewMockBuilder(ctrl)
	res.EXPECT().Resolve(gomock.Any(), gomock.Any()).Return(nil, errors.New("nothing matches"))

	srv := newServer(t, Config{Resolver: res, Builder: bld})
	resp := postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{
		Intent: "x", AgentID: "agent-1",
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestServer_Context_PropagatesBuilderError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	res := NewMockResolver(ctrl)
	bld := NewMockBuilder(ctrl)
	res.EXPECT().Resolve(gomock.Any(), gomock.Any()).Return([]string{"x"}, nil)
	bld.EXPECT().Build(gomock.Any(), []string{"x"}).Return(manifest.ExecutionManifest{}, errors.New("no tools"))

	srv := newServer(t, Config{Resolver: res, Builder: bld})
	resp := postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{
		Intent: "x", AgentID: "agent-1",
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestServer_Auth(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	res := NewMockResolver(ctrl)
	bld := NewMockBuilder(ctrl)
	res.EXPECT().Resolve(gomock.Any(), gomock.Any()).Return([]string{"sql"}, nil)
	bld.EXPECT().Build(gomock.Any(), []string{"sql"}).Return(manifest.ExecutionManifest{ManifestID: "m"}, nil)

	srv := newServer(t, Config{Resolver: res, Builder: bld, AuthToken: "secret"})

	// Missing token -> 401.
	resp := postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{Intent: "x", AgentID: "a"}, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", resp.StatusCode)
	}

	// Wrong token -> 401.
	resp = postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{Intent: "x", AgentID: "a"},
		map[string]string{"Authorization": "Bearer nope"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", resp.StatusCode)
	}

	// Correct token -> 200.
	resp = postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{Intent: "x", AgentID: "a"},
		map[string]string{"Authorization": "Bearer secret"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("correct token status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestServer_Feedback_HappyPath(t *testing.T) {
	t.Parallel()

	sink := &LoggingFeedbackSink{}
	srv := newServer(t, Config{Feedback: sink})

	resp := postJSON(t, srv.URL+"/v1/feedback", manifest.FeedbackEvent{
		ManifestID: "m-1", ActionID: "a1", Outcome: manifest.FeedbackSuccess, LatencyMS: 42,
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	if sink.Count() != 1 {
		t.Fatalf("sink count = %d, want 1", sink.Count())
	}
}

func TestServer_Feedback_Validation(t *testing.T) {
	t.Parallel()

	srv := newServer(t, Config{Feedback: &LoggingFeedbackSink{}})

	cases := []manifest.FeedbackEvent{
		{ActionID: "a1"},
		{ManifestID: "m-1"},
	}
	for i, ev := range cases {
		i, ev := i, ev
		t.Run("case", func(t *testing.T) {
			resp := postJSON(t, srv.URL+"/v1/feedback", ev, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("case %d status = %d, want 400", i, resp.StatusCode)
			}
		})
	}
}

func TestCryptoIDSource_Format(t *testing.T) {
	t.Parallel()
	id1 := CryptoIDSource{}.NewID()
	id2 := CryptoIDSource{}.NewID()

	if !strings.HasPrefix(id1, "m-") {
		t.Fatalf("id missing prefix: %s", id1)
	}
	if id1 == id2 {
		t.Fatalf("ids should differ: %s == %s", id1, id2)
	}
	if len(id1) != len("m-")+16 {
		t.Fatalf("id length = %d, want %d", len(id1), len("m-")+16)
	}
}

// recordingPersister captures every manifest the server hands it.
type recordingPersister struct {
	got []manifest.ExecutionManifest
}

func (p *recordingPersister) Put(mf *manifest.ExecutionManifest) {
	if mf != nil {
		p.got = append(p.got, *mf)
	}
}

func TestServer_Context_PersistsManifest(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	res := NewMockResolver(ctrl)
	bld := NewMockBuilder(ctrl)
	persister := &recordingPersister{}

	res.EXPECT().Resolve("query db", []string(nil)).Return([]string{"sql"}, nil)
	bld.EXPECT().Build(gomock.Any(), []string{"sql"}).Return(manifest.ExecutionManifest{
		ManifestID: "m-persist",
		Version:    manifest.ProtocolVersion,
	}, nil)

	srv := newServer(t, Config{Resolver: res, Builder: bld, Persister: persister})
	resp := postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{
		Intent: "query db", AgentID: "agent",
	}, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(persister.got) != 1 {
		t.Fatalf("persister got %d manifests, want 1", len(persister.got))
	}
	if persister.got[0].ManifestID != "m-persist" {
		t.Fatalf("persisted manifest_id = %q", persister.got[0].ManifestID)
	}
}

func TestServer_Context_NoPersisterIsOK(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	res := NewMockResolver(ctrl)
	bld := NewMockBuilder(ctrl)
	res.EXPECT().Resolve(gomock.Any(), gomock.Any()).Return([]string{"sql"}, nil)
	bld.EXPECT().Build(gomock.Any(), []string{"sql"}).Return(manifest.ExecutionManifest{ManifestID: "m"}, nil)

	srv := newServer(t, Config{Resolver: res, Builder: bld}) // no Persister
	resp := postJSON(t, srv.URL+"/v1/context", manifest.ContextRequest{
		Intent: "x", AgentID: "agent",
	}, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestServer_ProxyMounted(t *testing.T) {
	t.Parallel()

	// A trivial proxy that just records that it was hit.
	hit := false
	proxyHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	})

	srv := newServer(t, Config{Proxy: proxyHandler})
	resp, err := http.Get(srv.URL + "/v1/exec/m1/a1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !hit {
		t.Fatalf("proxy handler not invoked")
	}
}
