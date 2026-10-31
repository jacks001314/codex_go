package execserver

import (
	"context"
	"log/slog"
	"strings"
	"unicode"

	"codex_go/network"
	"codex_go/sandbox"
)

const (
	maxProcessTelemetryIDBytes    = 256
	maxProcessTelemetryTraceBytes = 64
)

// executorRegistrationIdentity is registry-issued identity captured from the
// executor's authenticated connection. It is telemetry attribution only and
// never authorizes a process or network request.
type executorRegistrationIdentity struct {
	environmentID  string
	registrationID string
}

func newExecutorRegistrationIdentity(environmentID string, registrationID string) *executorRegistrationIdentity {
	if !validExecutorTelemetryID(environmentID) || !validExecutorTelemetryID(registrationID) {
		return nil
	}
	return &executorRegistrationIdentity{environmentID: environmentID, registrationID: registrationID}
}

func validExecutorTelemetryID(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > maxProcessTelemetryIDBytes {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

// processTelemetry captures launch identity once. Reconnects and later process
// operations must not replace it (Rust #42373).
type processTelemetry struct {
	launchTraceID          string
	launchSpanID           string
	threadID               string
	toolCallID             string
	executorEnvironmentID  string
	executorRegistrationID string
}

func newProcessTelemetry(ctx context.Context, params *ExecParams, identity *executorRegistrationIdentity) processTelemetry {
	trace := traceContextFromContext(ctx)
	telemetry := processTelemetry{
		launchTraceID: boundedTelemetryTraceID(trace.TraceID),
		launchSpanID:  boundedTelemetryTraceID(trace.SpanID),
	}
	if params != nil && params.Metadata != nil {
		telemetry.threadID = boundedTelemetryThreadID(params.Metadata.ThreadID)
		telemetry.toolCallID = boundedTelemetryToolCallID(params.Metadata.ToolCallID)
	}
	if identity != nil && validExecutorTelemetryID(identity.environmentID) && validExecutorTelemetryID(identity.registrationID) {
		telemetry.executorEnvironmentID = identity.environmentID
		telemetry.executorRegistrationID = identity.registrationID
	}
	return telemetry
}

func boundedTelemetryTraceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxProcessTelemetryTraceBytes || strings.ContainsFunc(value, unicode.IsControl) {
		return ""
	}
	return value
}

func boundedTelemetryThreadID(value *string) string {
	if value == nil {
		return ""
	}
	threadID := *value
	if len(threadID) > maxProcessTelemetryIDBytes || strings.ContainsFunc(threadID, unicode.IsControl) {
		return ""
	}
	return threadID
}

// boundedTelemetryToolCallID mirrors Rust's controller-supplied correlation
// bound: non-empty, at most 256 bytes, and only ASCII alphanumerics or `_-.:`.
func boundedTelemetryToolCallID(value *string) string {
	if value == nil {
		return ""
	}
	callID := *value
	if callID == "" || len(callID) > maxProcessTelemetryIDBytes {
		return ""
	}
	for index := 0; index < len(callID); index++ {
		character := callID[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '_', '-', '.', ':':
			continue
		default:
			return ""
		}
	}
	return callID
}

func (t processTelemetry) networkProcessLogMetadata() network.ProxyProcessLogMetadata {
	return network.ProxyProcessLogMetadata{
		ThreadID:               t.threadID,
		ToolCallID:             t.toolCallID,
		ExecutorEnvironmentID:  t.executorEnvironmentID,
		ExecutorRegistrationID: t.executorRegistrationID,
		LaunchTraceID:          t.launchTraceID,
		LaunchSpanID:           t.launchSpanID,
	}
}

func (t processTelemetry) log(eventName string, sandboxType sandbox.SandboxType, exitCode *int, terminationRequested *bool, reason string) {
	attributes := []any{
		"target", "codex_otel.log_only",
		"event.name", eventName,
		"launch.trace_id", t.launchTraceID,
		"launch.span_id", t.launchSpanID,
		"conversation.id", t.threadID,
		"tool.call_id", t.toolCallID,
		"executor.environment_id", t.executorEnvironmentID,
		"executor.registration_id", t.executorRegistrationID,
		"sandbox.type", sandboxTelemetryName(sandboxType),
	}
	if exitCode != nil {
		attributes = append(attributes, "process.exit_code", *exitCode)
	}
	if terminationRequested != nil {
		attributes = append(attributes, "process.termination_requested", *terminationRequested)
	}
	if reason != "" {
		attributes = append(attributes, "reason", reason)
	}
	slog.Info(eventName, attributes...)
}

func (t processTelemetry) start(sandboxType sandbox.SandboxType) {
	t.log("codex.exec_server.process_start", sandboxType, nil, nil, "")
}

func (t processTelemetry) spawnFailed(sandboxType sandbox.SandboxType) {
	t.log("codex.exec_server.process_spawn_failed", sandboxType, nil, nil, "")
}

func (t processTelemetry) sandboxDenied(sandboxType sandbox.SandboxType) {
	t.log("codex.exec_server.sandbox_denied", sandboxType, nil, nil, "inferred_denial")
}

func (t processTelemetry) exit(sandboxType sandbox.SandboxType, exitCode int, terminationRequested bool) {
	t.log("codex.exec_server.process_exit", sandboxType, &exitCode, &terminationRequested, "")
}

// sandboxTelemetryName uses Rust's Debug spellings for sandbox.type.
func sandboxTelemetryName(sandboxType sandbox.SandboxType) string {
	switch sandboxType {
	case sandbox.SandboxTypeMacosSeatbelt:
		return "MacosSeatbelt"
	case sandbox.SandboxTypeLinuxSeccomp:
		return "LinuxSeccomp"
	case sandbox.SandboxTypeWindowsRestrictedToken:
		return "WindowsRestrictedToken"
	case sandbox.SandboxTypeWindowsMxc:
		return "WindowsMxc"
	default:
		return "None"
	}
}

// setExecutorRegistrationIdentity records the registration issued by the
// environment registry for this executor process.
func (s *Server) SetExecutorRegistrationIdentity(environmentID string, registrationID string) {
	if s == nil {
		return
	}
	identity := newExecutorRegistrationIdentity(environmentID, registrationID)
	s.registrationMu.Lock()
	s.executorRegistration = identity
	s.registrationMu.Unlock()
}

func (s *Server) executorRegistrationIdentityValue() *executorRegistrationIdentity {
	if s == nil {
		return nil
	}
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	return s.executorRegistration
}
