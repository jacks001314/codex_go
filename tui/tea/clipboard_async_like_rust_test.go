package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

// Rust #47847: clipboard writes run off the update loop.
func TestClipboardCopyRunsOffUpdateLoopLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	called := false
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnClipboardWrite: func(text string) error {
			called = true
			return nil
		},
	})
	model.copyTargets = chatwidget.CopyTargetsFromMarkdown("answer")
	cmd := model.applyCopyTargetModalOption(chatwidget.CopyTargetWholeID)
	if cmd == nil || called {
		t.Fatalf("copy command = %v called=%v, want deferred command", cmd != nil, called)
	}
	model.Update(cmd())
	if !called || !strings.Contains(utils.StripANSI(model.View()), "Copied Whole response to clipboard") {
		t.Fatalf("copy did not complete: called=%v view=%s", called, utils.StripANSI(model.View()))
	}
}

func TestClipboardCopyFailureReturnsNoticeLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnClipboardWrite: func(string) error {
			return errors.New("clipboard busy")
		},
		OnClipboardForward: func(string, bool) error {
			return errors.New("osc blocked")
		},
	})
	model.copyTargets = chatwidget.CopyTargetsFromMarkdown("answer")
	cmd := model.applyCopyTargetModalOption(chatwidget.CopyTargetWholeID)
	if cmd == nil {
		t.Fatal("copy returned no command")
	}
	model.Update(cmd())
	if !strings.Contains(utils.StripANSI(model.View()), "Copy failed: clipboard busy") {
		t.Fatalf("copy failure notice missing:\n%s", utils.StripANSI(model.View()))
	}
}

// Rust #52557: a Herdr session copies natively, forwards OSC 52 even though the
// native write succeeded, and keeps the success notice.
func TestHerdrClipboardCopyForwardsAfterNativeLikeRust(t *testing.T) {
	calls := []string{}
	environment := codextui.ClipboardEnvironment{HerdrSession: true}
	model := NewModel(codextui.NewState(nil), Options{
		Width:                100,
		Height:               30,
		ClipboardEnvironment: &environment,
		OnClipboardWrite: func(string) error {
			calls = append(calls, "native")
			return nil
		},
		OnClipboardForward: func(_ string, tmux bool) error {
			if tmux {
				calls = append(calls, "tmux")
			} else {
				calls = append(calls, "osc52")
			}
			return nil
		},
	})
	model.copyTargets = chatwidget.CopyTargetsFromMarkdown("answer")
	cmd := model.applyCopyTargetModalOption(chatwidget.CopyTargetWholeID)
	if cmd == nil {
		t.Fatal("copy returned no command")
	}
	model.Update(cmd())
	if got := strings.Join(calls, ","); got != "native,osc52" {
		t.Fatalf("clipboard calls = %s, want native,osc52", got)
	}
	if !strings.Contains(utils.StripANSI(model.View()), "Copied Whole response to clipboard") {
		t.Fatalf("copy success notice missing:\n%s", utils.StripANSI(model.View()))
	}
}
