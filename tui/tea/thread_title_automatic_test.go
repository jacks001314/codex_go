package tea

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func automaticTitleModel(t *testing.T, renameErr error, suggestion string) (*Model, *[]string, *int) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-auto")
	renames := []string{}
	generations := 0
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnRenameThread: func(threadID string, name string) error {
			if renameErr != nil {
				return renameErr
			}
			renames = append(renames, threadID+":"+name)
			return nil
		},
		OnSuggestThreadName: func(_ string, _ RecapThreadOptions, prompt string, _ map[string]any) (string, error) {
			generations++
			if !strings.Contains(prompt, "Fix") || !strings.Contains(prompt, "login timeout") {
				t.Fatalf("title prompt = %q", prompt)
			}
			return suggestion, nil
		},
	})
	return model, &renames, &generations
}

// TestAutomaticThreadTitleReplacesProvisionalLikeRust covers Rust #40492:
// the first user message sets a provisional title and the generated title
// replaces it silently when it is still current.
func TestAutomaticThreadTitleReplacesProvisionalLikeRust(t *testing.T) {
	model, renames, generations := automaticTitleModel(t, nil, `{"title":"Fix login timeout"}`)
	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "  Fix   the login timeout  ",
	})
	if model.State.ThreadName != "Fix the login timeout" {
		t.Fatalf("provisional title = %q", model.State.ThreadName)
	}
	runTeaCmd(t, model, cmd)
	if *generations != 1 {
		t.Fatalf("generations = %d, want one", *generations)
	}
	if model.State.ThreadName != "Fix login timeout" {
		t.Fatalf("generated title = %q", model.State.ThreadName)
	}
	want := []string{"thread-auto:Fix the login timeout", "thread-auto:Fix login timeout"}
	if len(*renames) != 2 || (*renames)[0] != want[0] || (*renames)[1] != want[1] {
		t.Fatalf("renames = %#v, want %#v", *renames, want)
	}
	if strings.Contains(modelMessageText(model), "Thread renamed to") {
		t.Fatalf("automatic rename leaked into transcript: %q", modelMessageText(model))
	}
}

// TestAutomaticThreadTitlePreservesManualRenameLikeRust covers the manual
// rename race: a generated title is discarded once the provisional is gone.
func TestAutomaticThreadTitlePreservesManualRenameLikeRust(t *testing.T) {
	model, renames, _ := automaticTitleModel(t, nil, `{"title":"Generated title"}`)
	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "Fix the login timeout",
	})
	model.renameCurrentThread("Manual title")
	runTeaCmd(t, model, cmd)
	if model.State.ThreadName != "Manual title" {
		t.Fatalf("manual title was overwritten: %q", model.State.ThreadName)
	}
	for _, rename := range *renames {
		if strings.HasSuffix(rename, ":Generated title") {
			t.Fatalf("generated title was persisted after manual rename: %#v", *renames)
		}
	}
}

// TestAutomaticThreadTitleRenameFailureClearsProvisionalLikeRust covers a
// failed provisional persist without leaving a fake title.
func TestAutomaticThreadTitleRenameFailureClearsProvisionalLikeRust(t *testing.T) {
	model, _, _ := automaticTitleModel(t, errors.New("rename failed"), `{"title":"Generated title"}`)
	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "Fix the login timeout",
	})
	runTeaCmd(t, model, cmd)
	if model.State.ThreadName != "" {
		t.Fatalf("failed provisional rename left title %q", model.State.ThreadName)
	}
}

// TestProvisionalThreadTitleBoundsLikeRust covers normalization and the
// 36-rune limit.
func TestProvisionalThreadTitleBoundsLikeRust(t *testing.T) {
	if got := tuiapp.ProvisionalThreadTitle("/status"); got != "" {
		t.Fatalf("slash command provisional title = %q", got)
	}
	got := tuiapp.ProvisionalThreadTitle(strings.Repeat("界", 40))
	if len([]rune(got)) != 36 {
		t.Fatalf("provisional runes = %d", len([]rune(got)))
	}
}

// TestAutomaticThreadTitlePendingShowsNoProgressSpinnerLikeRust covers Rust
// #55324: automatic naming stays in the background, and the footer, status
// line, and terminal title carry no title-generation spinner while pending.
func TestAutomaticThreadTitlePendingShowsNoProgressSpinnerLikeRust(t *testing.T) {
	model, renames, generations := automaticTitleModel(t, nil, `{"title":"Fix login timeout"}`)
	model.State.CWD = filepath.Join(t.TempDir(), "project")
	model.State.Model = "gpt-5.2"
	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "Fix the login timeout",
	})
	if cmd == nil || !model.automaticThreadTitlePending {
		t.Fatalf("automatic title did not start: cmd=%v pending=%v", cmd != nil, model.automaticThreadTitlePending)
	}
	model.refreshStatusControls()

	view := utils.StripANSI(model.View())
	statusLine, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineThreadTitle)
	if !ok || statusLine != "Fix the login timeout" {
		t.Fatalf("thread title status line = %q, %v", statusLine, ok)
	}
	for _, frame := range chatwidget.TerminalTitleSpinnerFrames {
		if strings.Contains(view, frame) {
			t.Fatalf("footer rendered title-generation spinner %q:\n%s", frame, view)
		}
		if strings.Contains(statusLine, frame) {
			t.Fatalf("status line rendered title-generation spinner: %q", statusLine)
		}
		if strings.Contains(model.statusControls.LastTerminalTitle, frame) {
			t.Fatalf("terminal title rendered title-generation spinner: %q", model.statusControls.LastTerminalTitle)
		}
	}

	runTeaCmd(t, model, cmd)
	if *generations != 1 {
		t.Fatalf("generations = %d, want one", *generations)
	}
	if model.State.ThreadName != "Fix login timeout" {
		t.Fatalf("generated title = %q", model.State.ThreadName)
	}
	if len(*renames) != 2 || (*renames)[1] != "thread-auto:Fix login timeout" {
		t.Fatalf("renames = %#v", *renames)
	}
}
