package tool

import (
	"errors"
	"fmt"
	"testing"
)

// Rust #51355 (`c0c230e673`): `CodexErr::with_agent_context` attaches bounded
// diagnostics without replacing a more precise inner annotation
// (`agent_error.get_or_insert(context)`), and `CodexErr::agent_context` reads
// it back. The annotation must not change the error's kind or its
// user-visible message, which Rust's
// `spawn_failure_context_survives_manager_and_fork_errors`
// (core/src/agent/control_tests.rs) asserts for the preserved errors.
func TestAgentErrorContextPreservesErrorAndFirstAnnotationLikeRust(t *testing.T) {
	base := errors.New("agent runtime is unavailable")

	// An unannotated error reports no origin.
	if origin, ok := AgentErrorContextOf(base); ok {
		t.Fatalf("origin = %q, want none", origin)
	}

	annotated := WithAgentErrorContext(base, AgentErrorContextManagerUnavailable)
	origin, ok := AgentErrorContextOf(annotated)
	if !ok || origin != AgentErrorContextManagerUnavailable {
		t.Fatalf("origin = %q (%v), want manager_unavailable", origin, ok)
	}
	if annotated.Error() != base.Error() {
		t.Fatalf("message = %q, want the original %q", annotated.Error(), base.Error())
	}
	if !errors.Is(annotated, base) {
		t.Fatal("the annotated error no longer matches its original")
	}

	// The first annotation wins, so an outer stage cannot overwrite the more
	// precise origin an inner stage recorded.
	reannotated := WithAgentErrorContext(annotated, AgentErrorContextForkHistory)
	if origin, _ := AgentErrorContextOf(reannotated); origin != AgentErrorContextManagerUnavailable {
		t.Fatalf("origin after re-annotation = %q, want manager_unavailable", origin)
	}

	// The origin is readable through a wrapped chain, which is how the spawn
	// path reports it.
	wrapped := fmt.Errorf("collab spawn failed: %w", reannotated)
	if origin, ok := AgentErrorContextOf(wrapped); !ok || origin != AgentErrorContextManagerUnavailable {
		t.Fatalf("wrapped origin = %q (%v), want manager_unavailable", origin, ok)
	}

	// A nil error and an empty origin are returned unchanged.
	if got := WithAgentErrorContext(nil, AgentErrorContextChildStartup); got != nil {
		t.Fatalf("nil error = %v, want nil", got)
	}
	if got := WithAgentErrorContext(base, ""); got != base {
		t.Fatalf("empty origin = %v, want the original error", got)
	}
}

// The bounded vocabulary is the snake_case spelling Rust serializes, so a Go
// label and a Rust label name the same failure origin.
func TestAgentErrorContextSpellsRustLabelsLikeRust(t *testing.T) {
	want := map[AgentErrorContext]string{
		AgentErrorContextRegistryCapacity:    "registry_capacity",
		AgentErrorContextDuplicatePath:       "duplicate_path",
		AgentErrorContextNicknameUnavailable: "nickname_unavailable",
		AgentErrorContextManagerUnavailable:  "manager_unavailable",
		AgentErrorContextForkHistory:         "fork_history",
		AgentErrorContextChildStartup:        "child_startup",
		AgentErrorContextInputAdmission:      "input_admission",
	}
	for context, label := range want {
		if string(context) != label {
			t.Fatalf("context %q = %q, want %q", context, string(context), label)
		}
	}
}
