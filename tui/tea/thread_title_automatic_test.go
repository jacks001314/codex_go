package tea

import (
	"errors"
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
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
