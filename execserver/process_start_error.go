package execserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
)

// Rust parity: codex-rs/exec-server-protocol/src/protocol.rs
// ProcessStartErrorData (#61358). A rejected process/start carries an optional
// tagged value in the JSON-RPC error data so the caller can tell a missing or
// denied executable apart from a failed spawn and from a sandbox that could not
// be used or prepared.
type ProcessStartErrorKind string

const (
	ProcessStartErrorSpawnNotFound         ProcessStartErrorKind = "spawnNotFound"
	ProcessStartErrorSpawnPermissionDenied ProcessStartErrorKind = "spawnPermissionDenied"
	ProcessStartErrorSpawnFailed           ProcessStartErrorKind = "spawnFailed"
	ProcessStartErrorSandboxUnavailable    ProcessStartErrorKind = "sandboxUnavailable"
	ProcessStartErrorSandboxPreparation    ProcessStartErrorKind = "sandboxPreparation"
)

// processStartErrorData is the wire shape of Rust's internally tagged enum.
type processStartErrorData struct {
	Kind ProcessStartErrorKind `json:"kind"`
}

func processStartErrorKinds() []ProcessStartErrorKind {
	return []ProcessStartErrorKind{
		ProcessStartErrorSpawnNotFound,
		ProcessStartErrorSpawnPermissionDenied,
		ProcessStartErrorSpawnFailed,
		ProcessStartErrorSandboxUnavailable,
		ProcessStartErrorSandboxPreparation,
	}
}

// attachProcessStartErrorKind tags a JSON-RPC error with the structured
// process/start failure kind. Only JSON-RPC errors carry error data, so other
// error values pass through unchanged.
func attachProcessStartErrorKind(err error, kind ProcessStartErrorKind) error {
	if err == nil || kind == "" {
		return err
	}
	var failure *requestFailure
	if !errors.As(err, &failure) {
		return err
	}
	data, marshalErr := json.Marshal(processStartErrorData{Kind: kind})
	if marshalErr != nil {
		return err
	}
	failure.data = data
	return err
}

// processSandboxPreparationFailure mirrors Rust's sandbox_preparation_error
// helper: any failure while preparing the process sandbox is an
// invalid-params error tagged sandboxPreparation.
func processSandboxPreparationFailure(err error) error {
	return attachProcessStartErrorKind(
		requestError(-32602, fmt.Sprintf("failed to prepare process sandbox: %v", err)),
		ProcessStartErrorSandboxPreparation,
	)
}

// processSandboxUnavailableFailure mirrors Rust tagging a request whose
// sandbox backend cannot be used at all (for example native MXC missing, or an
// executor that cannot enforce the requested sandbox intent).
func processSandboxUnavailableFailure(message string) error {
	return attachProcessStartErrorKind(requestError(-32602, message), ProcessStartErrorSandboxUnavailable)
}

// processStartSpawnErrorKind classifies a spawn failure the way Rust's
// local_process.rs inspects the io::Error kind in the error chain: a missing
// executable, a denied one, and anything else.
func processStartSpawnErrorKind(err error) ProcessStartErrorKind {
	switch {
	// os/exec reports a failed PATH lookup with its own sentinel instead of an
	// io error, while Rust's Command::spawn yields io::ErrorKind::NotFound for
	// the same condition.
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return ProcessStartErrorSpawnNotFound
	case errors.Is(err, fs.ErrPermission):
		return ProcessStartErrorSpawnPermissionDenied
	default:
		return ProcessStartErrorSpawnFailed
	}
}

// processStartSpawnFailure wraps a raw spawn error in the internal-error
// JSON-RPC failure Rust uses, tagged with the classified kind.
func processStartSpawnFailure(err error) error {
	if err == nil {
		return nil
	}
	var failure *requestFailure
	if errors.As(err, &failure) {
		if len(failure.data) > 0 {
			// The sandbox layer already classified this failure.
			return err
		}
		return attachProcessStartErrorKind(err, processStartSpawnErrorKind(err))
	}
	return attachProcessStartErrorKind(requestError(-32603, err.Error()), processStartSpawnErrorKind(err))
}

// processStartErrorKindFromData decodes the optional structured kind attached
// to a process/start error response. Unknown or malformed payloads report no
// kind, matching Rust's failed serde decode.
func processStartErrorKindFromData(data json.RawMessage) (ProcessStartErrorKind, bool) {
	if len(data) == 0 {
		return "", false
	}
	var decoded processStartErrorData
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", false
	}
	for _, kind := range processStartErrorKinds() {
		if decoded.Kind == kind {
			return kind, true
		}
	}
	return "", false
}
