//go:build windows

package tea

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	gopty "github.com/aymanbagabas/go-pty"
)

const sideNavigationDiffEnv = "CODEX_TUI_SIDE_DIFF"

// TestSideConversationSurvivesThreadNavigationPTY is the real-terminal
// differential for Rust #52184. It drives the same prompt/side/new/resume/side
// sequence through both CLIs against a deterministic local Responses API and
// requires the retained side transcript to reappear without a new fork.
func TestSideConversationSurvivesThreadNavigationPTY(t *testing.T) {
	if os.Getenv(sideNavigationDiffEnv) != "1" {
		t.Skipf("set %s=1 to run the Rust/Go side-conversation ConPTY diff", sideNavigationDiffEnv)
	}
	rustExe := strings.TrimSpace(os.Getenv("CODEX_RUST_EXE"))
	if rustExe == "" {
		rustExe = `D:\qax\reagent\dev\git\codex\codex-rs\target\release\codex.exe`
	}
	goExe := strings.TrimSpace(os.Getenv("CODEX_GO_EXE"))
	if goExe == "" {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("get cwd: %v", err)
		}
		goExe = filepath.Clean(filepath.Join(cwd, "..", "..", "bin", "codex.exe"))
	}
	for _, exe := range []string{rustExe, goExe} {
		if _, err := os.Stat(exe); err != nil {
			t.Fatalf("required executable %s: %v", exe, err)
		}
	}

	server := newDeterministicResponsesServer(t)
	defer server.Close()

	implementations := []struct {
		name string
		exe  string
	}{
		{name: "rust", exe: rustExe},
		{name: "go", exe: goExe},
	}
	if selected := strings.TrimSpace(os.Getenv("CODEX_TUI_SIDE_IMPL")); selected != "" {
		filtered := implementations[:0]
		for _, implementation := range implementations {
			if implementation.name == selected {
				filtered = append(filtered, implementation)
			}
		}
		implementations = filtered
	}
	if len(implementations) == 0 {
		t.Fatalf("CODEX_TUI_SIDE_IMPL=%q matches neither rust nor go", os.Getenv("CODEX_TUI_SIDE_IMPL"))
	}
	for _, implementation := range implementations {
		output := runSideNavigationPTY(t, implementation.name, implementation.exe, server.URL)
		if !strings.Contains(output, "SIDE_OK") {
			t.Fatalf("%s did not reopen the retained side transcript", implementation.name)
		}
	}
}

func newDeterministicResponsesServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		body := string(bodyBytes)
		answer := "OK"
		switch {
		case strings.Contains(body, "Generate a concise, single-line task title"):
			answer = `{"title":"Side navigation"}`
		case strings.Contains(body, "followup prompt"):
			answer = `- :codex-followup[Inspect **items**]{prompt="private"}`
		case strings.Contains(body, "after resume"):
			answer = "RESUMED_OK"
		case strings.Contains(body, "side prompt"):
			answer = "SIDE_OK"
		case strings.Contains(body, "parent prompt"):
			answer = "PARENT_OK"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		payloads := []string{
			`{"type":"response.created","response":{"id":"resp-side"}}`,
			fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg-side","delta":%s}`, strconv.Quote(answer)),
			fmt.Sprintf(`{"type":"response.output_item.done","item":{"id":"msg-side","type":"message","role":"assistant","content":[{"type":"output_text","text":%s}]}}`, strconv.Quote(answer)),
			fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-side","output":[{"id":"msg-side","type":"message","role":"assistant","content":[{"type":"output_text","text":%s}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, strconv.Quote(answer)),
		}
		for index, payload := range payloads {
			var event struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(payload), &event)
			_, _ = fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event.Type, index+1, payload)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

func runSideNavigationPTY(t *testing.T, impl string, exe string, baseURL string) string {
	t.Helper()
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
`, baseURL+"/v1", strconv.Quote(workspace))
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("%s write config: %v", impl, err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("%s write auth: %v", impl, err)
	}
	runSeedParentPTY(t, impl, exe, home, workspace)
	parentThreadID := newestRolloutThreadID(t, home)

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("%s create ConPTY: %v", impl, err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("%s resize ConPTY: %v", impl, err)
	}
	command := terminal.Command(exe, "--no-alt-screen", "--no-daemon")
	command.Dir = workspace
	command.Env = append(os.Environ(),
		"CODEX_HOME="+home,
		"OPENAI_API_KEY=sk-test",
		"TERM=xterm-256color",
		"NO_COLOR=1",
	)
	if err := command.Start(); err != nil {
		t.Fatalf("%s start through ConPTY: %v", impl, err)
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
	wait := func(needle string, timeout time.Duration) {
		t.Helper()
		ok, earlyExit := waitForGoPTYOutputOrExit(&output, needle, exited, timeout)
		if ok {
			return
		}
		captured := stop()
		if earlyExit != nil {
			t.Fatalf("%s exited before %q: %v output=%q", impl, needle, earlyExit, captured)
		}
		t.Fatalf("%s did not render %q; output=%q", impl, needle, captured)
	}
	waitSince := func(start int, needle string, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			captured := output.String()
			if len(captured) > start && strings.Contains(captured[start:], needle) {
				return
			}
			select {
			case earlyExit := <-exited:
				t.Fatalf("%s exited before %q: %v output=%q", impl, needle, earlyExit, output.String())
			case <-time.After(50 * time.Millisecond):
			}
		}
		captured := stop()
		t.Fatalf("%s did not render %q after the navigation step; output=%q", impl, needle, captured)
	}
	write := func(value string) {
		t.Helper()
		if _, err := terminal.Write([]byte(value)); err != nil {
			captured := stop()
			t.Fatalf("%s write %q: %v output=%q", impl, value, err, captured)
		}
	}

	startupMarker := "OpenAI Codex"
	if impl == "go" {
		startupMarker = "gcode"
	}
	wait(startupMarker, 20*time.Second)
	firstResume := len(output.String())
	write("/resume " + parentThreadID + "\r")
	waitSince(firstResume, "PARENT_OK", 20*time.Second)

	sideStart := len(output.String())
	write("/side\r")
	waitSince(sideStart, "Side from main thread", 15*time.Second)
	write("side prompt\r")
	wait("SIDE_OK", 30*time.Second)

	toggleStart := len(output.String())
	write("\x1f")
	waitSince(toggleStart, "ctrl+/ for side", 10*time.Second)
	write("/new\r")
	time.Sleep(750 * time.Millisecond)
	resumeStart := len(output.String())
	write("/resume " + parentThreadID + "\r")
	waitSince(resumeStart, "PARENT_OK", 20*time.Second)
	write("after resume\r")
	wait("RESUMED_OK", 30*time.Second)
	reopenStart := len(output.String())
	write("/side\r")
	waitSince(reopenStart, "SIDE_OK", 15*time.Second)
	return stop()
}

func runSeedParentPTY(t *testing.T, impl string, exe string, home string, workspace string) {
	t.Helper()
	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("%s seed create ConPTY: %v", impl, err)
	}
	if err := terminal.Resize(120, 36); err != nil {
		t.Fatalf("%s seed resize ConPTY: %v", impl, err)
	}
	command := terminal.Command(exe, "--no-alt-screen", "--no-daemon")
	command.Dir = workspace
	command.Env = append(os.Environ(),
		"CODEX_HOME="+home,
		"OPENAI_API_KEY=sk-test",
		"TERM=xterm-256color",
		"NO_COLOR=1",
	)
	if err := command.Start(); err != nil {
		t.Fatalf("%s seed start through ConPTY: %v", impl, err)
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
	startupMarker := "OpenAI Codex"
	if impl == "go" {
		startupMarker = "gcode"
	}
	ready, earlyExit := waitForGoPTYOutputOrExit(&output, startupMarker, exited, 20*time.Second)
	if !ready {
		_ = command.Process.Kill()
		t.Fatalf("%s seed did not render: %v output=%q", impl, earlyExit, output.String())
	}
	if _, err := terminal.Write([]byte("parent prompt\r")); err != nil {
		_ = command.Process.Kill()
		t.Fatalf("%s seed write parent prompt: %v", impl, err)
	}
	answered, earlyExit := waitForGoPTYOutputOrExit(&output, "PARENT_OK", exited, 30*time.Second)
	if !answered {
		_ = command.Process.Kill()
		t.Fatalf("%s seed parent did not answer: %v output=%q", impl, earlyExit, output.String())
	}
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
}

func newestRolloutThreadID(t *testing.T, home string) string {
	t.Helper()
	var newestPath string
	var newestTime time.Time
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			info, statErr := entry.Info()
			if statErr == nil && !info.ModTime().Before(newestTime) {
				newestTime = info.ModTime()
				newestPath = path
			}
			return nil
		})
		if newestPath != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if newestPath == "" {
		t.Fatalf("no rollout appeared under %s", home)
	}
	name := strings.TrimSuffix(filepath.Base(newestPath), ".jsonl")
	for i := len(name) - 36; i >= 0; i-- {
		candidate := name[i : i+36]
		if validUUID(candidate) {
			return candidate
		}
	}
	raw, err := os.ReadFile(newestPath)
	if err != nil {
		t.Fatalf("read rollout %s: %v", newestPath, err)
	}
	var line struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(string(raw), "\n", 2)[0]), &line); err == nil && line.Type == "session_meta" {
		var meta struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(line.Payload, &meta) == nil && meta.ID != "" {
			return meta.ID
		}
	}
	t.Fatalf("could not extract thread id from %s", newestPath)
	return ""
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !unicode.Is(unicode.ASCII_Hex_Digit, char) {
				return false
			}
		}
	}
	return true
}
