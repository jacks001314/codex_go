package tea

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
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

// TestAutomaticThreadTitleUsesFallbackExcerptLikeRust covers Rust #60842:
// the immediate title is a Markdown-stripped single line, capped at 60
// characters, instead of the raw first message.
func TestAutomaticThreadTitleUsesFallbackExcerptLikeRust(t *testing.T) {
	model, renames, _ := automaticTitleModel(t, nil, `{"title":"Generated title"}`)
	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "## Fix the login timeout\n\n" + strings.Repeat("界", 70),
	})
	want := "Fix the login timeout " + strings.Repeat("界", 59-22) + "\u2026"
	if model.State.ThreadName != want {
		t.Fatalf("fallback title = %q, want %q", model.State.ThreadName, want)
	}
	runTeaCmd(t, model, cmd)
	if model.State.ThreadName != "Generated title" {
		t.Fatalf("generated title = %q", model.State.ThreadName)
	}
	if len(*renames) != 2 || (*renames)[0] != "thread-auto:"+want {
		t.Fatalf("renames = %#v", *renames)
	}
}

// TestAutomaticThreadTitleKeepsFallbackWhenGenerationFailsLikeRust covers
// Rust #60842: a failed or invalid title keeps the first-message excerpt.
func TestAutomaticThreadTitleKeepsFallbackWhenGenerationFailsLikeRust(t *testing.T) {
	for name, suggestion := range map[string]string{
		"error":   "",
		"invalid": `{"title":"   "}`,
	} {
		t.Run(name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread-auto")
			renames := []string{}
			model := NewModel(state, Options{
				Width:  100,
				Height: 30,
				OnRenameThread: func(threadID string, rename string) error {
					renames = append(renames, threadID+":"+rename)
					return nil
				},
				OnSuggestThreadName: func(_ string, _ RecapThreadOptions, _ string, _ map[string]any) (string, error) {
					if name == "error" {
						return "", errors.New("title generation failed")
					}
					return suggestion, nil
				},
			})
			cmd := model.applyItemCompleted(&protocol.ThreadItem{
				ID:   "user-1",
				Type: "user_message",
				Text: "# Fix the login timeout",
			})
			if model.State.ThreadName != "Fix the login timeout" {
				t.Fatalf("fallback title = %q", model.State.ThreadName)
			}
			runTeaCmd(t, model, cmd)
			if model.State.ThreadName != "Fix the login timeout" {
				t.Fatalf("fallback title was lost: %q", model.State.ThreadName)
			}
			if len(renames) != 1 || renames[0] != "thread-auto:Fix the login timeout" {
				t.Fatalf("renames = %#v", renames)
			}
		})
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

	model.viewString()
	footerText := strings.Join(model.renderedSelection.regions[renderedRegionFooter].text, "\n")
	statusLine, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineThreadTitle)
	if !ok || statusLine != "Fix the login timeout" {
		t.Fatalf("thread title status line = %q, %v", statusLine, ok)
	}
	for _, frame := range chatwidget.TerminalTitleSpinnerFrames {
		// The settled welcome blossom is braille art, so only the footer,
		// status line, and terminal title must stay spinner-free.
		if strings.Contains(footerText, frame) {
			t.Fatalf("footer rendered title-generation spinner %q\nfooter=%q", frame, footerText)
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
