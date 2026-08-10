/*
Copyright 2026 Clawdlinux.
Licensed under the Business Source License 1.1.
*/

package observability_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Clawdlinux/agent-native-format/internal/observability"
)

func setupRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func attrMap(kvs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(kvs))
	for _, kv := range kvs {
		out[string(kv.Key)] = kv.Value
	}
	return out
}

func TestStartContextSpan_RecordsIntentHash(t *testing.T) {
	rec, restore := setupRecorder(t)
	defer restore()

	_, span := observability.StartContextSpan(context.Background(), "send slack notification")
	span.End()

	got := rec.Ended()[0]
	if got.Name() != observability.SpanContextHandler {
		t.Errorf("name=%q", got.Name())
	}
	a := attrMap(got.Attributes())
	if a["clawd.acp.intent"].AsString() != "send slack notification" {
		t.Errorf("intent=%q", a["clawd.acp.intent"].AsString())
	}
	if len(a["clawd.acp.intent_hash"].AsString()) != 16 {
		t.Errorf("intent_hash len=%d, want 16", len(a["clawd.acp.intent_hash"].AsString()))
	}
}

func TestStartContextSpan_TruncatesLongIntent(t *testing.T) {
	rec, restore := setupRecorder(t)
	defer restore()

	long := make([]byte, 1024)
	for i := range long {
		long[i] = 'x'
	}
	_, span := observability.StartContextSpan(context.Background(), string(long))
	span.End()

	got := rec.Ended()[0]
	a := attrMap(got.Attributes())
	intent := a["clawd.acp.intent"].AsString()
	// 256 chars + "…" (3 bytes UTF-8) = 259 bytes
	if len([]rune(intent)) != 257 {
		t.Errorf("intent rune count=%d, want 257", len([]rune(intent)))
	}
}

func TestStartExecSpan_RecordsToolAndManifest(t *testing.T) {
	rec, restore := setupRecorder(t)
	defer restore()

	_, span := observability.StartExecSpan(context.Background(), "mf-1", "act-1", "slack.send")
	span.End()

	got := rec.Ended()[0]
	a := attrMap(got.Attributes())
	if a["gen_ai.operation.name"].AsString() != "execute_tool" {
		t.Error("operation.name")
	}
	if a["gen_ai.tool.name"].AsString() != "slack.send" {
		t.Error("tool.name")
	}
	if a["clawd.acp.manifest_id"].AsString() != "mf-1" {
		t.Error("manifest_id")
	}
	if a["clawd.acp.action_id"].AsString() != "act-1" {
		t.Error("action_id")
	}
}

func TestSetManifestEmitted_RecordsCounts(t *testing.T) {
	rec, restore := setupRecorder(t)
	defer restore()

	_, span := observability.StartContextSpan(context.Background(), "x")
	observability.SetManifestEmitted(span, "mf-2", 4, true)
	span.End()

	got := rec.Ended()[0]
	a := attrMap(got.Attributes())
	if a["clawd.acp.manifest_id"].AsString() != "mf-2" {
		t.Error("manifest_id")
	}
	if a["clawd.acp.action_count"].AsInt64() != 4 {
		t.Errorf("action_count=%d", a["clawd.acp.action_count"].AsInt64())
	}
	if !a["clawd.acp.cache_hit"].AsBool() {
		t.Error("cache_hit")
	}
}

func TestRecordError_SetsStatusError(t *testing.T) {
	rec, restore := setupRecorder(t)
	defer restore()

	_, span := observability.StartContextSpan(context.Background(), "x")
	observability.RecordError(span, errors.New("boom"))
	span.End()

	got := rec.Ended()[0]
	if got.Status().Code.String() != "Error" {
		t.Errorf("status=%v", got.Status().Code)
	}
}

func TestInit_DisabledNoOp(t *testing.T) {
	shutdown, err := observability.Init(context.Background(), observability.Config{Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInit_NoEndpointReturnsNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := observability.Init(context.Background(), observability.Config{ServiceName: "acp"})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(context.Background())
	if otel.GetTextMapPropagator() == nil {
		t.Fatal("propagator not set")
	}
}

func TestLinkAuditEntry_SetsSeq(t *testing.T) {
	rec, restore := setupRecorder(t)
	defer restore()

	_, span := observability.StartContextSpan(context.Background(), "x")
	observability.LinkAuditEntry(span, 7, "tr-1")
	span.End()

	got := rec.Ended()[0]
	a := attrMap(got.Attributes())
	if a["clawd.audit.seq"].AsInt64() != 7 {
		t.Errorf("seq=%d", a["clawd.audit.seq"].AsInt64())
	}
	if a["clawd.audit.trace_id"].AsString() != "tr-1" {
		t.Errorf("trace_id=%q", a["clawd.audit.trace_id"].AsString())
	}
}
