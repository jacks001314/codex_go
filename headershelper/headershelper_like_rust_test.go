package headershelper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Mirrors Rust #61018: the shared parser rejects malformed output, duplicate
// names, and headers that cannot travel on the wire.
func TestParseRejectsInvalidHeadersLikeRust(t *testing.T) {
	cases := map[string]string{
		"malformed json":     `not-json`,
		"duplicate names":    `{"X-A":"one","x-a":"two"}`,
		"invalid name":       `{"Bad Header":"value"}`,
		"invalid value":      `{"X-A":"one\nsecond"}`,
		"non-object payload": `["X-A"]`,
		"non-string value":   `{"X-A":7}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(payload)); err == nil {
				t.Fatalf("Parse(%s) = nil error, want rejection", payload)
			}
		})
	}
	headers, err := Parse([]byte(`{"X-A":"one","X-B":"two"}`))
	if err != nil || headers.Len() != 2 || headers.HTTPHeader().Get("X-B") != "two" {
		t.Fatalf("Parse() = %v, %v", headers, err)
	}
}

// Mirrors Rust #61018: helper values are credentials, so debug formatting
// describes the count instead of the contents.
func TestHeadersDebugFormattingRedactsValuesLikeRust(t *testing.T) {
	headers, err := Parse([]byte(`{"Authorization":"Bearer secret"}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	formatted := fmt.Sprintf("%v %#v %s", headers, headers, headers)
	if strings.Contains(formatted, "secret") {
		t.Fatalf("debug formatting leaked the value: %s", formatted)
	}
}

// The runner executes the command in the requested directory and parses the
// JSON object it prints.
func TestRunExecutesCommandInDirectoryLikeRust(t *testing.T) {
	cwd := t.TempDir()
	payloadPath := filepath.Join(cwd, "headers.json")
	if err := os.WriteFile(payloadPath, []byte(`{"X-Run":"ok"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	command := `cat "` + payloadPath + `"`
	if runtime.GOOS == "windows" {
		command = `type "` + payloadPath + `"`
	}
	headers, err := Run(context.Background(), command, cwd)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := headers.HTTPHeader().Get("X-Run"); got != "ok" {
		t.Fatalf("X-Run = %q", got)
	}
	if _, err := Run(context.Background(), "   ", cwd); err == nil {
		t.Fatal("Run() accepted an empty command")
	}
}
