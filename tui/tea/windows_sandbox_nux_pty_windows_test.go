//go:build windows

package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"
)

const windowsSandboxNUXPTYEnv = "CODEX_GO_WINDOWS_SANDBOX_NUX_PTY"

func TestLocalCodexWindowsSandboxNUXWithConPTY(t *testing.T) {
	if os.Getenv(windowsSandboxNUXPTYEnv) != "1" {
		t.Skipf("set %s=1 to run the Windows sandbox startup prompt through ConPTY", windowsSandboxNUXPTYEnv)
	}
	exe := strings.TrimSpace(os.Getenv("CODEX_GO_EXE"))
	if exe == "" {
		t.Fatal("CODEX_GO_EXE is required")
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("local Codex executable %s: %v", exe, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	cwd = filepath.Clean(filepath.Join(cwd, "..", ".."))
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create ConPTY: %v", err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("resize ConPTY: %v", err)
	}
	command := terminal.Command(exe, "--no-alt-screen")
	command.Dir = cwd
	command.Env = append(os.Environ(),
		"CODEX_HOME="+home,
		"OPENAI_API_KEY=sk-test",
		"TERM=xterm-256color",
	)
	if err := command.Start(); err != nil {
		t.Fatalf("spawn local Codex: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()

	var output lockedTerminalOutput
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buffer := make([]byte, 4096)
		for {
			read, readErr := terminal.Read(buffer)
			if read > 0 {
				output.Write(buffer[:read])
			}
			if readErr != nil {
				return
			}
		}
	}()

	rendered, earlyExit := waitForGoPTYOutputOrExit(&output, "Set up the Codex agent sandbox", exited, 15*time.Second)
	if !rendered {
		_ = command.Process.Kill()
		if earlyExit != nil {
			t.Fatalf("local Codex exited before rendering sandbox prompt: %v output=%q", earlyExit, output.String())
		}
		t.Fatalf("sandbox prompt did not render through ConPTY; output=%q", output.String())
	}
	for _, want := range []string{
		"Set up default sandbox (requires Administrator permissions)",
		"Use non-admin sandbox (higher risk if prompt injected)",
		"Quit",
	} {
		if !output.Contains(want) {
			t.Fatalf("sandbox prompt missing %q; output=%q", want, output.String())
		}
	}

	_ = command.Process.Kill()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatalf("local Codex did not stop after prompt capture; output=%q", output.String())
	}
	if conPTY, ok := terminal.(gopty.ConPty); ok {
		_ = conPTY.InputPipe().Close()
		_ = conPTY.OutputPipe().Close()
	}
	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatal("timed out draining ConPTY output")
	}
}
