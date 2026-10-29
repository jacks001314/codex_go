package tea

import (
	"regexp"
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
)

var ansiSequenceRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func TestModelTerminalSnapshots(t *testing.T) {
	t.Run("wide region chrome", func(t *testing.T) {
		state := codextui.NewState(&codextui.Options{
			Model:           "gpt-5",
			ApprovalPolicy:  "on-request",
			Sandbox:         "workspace-write",
			ReasoningEffort: "high",
		})
		state.SetThreadID("thread-wide")
		state.AddMessage(codextui.RoleUser, "inspect the workspace")
		state.AddMessage(codextui.RoleAssistant, "I am checking the files.")
		model := NewModel(state, Options{Width: 104, Height: 20})

		view := model.View()
		got := normalizeTerminalSnapshot(view)
		// Rust has no app-level chrome rows: the transcript owns the viewport,
		// the composer is a shaded full-width block, and the status line sits
		// directly below it.
		for _, want := range []string{
			"› inspect the workspace",
			"• I am checking the files.",
			"› Ask Codex to do anything",
			"  gpt-5 high · ",
			"  ? for shortcuts",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("wide snapshot missing %q:\n%s", want, got)
			}
		}
		for _, unwanted := range []string{"Thread:", "ACTIVITY", "╭", "> Ask gcode"} {
			if strings.Contains(got, unwanted) {
				t.Fatalf("wide snapshot kept legacy chrome %q:\n%s", unwanted, got)
			}
		}
		// The frame must fill the terminal exactly. normalizeTerminalSnapshot
		// trims the blank row Rust paints before the first transcript cell, so
		// measure the raw view for the row budget.
		raw := strings.ReplaceAll(view, "\r\n", "\n")
		if lines := strings.Count(raw, "\n") + 1; lines != 20 {
			t.Fatalf("wide snapshot rows = %d, want 20:\n%s", lines, got)
		}
		for _, line := range strings.Split(got, "\n") {
			if len([]rune(line)) > 104 {
				t.Fatalf("wide snapshot line exceeds terminal width: %d: %q", len([]rune(line)), line)
			}
		}
	})

	t.Run("main view", func(t *testing.T) {
		state := codextui.NewState(&codextui.Options{
			Model:           "gpt-5",
			ApprovalPolicy:  "on-request",
			Sandbox:         "workspace-write",
			ReasoningEffort: "high",
			Search:          true,
		})
		state.SetThreadID("thread-snap")
		state.AddMessage(codextui.RoleUser, "summarize the repo")
		state.AddMessage(codextui.RoleAssistant, "The repo has a Go TUI shell.")
		model := NewModel(state, Options{Width: 82, Height: 18})

		assertTerminalSnapshot(t, model.View(), `
› summarize the repo


• The repo has a Go TUI shell.









› Ask Codex to do anything

  gpt-5 high · D:\qax\reagent\dev\codex_go\tui\tea
  ? for shortcuts`)
	})

	t.Run("approval modal", func(t *testing.T) {
		model := NewModel(codextui.NewState(&codextui.Options{Model: "gpt-5"}), Options{Width: 76, Height: 18})
		model.Update(ApprovalRequestMsg{
			ID:      "approval-snap",
			Title:   "Run command?",
			Body:    "Reason: needs tests\nWorking directory: D:\\repo",
			Command: "go test ./...",
		})

		assertTerminalSnapshot(t, model.View(), `
No messages yet.








  Run command?

  Reason: needs tests
  Working directory: D:\repo
  $ go test ./...
› 1. Yes, proceed (y)
  2. Yes, and don't ask again for this command in this session (a)
  3. No, and tell Codex what to do differently (esc)
  Press enter to confirm or esc to cancel`)
	})

	t.Run("session picker", func(t *testing.T) {
		model := NewModel(nil, Options{
			Width:            86,
			Height:           18,
			SessionPickerCWD: `D:\repo`,
			SessionPickerItems: []codextui.SessionSummary{
				{
					ThreadID:  "thread-new",
					Title:     "Newer Session",
					CWD:       `D:\repo`,
					Provider:  "openai",
					UpdatedAt: time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC),
				},
				{
					ThreadID:  "thread-old",
					Title:     "Older Session",
					CWD:       `D:\other`,
					Provider:  "openai",
					UpdatedAt: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
				},
			},
		})
		model.now = func() time.Time {
			return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
		}
		model.openSessionPicker(codextui.SessionPickerResume)

		view := normalizeTerminalSnapshot(model.View())
		if !strings.HasPrefix(view, "Resume a previous session\n\nType to search") {
			t.Fatalf("session picker should start at the top of its full-screen surface:\n%s", view)
		}
		for _, want := range []string{"Newer Session", "1 / 1", "enter resume", codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "o comfy"} {
			if !strings.Contains(view, want) {
				t.Fatalf("session picker snapshot missing %q:\n%s", want, view)
			}
		}
		for _, unwanted := range []string{"Thread: new", "No messages yet.", footerHelpText} {
			if strings.Contains(view, unwanted) {
				t.Fatalf("full-screen session picker contains chat surface %q:\n%s", unwanted, view)
			}
		}
	})

	t.Run("request user input", func(t *testing.T) {
		timeout := 60000
		model := NewModel(nil, Options{Width: 78, Height: 18})
		model.Update(RequestUserInputMsg{
			ID: "input-snap",
			Questions: []codextui.RequestUserInputQuestion{
				{
					ID:       "scope",
					Header:   "Scope",
					Question: "Where should this apply?",
					Options: []codextui.RequestUserInputChoice{
						{Label: "Plan", Description: "Only update the plan."},
						{Label: "All", Description: "Apply everywhere."},
					},
				},
			},
			AutoResolutionMS: &timeout,
		})

		assertTerminalSnapshot(t, model.View(), `
No messages yet.








  Request user input

  Scope
  Question 1/1 (1 unanswered) · auto-resolves in 1m 00s
  Where should this apply?

  › 1. Plan  Only update the plan.
    2. All   Apply everywhere.
  Esc cancel | Enter choose`)
	})
}

func assertTerminalSnapshot(t *testing.T, view string, want string) {
	t.Helper()
	got := normalizeTerminalSnapshot(view)
	want = strings.TrimSpace(strings.ReplaceAll(want, "\r\n", "\n"))
	if want == "" {
		t.Fatalf("missing snapshot; got:\n%s", got)
	}
	if got != want {
		t.Fatalf("snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func normalizeTerminalSnapshot(view string) string {
	view = ansiSequenceRE.ReplaceAllString(view, "")
	view = strings.ReplaceAll(view, "\r\n", "\n")
	lines := strings.Split(view, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
