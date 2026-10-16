package appserver

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"codex_go/session"
)

// TestThreadStoreMutationErrorPolicyLikeRust mirrors Rust #54664's
// thread_processor_tests::storage_failure_classification_does_not_change_mutation_error_policy:
// adding the retryable / unknown-write-outcome categories must not change the
// mutation error policy. Conflict still reaches the client as an invalid
// request, while the new categories - like any other unclassified store
// failure - stay internal errors that keep the operation context.
func TestThreadStoreMutationErrorPolicyLikeRust(t *testing.T) {
	conflict := fmt.Errorf("%w: writer already active", session.ErrConflict)
	mapped := threadStoreMutationError("archive", conflict)
	if !errors.Is(mapped, ErrJSONRPCInvalidRequest) {
		t.Fatalf("conflict mapped to %v, want an invalid request", mapped)
	}
	if mapped.Error() != "writer already active" {
		t.Fatalf("conflict message = %q, want the store detail only", mapped.Error())
	}
	if code := errorCode(mapped); code != JSONRPCInvalidRequestErrorCode {
		t.Fatalf("conflict code = %d, want %d", code, JSONRPCInvalidRequestErrorCode)
	}

	internal := fmt.Errorf("retryable conflict; outcome unknown")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"internal", internal},
		{"retryable", fmt.Errorf("%w: store temporarily unavailable", session.ErrRetryable)},
		{"outcome unknown", fmt.Errorf("%w: connection lost after write", session.ErrOutcomeUnknown)},
	} {
		mapped := threadStoreMutationError("archive", tc.err)
		if mapped == nil {
			t.Fatalf("%s: mapped to nil, want the failure to surface", tc.name)
		}
		if errors.Is(mapped, ErrJSONRPCInvalidRequest) {
			t.Fatalf("%s: storage failure was reported as a client mistake: %v", tc.name, mapped)
		}
		if code := errorCode(mapped); code != JSONRPCInternalErrorCode {
			t.Fatalf("%s: code = %d, want %d", tc.name, code, JSONRPCInternalErrorCode)
		}
		want := "failed to archive session: " + tc.err.Error()
		if mapped.Error() != want {
			t.Fatalf("%s: message = %q, want %q", tc.name, mapped.Error(), want)
		}
		// The classification travels with the wrapped failure: callers can still
		// tell a safe retry from an unknown write outcome, and the mapper itself
		// neither retries nor swallows the failure.
		if tc.name != "internal" && !errors.Is(mapped, tc.err) {
			t.Fatalf("%s: category was lost: %v", tc.name, mapped)
		}
	}
}

// TestThreadStoreCategoriesStayDistinctLikeRust pins the carrier semantics Rust
// #54664 documents on its variants: a conflict alone does not imply
// retryability, and an unknown write outcome is not an ordinary retryable
// failure.
func TestThreadStoreCategoriesStayDistinctLikeRust(t *testing.T) {
	if errors.Is(session.ErrConflict, session.ErrRetryable) || errors.Is(session.ErrRetryable, session.ErrConflict) {
		t.Fatal("a conflict must not satisfy the retryable category")
	}
	if errors.Is(session.ErrRetryable, session.ErrOutcomeUnknown) || errors.Is(session.ErrOutcomeUnknown, session.ErrRetryable) {
		t.Fatal("an unknown write outcome must not be an ordinary retryable failure")
	}
	if session.ErrRetryable.Error() == "" || session.ErrOutcomeUnknown.Error() == "" {
		t.Fatal("the new categories need a message")
	}
	if errors.Is(session.ErrThreadNotFound, session.ErrOutcomeUnknown) {
		t.Fatal("an unrelated store failure must not classify as an unknown write outcome")
	}
}

// TestAgentShutdownErrorKindReportsNewStoreCategoriesLikeRust mirrors Rust
// #54664's shutdown diagnostics: the new categories report their own stable,
// payload-free kind names (thread_manager::thread_store_error_kind), and the
// raw error payload never reaches the report.
func TestAgentShutdownErrorKindReportsNewStoreCategoriesLikeRust(t *testing.T) {
	const secret = "sensitive thread-store error payload"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"retryable", fmt.Errorf("%w: %s", session.ErrRetryable, secret), "thread_store_retryable"},
		{"outcome unknown", fmt.Errorf("%w: %s", session.ErrOutcomeUnknown, secret), "thread_store_outcome_unknown"},
		{"internal", errors.New(secret), "operation_error"},
		{"conflict", fmt.Errorf("%w: %s", session.ErrConflict, secret), "thread_store_conflict"},
	} {
		got := agentShutdownErrorKind(tc.err)
		if got != tc.want {
			t.Fatalf("%s: kind = %q, want %q", tc.name, got, tc.want)
		}
		if strings.Contains(got, secret) {
			t.Fatalf("%s: kind leaked the raw error payload: %q", tc.name, got)
		}
	}
	if got := agentShutdownErrorKind(nil); got != "" {
		t.Fatalf("nil error kind = %q, want the empty kind", got)
	}
}
