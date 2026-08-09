//go:build windows

package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"

	"codex_go/utils"
)

const sideShortcutTUIParityEnv = "CODEX_GO_TUI_SIDE_SHORTCUT_PARITY"

func TestSideShortcutTUIParityWithConPTY(t *testing.T) {
	if os.Getenv(sideShortcutTUIParityEnv) != "1" {
		t.Skipf("set %s=1 to run Rust vs Go /side parity and the Go Ctrl+/ shortcut through ConPTY", sideShortcutTUIParityEnv)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	binaries := map[string]string{
		"rust": strings.TrimSpace(os.Getenv("CODEX_RUST_EXE")),
		"go":   strings.TrimSpace(os.Getenv("CODEX_GO_EXE")),
	}
	for _, implementation := range []string{"rust", "go"} {
		implementation := implementation
		t.Run(implementation, func(t *testing.T) {
			binary := binaries[implementation]
			if binary == "" {
				t.Fatalf("%s binary environment variable is empty", implementation)
			}
			if _, err := os.Stat(binary); err != nil {
				t.Fatalf("required %s binary %s: %v", implementation, binary, err)
			}
			verifyShortcut := implementation == "go"
			raw := runSideShortcutTUIParityPTY(t, binary, repoRoot, verifyShortcut)
			writeMultiAgentTUIArtifact(t, "side-shortcut-"+implementation+"-raw.ansi", raw)
			writeMultiAgentTUIArtifact(t, "side-shortcut-"+implementation+"-text.txt", utils.StripANSI(raw))
			if !verifyShortcut {
				t.Log("Rust /side baseline passed; ConPTY byte input cannot synthesize the VK_OEM_2 KEY_EVENT_RECORD used by crossterm to recognize physical Ctrl+/")
			}
		})
	}
}

func runSideShortcutTUIParityPTY(t *testing.T, binary string, cwd string, verifyShortcut bool) string {
	t.Helper()
	home := t.TempDir()
	copyMultiAgentTUIHomeFile(t, "auth.json", home)
	copyMultiAgentTUIHomeFile(t, "config.toml", home)

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create ConPTY for %s: %v", binary, err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("resize ConPTY for %s: %v", binary, err)
	}
	command := terminal.Command(
		binary,
		"--disable", "code_mode",
		"--disable", "code_mode_only",
		"--no-alt-screen",
		"--sandbox", "read-only",
		"--ask-for-approval", "never",
		"--dangerously-bypass-hook-trust",
		"--cd", cwd,
	)
	command.Dir = cwd
	command.Env = append(os.Environ(), "CODEX_HOME="+home, "TERM=xterm-256color")
	if err := command.Start(); err != nil {
		t.Fatalf("spawn %s through ConPTY: %v", binary, err)
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

	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		if conPTY, ok := terminal.(gopty.ConPty); ok {
			_ = conPTY.InputPipe().Close()
			_ = conPTY.OutputPipe().Close()
		}
	}()

	waitForSideShortcutOutput(t, &output, exited, 0, "gcode", 15*time.Second)
	writeSideShortcutInput(t, terminal, "Reply exactly MAIN_READY. Do not call tools.\r")
	if !waitForWeatherFinalAnswer(home, exited, 120*time.Second) {
		raw := output.String()
		writeMultiAgentTUIArtifact(t, "side-shortcut-failure-raw.ansi", raw)
		writeMultiAgentTUIArtifact(t, "side-shortcut-failure-text.txt", utils.StripANSI(raw))
		t.Fatalf("%s did not persist the initial final answer", binary)
	}
	time.Sleep(time.Second)

	sideStart := len(output.String())
	writeSideShortcutCommand(t, terminal, "/side")
	waitForSideShortcutOutput(t, &output, exited, sideStart, "Side from main thread", 45*time.Second)
	time.Sleep(3 * time.Second)

	if verifyShortcut {
		// A terminal encodes Ctrl+/ as the C0 unit-separator byte. Bubble Tea
		// exposes this as KeyCtrlUnderscore on Windows.
		parentStart := len(output.String())
		writeSideShortcutInput(t, terminal, string([]byte{0x1f}))
		waitForSideShortcutOutput(t, &output, exited, parentStart, "ctrl + / for side", 10*time.Second)

		sideReturnStart := len(output.String())
		writeSideShortcutInput(t, terminal, string([]byte{0x1f}))
		waitForSideShortcutOutput(t, &output, exited, sideReturnStart, "Side from main thread", 10*time.Second)
	}

	writeSideShortcutCommand(t, terminal, "/exit")
	select {
	case <-exited:
	case <-time.After(8 * time.Second):
		_ = command.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not stop after /side shortcut capture", binary)
		}
	}
	if conPTY, ok := terminal.(gopty.ConPty); ok {
		_ = conPTY.InputPipe().Close()
		_ = conPTY.OutputPipe().Close()
	}
	select {
	case <-outputDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out draining %s ConPTY output", binary)
	}
	return output.String()
}

func waitForSideShortcutOutput(t *testing.T, output *lockedTerminalOutput, exited <-chan error, offset int, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw := output.String()
		if offset < len(raw) && strings.Contains(utils.StripANSI(raw[offset:]), needle) {
			return
		}
		select {
		case err := <-exited:
			t.Fatalf("process exited before rendering %q: %v output=%q", needle, err, utils.StripANSI(raw))
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %q after offset %d; output=%q", needle, offset, utils.StripANSI(raw))
		}
	}
}

func writeSideShortcutInput(t *testing.T, terminal gopty.Pty, input string) {
	t.Helper()
	if _, err := terminal.Write([]byte(input)); err != nil {
		t.Fatalf("write ConPTY input %q: %v", input, err)
	}
}

func writeSideShortcutCommand(t *testing.T, terminal gopty.Pty, command string) {
	t.Helper()
	writeSideShortcutInput(t, terminal, command)
	time.Sleep(500 * time.Millisecond)
	writeSideShortcutInput(t, terminal, "\r")
}
