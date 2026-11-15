package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

func cloudAutomaticTitleModel(t *testing.T, suggestion string, generationErr error) (*Model, *[]string) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-cloud")
	saves := []string{}
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnGenerateCloudThreadTitle: func(_ string, _ RecapThreadOptions, prompt string, _ map[string]any) (string, error) {
			if !strings.Contains(prompt, "Fix the login timeout") {
				t.Fatalf("title prompt = %q", prompt)
			}
			if generationErr != nil {
				return "", generationErr
			}
			return suggestion, nil
		},
		OnSaveCloudThreadName: func(threadID string, title string, expected string) error {
			saves = append(saves, threadID+":"+title+"|"+expected)
			return nil
		},
	})
	return model, &saves
}

// Mirrors Rust #61600: a Cloud conversation generates its title with the Cloud
// account and saves only the final name, not the provisional one.
func TestCloudAutomaticThreadTitleSavesGeneratedTitleLikeRust(t *testing.T) {
	model, saves := cloudAutomaticTitleModel(t, `{"title":"Login timeout fix"}`, nil)
	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "Fix the login timeout",
	})
	if model.State.ThreadName != "Fix the login timeout" {
		t.Fatalf("provisional title = %q", model.State.ThreadName)
	}
	if len(*saves) != 0 {
		t.Fatalf("provisional title was persisted before generation: %#v", *saves)
	}
	runTeaCmd(t, model, cmd)
	if model.State.ThreadName != "Login timeout fix" {
		t.Fatalf("generated title = %q", model.State.ThreadName)
	}
	want := []string{"thread-cloud:Login timeout fix|"}
	if len(*saves) != 1 || (*saves)[0] != want[0] {
		t.Fatalf("saves = %#v, want %#v", *saves, want)
	}
}

// Mirrors Rust #61600: an unavailable or invalid generation persists the
// first-message fallback.
func TestCloudAutomaticThreadTitleSavesFallbackLikeRust(t *testing.T) {
	for name, tc := range map[string]struct {
		suggestion string
		err        error
	}{
		"error":   {err: errors.New("title generation unavailable")},
		"invalid": {suggestion: `{"title":"   "}`},
	} {
		t.Run(name, func(t *testing.T) {
			model, saves := cloudAutomaticTitleModel(t, tc.suggestion, tc.err)
			cmd := model.applyItemCompleted(&protocol.ThreadItem{
				ID:   "user-1",
				Type: "user_message",
				Text: "Fix the login timeout",
			})
			runTeaCmd(t, model, cmd)
			if model.State.ThreadName != "Fix the login timeout" {
				t.Fatalf("fallback title = %q", model.State.ThreadName)
			}
			if len(*saves) != 1 || (*saves)[0] != "thread-cloud:Fix the login timeout|" {
				t.Fatalf("saves = %#v", *saves)
			}
		})
	}
}

// Mirrors Rust #61600: a manual rename cancels the pending Cloud title before
// its write starts.
func TestCloudAutomaticThreadTitlePreservesManualRenameLikeRust(t *testing.T) {
	model, saves := cloudAutomaticTitleModel(t, `{"title":"Generated"}`, nil)
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
	if len(*saves) != 0 {
		t.Fatalf("generated title was saved after the manual rename: %#v", *saves)
	}
}

// Mirrors Rust #61600 cloud_title_shutdown: pending saves get a bounded chance
// to finish, and no save starts after shutdown.
func TestCloudTitleSavesShutdownLikeRust(t *testing.T) {
	saves := &cloudTitleSavesState{}
	if !saves.begin() {
		t.Fatal("first save was refused")
	}
	finished := make(chan struct{})
	go func() {
		saves.shutdown(time.Second)
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("shutdown returned while a save was pending")
	case <-time.After(50 * time.Millisecond):
	}
	saves.end()
	<-finished
	if saves.begin() {
		t.Fatal("save started after shutdown")
	}
}
