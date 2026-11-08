package model

import (
	"context"
	"errors"
	"testing"
)

// TestRuntimeAuthDenialIsNotRecoverableAuthErrorLikeRust pins Rust #60096's
// provider predicate directly: an end-to-end test cannot tell a correct
// predicate from "recovery happened not to run".
func TestRuntimeAuthDenialIsNotRecoverableAuthErrorLikeRust(t *testing.T) {
	// A request without runtime credentials keeps ordinary 401 recovery even
	// when the body carries a runtime denial code.
	plain := &ResponsesAgentRunner{}
	if !plain.isRecoverableAuthError(401, `{"error":{"code":"runtime_instance_revoked"}}`, "") {
		t.Fatal("a request without runtime auth must keep ordinary credential recovery")
	}

	runtimeRunner := &ResponsesAgentRunner{RuntimeAuthActive: true}
	for _, code := range []string{
		"runtime_assertion_invalid",
		"runtime_instance_paused",
		"runtime_instance_revoked",
		"invalid_agent_instance_assertion",
	} {
		if runtimeRunner.isRecoverableAuthError(401, `{"error":{"code":"`+code+`"}}`, "") {
			t.Fatalf("body denial %q is recoverable, want terminal", code)
		}
		if runtimeRunner.isRecoverableAuthError(401, "", code) {
			t.Fatalf("identity error code %q is recoverable, want terminal", code)
		}
	}

	// Ordinary credential expiry and unrelated codes stay recoverable.
	if !runtimeRunner.isRecoverableAuthError(401, `{"error":{"code":"invalid_api_key"}}`, "") {
		t.Fatal("an ordinary 401 must stay recoverable under runtime auth")
	}
	if !runtimeRunner.isRecoverableAuthError(401, "not json", "") {
		t.Fatal("an unparseable 401 body must stay recoverable")
	}
	// Only 401 is recoverable, and the denial itself needs 401 or 403.
	if runtimeRunner.isRecoverableAuthError(403, "", "") {
		t.Fatal("a 403 is not a recoverable auth error")
	}
	if isRuntimeAuthDenial(500, []byte(`{"error":{"code":"runtime_instance_paused"}}`), "") {
		t.Fatal("a 500 is not a runtime auth denial")
	}
	if !isRuntimeAuthDenial(403, []byte(`{"error":{"code":"runtime_instance_paused"}}`), "") {
		t.Fatal("403 denials are recognized, they are just not recoverable")
	}
	// Callers that recorded no status keep the previous behavior.
	if !runtimeRunner.isRecoverableAuthError(0, "", "") {
		t.Fatal("an unset status must not change recovery behavior")
	}
}

// TestTerminalRuntimeDenialsSurfaceSignInLikeRust pins the recovery entry point:
// a terminal runtime denial returns the sign-in error instead of running a
// recovery step, while an ordinary 401 still enters the plan.
func TestTerminalRuntimeDenialsSurfaceSignInLikeRust(t *testing.T) {
	ctx := context.Background()
	runner := &ResponsesAgentRunner{RuntimeAuthActive: true}

	denied := authRecoveryDebug{StatusCode: 401, ResponseBody: `{"error":{"code":"runtime_instance_revoked"}}`}
	mode, step, err := runner.recoverUnauthorizedAuth(ctx, &authRecoveryState{}, denied)
	if !errors.Is(err, errTerminalRuntimeAuthDenial) {
		t.Fatalf("terminal denial error = %v, want the sign-in error", err)
	}
	if mode != "" || step != "" {
		t.Fatalf("terminal denial reported mode %q step %q, want no recovery step", mode, step)
	}

	// The identity error header alone is enough to stop recovery.
	headerDenied := authRecoveryDebug{StatusCode: 401, AuthErrorCode: "runtime_assertion_invalid"}
	if _, _, err := runner.recoverUnauthorizedAuth(ctx, &authRecoveryState{}, headerDenied); !errors.Is(err, errTerminalRuntimeAuthDenial) {
		t.Fatalf("header denial error = %v, want the sign-in error", err)
	}

	// An ordinary 401 keeps the existing path: it tries the recovery plan.
	ordinary := authRecoveryDebug{StatusCode: 401, ResponseBody: `{"error":{"code":"invalid_api_key"}}`}
	if _, _, err := runner.recoverUnauthorizedAuth(ctx, &authRecoveryState{}, ordinary); errors.Is(err, errTerminalRuntimeAuthDenial) {
		t.Fatal("an ordinary 401 must not take the terminal-denial path")
	}
}
