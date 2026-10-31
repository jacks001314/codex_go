package network

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

func TestProxyPolicyAuditUsesLaunchProcessAttributionLikeRust(t *testing.T) {
	var mu sync.Mutex
	var records []map[string]any
	previous := slog.Default()
	slog.SetDefault(slog.New(proxyAuditCaptureHandler(func(record slog.Record) {
		attrs := map[string]any{}
		record.Attrs(func(attr slog.Attr) bool {
			attrs[attr.Key] = attr.Value.Any()
			return true
		})
		mu.Lock()
		records = append(records, attrs)
		mu.Unlock()
	})))
	defer slog.SetDefault(previous)

	metadata := ProxyAuditMetadata{
		ConversationID: "conversation-1",
		AppVersion:     "1.2.3",
		ProcessLog: ProxyProcessLogMetadata{
			ThreadID:               "process-thread-1",
			ToolCallID:             "call-1",
			ExecutorEnvironmentID:  "environment-1",
			ExecutorRegistrationID: "registration-1",
			LaunchTraceID:          "trace-1",
			LaunchSpanID:           "span-1",
		},
	}
	event := ProxyPolicyAuditEvent{
		Request: ProxyPolicyRequest{
			Protocol:    ProxyProtocolHTTP,
			Host:        "example.com",
			Port:        443,
			ExecutionID: "execution-1",
			Method:      "GET",
		},
		Timestamp: "2026-10-31T10:00:00.000Z",
		Scope:     "domain",
		Decision:  "allow",
		Source:    ProxyDecisionSourceBaselinePolicy,
		Reason:    "allow",
		Metadata:  metadata,
	}
	logProxyPolicyAudit(event)
	metadata.ProcessLog.ThreadID = ""
	event.Metadata = metadata
	logProxyPolicyAudit(event)

	mu.Lock()
	defer mu.Unlock()
	if len(records) != 2 {
		t.Fatalf("audit records = %#v, want two records", records)
	}
	first := records[0]
	if first["target"] != "codex_otel.network_proxy" || first["event.name"] != "codex.network_proxy.policy_decision" {
		t.Fatalf("audit target = %#v", first)
	}
	if first["conversation.id"] != "process-thread-1" || first["tool.call_id"] != "call-1" {
		t.Fatalf("process attribution = %#v", first)
	}
	if first["executor.environment_id"] != "environment-1" || first["executor.registration_id"] != "registration-1" {
		t.Fatalf("executor attribution = %#v", first)
	}
	if first["launch.trace_id"] != "trace-1" || first["launch.span_id"] != "span-1" || first["execution.id"] != "execution-1" {
		t.Fatalf("launch attribution = %#v", first)
	}
	if records[1]["conversation.id"] != "conversation-1" {
		t.Fatalf("conversation fallback = %#v", records[1])
	}
}

type proxyAuditCaptureHandler func(record slog.Record)

func (f proxyAuditCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (f proxyAuditCaptureHandler) Handle(_ context.Context, record slog.Record) error {
	f(record)
	return nil
}
func (f proxyAuditCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return f }
func (f proxyAuditCaptureHandler) WithGroup(string) slog.Handler      { return f }
