/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package proxy implements the ACP auth-injection proxy.
//
// The proxy fronts every action endpoint declared in an Execution Manifest.
// Agents call the proxy URL (acp://proxy/v1/exec/{manifest_id}/{action_id})
// instead of the upstream tool directly. The proxy:
//
//  1. Looks up the manifest and action.
//  2. Strips any incoming Authorization header so an agent cannot smuggle one.
//  3. Enforces the manifest's egress allow-list against the action endpoint.
//  4. Blocks actions in boundaries.require_approval until the configured
//     ApprovalGate records an approval.
//  5. Injects the credentials returned by the Injector for that action.
//  6. Forwards the request to the upstream via httputil.ReverseProxy.
//
// Credentials never enter the agent context window.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

//go:generate ../../bin/mockgen -source=proxy.go -destination=mocks_test.go -package=proxy

// ManifestStore exposes the subset of manifest lookups the proxy needs. It is
// declared in the proxy package so tests can mock it without depending on the
// builder or server packages.
type ManifestStore interface {
	Get(manifestID string) (*manifest.ExecutionManifest, bool)
}

// Injector returns the credential headers to inject for a given manifest /
// action pair. The returned http.Header is added to the upstream request and
// MUST NOT be logged.
type Injector interface {
	Inject(ctx context.Context, manifestID string, action manifest.Action) (http.Header, error)
}

// ApprovalGate decides whether an action that appears in
// boundaries.require_approval may execute. Non-approval actions never reach
// the gate.
type ApprovalGate interface {
	IsApproved(ctx context.Context, manifestID, actionID string) bool
}

// AlwaysApprove approves every action. Useful in tests and for development
// stacks; never use in production.
type AlwaysApprove struct{}

// IsApproved implements ApprovalGate by returning true.
func (AlwaysApprove) IsApproved(context.Context, string, string) bool { return true }

// AlwaysDeny blocks every gated action. Used as the safe default when no
// ApprovalGate is configured.
type AlwaysDeny struct{}

// IsApproved implements ApprovalGate by returning false.
func (AlwaysDeny) IsApproved(context.Context, string, string) bool { return false }

// MapInjector is a static, in-memory Injector indexed by (manifest_id, action_id)
// or fallback by action_id alone. Production deployments should plug in a
// secret-store backed implementation.
type MapInjector struct {
	// PerAction["manifest_id|action_id"] takes priority.
	PerAction map[string]http.Header
	// PerActionID["action_id"] is the fallback when no per-manifest entry exists.
	PerActionID map[string]http.Header
}

// Inject implements Injector.
func (m *MapInjector) Inject(_ context.Context, manifestID string, action manifest.Action) (http.Header, error) {
	if m == nil {
		return http.Header{}, nil
	}
	if h, ok := m.PerAction[manifestID+"|"+action.ID]; ok {
		return h.Clone(), nil
	}
	if h, ok := m.PerActionID[action.ID]; ok {
		return h.Clone(), nil
	}
	return http.Header{}, nil
}

// Config wires the proxy's collaborators.
type Config struct {
	Store    ManifestStore
	Injector Injector
	Approval ApprovalGate
	Logger   *slog.Logger
}

// Handler is the http.Handler that serves /v1/exec/{manifest_id}/{action_id}.
type Handler struct {
	cfg Config
}

// New constructs a Handler. A nil Approval gate defaults to AlwaysDeny so
// gated actions fail closed.
func New(cfg Config) *Handler {
	if cfg.Approval == nil {
		cfg.Approval = AlwaysDeny{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Handler{cfg: cfg}
}

// ExecPathPrefix is the URL prefix the proxy serves.
const ExecPathPrefix = "/v1/exec/"

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Store == nil || h.cfg.Injector == nil {
		writeError(w, http.StatusInternalServerError, "proxy not configured")
		return
	}
	if !strings.HasPrefix(r.URL.Path, ExecPathPrefix) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	manifestID, actionID, ok := splitExecPath(strings.TrimPrefix(r.URL.Path, ExecPathPrefix))
	if !ok {
		writeError(w, http.StatusBadRequest, "expected /v1/exec/{manifest_id}/{action_id}")
		return
	}

	mf, ok := h.cfg.Store.Get(manifestID)
	if !ok {
		writeError(w, http.StatusNotFound, "manifest not found or expired")
		return
	}

	action, ok := findAction(mf, actionID)
	if !ok {
		writeError(w, http.StatusNotFound, "action not found in manifest")
		return
	}

	if requiresApproval(mf, actionID) {
		if !h.cfg.Approval.IsApproved(r.Context(), manifestID, actionID) {
			w.Header().Set("X-ACP-Approval-Required", "true")
			writeError(w, http.StatusForbidden, "action requires approval")
			return
		}
	}

	upstream, err := url.Parse(action.Endpoint)
	if err != nil || upstream.Host == "" {
		writeError(w, http.StatusBadGateway, "invalid upstream endpoint")
		return
	}
	if !egressAllowed(mf, upstream.Host) {
		writeError(w, http.StatusForbidden, "upstream host not in egress allow-list")
		return
	}

	creds, err := h.cfg.Injector.Inject(r.Context(), manifestID, action)
	if err != nil {
		writeError(w, http.StatusBadGateway, "credential injection failed")
		return
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Defaults set X-Forwarded-* and clear hop-by-hop headers.
			pr.SetURL(upstream)
			pr.SetXForwarded()

			// Ensure the agent's Authorization never reaches the upstream.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Proxy-Authorization")

			// Inject the server-side credentials.
			for k, vs := range creds {
				for _, v := range vs {
					pr.Out.Header.Add(k, v)
				}
			}
			// Preserve the upstream path from the manifest endpoint.
			pr.Out.URL.Path = upstream.Path
			pr.Out.URL.RawPath = upstream.RawPath
			pr.Out.Host = upstream.Host
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, e error) {
			h.cfg.Logger.Warn("acp.proxy.upstream_error",
				slog.String("manifest_id", manifestID),
				slog.String("action_id", actionID),
				slog.String("err", e.Error()),
			)
			writeError(rw, http.StatusBadGateway, "upstream error")
		},
	}

	h.cfg.Logger.Info("acp.proxy.exec",
		slog.String("manifest_id", manifestID),
		slog.String("action_id", actionID),
		slog.String("upstream_host", upstream.Host),
	)
	rp.ServeHTTP(w, r)
}

// MemoryStore is an in-memory ManifestStore implementation used by the
// development server.
type MemoryStore struct {
	manifests map[string]*manifest.ExecutionManifest
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{manifests: map[string]*manifest.ExecutionManifest{}}
}

// Put stores a manifest.
func (s *MemoryStore) Put(mf *manifest.ExecutionManifest) {
	if mf == nil || mf.ManifestID == "" {
		return
	}
	s.manifests[mf.ManifestID] = mf
}

// Get implements ManifestStore.
func (s *MemoryStore) Get(id string) (*manifest.ExecutionManifest, bool) {
	mf, ok := s.manifests[id]
	return mf, ok
}

// errResponse mirrors manifest.ErrorResponse without importing JSON encoding
// machinery into this hot path beyond what is necessary.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func splitExecPath(rest string) (manifestID, actionID string, ok bool) {
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func findAction(mf *manifest.ExecutionManifest, id string) (manifest.Action, bool) {
	for _, a := range mf.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return manifest.Action{}, false
}

func requiresApproval(mf *manifest.ExecutionManifest, actionID string) bool {
	for _, id := range mf.Boundaries.RequireApproval {
		if id == actionID {
			return true
		}
	}
	return false
}

func egressAllowed(mf *manifest.ExecutionManifest, host string) bool {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	for _, allowed := range mf.Boundaries.Egress {
		if strings.EqualFold(allowed, host) {
			return true
		}
	}
	return false
}

// ErrUnconfigured is returned when New is called with required fields nil.
var ErrUnconfigured = errors.New("proxy: store and injector are required")
