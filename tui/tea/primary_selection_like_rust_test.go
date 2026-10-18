package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestComposerCopyOnSelectPublishesPrimaryLikeRust(t *testing.T) {
	writes := []string{}
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
	})
	model.composer.SetValue("hello world")
	model.View()
	runTeaCmd(t, model, dragComposerSelection(t, model, 0, len("hello")))
	if len(writes) != 1 || writes[0] != "hello" {
		t.Fatalf("copy-on-select primary writes = %#v", writes)
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
	model.View()
	handled, cmd := model.startPrimarySelectionPaste(bubbletea.MouseMsg{
		Button: bubbletea.MouseButtonMiddle,
		Action: bubbletea.MouseActionPress,
	})
	if !handled || cmd == nil {
		t.Fatalf("middle-click handled=%v cmd=%v", handled, cmd != nil)
	}
	model.Update(cmd())
	if got := model.composer.Value(); got != "primary text" {
		t.Fatalf("composer after primary paste = %q", got)
	}
}
