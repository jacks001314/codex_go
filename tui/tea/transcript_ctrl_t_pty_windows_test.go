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

const transcriptCtrlTPTYEnv = "CODEX_GO_CTRL_T_PTY"

func TestTranscriptCtrlTOpensThroughConPTY(t *testing.T) {
	if os.Getenv(transcriptCtrlTPTYEnv) != "1" {
		t.Skipf("set %s=1 to run the transcript Ctrl+T ConPTY regression", transcriptCtrlTPTYEnv)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	exe := strings.TrimSpace(os.Getenv(slashParityLocalExeEnv))
	if exe == "" {
		exe = filepath.Join(root, ".tmp", "codex-ctrl-t.exe")
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("required executable %s: %v", exe, err)
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("write isolated PTY auth fixture: %v", err)
	}
	config := "[projects." + strconv.Quote(root) + "]\ntrust_level = \"trusted\"\n\n[windows]\nsandbox = \"unelevated\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write isolated PTY trust fixture: %v", err)
	}

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create production ConPTY: %v", err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("resize production ConPTY: %v", err)
	}
	command := terminal.Command(exe, "--no-alt-screen")
	command.Dir = root
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

	ready, earlyExit := waitForGoPTYOutputOrExit(&output, "OpenAI Codex", exited, 15*time.Second)
	if !ready {
		_ = command.Process.Kill()
		if earlyExit != nil {
			t.Fatalf("%s exited before rendering: %v output=%q", exe, earlyExit, output.String())
		}
		t.Fatalf("%s did not render through production ConPTY; output=%q", exe, output.String())
	}
	if _, err := terminal.Write([]byte{0x14}); err != nil {
		_ = command.Process.Kill()
		t.Fatalf("write Ctrl+T to %s: %v", exe, err)
	}
	opened, earlyExit := waitForGoPTYOutputOrExit(&output, "T R A N S C R I P T", exited, 5*time.Second)
	if !opened {
		_ = command.Process.Kill()
		if earlyExit != nil {
			t.Fatalf("%s exited before opening transcript: %v output=%q", exe, earlyExit, output.String())
		}
		t.Fatalf("Ctrl+T did not open transcript through ConPTY; output=%q", output.String())
	}

	_ = command.Process.Kill()
	conPTY, ok := terminal.(gopty.ConPty)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		if ok {
			_ = conPTY.InputPipe().Close()
		}
		t.Fatalf("%s did not stop after transcript capture", exe)
	}
	if ok {
		_ = conPTY.InputPipe().Close()
		_ = conPTY.OutputPipe().Close()
	}
	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatal("timed out draining transcript ConPTY output")
	}
}
