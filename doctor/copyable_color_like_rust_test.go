package doctor

import (
	"bytes"
	"strings"
	"testing"
)

const (
	doctorCopyableDescription = "/tmp/logs_2.sqlite, ~/goals_1.sqlite integrity check; try --summary or `codex doctor`"
	doctorCopyableDetails     = "see ./logs, ../goals. C:\\logs, D:/goals. c:\\logs, \\\\server\\share\\logs, \\\\?\\C:\\logs, \\\\?\\UNC\\server\\share\\logs, \\\\.\\pipe\\codex, \\??\\C:\\logs, \\DosDevices\\C:\\logs, \\rooted. http://localhost:8080: https://example.com; wss://example.com) and `/tmp/my data/logs_2.sqlite`"
)

func TestDoctorCopyableItemsUseCheckStatusLikeRust(t *testing.T) {
	colorOptions := &Options{Color: true, ASCII: true}
	for _, tc := range []struct {
		name      string
		status    CheckStatus
		itemColor int
	}{
		{name: "ok", status: CheckStatusOK, itemColor: 117},
		{name: "warning", status: CheckStatusWarning, itemColor: 117},
		{name: "fail", status: CheckStatusFail, itemColor: 196},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := NewCheck("state.paths", "state", tc.status, doctorCopyableDescription).
				Detail("locations: " + doctorCopyableDetails)
			var rendered strings.Builder
			writeCheck(&rendered, check, colorOptions)
			out := rendered.String()

			if !strings.Contains(out, doctorColor256("/tmp/logs_2.sqlite", tc.itemColor, colorOptions)) {
				t.Fatalf("description path not styled with code %d:\n%q", tc.itemColor, out)
			}
			if !strings.Contains(out, doctorColor256("~/goals_1.sqlite", tc.itemColor, colorOptions)) {
				t.Fatalf("description home path not styled with code %d:\n%q", tc.itemColor, out)
			}
			for _, token := range []string{"--summary", "codex doctor"} {
				if !strings.Contains(out, doctorColor256(token, 117, colorOptions)) {
					t.Fatalf("flag/code token %q should stay cyan:\n%q", token, out)
				}
			}
			for _, item := range []string{"./logs", "../goals", "http://localhost:8080", "https://example.com", "wss://example.com", "/tmp/my data/logs_2.sqlite"} {
				if !strings.Contains(out, doctorColor256(item, tc.itemColor, colorOptions)) {
					t.Fatalf("detail item %q not styled with code %d:\n%q", item, tc.itemColor, out)
				}
			}
			if tc.status != CheckStatusFail && strings.Contains(out, doctorColor256("", 196, colorOptions)) {
				t.Fatalf("non-failing check must not use red:\n%q", out)
			}
			// Backtick delimiters are consumed by the highlighter, as in Rust.
			assertDoctorVisibleText(t, out, strings.ReplaceAll(doctorCopyableDescription, "`", ""))
			assertDoctorVisibleText(t, out, strings.ReplaceAll(doctorCopyableDetails, "`", ""))
		})
	}
}

func TestDoctorCopyableHighlightLeavesPlainTextWhenColorDisabledLikeRust(t *testing.T) {
	for _, status := range []doctorDisplayStatus{doctorDisplayStatusOK, doctorDisplayStatusWarning, doctorDisplayStatusFail} {
		for _, opts := range []*Options{nil, {}, {ASCII: true}, {NoColor: true}} {
			if got := highlightActions(doctorCopyableDescription, status, opts); got != doctorCopyableDescription {
				t.Fatalf("status %v disabled color changed description: %q", status, got)
			}
			if got := styleDetailText(doctorCopyableDetails, status, opts); got != doctorCopyableDetails {
				t.Fatalf("status %v disabled color changed details: %q", status, got)
			}
			if strings.ContainsRune(highlightActions(doctorCopyableDescription, status, opts), '\x1b') {
				t.Fatalf("status %v disabled color emitted ANSI", status)
			}
		}
	}
}

func TestDoctorCopyableHighlightPreservesPunctuationAndWhitespaceLikeRust(t *testing.T) {
	opts := &Options{Color: true}
	const input = "see  ./logs ,  ../goals.  https://example.com)  "

	for _, status := range []doctorDisplayStatus{doctorDisplayStatusOK, doctorDisplayStatusFail} {
		rendered := styleDetailText(input, status, opts)
		if got := stripDoctorANSIForTest(rendered); got != input {
			t.Fatalf("status %v: visible text changed: %q", status, got)
		}
		if visible := stripDoctorANSIForTest(rendered); !strings.Contains(visible, "./logs ,  ../goals.  https://example.com)  ") {
			t.Fatalf("status %v: punctuation/whitespace not preserved: %q", status, visible)
		}
		if !strings.HasSuffix(rendered, "  ") {
			t.Fatalf("status %v: trailing whitespace lost: %q", status, rendered)
		}
	}
	code := styleDetailText("`/tmp/my data/logs_2.sqlite`", doctorDisplayStatusFail, opts)
	if !strings.Contains(code, doctorColor256("/tmp/my data/logs_2.sqlite", 196, opts)) {
		t.Fatalf("backticked path with spaces not styled as one span: %q", code)
	}
}

func TestDoctorLooksCopyableWindowsFormsLikeRust(t *testing.T) {
	windowsCopyable := []string{
		`C:\logs`,
		`D:/goals`,
		`c:\logs`,
		`\\server\share\logs`,
		`\\?\C:\logs`,
		`\\?\UNC\server\share\logs`,
		`\\.\pipe\codex`,
		`\??\C:\logs`,
	}
	for _, text := range windowsCopyable {
		if !looksCopyableOnGOOS(text, "windows") {
			t.Errorf("%q should be copyable on windows", text)
		}
	}
	for _, text := range []string{`\DosDevices\C:\logs`, `\rooted`, `C:logs`, `C:`, `logs`, `DosDevices`} {
		if looksCopyableOnGOOS(text, "windows") {
			t.Errorf("%q should not be copyable on windows", text)
		}
	}
	for _, text := range []string{"http://localhost:8080", "https://example.com", "wss://example.com", "~/goals", "/tmp/logs", "./logs", "../goals"} {
		for _, goos := range []string{"windows", "darwin", "linux"} {
			if !looksCopyableOnGOOS(text, goos) {
				t.Errorf("%q should be copyable on %s", text, goos)
			}
		}
	}
	for _, text := range windowsCopyable {
		if looksCopyableOnGOOS(text, "linux") || looksCopyableOnGOOS(text, "darwin") {
			t.Errorf("%q should not be copyable outside windows", text)
		}
	}
}

func TestDoctorColorEnabledMatchesTerminalSignalsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		terminal bool
		want     bool
	}{
		{name: "xterm terminal", env: map[string]string{"TERM": "xterm-256color"}, terminal: true, want: true},
		{name: "not a terminal", env: map[string]string{"TERM": "xterm-256color"}, terminal: false, want: false},
		{name: "no color env", env: map[string]string{"TERM": "xterm-256color", "NO_COLOR": ""}, terminal: true, want: false},
		{name: "dumb terminal", env: map[string]string{"TERM": "dumb"}, terminal: true, want: false},
		{name: "no color support", env: map[string]string{"TERM": "unknown"}, terminal: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := doctorColorEnabledForEnv(tc.env, tc.terminal); got != tc.want {
				t.Fatalf("doctorColorEnabledForEnv() = %v, want %v", got, tc.want)
			}
		})
	}
	if doctorColorEnabled(&Options{NoColor: true}, &bytes.Buffer{}) {
		t.Fatal("--no-color must disable styling")
	}
	if doctorColorEnabled(&Options{}, &bytes.Buffer{}) {
		t.Fatal("non-file output must stay unstyled")
	}
}

func assertDoctorVisibleText(t *testing.T, rendered string, want string) {
	t.Helper()
	if visible := stripDoctorANSIForTest(rendered); !strings.Contains(visible, want) {
		t.Fatalf("rendered text lost %q:\n%s", want, visible)
	}
}

func stripDoctorANSIForTest(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\x1b' {
			out.WriteByte(value[i])
			continue
		}
		i++
		if i < len(value) && value[i] == '[' {
			for i++; i < len(value) && value[i] != 'm'; i++ {
			}
		}
	}
	return out.String()
}
