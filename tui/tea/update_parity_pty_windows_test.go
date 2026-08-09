//go:build windows

package tea

import (
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

const updateParityEnv = "CODEX_GO_UPDATE_PARITY"

func TestUpdatePromptParityWithConPTY(t *testing.T) {
	if os.Getenv(updateParityEnv) != "1" {
		t.Skipf("set %s=1 to run system Codex vs local Go update prompt parity through ConPTY", updateParityEnv)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	localExe := strings.TrimSpace(os.Getenv("CODEX_GO_EXE"))
	if localExe == "" {
		localExe = filepath.Join(root, ".tmp", "update-e2e", "codex-go.exe")
	}
	systemExe := strings.TrimSpace(os.Getenv("CODEX_SYSTEM_EXE"))
	if systemExe == "" {
		systemExe = filepath.Join(
			os.Getenv("APPDATA"),
			"npm", "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64",
			"vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe",
		)
	}

	for _, test := range []struct {
		name      string
		exe       string
		updateCmd string
	}{
		{name: "rust", exe: systemExe, updateCmd: "npm install -g @openai/codex"},
		{name: "go", exe: localExe, updateCmd: "npm install -g @jacks001314/codex-go@latest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := os.Stat(test.exe); err != nil {
				t.Fatalf("required executable %s: %v", test.exe, err)
			}
			current := codexExecutableVersion(t, test.exe)
			output := utils.StripANSI(captureUpdatePromptPTY(t, test.exe, root))
			for _, want := range []string{
				"Update available! " + current + " -> 999.0.0",
				"Update now",
				test.updateCmd,
				"Skip",
				"Skip until next version",
			} {
				if !strings.Contains(output, want) {
					t.Fatalf("update prompt missing %q; output=%q", want, output)
				}
			}
		})
	}
}

func codexExecutableVersion(t *testing.T, exe string) string {
	t.Helper()
	output, err := exec.Command(exe, "--version").Output()
	if err != nil {
		t.Fatalf("read %s version: %v", exe, err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(output)), "codex-cli "))
	if version == "" {
		t.Fatalf("%s returned an empty version", exe)
	}
	return version
}

func captureUpdatePromptPTY(t *testing.T, exe string, cwd string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("write isolated auth fixture: %v", err)
	}
	config := "check_for_update_on_startup = true\n[projects." + strconv.Quote(cwd) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write isolated config fixture: %v", err)
	}
	version := `{"latest_version":"999.0.0","last_checked_at":"2099-01-01T00:00:00Z","dismissed_version":null}`
	if err := os.WriteFile(filepath.Join(home, "version.json"), []byte(version), 0o600); err != nil {
		t.Fatalf("write update cache fixture: %v", err)
	}

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create ConPTY for %s: %v", exe, err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("resize ConPTY for %s: %v", exe, err)
	}
	command := terminal.Command(exe, "--no-alt-screen")
	command.Dir = cwd
	command.Env = updatePromptEnvironment(os.Environ(), home)
	if err := command.Start(); err != nil {
		t.Fatalf("spawn %s through ConPTY: %v", exe, err)
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
	ready, earlyExit := waitForGoPTYOutputOrExit(&output, "Update available!", exited, 15*time.Second)
	if !ready {
		if earlyExit != nil {
			t.Fatalf("%s exited before rendering update prompt: %v output=%q", exe, earlyExit, output.String())
		}
		_ = command.Process.Kill()
		t.Fatalf("%s did not render update prompt; output=%q", exe, output.String())
	}
	_ = command.Process.Kill()
	conPTY, ok := terminal.(gopty.ConPty)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		if ok {
			_ = conPTY.InputPipe().Close()
		}
		t.Fatalf("%s did not stop after update prompt capture", exe)
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

func updatePromptEnvironment(environment []string, home string) []string {
	blocked := map[string]bool{
		"CODEX_HOME":            true,
		"CODEX_MANAGED_BY_NPM":  true,
		"CODEX_MANAGED_BY_BUN":  true,
		"CODEX_MANAGED_BY_PNPM": true,
		"OPENAI_API_KEY":        true,
		"TERM":                  true,
	}
	result := make([]string, 0, len(environment)+4)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[strings.ToUpper(key)] {
			result = append(result, entry)
		}
	}
	return append(result,
		"CODEX_HOME="+home,
		"CODEX_MANAGED_BY_NPM=1",
		"OPENAI_API_KEY=sk-test",
		"TERM=xterm-256color",
	)
}
