package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// These tests mirror Rust #54434 (`16e578dd1d`,
// codex-rs/cli/src/exec_server_credential_tests.rs), which locks the rotating
// stdin credential contract one test per upstream test:
//
//	waits_for_initial_rotation_replaces_token_and_eof_clears_auth
//	malformed_rotations_latch_failure_and_clear_existing_credential
//	eof_before_a_complete_frame_never_authenticates
//	expiry_is_checked_again_on_every_request
//	parser_rejects_unsupported_schema_without_exposing_input
//
// Go landing: execserver/executor_credential.go.

// credentialPipeForTest is Rust's `pipe()` helper: the test keeps the writer and
// the pipe reads newline-delimited frames from the paired reader.
func credentialPipeForTest(environmentID string) (*io.PipeWriter, *ExecutorCredentialPipe) {
	reader, writer := io.Pipe()
	return writer, NewExecutorCredentialPipe(environmentID, reader)
}

// credentialFrameForTest is Rust's `frame()` helper: one JSON frame plus its
// trailing newline.
func credentialFrameForTest(t *testing.T, environmentID string, token string, expiresAt int64) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"version":        1,
		"environment_id": environmentID,
		"token":          token,
		"expires_at":     expiresAt,
	})
	if err != nil {
		t.Fatalf("serialize credential frame: %v", err)
	}
	return append(encoded, '\n')
}

// writeCredentialFrameForTest writes a frame and fails on a write error.
func writeCredentialFrameForTest(t *testing.T, writer io.Writer, frame []byte) {
	t.Helper()
	if _, err := writer.Write(frame); err != nil {
		t.Fatalf("write credential frame: %v", err)
	}
}

// waitForPipeEndForTest waits for the credential reader to publish its final
// state, which is Rust's `pipe.parent_closed.await`.
func waitForPipeEndForTest(t *testing.T, pipe *ExecutorCredentialPipe) {
	t.Helper()
	select {
	case <-pipe.ParentClosed():
	case <-time.After(5 * time.Second):
		t.Fatal("credential reader never finished")
	}
}

// waitForResolvedBearerForTest polls until the pipe publishes the expected
// bearer. Rust observes the same transition through its watch channel
// (`next_update`); Go publishes it from the reader goroutine, so the test waits
// for it instead of racing it.
func waitForResolvedBearerForTest(t *testing.T, pipe *ExecutorCredentialPipe, want string) http.Header {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		headers, err := pipe.ResolveAuthHeaders(context.Background())
		if err == nil && headers.Get("Authorization") == want {
			return headers
		}
		lastErr = err
		if time.Now().After(deadline) {
			t.Fatalf("ResolveAuthHeaders() never returned %q (last error %v)", want, lastErr)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestExecutorCredentialWaitsForInitialRotationAndEOFClosesAuthLikeRust mirrors
// `waits_for_initial_rotation_replaces_token_and_eof_clears_auth`.
func TestExecutorCredentialWaitsForInitialRotationAndEOFClosesAuthLikeRust(t *testing.T) {
	writer, pipe := credentialPipeForTest("env-a")
	expires := time.Now().Unix() + 60

	// No frame yet: the bounded initial wait times out and nothing authenticates.
	if err := pipe.waitForInitialWithin(context.Background(), 5*time.Millisecond); err != ErrCredentialInitialTimeout {
		t.Fatalf("waitForInitialWithin() = %v, want %v", err, ErrCredentialInitialTimeout)
	}
	if _, err := pipe.ResolveAuthHeaders(context.Background()); err == nil {
		t.Fatal("ResolveAuthHeaders() before the initial frame = nil, want an error")
	}

	// The first frame must produce exactly one Authorization bearer, and that
	// bearer must not be rendered by diagnostics.
	writeCredentialFrameForTest(t, writer, credentialFrameForTest(t, "env-a", "first-scoped-token", expires))
	if err := pipe.WaitForInitial(context.Background()); err != nil {
		t.Fatalf("WaitForInitial() = %v", err)
	}
	headers := waitForResolvedBearerForTest(t, pipe, "Bearer first-scoped-token")
	if len(headers) != 1 {
		t.Fatalf("resolved headers = %#v, want exactly one header", headers)
	}
	if rendered := RedactedExecServerHeaders(headers); strings.Contains(rendered, "first-scoped-token") {
		t.Fatalf("redacted headers leaked the credential: %s", rendered)
	}

	// A rotation replaces the credential for the next request.
	writeCredentialFrameForTest(t, writer, credentialFrameForTest(t, "env-a", "rotated-scoped-token", expires))
	headers = waitForResolvedBearerForTest(t, pipe, "Bearer rotated-scoped-token")
	if len(headers) != 1 {
		t.Fatalf("rotated headers = %#v, want exactly one header", headers)
	}

	// Closing the parent ends the pipe: authentication stops and the pipe reports
	// a clean end rather than a failure.
	if err := writer.Close(); err != nil {
		t.Fatalf("close credential writer: %v", err)
	}
	waitForPipeEndForTest(t, pipe)
	if _, err := pipe.ResolveAuthHeaders(context.Background()); err == nil {
		t.Fatal("ResolveAuthHeaders() after EOF = nil, want an error")
	}
	if err := pipe.CheckPipeFailure(); err != nil {
		t.Fatalf("CheckPipeFailure() after a clean EOF = %v, want nil", err)
	}
}

// TestExecutorCredentialMalformedRotationsLatchFailureAndClearCredentialLikeRust
// mirrors `malformed_rotations_latch_failure_and_clear_existing_credential`.
func TestExecutorCredentialMalformedRotationsLatchFailureAndClearCredentialLikeRust(t *testing.T) {
	expires := time.Now().Unix() + 60
	oversized := append(bytes.Repeat([]byte("x"), ExecutorCredentialMaxFrameBytes), '\n')
	for _, testCase := range []struct {
		name  string
		frame []byte
		want  error
	}{
		{
			name:  "wrong_environment",
			frame: credentialFrameForTest(t, "env-b", "do-not-log-cross-env", expires),
			want:  ErrCredentialWrongEnvironment,
		},
		{
			name:  "expired",
			frame: credentialFrameForTest(t, "env-a", "do-not-log-expired", 1),
			want:  ErrCredentialExpired,
		},
		{
			name:  "token_with_newline",
			frame: credentialFrameForTest(t, "env-a", "invalid\ntoken", expires),
			want:  ErrCredentialInvalidFrame,
		},
		{
			name:  "malformed_json",
			frame: []byte("invalid do-not-log-malformed\n"),
			want:  ErrCredentialInvalidFrame,
		},
		{
			name:  "empty_frame",
			frame: []byte("\n"),
			want:  ErrCredentialInvalidFrame,
		},
		{
			name:  "oversized_frame",
			frame: oversized,
			want:  ErrCredentialFrameTooLarge,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			writer, pipe := credentialPipeForTest("env-a")
			writeCredentialFrameForTest(t, writer, credentialFrameForTest(t, "env-a", "initial-scoped-token", expires))
			if err := pipe.WaitForInitial(context.Background()); err != nil {
				t.Fatalf("WaitForInitial() = %v", err)
			}

			// A malformed rotation terminates the reader on its own, without EOF.
			writeCredentialFrameForTest(t, writer, testCase.frame)
			waitForPipeEndForTest(t, pipe)

			_, authErr := pipe.ResolveAuthHeaders(context.Background())
			if authErr == nil {
				t.Fatal("ResolveAuthHeaders() after a malformed rotation = nil, want an error")
			}
			if strings.Contains(authErr.Error(), "do-not-log") || strings.Contains(authErr.Error(), "initial-scoped-token") {
				t.Fatalf("auth error leaked frame input: %v", authErr)
			}
			if got := pipe.CheckPipeFailure(); got != testCase.want {
				t.Fatalf("CheckPipeFailure() = %v, want %v", got, testCase.want)
			}

			// The failure latches: Rust writes a later valid frame (`let _ =
			// writer.write_all(...)`) and still observes the same failure. No
			// reader is left, so the write blocks until the writer is released.
			later := credentialFrameForTest(t, "env-a", "later-scoped-token", expires)
			written := make(chan struct{})
			go func() {
				defer close(written)
				_, _ = writer.Write(later)
			}()
			if got := pipe.CheckPipeFailure(); got != testCase.want {
				t.Fatalf("CheckPipeFailure() after a later frame = %v, want the latched %v", got, testCase.want)
			}
			_ = writer.CloseWithError(io.ErrClosedPipe)
			<-written
			if got := pipe.CheckPipeFailure(); got != testCase.want {
				t.Fatalf("CheckPipeFailure() after the writer was released = %v, want the latched %v", got, testCase.want)
			}
		})
	}
}

// TestExecutorCredentialEOFEforeCompleteFrameNeverAuthenticatesLikeRust mirrors
// `eof_before_a_complete_frame_never_authenticates`.
func TestExecutorCredentialEOFEforeCompleteFrameNeverAuthenticatesLikeRust(t *testing.T) {
	expires := time.Now().Unix() + 60
	complete := credentialFrameForTest(t, "env-a", "scoped-token", expires)
	for _, testCase := range []struct {
		name  string
		bytes []byte
		want  error
	}{
		{name: "eof_without_any_frame", bytes: nil, want: ErrCredentialClosed},
		{name: "eof_in_the_middle_of_a_frame", bytes: complete[:len(complete)-1], want: ErrCredentialInvalidFrame},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			writer, pipe := credentialPipeForTest("env-a")
			if len(testCase.bytes) > 0 {
				writeCredentialFrameForTest(t, writer, testCase.bytes)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close credential writer: %v", err)
			}
			waitForPipeEndForTest(t, pipe)
			if err := pipe.WaitForInitial(context.Background()); err != testCase.want {
				t.Fatalf("WaitForInitial() = %v, want %v", err, testCase.want)
			}
			if _, err := pipe.ResolveAuthHeaders(context.Background()); err == nil {
				t.Fatal("ResolveAuthHeaders() after an incomplete pipe = nil, want an error")
			}
		})
	}
}

// TestExecutorCredentialExpiryIsCheckedOnEveryRequestLikeRust mirrors
// `expiry_is_checked_again_on_every_request`.
func TestExecutorCredentialExpiryIsCheckedOnEveryRequestLikeRust(t *testing.T) {
	now := time.Now().Unix()

	// Rust drives an `Active { expires_at: now }` state straight into the
	// provider: the credential is rejected when it is used, not when it arrived.
	expired := credentialState{kind: credentialStateActive, token: "unused-expired-token", expiresAt: uint64(now)}
	if _, err := expired.tokenValue(); err != ErrCredentialExpired {
		t.Fatalf("tokenValue(expires_at=now) = %v, want %v", err, ErrCredentialExpired)
	}
	renewed := credentialState{kind: credentialStateActive, token: "new-scoped-token", expiresAt: uint64(now + 60)}
	if token, err := renewed.tokenValue(); err != nil || token != "new-scoped-token" {
		t.Fatalf("tokenValue(renewed) = (%q, %v), want the renewed token", token, err)
	}

	// End to end: a frame that is valid on arrival expires while the executor is
	// still running, and the next request must fail instead of sending it.
	writer, pipe := credentialPipeForTest("env-a")
	defer func() { _ = writer.Close() }()
	writeCredentialFrameForTest(t, writer, credentialFrameForTest(t, "env-a", "expiring-scoped-token", now+1))
	if err := pipe.WaitForInitial(context.Background()); err != nil {
		t.Fatalf("WaitForInitial() = %v", err)
	}
	if _, err := pipe.ResolveAuthHeaders(context.Background()); err != nil {
		t.Fatalf("ResolveAuthHeaders() before expiry = %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := pipe.ResolveAuthHeaders(context.Background()); err != ErrCredentialExpired {
		t.Fatalf("ResolveAuthHeaders() after expiry = %v, want %v", err, ErrCredentialExpired)
	}
}

// TestExecutorCredentialParserRejectsUnsupportedSchemaLikeRust mirrors
// `parser_rejects_unsupported_schema_without_exposing_input`.
func TestExecutorCredentialParserRejectsUnsupportedSchemaLikeRust(t *testing.T) {
	expires := time.Now().Unix() + 60
	for _, testCase := range []struct {
		name  string
		frame map[string]any
	}{
		{
			name:  "unsupported_version",
			frame: map[string]any{"version": 2, "environment_id": "env-a", "token": "secret", "expires_at": expires},
		},
		{
			name:  "unknown_field",
			frame: map[string]any{"version": 1, "environment_id": "env-a", "token": "secret", "expires_at": expires, "unexpected": true},
		},
		{
			name:  "negative_expiry",
			frame: map[string]any{"version": 1, "environment_id": "env-a", "token": "secret", "expires_at": -1},
		},
		{
			name:  "blank_token",
			frame: map[string]any{"version": 1, "environment_id": "env-a", "token": " ", "expires_at": expires},
		},
		{
			name:  "token_with_space",
			frame: map[string]any{"version": 1, "environment_id": "env-a", "token": "secret token", "expires_at": expires},
		},
		{
			name:  "token_with_non_ascii",
			frame: map[string]any{"version": 1, "environment_id": "env-a", "token": "secret-\u2603", "expires_at": expires},
		},
		{
			name:  "missing_version",
			frame: map[string]any{"environment_id": "env-a", "token": "secret", "expires_at": expires},
		},
		{
			name:  "missing_environment_id",
			frame: map[string]any{"version": 1, "token": "secret", "expires_at": expires},
		},
		{
			name:  "missing_token",
			frame: map[string]any{"version": 1, "environment_id": "env-a", "expires_at": expires},
		},
		{
			name:  "missing_expiry",
			frame: map[string]any{"version": 1, "environment_id": "env-a", "token": "secret"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			serialized, err := json.Marshal(testCase.frame)
			if err != nil {
				t.Fatalf("serialize test input: %v", err)
			}
			_, parseErr := parseCredentialFrame(serialized, "env-a")
			if parseErr != ErrCredentialInvalidFrame {
				t.Fatalf("parseCredentialFrame() = %v, want %v", parseErr, ErrCredentialInvalidFrame)
			}
			if strings.Contains(parseErr.Error(), "secret") {
				t.Fatalf("parser error leaked its input: %v", parseErr)
			}
		})
	}
}
