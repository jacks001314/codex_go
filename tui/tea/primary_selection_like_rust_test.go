package tea

import (
	mousemsg "codex_go/tui/mousemsg"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func TestComposerCopyOnSelectPublishesPrimaryLikeRust(t *testing.T) {
	writes := []string{}
	releases := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:               120,
		Height:              30,
		AlternateScreenMode: "always",
		CopyOnSelect:        "always",
		OnClipboardWrite:    func(string) error { return nil },
		OnPrimarySelectionWrite: func(text string) error {
			writes = append(writes, text)
			return nil
		},
		OnPrimarySelectionRelease: func() error {
			releases++
			return nil
		},
	})
	model.composer.SetValue("hello world")
	model.viewString()
	runTeaCmd(t, model, dragComposerSelection(t, model, 0, len("hello")))
	if len(writes) != 1 || writes[0] != "hello" {
		t.Fatalf("copy-on-select primary writes = %#v", writes)
	}
	if !model.primarySelectionPublished {
		t.Fatal("primary publication result was not recorded")
	}
	_, releaseCmd := model.Update(bubbletea.BlurMsg{})
	runTeaCmd(t, model, releaseCmd)
	if releases != 1 || model.primarySelectionPublished {
		t.Fatalf("focus loss releases=%d published=%v", releases, model.primarySelectionPublished)
	}

	writes = nil
	_, cmd := model.Update(ctrlInsertCopyMsg{})
	runTeaCmd(t, model, cmd)
	if len(writes) != 0 {
		t.Fatalf("explicit copy published PRIMARY: %#v", writes)
	}
}

func TestMiddleClickPastesPrimarySelectionLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnPrimarySelectionRead: func() (string, error) {
			return "primary text", nil
		},
	})
	model.viewString()
	handled, cmd := model.startPrimarySelectionPaste(mousemsg.MouseMsg{
		Button: mousemsg.MouseButtonMiddle,
		Action: mousemsg.MouseActionPress,
	})
	if !handled || cmd == nil {
		t.Fatalf("middle-click handled=%v cmd=%v", handled, cmd != nil)
	}
	model.Update(cmd())
	if got := model.composer.Value(); got != "primary text" {
		t.Fatalf("composer after primary paste = %q", got)
	}
}

func TestResumeReleasesPrimaryOwnershipLikeRust(t *testing.T) {
	released := make(chan struct{}, 1)
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnPrimarySelectionRelease: func() error {
			released <- struct{}{}
			return nil
		},
	})
	model.primarySelectionPublished = true
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-a"},
	})
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("resume did not release PRIMARY ownership")
	}
	if model.primarySelectionPublished {
		t.Fatal("primary ownership remained marked after resume")
	}
}

func TestLatePrimaryPasteIsDiscardedAfterInputLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnPrimarySelectionRead: func() (string, error) {
			return "stale primary", nil
		},
	})
	model.viewString()
	handled, cmd := model.startPrimarySelectionPaste(mousemsg.MouseMsg{
		Button: mousemsg.MouseButtonMiddle,
		Action: mousemsg.MouseActionPress,
	})
	if !handled || cmd == nil {
		t.Fatal("middle-click did not start")
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("x")})
	model.Update(cmd())
	if got := model.composer.Value(); got != "x" {
		t.Fatalf("late primary paste changed composer to %q", got)
	}
}
