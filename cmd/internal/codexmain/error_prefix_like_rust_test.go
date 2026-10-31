package codexmain

import (
	"bytes"
	"errors"
	"testing"

	"codex_go/app"
	"codex_go/prompt"
)

// TestPrintRunErrorMatchesRustPrefixes pins how failures are rendered: anyhow
// errors print as `Error: <message>`, while pre-rendered exit messages and the
// direct `eprintln!; exit(1)` sites keep their own text.
func TestPrintRunErrorMatchesRustPrefixes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "anyhow", err: errors.New("boom"), want: "Error: boom\n"},
		{name: "exit message", err: &app.ExitError{Code: 1, Message: "quiet"}, want: "quiet\n"},
		{name: "direct message", err: &prompt.DirectExitError{Text: "No prompt provided via stdin."}, want: "No prompt provided via stdin.\n"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stderr bytes.Buffer
			printRunError(&stderr, testCase.err)
			if stderr.String() != testCase.want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), testCase.want)
			}
		})
	}
}
