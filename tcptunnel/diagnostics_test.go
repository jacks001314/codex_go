package tcptunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

// flushRecorder counts flushes so a terminal record's single flush is asserted.
type flushRecorder struct {
	bytes   bytes.Buffer
	flushes int
}

func (o *flushRecorder) Write(p []byte) (int, error) { return o.bytes.Write(p) }
func (o *flushRecorder) Flush() error                { o.flushes++; return nil }

func assertRecord(t *testing.T, data []byte, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", data, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %#v, want %#v", got, want)
	}
}

// Mirrors Rust's `fatal_diagnostic_drops_untrusted_error_chain_and_flushes_one_record`.
func TestFatalDiagnosticDropsChainAndFlushesOneRecord(t *testing.T) {
	output := &flushRecorder{}
	err := DiagnosticsJSON.finish(
		classify(errors.New("Bearer secret; x-route: private; https://private.example/path"), CodeTimeout),
		PhaseStartup,
		output,
	)
	if err == nil || err.Error() != "TCP tunnel failed" {
		t.Fatalf("finish() error = %v, want generic failure", err)
	}
	if output.flushes != 1 {
		t.Fatalf("flushes = %d, want 1", output.flushes)
	}
	if output.bytes.Len() >= 256 {
		t.Fatalf("record length = %d, want < 256", output.bytes.Len())
	}
	if count := bytes.Count(output.bytes.Bytes(), []byte("\n")); count != 1 {
		t.Fatalf("newlines = %d, want 1", count)
	}
	assertRecord(t, output.bytes.Bytes(), map[string]any{
		"v": float64(1), "phase": "startup", "code": "timeout", "terminal": true,
	})
}

// Mirrors Rust's `control_io_failure_and_invalid_input_have_distinct_safe_codes`.
func TestControlIOFailureAndInvalidInputHaveDistinctCodes(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		code string
	}{
		{name: "invalid input", err: errors.New("invalid private bearer"), code: "invalid_input"},
		{name: "io failure", err: fmt.Errorf("reading MASQUE token: %w", io.ErrUnexpectedEOF), code: "failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			err := DiagnosticsJSON.finish(
				DiagnosticsJSON.withPhase(testCase.err, PhaseControl),
				PhaseStartup,
				&output,
			)
			if err == nil {
				t.Fatal("finish() error = nil, want generic failure")
			}
			assertRecord(t, output.Bytes(), map[string]any{
				"v": float64(1), "phase": "control", "code": testCase.code, "terminal": true,
			})
		})
	}
}

// Mirrors Rust's `connect_rejection_is_nonterminal_and_contains_only_valid_status`.
func TestConnectRejectionNonterminalWithValidStatusOnly(t *testing.T) {
	for _, status := range []int{403, 407, 503, 999} {
		var output bytes.Buffer
		DiagnosticsJSON.writeDiagnostic(&output, PhaseConnect, rejectedError(status, nil), false)
		want := map[string]any{
			"v": float64(1), "phase": "connect", "code": "rejected", "terminal": false,
		}
		if status < 600 {
			want["http_status"] = float64(status)
		}
		assertRecord(t, output.Bytes(), want)
	}
}

// Mirrors Rust's `legacy_errors_and_successful_shutdown_produce_no_json`.
func TestLegacyErrorsAndSuccessfulShutdownProduceNoJSON(t *testing.T) {
	var output bytes.Buffer
	result := DiagnosticsHuman.withPhase(errors.New("legacy error"), PhaseTransport)
	err := DiagnosticsHuman.finish(result, PhaseStartup, &output)
	if err == nil || err.Error() != "legacy error" {
		t.Fatalf("finish() error = %v, want legacy error", err)
	}
	if err := DiagnosticsJSON.finish(nil, PhaseControl, &output); err != nil {
		t.Fatalf("finish(ok) error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("legacy output = %q, want empty", output.Bytes())
	}
}

// Mirrors Rust's `handshake_timeout_preserves_legacy_debug_output`.
func TestHandshakeTimeoutRecord(t *testing.T) {
	err := &codeError{code: CodeHandshakeTimeout}
	if err.Error() != "QUIC handshake failed: QUIC handshake timed out" {
		t.Fatalf("Error() = %q", err.Error())
	}
	var output bytes.Buffer
	DiagnosticsJSON.writeDiagnostic(&output, PhaseTransport, err, false)
	assertRecord(t, output.Bytes(), map[string]any{
		"v": float64(1), "phase": "transport", "code": "timeout", "terminal": false,
	})
}

// Mirrors the Rust flag-parsing coverage for `--diagnostics-json`.
func TestParseArgsDiagnosticsJSON(t *testing.T) {
	base := []string{"--proxy-url", "https://proxy.example.org", "--proxy-origins-file", "origins.txt", "--target", "127.0.0.1:22"}
	parsed, err := ParseArgs(append(append([]string{}, base...), "--diagnostics-json"))
	if err != nil {
		t.Fatalf("ParseArgs() error = %v", err)
	}
	if !parsed.DiagnosticsJSON {
		t.Fatal("DiagnosticsJSON = false, want true")
	}
	if _, err := ParseArgs(append(append([]string{}, base...), "--diagnostics-json=1")); err == nil {
		t.Fatal("ParseArgs(--diagnostics-json=1) error = nil, want rejection")
	}
}
