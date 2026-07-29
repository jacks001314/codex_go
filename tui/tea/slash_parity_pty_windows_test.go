//go:build windows

package tea

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"
)

const slashParityEnv = "CODEX_GO_SLASH_PARITY"
const slashParityLocalExeEnv = "CODEX_GO_EXE"

func TestSystemCodexSlashParityWithConPTY(t *testing.T) {
	if os.Getenv(slashParityEnv) != "1" {
		t.Skipf("set %s=1 to run system Codex vs local code.exe slash parity through ConPTY", slashParityEnv)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	localExe := strings.TrimSpace(os.Getenv(slashParityLocalExeEnv))
	if localExe == "" {
		localExe = filepath.Join(root, "code.exe")
	}
	systemExe := strings.TrimSpace(os.Getenv("CODEX_SYSTEM_EXE"))
	if systemExe == "" {
		systemExe = filepath.Join(os.Getenv("APPDATA"), "npm", "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
	}
	for _, path := range []string{localExe, systemExe} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("required executable %s: %v", path, err)
		}
	}

	commands := []string{"/status", "/mcp"}
	system := runCodexSlashPTY(t, systemExe, root, commands)
	local := runCodexSlashPTY(t, localExe, root, commands)

	for _, want := range []string{
		"OpenAI Codex",
		"/status",
		"/mcp",
		"model",
		"approval",
		"permissions",
		"reasoning low",
		"read only",
		"collaboration mode",
		"session",
	} {
		if !strings.Contains(strings.ToLower(system), strings.ToLower(want)) {
			t.Fatalf("system output missing %q:\n%s", want, system)
		}
		if !strings.Contains(strings.ToLower(local), strings.ToLower(want)) {
			t.Fatalf("local output missing %q:\n%s", want, local)
		}
	}
}

func runCodexSlashPTY(t *testing.T, exe string, cwd string, commands []string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("write isolated PTY auth fixture: %v", err)
	}
	config := "[projects." + strconv.Quote(cwd) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write isolated PTY trust fixture: %v", err)
	}
	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create production ConPTY for %s: %v", exe, err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("resize production ConPTY for %s: %v", exe, err)
	}
	command := terminal.Command(exe, "--no-alt-screen")
	command.Dir = cwd
	command.Env = append(os.Environ(),
		"CODEX_HOME="+home,
		"OPENAI_API_KEY=sk-test",
		"TERM=xterm-256color",
	)
	if err := command.Start(); err != nil {
		t.Fatalf("spawn %s through production ConPTY: %v", exe, err)
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
	ready, earlyExit := waitForGoPTYOutputOrExit(&output, "OpenAI Codex", exited, 10*time.Second)
	if !ready {
		if earlyExit != nil {
			t.Fatalf("%s exited before rendering: %v output=%q", exe, earlyExit, output.String())
		}
		_ = command.Process.Kill()
		t.Fatalf("%s did not render through production ConPTY; output=%q", exe, output.String())
	}
	time.Sleep(3 * time.Second)
	for _, command := range commands {
		if _, err := terminal.Write([]byte(command + "\r")); err != nil {
			t.Fatalf("write %q to %s: %v", command, exe, err)
		}
		time.Sleep(750 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	_ = command.Process.Kill()
	conPTY, ok := terminal.(gopty.ConPty)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		if ok {
			_ = conPTY.InputPipe().Close()
		}
		t.Fatalf("%s did not stop after slash capture; output=%q", exe, output.String())
	}
	if ok {
		_ = conPTY.InputPipe().Close()
		_ = conPTY.OutputPipe().Close()
	}
	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatalf("timed out draining %s ConPTY output", exe)
	}
	return output.String()
}
