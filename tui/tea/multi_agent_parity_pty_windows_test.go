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

const multiAgentTUIParityEnv = "CODEX_GO_TUI_MULTI_AGENT_PARITY"
const multiAgentTUIParityTimeoutEnv = "CODEX_GO_TUI_MULTI_AGENT_TIMEOUT"

func TestMultiAgentTUIParityWithConPTY(t *testing.T) {
	if os.Getenv(multiAgentTUIParityEnv) != "1" {
		t.Skipf("set %s=1 to run Rust vs Go multi-agent TUI parity through ConPTY", multiAgentTUIParityEnv)
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
	for implementation, binary := range binaries {
		if binary == "" {
			t.Fatalf("%s binary environment variable is empty", implementation)
		}
		if _, err := os.Stat(binary); err != nil {
			t.Fatalf("required %s binary %s: %v", implementation, binary, err)
		}
	}

	prompt := "Use the collaboration tools directly. Spawn exactly one agent named tui_worker and ask it to reply exactly the token formed by joining TUI, CHILD, and OK with underscores. Call the wait_agent tool from the collaboration namespace, never the plain wait tool, and wait until the agent completes. Then reply exactly the token formed by joining TUI, PARITY, and DONE with underscores. Do not use shell or modify files."
	outputs := make(map[string]string, len(binaries))
	for _, implementation := range []string{"rust", "go"} {
		implementation := implementation
		t.Run(implementation, func(t *testing.T) {
			raw := runMultiAgentTUIParityPTY(t, implementation, binaries[implementation], repoRoot, prompt, "TUI_PARITY_DONE")
			clean := utils.StripANSI(raw)
			outputs[implementation] = clean
			writeMultiAgentTUIArtifact(t, implementation+"-raw.ansi", raw)
			writeMultiAgentTUIArtifact(t, implementation+"-text.txt", clean)
		})
	}
	if t.Failed() {
		return
	}

	for implementation, output := range outputs {
		for _, want := range []string{"TUI_PARITY_DONE", "Started `/root/tui_worker`", "Waiting for agents", "Finished waiting", "No agents completed yet"} {
			if !strings.Contains(output, want) {
				t.Fatalf("%s TUI output missing %q; capture preserved in CODEX_TUI_ARTIFACT_DIR", implementation, want)
			}
		}
		if got := strings.Count(output, "Started `/root/tui_worker`"); got < 2 {
			t.Fatalf("%s TUI output contains %d started activity lifecycle rows, want at least 2; capture preserved in CODEX_TUI_ARTIFACT_DIR", implementation, got)
		}
		for _, forbidden := range []string{"collaboration.", "gAAAA", `"agent_type"`, `"task_name"`, "Running wait_agent", "Ran 'collaboration"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("%s TUI output leaked %q; capture preserved in CODEX_TUI_ARTIFACT_DIR", implementation, forbidden)
			}
		}
	}
}

func runMultiAgentTUIParityPTY(t *testing.T, implementation string, binary string, cwd string, prompt string, completionToken string) string {
	t.Helper()
	home := t.TempDir()
	preserveMultiAgentTUIHome(t, implementation, home)
	copyMultiAgentTUIHomeFile(t, "auth.json", home)
	copyMultiAgentTUIHomeFile(t, "config.toml", home)

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create production ConPTY for %s: %v", binary, err)
	}
	if err := terminal.Resize(150, 45); err != nil {
		t.Fatalf("resize production ConPTY for %s: %v", binary, err)
	}
	command := terminal.Command(
		binary,
		"--enable", "multi_agent_v2",
		"--disable", "code_mode",
		"--disable", "code_mode_only",
		"--no-alt-screen",
		"--sandbox", "read-only",
		"--ask-for-approval", "never",
		"--cd", cwd,
	)
	command.Dir = cwd
	command.Env = append(os.Environ(), "CODEX_HOME="+home, "TERM=xterm-256color")
	if err := command.Start(); err != nil {
		t.Fatalf("spawn %s through production ConPTY: %v", binary, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()

	var output lockedTerminalOutput
	defer func() {
		if !t.Failed() {
			return
		}
		raw := output.String()
		writeMultiAgentTUIArtifact(t, implementation+"-failure-raw.ansi", raw)
		writeMultiAgentTUIArtifact(t, implementation+"-failure-text.txt", utils.StripANSI(raw))
	}()
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

	ready, earlyExit := waitForGoPTYOutputOrExit(&output, "OpenAI Codex", exited, 15*time.Second)
	if !ready {
		_ = command.Process.Kill()
		if earlyExit != nil {
			t.Fatalf("%s exited before rendering: %v", binary, earlyExit)
		}
		t.Fatalf("%s did not render through production ConPTY", binary)
	}
	if _, err := terminal.Write([]byte(prompt + "\r")); err != nil {
		_ = command.Process.Kill()
		t.Fatalf("write multi-agent prompt to %s: %v", binary, err)
	}
	completed, earlyExit := waitForGoPTYOutputOrExit(&output, completionToken, exited, multiAgentTUIParityTimeout(t))
	if !completed {
		_ = command.Process.Kill()
		if earlyExit != nil {
			t.Fatalf("%s exited before multi-agent completion: %v", binary, earlyExit)
		}
		t.Fatalf("%s did not complete the multi-agent prompt", binary)
	}
	time.Sleep(time.Second)
	_, _ = terminal.Write([]byte("/exit\r"))
	select {
	case <-exited:
	case <-time.After(8 * time.Second):
		_ = command.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not stop after TUI capture", binary)
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

func preserveMultiAgentTUIHome(t *testing.T, implementation string, home string) {
	t.Helper()
	directory := strings.TrimSpace(os.Getenv("CODEX_TUI_ARTIFACT_DIR"))
	if directory == "" {
		return
	}
	t.Cleanup(func() {
		target := filepath.Join(directory, implementation+"-codex-home-"+time.Now().Format("20060102-150405.000000000"))
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Errorf("create preserved %s TUI home: %v", implementation, err)
			return
		}
		entries, err := os.ReadDir(home)
		if err != nil {
			t.Errorf("read isolated %s TUI home: %v", implementation, err)
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			source := filepath.Join(home, name)
			destination := filepath.Join(target, name)
			if entry.IsDir() {
				if name != "sessions" && name != "archived_sessions" && name != "log" && name != "logs" {
					continue
				}
				if err := os.CopyFS(destination, os.DirFS(source)); err != nil {
					t.Errorf("preserve %s TUI %s: %v", implementation, name, err)
				}
				continue
			}
			if !isMultiAgentTUIDiagnosticFile(name) {
				continue
			}
			data, err := os.ReadFile(source)
			if err != nil {
				t.Errorf("read %s TUI diagnostic %s: %v", implementation, name, err)
				continue
			}
			if err := os.WriteFile(destination, data, 0o600); err != nil {
				t.Errorf("preserve %s TUI diagnostic %s: %v", implementation, name, err)
			}
		}
	})
}

func isMultiAgentTUIDiagnosticFile(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(name, ".sqlite") ||
		strings.HasSuffix(name, ".sqlite-shm") ||
		strings.HasSuffix(name, ".sqlite-wal") ||
		strings.HasSuffix(name, ".log") ||
		strings.HasSuffix(name, ".jsonl")
}

func multiAgentTUIParityTimeout(t *testing.T) time.Duration {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(multiAgentTUIParityTimeoutEnv))
	if value == "" {
		return 240 * time.Second
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		t.Fatalf("parse %s=%q as positive duration: %v", multiAgentTUIParityTimeoutEnv, value, err)
	}
	return timeout
}

func copyMultiAgentTUIHomeFile(t *testing.T, name string, targetHome string) {
	t.Helper()
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve user home: %v", err)
	}
	source := filepath.Join(userHome, ".codex", name)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read isolated TUI %s source: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(targetHome, name), data, 0o600); err != nil {
		t.Fatalf("write isolated TUI %s: %v", name, err)
	}
}

func writeMultiAgentTUIArtifact(t *testing.T, name string, content string) {
	t.Helper()
	directory := strings.TrimSpace(os.Getenv("CODEX_TUI_ARTIFACT_DIR"))
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create TUI artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write TUI artifact %s: %v", name, err)
	}
}
