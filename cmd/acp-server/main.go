/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Command acp-server runs the Week 1 ACP HTTP server with an in-memory
// registry, the keyword resolver, and the manifest builder. Week 2 adds the
// auth-injection proxy mounted at /v1/exec/.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	builder "github.com/Clawdlinux/agent-native-format/internal/builder"
	"github.com/Clawdlinux/agent-native-format/internal/observability"
	"github.com/Clawdlinux/agent-native-format/internal/proxy"
	"github.com/Clawdlinux/agent-native-format/internal/registry"
	"github.com/Clawdlinux/agent-native-format/internal/resolver"
	"github.com/Clawdlinux/agent-native-format/internal/server"
)

// acpVersion is the version string set at build time via -ldflags.
var acpVersion = "0.1.0-dev"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	authToken := flag.String("auth-token", os.Getenv("ACP_AUTH_TOKEN"),
		"bearer token required on /v1/* (default: ACP_AUTH_TOKEN env var; empty disables auth)")
	feedbackEndpoint := flag.String("feedback-endpoint", "/v1/feedback",
		"feedback endpoint advertised in manifests")
	enableProxy := flag.Bool("enable-proxy", true, "mount the auth-injection proxy at /v1/exec/")
	autoApprove := flag.Bool("auto-approve", false, "approve every gated action (DEV ONLY)")
	resolverKind := flag.String("resolver", "keyword",
		"intent resolver: 'keyword' (deterministic substring match, default) or 'embedding' (hash TF-IDF, generalizes beyond literal keywords; falls back to keyword)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Bootstrap OpenTelemetry. The endpoint is read from
	// OTEL_EXPORTER_OTLP_ENDPOINT (env). When unset, Init is a no-op
	// (air-gapped lite deployments without a collector).
	otelCtx, otelCancel := context.WithTimeout(context.Background(), 10*time.Second)
	otelShutdown, err := observability.Init(otelCtx, observability.Config{
		ServiceName:    "acp-server",
		ServiceVersion: acpVersion,
		Environment:    os.Getenv("CLAWD_DEPLOYMENT_ENV"),
	})
	otelCancel()
	if err != nil {
		logger.Warn("acp.observability.init_failed", slog.String("err", err.Error()))
		otelShutdown = func(context.Context) error { return nil }
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = otelShutdown(shutdownCtx)
	}()

	reg := registry.NewMemoryRegistry()
	if err := registry.Seed(reg); err != nil {
		logger.Error("registry seed failed", slog.String("err", err.Error()))
		os.Exit(1)
	}

	bld := builder.New(
		reg,
		server.CryptoIDSource{},
		builder.Options{
			TTL:                "300s",
			FeedbackEndpoint:   *feedbackEndpoint,
			MaxTokensPerAction: 15000,
		},
	)

	cfg := server.Config{
		Resolver:  pickResolver(*resolverKind, logger),
		Builder:   bld,
		Feedback:  &server.LoggingFeedbackSink{Logger: logger},
		AuthToken: *authToken,
		Logger:    logger,
	}

	if *enableProxy {
		store := proxy.NewMemoryStore()
		var gate proxy.ApprovalGate = proxy.AlwaysDeny{}
		if *autoApprove {
			gate = proxy.AlwaysApprove{}
		}
		cfg.Persister = store
		cfg.Proxy = proxy.New(proxy.Config{
			Store:    store,
			Injector: &proxy.MapInjector{}, // placeholder; production wires to a vault
			Approval: gate,
			Logger:   logger,
		})
	}

	handler := server.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("acp-server starting",
		slog.String("addr", *addr),
		slog.Bool("auth_required", *authToken != ""),
		slog.Bool("proxy_enabled", *enableProxy),
		slog.Bool("auto_approve", *autoApprove),
		slog.String("resolver", *resolverKind),
		slog.Int("seeded_tools", len(reg.All())),
	)

	if err := server.ListenAndServe(ctx, *addr, handler); err != nil {
		fmt.Fprintln(os.Stderr, "acp-server:", err)
		os.Exit(1)
	}
	logger.Info("acp-server stopped")
}

// pickResolver returns the configured intent resolver. Unknown values
// log a warning and fall back to the keyword resolver so a typo in the
// flag never takes the server offline.
func pickResolver(kind string, logger *slog.Logger) resolver.Resolver {
	switch kind {
	case "embedding":
		return resolver.NewEmbeddingResolver(resolver.DefaultExamples(), resolver.EmbeddingOptions{})
	case "", "keyword":
		return resolver.NewKeywordResolver(nil)
	default:
		logger.Warn("acp-server unknown resolver, falling back to keyword",
			slog.String("requested", kind))
		return resolver.NewKeywordResolver(nil)
	}
}
