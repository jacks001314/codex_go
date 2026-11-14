package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"codex_go/tracehook"
)

// Mirrors Rust #61358: a spawn failure for a missing executable answers the
// process/start JSON-RPC error with the tagged spawnNotFound kind.
func TestProcessStartMissingExecutableCarriesSpawnNotFoundLikeRust(t *testing.T) {
	server := NewServer()
	_, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "missing-binary",
		Argv:      []string{"codex-go-command-that-does-not-exist"},
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	})
	if err == nil {
		t.Fatal("startProcess() error = nil, want spawn failure")
	}
	encoded, marshalErr := json.Marshal(errorResponseForRequest(RequestID{value: 7}, err))
	if marshalErr != nil {
		t.Fatalf("Marshal(error response) error = %v", marshalErr)
	}
	wire := string(encoded)
	if !strings.Contains(wire, `"code":-32603`) {
		t.Fatalf("error response = %s, want internal error code", wire)
	}
	if !strings.Contains(wire, `"data":{"kind":"spawnNotFound"}`) {
		t.Fatalf("error response = %s, want spawnNotFound data", wire)
	}
	var failure *requestFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %#v, want JSON-RPC failure", err)
	}
	if kind, ok := processStartErrorKindFromData(failure.data); !ok || kind != ProcessStartErrorSpawnNotFound {
		t.Fatalf("decoded kind = %q, %v", kind, ok)
	}
}

func TestProcessStartSpawnErrorKindClassificationLikeRust(t *testing.T) {
	permission := &fs.PathError{Op: "fork/exec", Path: "secret", Err: fs.ErrPermission}
	wrapped := errors.Join(errors.New("spawn wrapper"), &fs.PathError{Op: "fork/exec", Path: "gone", Err: fs.ErrNotExist})
	cases := []struct {
		name string
		err  error
		want ProcessStartErrorKind
	}{
		{"missing", &fs.PathError{Op: "fork/exec", Path: "gone", Err: fs.ErrNotExist}, ProcessStartErrorSpawnNotFound},
		{"missingWrapped", wrapped, ProcessStartErrorSpawnNotFound},
		{"permission", permission, ProcessStartErrorSpawnPermissionDenied},
		{"other", errors.New("resource temporarily unavailable"), ProcessStartErrorSpawnFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := processStartSpawnErrorKind(tc.err); got != tc.want {
				t.Fatalf("kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProcessStartSandboxFailuresAreTaggedLikeRust(t *testing.T) {
	unavailable := processSandboxUnavailableFailure("native MXC is unavailable on this executor")
	encoded, err := json.Marshal(errorResponseForRequest(RequestID{value: 1}, unavailable))
	if err != nil {
		t.Fatalf("Marshal(unavailable) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"code":-32602`) ||
		!strings.Contains(string(encoded), `"data":{"kind":"sandboxUnavailable"}`) {
		t.Fatalf("unavailable response = %s", encoded)
	}

	prepared := processSandboxPreparationFailure(errors.New("boom"))
	encoded, err = json.Marshal(errorResponseForRequest(RequestID{value: 2}, prepared))
	if err != nil {
		t.Fatalf("Marshal(preparation) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"message":"failed to prepare process sandbox: boom"`) ||
		!strings.Contains(string(encoded), `"data":{"kind":"sandboxPreparation"}`) {
		t.Fatalf("preparation response = %s", encoded)
	}
}

func TestProcessStartErrorKindDecodeLikeRust(t *testing.T) {
	cases := []struct {
		name string
		data json.RawMessage
		want ProcessStartErrorKind
		ok   bool
	}{
		{"absent", nil, "", false},
		{"spawnFailed", json.RawMessage(`{"kind":"spawnFailed"}`), ProcessStartErrorSpawnFailed, true},
		{"permission", json.RawMessage(`{"kind":"spawnPermissionDenied"}`), ProcessStartErrorSpawnPermissionDenied, true},
		{"unknown", json.RawMessage(`{"kind":"other"}`), "", false},
		{"malformed", json.RawMessage(`"spawnFailed"`), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, ok := processStartErrorKindFromData(tc.data)
			if ok != tc.ok || kind != tc.want {
				t.Fatalf("decode = %q, %v; want %q, %v", kind, ok, tc.want, tc.ok)
			}
		})
	}
}

// Mirrors Rust rpc_timing.rs: a failed process/start receipt records the error
// code and the structured kind; other methods and successes record neither.
func TestProcessStartReceiptRecordsStructuredErrorLikeRust(t *testing.T) {
	root := newRecordingTraceSpan()
	ctx := tracehook.WithSpan(context.Background(), root)

	var failed RpcCompletion
	failed.completedAt = time.Now()
	if err := json.Unmarshal([]byte(`{"id":1,"error":{"code":-32602,"message":"bad","data":{"kind":"sandboxPreparation"}}}`), &failed.response); err != nil {
		t.Fatalf("Unmarshal(failed response) error = %v", err)
	}
	failed.recordReceipt(ctx, MethodProcessStart)
	event := root.events[execServerResponseReceivedEvent]
	if event["outcome"] != "error" || event["process.start_error_code"] != "-32602" ||
		event["process.start_error_kind"] != "sandboxPreparation" {
		t.Fatalf("failed receipt event = %#v", event)
	}

	root = newRecordingTraceSpan()
	ctx = tracehook.WithSpan(context.Background(), root)
	var ok RpcCompletion
	ok.completedAt = time.Now()
	if err := json.Unmarshal([]byte(`{"id":2,"result":{"processId":"p"}}`), &ok.response); err != nil {
		t.Fatalf("Unmarshal(success response) error = %v", err)
	}
	ok.recordReceipt(ctx, MethodProcessStart)
	event = root.events[execServerResponseReceivedEvent]
	if _, present := event["process.start_error_code"]; present {
		t.Fatalf("success receipt event = %#v, want no error code", event)
	}

	root = newRecordingTraceSpan()
	ctx = tracehook.WithSpan(context.Background(), root)
	failed.recordReceipt(ctx, MethodProcessRead)
	event = root.events[execServerResponseReceivedEvent]
	if _, present := event["process.start_error_code"]; present {
		t.Fatalf("non-start receipt event = %#v, want no error code", event)
	}
}
