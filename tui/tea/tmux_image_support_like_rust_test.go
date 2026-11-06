package tea

import (
	"errors"
	"testing"
	"time"

	"codex_go/tui/pets"
)

// Rust #59579/#59580: fullscreen tmux panes with Kitty passthrough expose the
// Kitty image protocol; scrolling back or a failed probe keeps images off.
func TestPetImageSupportUsesFullscreenTmuxProbeLikeRust(t *testing.T) {
	probe := &pets.TmuxProbe{
		AllowPassthrough:   "all",
		ClientTermname:     "xterm-kitty",
		ClientTermfeatures: "RGB",
		PaneID:             "%3",
	}
	env := map[string]string{"TMUX": "1", "TMUX_PANE": "%3"}
	fullscreen := NewModel(nil, Options{PetEnv: env})
	fullscreen.tmuxImageProbe = probe
	if got := fullscreen.petImageSupport(); got.Protocol != pets.ImageProtocolKitty {
		t.Fatalf("fullscreen support = %#v", got)
	}
	scrollback := NewModel(nil, Options{PetEnv: env, NoAltScreen: true})
	scrollback.tmuxImageProbe = probe
	if got := scrollback.petImageSupport(); got.Protocol != "" || got.Reason != pets.PetImageUnsupportedTmux {
		t.Fatalf("scrollback support = %#v", got)
	}
}

func TestProbeTmuxImageSupportReadsDisplayMessageLikeRust(t *testing.T) {
	previous := runTmuxImageProbe
	defer func() { runTmuxImageProbe = previous }()
	var gotPane string
	runTmuxImageProbe = func(pane string, _ time.Duration) (string, error) {
		gotPane = pane
		return "all\txterm-kitty\tkitty(0.42)\tRGB\t%12\n", nil
	}
	probe := probeTmuxImageSupport(map[string]string{"TMUX": "/tmp/tmux", "TMUX_PANE": "%12"})
	if probe == nil || !probe.KittyPassthrough() || probe.PaneID != "%12" {
		t.Fatalf("probe = %#v", probe)
	}
	if gotPane != "%12" {
		t.Fatalf("probed pane = %q", gotPane)
	}
	runTmuxImageProbe = func(string, time.Duration) (string, error) { return "", errors.New("tmux missing") }
	if probe := probeTmuxImageSupport(map[string]string{"TMUX": "1"}); probe != nil {
		t.Fatalf("failed probe returned %#v", probe)
	}
	if probe := probeTmuxImageSupport(map[string]string{}); probe != nil {
		t.Fatalf("non-tmux env returned %#v", probe)
	}
}
