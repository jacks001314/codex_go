package telemetry

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Rust #47568 serializes sandbox_backend as a nullable Option<String>: an
// unobserved launch is null, while an unsandboxed run is the explicit "none".
func TestCommandExecutionEventSerializesSandboxBackendLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend *string
		want    string
	}{
		{name: "missing", backend: nil, want: `"sandbox_backend":null`},
		{name: "explicit none", backend: stringPtrTelemetry("none"), want: `"sandbox_backend":"none"`},
		{name: "platform backend", backend: stringPtrTelemetry("seccomp"), want: `"sandbox_backend":"seccomp"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := NewCodexCommandExecutionEvent(CodexCommandExecutionEventParams{SandboxBackend: tc.backend})
			payload, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if !bytes.Contains(payload, []byte(tc.want)) {
				t.Fatalf("payload = %s, want %s", payload, tc.want)
			}
		})
	}
}
