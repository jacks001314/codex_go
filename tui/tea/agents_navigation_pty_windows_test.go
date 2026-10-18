//go:build windows

package tea

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"

	"codex_go/utils"
)

const agentsNavigationPTYEnv = "CODEX_TUI_AGENTS_NAV_PTY"

// TestAgentsNavigationOpensOverviewPTY drives the production binary through a
// real local-daemon session: the empty composer advertises `<- for agents`, and
// pressing Left opens the command center.
func TestAgentsNavigationOpensOverviewPTY(t *testing.T) {
	if os.Getenv(agentsNavigationPTYEnv) != "1" {
		t.Skipf("set %s=1 to run the agents-navigation ConPTY check", agentsNavigationPTYEnv)
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
	// A daemon launched for this session keeps its control socket and SQLite
	// files open after the TUI exits, so stop only processes rooted at this
	// test's temporary CODEX_HOME before TempDir cleanup runs.
	t.Cleanup(func() {
		killCodexProcessesUnderPath(home)
		time.Sleep(500 * time.Millisecond)
	})
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
	command := terminal.Command(goExe, "--no-alt-screen")
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
		deadline := time.Now().Add(60 * time.Second)
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

	wait("for agents")
	if _, err := terminal.Write([]byte("\x1b[D")); err != nil {
		t.Fatalf("write Left: %v output=%q", err, utils.StripANSI(stop()))
	}
	wait("Agent command center")
	wait("Group: Project")
	wait("Search tasks")
	stop()
}

// killCodexProcessesUnderPath stops codex.exe processes whose command line
// references the isolated CODEX_HOME used by this test. Unrelated daemons and
// the user's normal CODEX_HOME are never matched.
func killCodexProcessesUnderPath(path string) {
	quoted := strings.ReplaceAll(path, "'", "''")
	script := "Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'codex.exe' -and $_.CommandLine -like '*" +
		quoted + "*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }"
	_ = exec.Command("powershell", "-NoProfile", "-Command", script).Run()
}
