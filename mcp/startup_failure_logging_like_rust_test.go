package mcp

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForStartupFailureLogs(t *testing.T, logs *lockedLogBuffer, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(logs.String(), "MCP server startup failed") >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("startup failure logs = %d, want %d:\n%s", strings.Count(logs.String(), "MCP server startup failed"), want, logs.String())
}

func TestMCPStartupFailureLogsOncePerAttemptLikeRust(t *testing.T) {
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"missing": {
			Name: "missing",
			Config: ServerConfig{
				Command: "codex-go-missing-mcp-startup-log-test",
				Enabled: true,
			},
		},
	}})
	defer service.Close()

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := service.ListStatusCheckedWithObserver(&MCPListServerStatusParams{
			Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull},
		}, nil); err != nil {
			t.Fatalf("ListStatusCheckedWithObserver() error = %v", err)
		}
	}
	waitForStartupFailureLogs(t, logs, 2)
	if got := strings.Count(logs.String(), "MCP server startup failed"); got != 2 {
		t.Fatalf("startup failure logs = %d, want one per attempt:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), "server_name=missing") {
		t.Fatalf("startup failure log is missing the server name:\n%s", logs.String())
	}
}

func TestMCPStartupFailureLogsDiscoveryResultWithoutNotificationLikeRust(t *testing.T) {
	if os.Getenv("MCP_STARTUP_LOG_SLOW_FAILURE_HELPER") == "1" {
		time.Sleep(300 * time.Millisecond)
		os.Exit(1)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"slow-missing": {
			Name: "slow-missing",
			Config: ServerConfig{
				Command:        executable,
				Args:           []string{"-test.run=TestMCPStartupFailureLogsDiscoveryResultWithoutNotificationLikeRust", "--"},
				Env:            map[string]string{"MCP_STARTUP_LOG_SLOW_FAILURE_HELPER": "1"},
				Enabled:        true,
				StartupTimeout: 5 * time.Second,
			},
		},
	}})
	defer service.Close()

	notifiedFailed := false
	response, err := service.ListStatusCheckedWithObserver(&MCPListServerStatusParams{
		Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull},
		NonBlockingOptional:  true,
		OptionalStartupGrace: 20 * time.Millisecond,
	}, func(name string, status MCPServerStartupState, _ *string, _ error) {
		if name == "slow-missing" && status == MCPServerFailed {
			notifiedFailed = true
		}
	})
	if err != nil || response == nil {
		t.Fatalf("ListStatusCheckedWithObserver response=%#v err=%v", response, err)
	}
	if notifiedFailed {
		t.Fatalf("optional attempt settled before the grace window, so no notification was dropped")
	}
	waitForStartupFailureLogs(t, logs, 1)
	if !strings.Contains(logs.String(), "server_name=slow-missing") {
		t.Fatalf("startup failure log is missing the dropped attempt:\n%s", logs.String())
	}
}

type outerOnlyError struct{ cause error }

func (e *outerOnlyError) Error() string { return "outer" }
func (e *outerOnlyError) Unwrap() error { return e.cause }

func TestMCPStartupErrorMessageKeepsErrorChainLikeRust(t *testing.T) {
	root := errors.New("root cause")
	if got := mcpStartupErrorMessage(fmt.Errorf("handshake failed: %w", root)); got != "handshake failed: root cause" {
		t.Fatalf("mcpStartupErrorMessage(embedded cause) = %q", got)
	}
	if got := mcpStartupErrorMessage(&outerOnlyError{cause: root}); got != "outer: root cause" {
		t.Fatalf("mcpStartupErrorMessage(outer only) = %q", got)
	}
	if got := mcpStartupErrorMessage(nil); got != "" {
		t.Fatalf("mcpStartupErrorMessage(nil) = %q", got)
	}
}
