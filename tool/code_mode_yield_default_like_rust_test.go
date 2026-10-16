package tool

import (
	"strings"
	"testing"
	"time"
)

// Rust carries two distinct yield-time defaults and Go mirrors both:
//
//  1. The protocol-layer DEFAULT_EXEC_YIELD_TIME_MS = 10_000
//     (codex-rs/code-mode-protocol/src/runtime.rs:15). The code-mode runtime
//     service uses it as the `unwrap_or` fallback when the wire request omits
//     `yield_time_ms` (code-mode-runtime/src/service.rs:71,
//     code-mode/src/grpc_session/operations.rs:123). Go mirrors it in
//     codemode.ProtocolDefaultExecYieldTimeMS (codemode/protocol_types.go:13),
//     which also has value 10000.
//
//  2. The config-layer DEFAULT_CODE_MODE_EXEC_YIELD_TIME_MS = 30_000
//     (codex-rs/core/src/config/mod.rs:1204), which is
//     `CodeModeConfig::default_exec_yield_time_ms` (mod.rs:1223) and the value
//     Rust substitutes into the `exec` description
//     (core/src/tools/spec_plan.rs:1078 ->
//     code-mode-protocol/src/description.rs:31).
//
// CodeModeDefaultExecYieldTime is the second one, so the rendered description
// must say 30000 ms by default while the protocol-layer constant stays 10000.
func TestCodeModeExecDefaultYieldTimeMatchesRustLikeRust(t *testing.T) {
	if CodeModeDefaultExecYieldTime != 30*time.Second {
		t.Fatalf("CodeModeDefaultExecYieldTime = %v, want Rust's config default 30s (core/src/config/mod.rs:1204)", CodeModeDefaultExecYieldTime)
	}
	runtime := NewCodeModeRuntime(nil, false)
	if got := runtime.exec.defaultExecYieldTimeMS(); got != 30000 {
		t.Fatalf("default exec yield = %d ms, want 30000 (Rust DEFAULT_CODE_MODE_EXEC_YIELD_TIME_MS)", got)
	}
	executor, _ := runtime.Executors(NewRegistry())
	if description := executor.Spec().Description; !strings.Contains(description, "Defaults to 30000 ms.") {
		t.Fatalf("exec description does not carry the config default: %s", description)
	}
}
