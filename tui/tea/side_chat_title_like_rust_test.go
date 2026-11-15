package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestSavedSideChatTitleUsesFirstDirectInputLikeRust covers Rust #61343: a
// saved side chat inherits its parent's name, but its first successful direct
// text input replaces that name with an automatic title.
func TestSavedSideChatTitleUsesFirstDirectInputLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("side-1")
	state.SetThreadName("Release plan")
	renames := []string{}
	generations := 0
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnRenameThread: func(threadID string, name string) error {
			renames = append(renames, threadID+":"+name)
			return nil
		},
		OnSuggestThreadName: func(_ string, _ RecapThreadOptions, prompt string, _ map[string]any) (string, error) {
			generations++
			if !strings.Contains(prompt, "Why is CI failing on Windows?") {
				t.Fatalf("title prompt = %q", prompt)
			}
			return `{"title":"Windows CI failure"}`, nil
		},
	})
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-1",
		SideThreadID:   "side-1",
		Storage:        SideChatStorageAttached,
		InheritedName:  "Release plan",
		TitleEligible:  true,
		ShowingSide:    true,
	}

	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "Why is CI failing on Windows?",
	})
	runTeaCmd(t, model, cmd)

	if generations != 1 {
		t.Fatalf("generations = %d, want one", generations)
	}
	if model.State.ThreadName != "Windows CI failure" {
		t.Fatalf("side chat title = %q", model.State.ThreadName)
	}
	if len(renames) != 1 || renames[0] != "side-1:Windows CI failure" {
		t.Fatalf("renames = %#v", renames)
	}
	if model.activeSide.TitleEligible {
		t.Fatalf("title eligibility was not consumed: %#v", model.activeSide)
	}
}

// TestTemporarySideChatKeepsFirstPromptLabelLikeRust covers Rust thread
// routing: automatic titles skip temporary side chats, which stay labeled by
// their first prompt until an explicit name is set.
func TestTemporarySideChatKeepsFirstPromptLabelLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("side-temp")
	generations := 0
	model := NewModel(state, Options{
		Width:          100,
		Height:         30,
		OnRenameThread: func(string, string) error { return nil },
		OnSuggestThreadName: func(string, RecapThreadOptions, string, map[string]any) (string, error) {
			generations++
			return `{"title":"Unexpected title"}`, nil
		},
	})
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-1",
		SideThreadID:   "side-temp",
		Storage:        SideChatStorageTemporary,
		TitleEligible:  false,
		ShowingSide:    true,
	}

	cmd := model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "Side question",
	})
	runTeaCmd(t, model, cmd)

	if generations != 0 {
		t.Fatalf("temporary side chat generated %d titles", generations)
	}
	if model.State.ThreadName != "" {
		t.Fatalf("temporary side chat title = %q", model.State.ThreadName)
	}
}
