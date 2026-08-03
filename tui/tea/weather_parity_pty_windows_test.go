//go:build windows

package tea

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"

	"codex_go/utils"
)

const weatherTUIParityEnv = "CODEX_GO_TUI_WEATHER_PARITY"
const weatherTUIParityTimeoutEnv = "CODEX_GO_TUI_WEATHER_TIMEOUT"

func TestWeatherTUIParityWithConPTY(t *testing.T) {
	if os.Getenv(weatherTUIParityEnv) != "1" {
		t.Skipf("set %s=1 to run Rust vs Go weather TUI parity through ConPTY", weatherTUIParityEnv)
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

	const prompt = "告诉我云南各个城市的天气"
	cities := []string{"昆明", "曲靖", "玉溪", "保山", "昭通", "丽江", "普洱", "临沧", "楚雄", "红河", "文山", "西双版纳", "大理", "德宏", "怒江", "迪庆"}
	for _, implementation := range []string{"rust", "go"} {
		implementation := implementation
		t.Run(implementation, func(t *testing.T) {
			raw, home := runWeatherTUIParityPTY(t, implementation, binaries[implementation], repoRoot, prompt)
			clean := utils.StripANSI(raw)
			writeWeatherTUIArtifact(t, "run2-"+implementation+"-raw.ansi", raw)
			writeWeatherTUIArtifact(t, "run2-"+implementation+"-text.txt", clean)
			summary := weatherSessionSummaryFromHome(home)
			writeWeatherTUIArtifact(t, "run2-"+implementation+"-answer.txt", summary.FinalAnswer+"\n")
			writeWeatherTUIArtifact(t, "run2-"+implementation+"-tools.txt", strings.Join(summary.ToolCalls, "\n")+"\n")
			if !strings.Contains(clean, prompt) {
				t.Fatalf("%s TUI output missing exact prompt %q", implementation, prompt)
			}
			if strings.TrimSpace(summary.FinalAnswer) == "" {
				t.Fatalf("%s session did not persist a final answer", implementation)
			}
			for _, city := range cities {
				if !strings.Contains(summary.FinalAnswer, city) {
					t.Fatalf("%s final answer missing Yunnan prefecture-level region %q: %s", implementation, city, summary.FinalAnswer)
				}
			}
		})
	}
}

func runWeatherTUIParityPTY(t *testing.T, implementation string, binary string, cwd string, prompt string) (string, string) {
	t.Helper()
	home := t.TempDir()
	preserveWeatherTUIHome(t, implementation, home)
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
		"--search",
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
		writeWeatherTUIArtifact(t, implementation+"-failure-raw.ansi", raw)
		writeWeatherTUIArtifact(t, implementation+"-failure-text.txt", utils.StripANSI(raw))
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
		t.Fatalf("write weather prompt to %s: %v", binary, err)
	}
	if !waitForWeatherFinalAnswer(home, exited, weatherTUIParityTimeout(t)) {
		_ = command.Process.Kill()
		t.Fatalf("%s did not complete the weather prompt", binary)
	}
	time.Sleep(time.Second)
	_, _ = terminal.Write([]byte("/exit\r"))
	select {
	case <-exited:
	case <-time.After(8 * time.Second):
		_ = command.Process.Kill()
		<-exited
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
	return output.String(), home
}

func waitForWeatherFinalAnswer(home string, exited <-chan error, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.TrimSpace(weatherSessionSummaryFromHome(home).FinalAnswer) != "" {
			return true
		}
		select {
		case <-exited:
			return strings.TrimSpace(weatherSessionSummaryFromHome(home).FinalAnswer) != ""
		case <-ticker.C:
		case <-deadline.C:
			return strings.TrimSpace(weatherSessionSummaryFromHome(home).FinalAnswer) != ""
		}
	}
}

type weatherSessionSummary struct {
	FinalAnswer string
	ToolCalls   []string
}

func weatherSessionSummaryFromHome(home string) weatherSessionSummary {
	summary := weatherSessionSummary{}
	_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			var item any
			if json.Unmarshal([]byte(line), &item) != nil {
				continue
			}
			collectWeatherSessionItem(item, &summary)
		}
		return nil
	})
	sort.Strings(summary.ToolCalls)
	return summary
}

func collectWeatherSessionItem(value any, summary *weatherSessionSummary) {
	switch typed := value.(type) {
	case map[string]any:
		if phase, _ := typed["phase"].(string); phase == "final_answer" {
			if text := weatherTextFromAny(typed["content"]); strings.TrimSpace(text) != "" {
				summary.FinalAnswer = strings.TrimSpace(text)
			}
		}
		if kind, _ := typed["type"].(string); kind == "function_call" || kind == "custom_tool_call" || kind == "web_search_call" {
			name, _ := typed["name"].(string)
			namespace, _ := typed["namespace"].(string)
			if name == "" {
				name = kind
			}
			if namespace != "" {
				name = namespace + "." + name
			}
			summary.ToolCalls = append(summary.ToolCalls, name)
		}
		for _, child := range typed {
			collectWeatherSessionItem(child, summary)
		}
	case []any:
		for _, child := range typed {
			collectWeatherSessionItem(child, summary)
		}
	}
}

func weatherTextFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if text, _ := typed["text"].(string); text != "" {
			return text
		}
		parts := make([]string, 0, len(typed))
		for _, child := range typed {
			if text := weatherTextFromAny(child); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, child := range typed {
			if text := weatherTextFromAny(child); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func weatherTUIParityTimeout(t *testing.T) time.Duration {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(weatherTUIParityTimeoutEnv))
	if value == "" {
		return 5 * time.Minute
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		t.Fatalf("parse %s=%q as positive duration: %v", weatherTUIParityTimeoutEnv, value, err)
	}
	return timeout
}

func preserveWeatherTUIHome(t *testing.T, implementation string, home string) {
	t.Helper()
	directory := strings.TrimSpace(os.Getenv("CODEX_TUI_WEATHER_ARTIFACT_DIR"))
	if directory == "" {
		return
	}
	t.Cleanup(func() {
		target := filepath.Join(directory, implementation+"-codex-home-"+time.Now().Format("20060102-150405.000000000"))
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Errorf("create preserved %s weather TUI home: %v", implementation, err)
			return
		}
		for _, name := range []string{"sessions", "archived_sessions", "log", "logs"} {
			source := filepath.Join(home, name)
			if info, err := os.Stat(source); err == nil && info.IsDir() {
				if err := os.CopyFS(filepath.Join(target, name), os.DirFS(source)); err != nil {
					t.Errorf("preserve %s weather TUI %s: %v", implementation, name, err)
				}
			}
		}
	})
}

func writeWeatherTUIArtifact(t *testing.T, name string, content string) {
	t.Helper()
	directory := strings.TrimSpace(os.Getenv("CODEX_TUI_WEATHER_ARTIFACT_DIR"))
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create weather TUI artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write weather TUI artifact %s: %v", name, err)
	}
}
