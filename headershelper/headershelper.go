// Package headershelper runs the shell command that prints a JSON object of
// HTTP headers, shared by MCP server configuration and the OTel HTTP exporters
// (Rust #61018 codex-http-headers-helper).
//
// The helper is a stdio-only child: it runs in the caller's working directory
// with a bounded lifetime and a bounded output size, and its process tree is
// terminated when the deadline expires.
package headershelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"codex_go/envutil"
	"golang.org/x/net/http/httpguts"
)

const (
	// Timeout bounds one helper execution.
	Timeout = 10 * time.Second
	// MaxOutputBytes bounds the captured stdout.
	MaxOutputBytes = 64 * 1024
)

var (
	// ErrInvalidOutput reports a helper produced something other than a JSON
	// object of header strings.
	ErrInvalidOutput = errors.New("HTTP headers helper must output a JSON object of strings")
	// ErrReservedOutputHeader is returned by callers that reject specific
	// header names through ParseOptions; Parse itself never reserves a name.
	ErrReservedOutputHeader = errors.New("HTTP headers helper returned a reserved header")
)

// Headers is one parsed helper response. Its values are credentials, so the
// debug representations describe the count instead of the contents.
type Headers struct {
	values http.Header
}

// Len reports how many headers the helper returned.
func (h Headers) Len() int { return len(h.values) }

// Names returns the header names in sorted order.
func (h Headers) Names() []string {
	names := make([]string, 0, len(h.values))
	for name := range h.values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HTTPHeader returns a clone of the parsed headers.
func (h Headers) HTTPHeader() http.Header {
	if h.values == nil {
		return nil
	}
	return h.values.Clone()
}

// String redacts the header values.
func (h Headers) String() string { return fmt.Sprintf("headershelper.Headers(%d)", len(h.values)) }

// GoString redacts the header values.
func (h Headers) GoString() string { return h.String() }

// Run executes the helper command and parses its output.
func Run(ctx context.Context, command string, cwd string) (Headers, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return Headers{}, errors.New("HTTP headers helper must not be empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := shellCommand(runCtx, command)
	cmd.Dir = cwd
	// Rust #48238: the headers helper is a stdio-only child, so it must not
	// allocate a console window when the CLI itself has no console.
	envutil.SuppressConsoleWindow(cmd)
	prepareProcess(cmd)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			killProcessTree(cmd)
			return Headers{}, fmt.Errorf("HTTP headers helper timed out after %s", Timeout)
		}
		// The command text can carry credentials, so the error reports the exit
		// status only.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Headers{}, fmt.Errorf("HTTP headers helper failed with exit code %d", exitErr.ExitCode())
		}
		return Headers{}, errors.New("HTTP headers helper failed to run")
	}
	if stdout.Len() > MaxOutputBytes {
		return Headers{}, fmt.Errorf("HTTP headers helper output exceeds %d bytes", MaxOutputBytes)
	}
	return Parse(stdout.Bytes())
}

// Parse validates one helper response. Duplicate names (compared
// case-insensitively) and names or values that cannot be sent on the wire are
// rejected.
func Parse(data []byte) (Headers, error) {
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return Headers{}, ErrInvalidOutput
	}
	headers := http.Header{}
	seen := make(map[string]bool, len(values))
	for name, value := range values {
		lower := strings.ToLower(strings.TrimSpace(name))
		if seen[lower] {
			return Headers{}, errors.New("HTTP headers helper returned duplicate header names")
		}
		seen[lower] = true
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return Headers{}, errors.New("HTTP headers helper returned an invalid header")
		}
		headers.Set(name, value)
	}
	return Headers{values: headers}, nil
}
