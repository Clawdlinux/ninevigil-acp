/*
Copyright 2026 Clawdlinux.
Licensed under the Business Source License 1.1.
*/

// Package observability bootstraps OpenTelemetry for the ACP server and
// exposes helpers that emit spans aligned with the OpenTelemetry GenAI
// semantic conventions (stable, January 2026), plus the clawd.* extension
// namespace consumed by the Clawdlinux observability stack.
//
// ACP-specific span names:
//
//   - acp.context  — the /v1/context handler (intent resolution + manifest build)
//   - acp.exec     — a single proxied tool invocation through /v1/exec/
//
// Both attach gen_ai.tool.name, clawd.acp.manifest_id and clawd.acp.action_id
// where applicable so they correlate with downstream gen_ai.execute_tool spans
// emitted by the agent that consumes the manifest.
package observability

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	TracerName = "github.com/Clawdlinux/agent-native-format/internal/observability"
	SchemaURL  = "https://opentelemetry.io/schemas/genai/1.30.0"
)

// Span names
const (
	SpanContextHandler = "acp.context"
	SpanExecAction     = "acp.exec"
)

// Attribute keys (mirror agentic-operator pkg/otel/genai/semconv.go).
const (
	AttrSystem        = attribute.Key("gen_ai.system")
	AttrOperationName = attribute.Key("gen_ai.operation.name")
	AttrToolName      = attribute.Key("gen_ai.tool.name")
	AttrToolType      = attribute.Key("gen_ai.tool.type")

	AttrCACPManifestID = attribute.Key("clawd.acp.manifest_id")
	AttrCACPActionID   = attribute.Key("clawd.acp.action_id")
	AttrCACPIntent     = attribute.Key("clawd.acp.intent")
	AttrCACPIntentHash = attribute.Key("clawd.acp.intent_hash")
	AttrCACPActions    = attribute.Key("clawd.acp.action_count")
	AttrCACPCacheHit   = attribute.Key("clawd.acp.cache_hit")
	AttrCAuditSeq      = attribute.Key("clawd.audit.seq")
	AttrCAuditTraceID  = attribute.Key("clawd.audit.trace_id")
)

// Config configures the OTel TracerProvider for the ACP server.
type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	OTLPEndpoint   string
	Insecure       bool
	SamplerRatio   float64
	Disabled       bool
}

// Init installs the global TracerProvider. When OTLP endpoint is unset and
// no env override is present, Init is a no-op (returns a no-op shutdown).
func Init(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if cfg.Disabled {
		return func(context.Context) error { return nil }, nil
	}
	endpoint := cfg.OTLPEndpoint
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{},
		))
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(coalesce(cfg.ServiceName, "acp-server")),
		semconv.ServiceVersion(coalesce(cfg.ServiceVersion, "0.0.0-dev")),
		attribute.String("clawd.component", "acp-server"),
		attribute.String("deployment.environment", coalesce(cfg.Environment, "dev")),
	))
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithCompressor("gzip"),
		otlptracegrpc.WithTimeout(10 * time.Second),
	}
	if cfg.Insecure || isLocalEndpoint(endpoint) {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("observability: OTLP exporter: %w", err)
	}

	sampler := sdktrace.AlwaysSample()
	if cfg.SamplerRatio > 0 && cfg.SamplerRatio < 1 {
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplerRatio))
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	var once sync.Once
	return func(c context.Context) error {
		var sErr error
		once.Do(func() { sErr = tp.Shutdown(c) })
		return sErr
	}, nil
}

// Tracer returns the package tracer.
func Tracer() trace.Tracer {
	return otel.GetTracerProvider().Tracer(TracerName, trace.WithSchemaURL(SchemaURL))
}

// StartContextSpan opens the root span for /v1/context handling. Returns
// the new ctx and span; caller MUST End() the span.
func StartContextSpan(ctx context.Context, intent string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, SpanContextHandler,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			AttrCACPIntent.String(truncate(intent, 256)),
			AttrCACPIntentHash.String(intentHash(intent)),
		),
	)
}

// StartExecSpan opens a span for a single proxied tool execution.
func StartExecSpan(ctx context.Context, manifestID, actionID, toolName string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, SpanExecAction,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			AttrOperationName.String("execute_tool"),
			AttrToolType.String("acp"),
			AttrToolName.String(toolName),
			AttrCACPManifestID.String(manifestID),
			AttrCACPActionID.String(actionID),
		),
	)
}

// SetManifestEmitted records the manifest id and action count on the
// /v1/context root span after Build returns.
func SetManifestEmitted(span trace.Span, manifestID string, actionCount int, cacheHit bool) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(
		AttrCACPManifestID.String(manifestID),
		AttrCACPActions.Int(actionCount),
		AttrCACPCacheHit.Bool(cacheHit),
	)
}

// LinkAuditEntry records the tamper-evident audit log sequence on a span.
func LinkAuditEntry(span trace.Span, seq uint64, auditTraceID string) {
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(
		AttrCAuditSeq.Int64(int64(seq)),
	)
	if auditTraceID != "" {
		span.SetAttributes(AttrCAuditTraceID.String(auditTraceID))
	}
}

// RecordError marks a span as failed.
func RecordError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// --- helpers ---

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func isLocalEndpoint(ep string) bool {
	return ep == "localhost:4317" || ep == "127.0.0.1:4317" ||
		ep == "0.0.0.0:4317" || ep == "otel-collector:4317" ||
		ep == "opentelemetry-collector:4317"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// intentHash produces a short, stable, non-cryptographic fingerprint of an
// intent string. Used to bucket distinct intents in the audit log without
// retaining the raw text. We use a 64-bit FNV-style mix here to avoid
// pulling crypto/sha256 just for telemetry.
func intentHash(s string) string {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		out[15-i] = hex[h&0xF]
		h >>= 4
	}
	return string(out)
}
