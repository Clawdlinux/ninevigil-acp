/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package server hosts the ACP HTTP surface: /v1/context, /v1/feedback,
// /healthz. The handlers depend only on small, consumer-defined interfaces so
// that the registry, resolver, and builder can be replaced or mocked freely.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Clawdlinux/agent-native-format/internal/observability"
	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

//go:generate ../../bin/mockgen -source=server.go -destination=mocks_test.go -package=server

// Resolver maps intent + hints to capability tags.
type Resolver interface {
	Resolve(intent string, hints []string) ([]string, error)
}

// Builder turns capabilities into an ExecutionManifest.
type Builder interface {
	Build(req manifest.ContextRequest, capabilities []string) (manifest.ExecutionManifest, error)
}

// FeedbackSink consumes /v1/feedback events.
type FeedbackSink interface {
	Record(event manifest.FeedbackEvent) error
}

// LoggingFeedbackSink writes feedback events to the configured logger.
type LoggingFeedbackSink struct {
	Logger *slog.Logger
	count  atomic.Int64
}

// Record logs the event and increments the in-memory counter.
func (s *LoggingFeedbackSink) Record(event manifest.FeedbackEvent) error {
	s.count.Add(1)
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("acp.feedback",
		slog.String("manifest_id", event.ManifestID),
		slog.String("action_id", event.ActionID),
		slog.String("outcome", string(event.Outcome)),
		slog.Int64("latency_ms", event.LatencyMS),
	)
	return nil
}

// Count returns the number of feedback events recorded by this sink.
func (s *LoggingFeedbackSink) Count() int64 { return s.count.Load() }

// Config wires the server's collaborators.
type Config struct {
	Resolver  Resolver
	Builder   Builder
	Feedback  FeedbackSink
	Persister ManifestPersister // optional: store every emitted manifest (e.g. for the proxy)
	Proxy     http.Handler      // optional: mounted at /v1/exec/
	AuthToken string            // optional; empty disables auth
	Logger    *slog.Logger
}

// ManifestPersister is an optional hook the server calls after every
// successful Build. Useful for the proxy to look up actions by manifest_id.
type ManifestPersister interface {
	Put(mf *manifest.ExecutionManifest)
}

// Server is an http.Handler hosting the ACP endpoints.
type Server struct {
	cfg Config
	mux *http.ServeMux
}

// New constructs a Server. The returned value is an http.Handler.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux()}

	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.Handle("/v1/context", s.requireAuth(http.HandlerFunc(s.handleContext)))
	s.mux.Handle("/v1/feedback", s.requireAuth(http.HandlerFunc(s.handleFeedback)))
	if cfg.Proxy != nil {
		s.mux.Handle("/v1/exec/", s.requireAuth(cfg.Proxy))
	}
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req manifest.ContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Intent) == "" && len(req.Capabilities) == 0 {
		writeError(w, http.StatusBadRequest, "intent or capabilities required")
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}

	ctx, span := observability.StartContextSpan(r.Context(), req.Intent)
	defer span.End()

	caps, err := s.cfg.Resolver.Resolve(req.Intent, req.Capabilities)
	if err != nil {
		observability.RecordError(span, err)
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	out, err := s.cfg.Builder.Build(req, caps)
	if err != nil {
		observability.RecordError(span, err)
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if s.cfg.Persister != nil {
		s.cfg.Persister.Put(&out)
	}

	observability.SetManifestEmitted(span, out.ManifestID, len(out.Actions), false)

	s.cfg.Logger.Info("acp.context",
		slog.String("agent_id", req.AgentID),
		slog.String("manifest_id", out.ManifestID),
		slog.Int("actions", len(out.Actions)),
	)
	_ = ctx
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var event manifest.FeedbackEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(event.ManifestID) == "" {
		writeError(w, http.StatusBadRequest, "manifest_id is required")
		return
	}
	if strings.TrimSpace(event.ActionID) == "" {
		writeError(w, http.StatusBadRequest, "action_id is required")
		return
	}

	if s.cfg.Feedback != nil {
		if err := s.cfg.Feedback.Record(event); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		const prefix = "Bearer "
		if !strings.HasPrefix(got, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(got, prefix))
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AuthToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CryptoIDSource mints short manifest IDs of the form "m-<16 hex chars>".
type CryptoIDSource struct{}

// NewID returns a fresh manifest ID. Falls back to a timestamp-derived ID if
// crypto/rand is unavailable, which should never happen on supported OSes.
func (CryptoIDSource) NewID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "m-" + hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000")))
	}
	return "m-" + hex.EncodeToString(buf[:])
}

// ListenAndServe runs the server until ctx is canceled, then performs a
// graceful shutdown.
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, manifest.ErrorResponse{Error: msg})
}
