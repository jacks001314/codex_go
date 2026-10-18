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

const followupDirectivePTYEnv = "CODEX_TUI_FOLLOWUP_PTY"

// TestFollowupDirectiveLabelsRenderPTY drives the production Go TUI against a
// deterministic Responses API and verifies #49089 end to end: the visible
// label reaches the transcript while the directive and private prompt do not.
func TestFollowupDirectiveLabelsRenderPTY(t *testing.T) {
	if os.Getenv(followupDirectivePTYEnv) != "1" {
		t.Skipf("set %s=1 to run the follow-up directive ConPTY check", followupDirectivePTYEnv)
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
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
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

	wait("gcode")
	wait("Tip:")
	if _, err := terminal.Write([]byte("followup prompt\r")); err != nil {
		t.Fatalf("write prompt: %v output=%q", err, stop())
	}
	wait("Inspect items")
	if _, err := terminal.Write([]byte{0x12}); err != nil {
		t.Fatalf("open history search: %v output=%q", err, stop())
	}
	wait("reverse-i-search:")
	if _, err := terminal.Write([]byte("not-present")); err != nil {
		t.Fatalf("write history query: %v output=%q", err, stop())
	}
	wait("no match")
	if _, err := terminal.Write([]byte{0x03}); err != nil {
		t.Fatalf("cancel history search: %v output=%q", err, stop())
	}
	captured := utils.StripANSI(stop())
	if strings.Contains(captured, "codex-followup") || strings.Contains(captured, `prompt="private"`) {
		t.Fatalf("follow-up directive leaked into production TUI output: %q", captured)
	}
	if strings.Contains(captured, "private") {
		t.Fatalf("follow-up prompt leaked into production TUI output: %q", captured)
	}
}
