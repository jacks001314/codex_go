package tea

import (
	"bytes"
	"strings"
	"testing"

	"codex_go/shell"
)

func TestLinkPointerShapeTransitionsLikeRust(t *testing.T) {
	var state linkPointerState
	if got := state.update(false); got != "" {
		t.Fatalf("untouched default emitted %q", got)
	}
	if got := state.update(true); got != "\x1b]22;pointer\x1b\\" {
		t.Fatalf("hover escape = %q", got)
	}
	if got := state.update(true); got != "" {
		t.Fatalf("unchanged hover repeated %q", got)
	}
	if got := state.update(false); got != "\x1b]22;default\x1b\\" {
		t.Fatalf("leave escape = %q", got)
	}
	if got := state.update(false); got != "" {
		t.Fatalf("unchanged default repeated %q", got)
	}
	if got := state.update(true); got == "" {
		t.Fatal("second hover did not emit the pointer shape")
	}
	if got := state.restore(true); got != "\x1b]22;\x1b\\" {
		t.Fatalf("kitty restore = %q", got)
	}
	if got := state.restore(true); got != "" {
		t.Fatalf("released shape restored twice: %q", got)
	}
	if got := state.update(true); got == "" {
		t.Fatal("hover after restore did not emit")
	}
	if got := state.restore(false); got != "\x1b]22;text\x1b\\" {
		t.Fatalf("ghostty restore = %q", got)
	}
}

func TestPointerShapeSupportedLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		info *shell.TerminalInfo
		want bool
	}{
		{name: "ghostty", info: &shell.TerminalInfo{Name: shell.TerminalGhostty}, want: true},
		{name: "kitty", info: &shell.TerminalInfo{Name: shell.TerminalKitty}, want: true},
		{name: "wezterm", info: &shell.TerminalInfo{Name: shell.TerminalWezTerm}, want: false},
		{name: "unknown", info: &shell.TerminalInfo{Name: shell.TerminalUnknown}, want: false},
		{name: "kitty under tmux", info: &shell.TerminalInfo{Name: shell.TerminalKitty, Multiplexer: &shell.Multiplexer{Name: shell.MultiplexerTmux}}, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model := &Model{linkPointerTerminal: testCase.info}
			if got := model.pointerShapeSupported(); got != testCase.want {
				t.Fatalf("pointerShapeSupported = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestModelPointerShapeFollowsHoverLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.ownedScreen = true
	model.mouseCaptureActive = true
	model.linkPointerTerminal = &shell.TerminalInfo{Name: shell.TerminalKitty}
	frame := "header\n" + hoverOSC8Link("https://example.com", "page") + "\ntail"
	model.hoverPointerX, model.hoverPointerY, model.hoverPointerSet = 2, 1, true
	if got := model.applyLinkHover(frame); got == frame {
		t.Fatal("hover did not recolour the link")
	}
	if got := model.pointerShapeOutput(); got != "\x1b]22;pointer\x1b\\" {
		t.Fatalf("hover shape = %q", got)
	}
	if got := model.pointerShapeOutput(); got != "" {
		t.Fatalf("hover shape repeated: %q", got)
	}

	model.hoverPointerY = 0
	if got := model.applyLinkHover(frame); got != frame {
		t.Fatalf("hover applied to a row without a link: %q", got)
	}
	if got := model.pointerShapeOutput(); got != "\x1b]22;default\x1b\\" {
		t.Fatalf("shape after leaving the link = %q", got)
	}

	model.mouseCaptureActive = false
	if got := model.pointerShapeOutput(); got != "\x1b]22;\x1b\\" {
		t.Fatalf("kitty restore after mouse capture ended = %q", got)
	}
	var out bytes.Buffer
	model.restorePointerShape(&out)
	if out.Len() != 0 {
		t.Fatalf("exit restore repeated the sequence: %q", out.String())
	}
}

func TestModelPointerShapeExitRestoreLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		term shell.TerminalName
		want string
	}{
		{name: "ghostty", term: shell.TerminalGhostty, want: "\x1b]22;text\x1b\\"},
		{name: "kitty", term: shell.TerminalKitty, want: "\x1b]22;\x1b\\"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model := NewModel(nil, Options{Width: 80, Height: 24})
			model.ownedScreen = true
			model.mouseCaptureActive = true
			model.linkPointerTerminal = &shell.TerminalInfo{Name: testCase.term}
			model.hoverPointerX, model.hoverPointerY, model.hoverPointerSet = 2, 0, true
			model.applyLinkHover(hoverOSC8Link("https://example.com", "page"))
			if got := model.pointerShapeOutput(); got != "\x1b]22;pointer\x1b\\" {
				t.Fatalf("hover shape = %q", got)
			}
			var out bytes.Buffer
			model.restorePointerShape(&out)
			if out.String() != testCase.want {
				t.Fatalf("exit restore = %q, want %q", out.String(), testCase.want)
			}
		})
	}
}

func TestModelPointerShapeSkipsUnsupportedTerminalsLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.ownedScreen = true
	model.mouseCaptureActive = true
	model.linkPointerTerminal = &shell.TerminalInfo{Name: shell.TerminalWezTerm}
	model.hoverPointerX, model.hoverPointerY, model.hoverPointerSet = 2, 0, true
	model.applyLinkHover(hoverOSC8Link("https://example.com", "page"))
	if got := model.pointerShapeOutput(); got != "" {
		t.Fatalf("unsupported terminal emitted %q", got)
	}
	if content := model.View().Content; strings.HasPrefix(content, "\x1b]22;") {
		t.Fatalf("view carries a pointer escape on an unsupported terminal: %q", content[:20])
	}
}

func TestModelViewPrefixesPointerShapeLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.ownedScreen = true
	model.mouseCaptureActive = true
	model.linkPointerTerminal = &shell.TerminalInfo{Name: shell.TerminalKitty}
	if got := model.pointerShapeOutput(); got != "" {
		t.Fatalf("untouched pointer shape emitted %q", got)
	}
	model.linkPointer.update(true)
	if content := model.View().Content; !strings.HasPrefix(content, "\x1b]22;default\x1b\\") {
		t.Fatalf("view does not carry the released hover shape: %q", content[:24])
	}
}
