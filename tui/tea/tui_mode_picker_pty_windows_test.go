//go:build windows

package tea

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"

	"codex_go/utils"
)

const tuiModePickerPTYEnv = "CODEX_TUI_MODE_PTY"

// TestTUIModePickerPersistsNextLaunchPTY drives the production binary through
// `/tui`, confirms Scrollback, and verifies the preference is written without
// changing the running `--no-alt-screen` renderer.
func TestTUIModePickerPersistsNextLaunchPTY(t *testing.T) {
	if os.Getenv(tuiModePickerPTYEnv) != "1" {
		t.Skipf("set %s=1 to run the /tui ConPTY check", tuiModePickerPTYEnv)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	goExe := strings.TrimSpace(os.Getenv("CODEX_GO_EXE"))
	if goExe == "" {
		goExe = filepath.Clean(filepath.Join(cwd, "..", "..", "bin", "codex.exe"))
	}
	if _, err := os.Stat(goExe); err != nil {
		t.Fatalf("required executable %s: %v", goExe, err)
	}

	server := newDeterministicResponsesServer(t)
	defer server.Close()
	home := t.TempDir()
	workspace := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	config := fmt.Sprintf(`model = "gpt-5.5"
openai_base_url = %q
approval_policy = "never"
sandbox_mode = "read-only"
cli_auth_credentials_store = "file"

[windows]
sandbox = "unelevated"

[projects.%s]
trust_level = "trusted"
`, server.URL+"/v1", strconv.Quote(workspace))
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create ConPTY: %v", err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("resize ConPTY: %v", err)
	}
	command := terminal.Command(goExe, "--no-alt-screen", "--no-daemon")
	command.Dir = workspace
	command.Env = append(os.Environ(),
		"CODEX_HOME="+home,
		"OPENAI_API_KEY=sk-test",
		"TERM=xterm-256color",
		"NO_COLOR=1",
	)
	if err := command.Start(); err != nil {
		t.Fatalf("start through ConPTY: %v", err)
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
	stop := func() string {
		_ = command.Process.Kill()
		conPTY, ok := terminal.(gopty.ConPty)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			if ok {
				_ = conPTY.InputPipe().Close()
			}
		}
		if ok {
			_ = conPTY.InputPipe().Close()
			_ = conPTY.OutputPipe().Close()
		}
		select {
		case <-outputDone:
		case <-time.After(time.Second):
		}
		return output.String()
	}
	wait := func(needle string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			captured := utils.StripANSI(output.String())
			if strings.Contains(captured, needle) {
				return
			}
			select {
			case earlyExit := <-exited:
				t.Fatalf("TUI exited before %q: %v output=%q", needle, earlyExit, captured)
			case <-time.After(50 * time.Millisecond):
			}
		}
		t.Fatalf("TUI did not render %q; output=%q", needle, utils.StripANSI(stop()))
	}

	wait("Tip:")
	if _, err := terminal.Write([]byte("\x14")); err != nil {
		t.Fatalf("open transcript pager: %v output=%q", err, utils.StripANSI(stop()))
	}
	wait("↑/↓ to scroll · pgup/pgdn to page · home/end to jump")
	wait("q close · f3 find · esc browse prompts")
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatalf("close transcript pager: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	// Shared pickers use the compact key hints from Rust #46680.
	if _, err := terminal.Write([]byte("/theme\r")); err != nil {
		t.Fatalf("open theme picker: %v output=%q", err, utils.StripANSI(stop()))
	}
	wait("enter select · esc back")
	wait("\u2193")
	if _, err := terminal.Write([]byte("\x1b")); err != nil {
		t.Fatalf("close theme picker: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := terminal.Write([]byte("/statusline\r")); err != nil {
		t.Fatalf("open status line setup: %v output=%q", err, utils.StripANSI(stop()))
	}
	wait("space toggle · enter save · esc cancel")
	wait("\u2193")
	if _, err := terminal.Write([]byte("\x1b")); err != nil {
		t.Fatalf("close status line setup: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := terminal.Write([]byte("/config\r")); err != nil {
		t.Fatalf("open config panel: %v output=%q", err, utils.StripANSI(stop()))
	}
	wait("Configuration")
	wait("type to search · ←/→ tabs · enter open · esc close")
	if _, err := terminal.Write([]byte("\x1b")); err != nil {
		t.Fatalf("close config panel: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	// The default leader chord opens the preset HUD.
	if _, err := terminal.Write([]byte("\x18p")); err != nil {
		t.Fatalf("open presets: %v output=%q", err, utils.StripANSI(stop()))
	}
	wait("No presets saved.")
	if _, err := terminal.Write([]byte("\x1b")); err != nil {
		t.Fatalf("close presets: %v", err)
	}
	if _, err := terminal.Write([]byte("/tui\r")); err != nil {
		t.Fatalf("open /tui: %v output=%q", err, utils.StripANSI(stop()))
	}
	wait("TUI mode for next launch")
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatalf("arm TUI mode: %v", err)
	}
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatalf("confirm TUI mode: %v", err)
	}
	wait("Saved TUI mode: Scrollback")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		body, readErr := os.ReadFile(configPath)
		if readErr == nil && strings.Contains(string(body), "fullscreen_transcript = false") {
			stop()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("config was not updated; output=%q", utils.StripANSI(stop()))
}
