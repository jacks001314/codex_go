//go:build windows

package execserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// Rust #61358 tags a request whose sandbox backend is unusable on this host as
// sandboxUnavailable. Go reports the same condition when native MXC is missing.
func TestProcessStartMXCUnavailableCarriesSandboxUnavailableKindLikeRust(t *testing.T) {
	if sandbox.WindowsMXCAvailable() {
		t.Skip("native MXC is available on this executor")
	}
	payload, err := json.Marshal(FileSystemSandboxContext{
		Permissions:         json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
		WindowsSandboxLevel: windowsSandboxLevelMxc,
	})
	if err != nil {
		t.Fatalf("Marshal(sandbox context) error = %v", err)
	}
	_, supported, startErr := startExecServerSandboxProcess(&ExecParams{
		ProcessID: "mxc-unavailable",
		Argv:      []string{"cmd.exe", "/c", "exit", "0"},
		CWD:       t.TempDir(),
		Env:       map[string]string{},
		Sandbox:   payload,
	})
	if !supported || startErr == nil {
		t.Fatalf("supported = %v, err = %v; want a sandbox failure", supported, startErr)
	}
	encoded, marshalErr := json.Marshal(errorResponseForRequest(RequestID{value: 3}, startErr))
	if marshalErr != nil {
		t.Fatalf("Marshal(error response) error = %v", marshalErr)
	}
	wire := string(encoded)
	if !strings.Contains(wire, `"code":-32602`) ||
		!strings.Contains(wire, `"data":{"kind":"sandboxUnavailable"}`) {
		t.Fatalf("error response = %s", wire)
	}
}
