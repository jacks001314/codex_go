package tea

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/anim"
	chatwidget "codex_go/tui/chatwidget"
)

func terminalTitleEffectsModel(t *testing.T, animations bool, effects EffectsSettings) (*Model, *[]string) {
	t.Helper()
	writes := []string{}
	model := NewModel(codextui.NewState(&codextui.Options{Model: "gpt-5"}), Options{
		Width:              90,
		Height:             24,
		AnimationsEnabled:  &animations,
		Effects:            &effects,
		TerminalTitleItems: []string{"activity", "project-name"},
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
	})
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	model.now = func() time.Time { return now }
	model.terminalTitleAnimationOrigin = now
	// These tests exercise the first animated write directly without running
	// Model.Init, which normally emits the initial static title.
	model.terminalTitleAnimationReady = false
	return model, &writes
}

// Rust #46832: the terminal-title action-required prefix blinks only while
// animations and effects.title are on; either switch keeps the static prefix.
func TestTerminalTitleActionRequiredBlinkHonorsTitleEffectLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		animations bool
		title      bool
		wantBlink  bool
	}{
		{name: "title effect on", animations: true, title: true, wantBlink: true},
		{name: "title effect off", animations: true, title: false},
		{name: "animations off", animations: false, title: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			effects := DefaultEffectsSettings()
			effects.Title = testCase.title
			model, writes := terminalTitleEffectsModel(t, testCase.animations, effects)
			now := model.currentTime()
			model.modal = &modalState{kind: ModalKindApproval}

			model.Update(anim.TickMsg{Tick: 0})
			if len(*writes) == 0 || !strings.Contains((*writes)[len(*writes)-1], "[ ! ] Action Required") {
				t.Fatalf("static action-required title missing: %#v", *writes)
			}

			model.now = func() time.Time { return now.Add(1100 * time.Millisecond) }
			model.Update(anim.TickMsg{Tick: 1})
			last := (*writes)[len(*writes)-1]
			if gotBlink := strings.Contains(last, "[ . ] Action Required"); gotBlink != testCase.wantBlink {
				t.Fatalf("blink = %v, want %v; writes = %#v", gotBlink, testCase.wantBlink, *writes)
			}
		})
	}
}

// Rust #46832: the terminal-title spinner frame advances under
// `animations && effects.progress` and stays absent when either is off.
func TestTerminalTitleSpinnerHonorsProgressEffectLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		animations bool
		progress   bool
		wantFrame  string
	}{
		{name: "progress on", animations: true, progress: true, wantFrame: "⠋"},
		{name: "progress off", animations: true, progress: false},
		{name: "animations off", animations: false, progress: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			effects := DefaultEffectsSettings()
			effects.Progress = testCase.progress
			model, writes := terminalTitleEffectsModel(t, testCase.animations, effects)
			model.State.Status = "running"

			model.Update(anim.TickMsg{Tick: 0})
			if len(*writes) == 0 {
				t.Fatal("running task did not refresh the terminal title")
			}
			last := (*writes)[len(*writes)-1]
			hasFrame := strings.Contains(last, "⠋")
			if testCase.wantFrame != "" && !hasFrame {
				t.Fatalf("spinner frame missing: %q", last)
			}
			if testCase.wantFrame == "" && hasFrame {
				t.Fatalf("spinner frame rendered with the effect off: %q", last)
			}

			if testCase.wantFrame != "" {
				now := model.currentTime()
				model.now = func() time.Time { return now.Add(100 * time.Millisecond) }
				model.Update(anim.TickMsg{Tick: 1})
				model.now = func() time.Time { return now.Add(time.Second) }
				model.Update(anim.TickMsg{Tick: 2})
				last = (*writes)[len(*writes)-1]
				if !strings.Contains((*writes)[len(*writes)-2], "⠙") || !strings.Contains(last, "⠋") {
					t.Fatalf("spinner frames = %#v, want ⠙ then ⠋", *writes)
				}
			}
		})
	}
}

// Rust writes its default terminal title without requiring an explicit
// `tui.terminal_title` configuration. The default order is activity,
// thread-name, project-name; an unnamed thread omits only thread-name.
func TestModelDefaultTerminalTitleMatchesRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.CWD = `D:\repo`
	state.ThreadName = "Fix login timeout"
	state.Status = "running"
	writes := []string{}
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
	})
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	model.now = func() time.Time { return now }
	model.terminalTitleAnimationOrigin = now
	model.terminalTitleAnimationReady = false

	model.Update(anim.TickMsg{Tick: 0})
	if len(writes) == 0 {
		t.Fatal("default terminal title was not written")
	}
	if got, want := writes[len(writes)-1], "\x1b]0;⠋ Fix login timeout | repo\x07"; got != want {
		t.Fatalf("default title = %q, want %q (writes %#v)", got, want, writes)
	}
}

// Rust derives the spinner frame from elapsed wall-clock time. A skipped or
// delayed animation tick must not freeze or rewind the visible frame.
func TestTerminalTitleSpinnerUsesElapsedTimeNotTickCountLikeRust(t *testing.T) {
	model, writes := terminalTitleEffectsModel(t, true, DefaultEffectsSettings())
	model.State.Status = "running"
	origin := model.currentTime()

	model.Update(anim.TickMsg{Tick: 0})
	model.now = func() time.Time { return origin.Add(250 * time.Millisecond) }
	model.Update(anim.TickMsg{Tick: 1})
	model.now = func() time.Time { return origin.Add(1050 * time.Millisecond) }
	model.Update(anim.TickMsg{Tick: 2})

	if len(*writes) < 3 {
		t.Fatalf("terminal title writes = %#v", *writes)
	}
	if !strings.Contains((*writes)[len(*writes)-2], "⠹") {
		t.Fatalf("250ms frame = %q, want ⠹", (*writes)[len(*writes)-2])
	}
	if !strings.Contains((*writes)[len(*writes)-1], "⠋") {
		t.Fatalf("1050ms frame = %q, want ⠋", (*writes)[len(*writes)-1])
	}
}

func TestClearTerminalTitleOnExitMatchesRust(t *testing.T) {
	model := &Model{lastTerminalTitleSequence: "\x1b]0;repo\x07"}
	var output bytes.Buffer
	clearTerminalTitleOnExit(model, &output)
	if got, want := output.String(), codextui.ClearTerminalTitleOSC(); got != want {
		t.Fatalf("exit clear = %q, want %q", got, want)
	}

	output.Reset()
	model.lastTerminalTitleSequence = codextui.ClearTerminalTitleOSC()
	clearTerminalTitleOnExit(model, &output)
	if output.Len() != 0 {
		t.Fatalf("already-cleared title wrote %q", output.String())
	}
}

// TestTerminalTitleProjectNameUsesRepoRootLikeRust covers the terminal-title
// project segment: Rust shows the repository root name even when cwd is a
// nested directory, and truncates the segment to 24 graphemes.
func TestTerminalTitleProjectNameUsesRepoRootLikeRust(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	state := codextui.NewState(nil)
	state.CWD = nested
	writes := []string{}
	model := NewModel(state, Options{
		Width:              90,
		Height:             24,
		TerminalTitleItems: []string{"project-name"},
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
	})
	// This test starts from an already-initialized animation phase, so force
	// the first animated refresh without running Model.Init.
	model.terminalTitleAnimationReady = false
	model.Update(anim.TickMsg{Tick: 0})
	if len(writes) == 0 {
		t.Fatal("project terminal title was not written")
	}
	if got, want := writes[len(writes)-1], "\x1b]0;"+filepath.Base(root)+"\x07"; got != want {
		t.Fatalf("project title = %q, want %q", got, want)
	}
}

// TestTerminalTitleWriterBypassesAlternateScreenSuppression proves the default
// title writer reaches the terminal output directly. Bubble Tea's Printf and
// Println commands are ignored while the alternate screen is active, whereas
// Rust manages the title in both modes.
func TestTerminalTitleWriterBypassesAlternateScreenSuppression(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 90, Height: 24})
	var output bytes.Buffer
	model.petRuntime.setOutput(&output)
	model.terminalTitleWriter = model.writeTerminalTitleSequence

	cmd := model.applyTerminalTitleResult(chatwidget.StatusSurfaceRefreshResult{
		TerminalTitleRendered: true,
		TerminalTitleText:     "repo",
	})
	if cmd != nil {
		t.Fatalf("direct terminal-title writer returned a command: %#v", cmd)
	}
	if got, want := output.String(), "\x1b]0;repo\x07"; got != want {
		t.Fatalf("direct title write = %q, want %q", got, want)
	}
}

// A rename must repaint both the thread-name status item and the default
// terminal title immediately, not wait for the next turn-level refresh.
func TestThreadRenameRefreshesTerminalTitleLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-rename")
	writes := []string{}
	model := NewModel(state, Options{
		Width:              90,
		Height:             24,
		TerminalTitleItems: []string{"thread-name"},
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
	})
	runTeaCmd(t, model, model.renameCurrentThread("Fix login timeout"))
	if len(writes) == 0 {
		t.Fatal("rename did not refresh the terminal title")
	}
	if got, want := writes[len(writes)-1], "\x1b]0;Fix login timeout\x07"; got != want {
		t.Fatalf("renamed title = %q, want %q", got, want)
	}
}
