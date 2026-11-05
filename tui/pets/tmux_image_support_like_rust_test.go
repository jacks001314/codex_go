package pets

import (
	"strings"
	"testing"
)

// Rust #59579/#59580: a tmux client only carries pane-local Kitty images when
// passthrough is `all`, the client is Kitty or Ghostty, and RGB is available.
func TestDetectImageSupportInFullscreenTmuxLikeRust(t *testing.T) {
	env := map[string]string{"TMUX": "1", "TMUX_PANE": "%3"}
	kitty := &TmuxProbe{
		AllowPassthrough:   "all",
		ClientTermname:     "xterm-kitty",
		ClientTermfeatures: "RGB,kitty",
		PaneID:             "%3",
	}
	if support := DetectImageSupportInFullscreen(env, kitty, true); support.Protocol != ImageProtocolKitty {
		t.Fatalf("fullscreen kitty support = %#v", support)
	}
	if support := DetectImageSupportInFullscreen(env, kitty, false); support.Protocol != "" || support.Reason != PetImageUnsupportedTmux {
		t.Fatalf("scrollback support = %#v, want tmux disabled", support)
	}
	ghostty := &TmuxProbe{
		AllowPassthrough:   "all",
		ClientTermtype:     "ghostty 1.1.0",
		ClientTermfeatures: "RGB",
	}
	if support := DetectImageSupportInFullscreen(env, ghostty, true); support.Protocol != ImageProtocolKitty {
		t.Fatalf("fullscreen ghostty support = %#v", support)
	}
	visibleOnly := *kitty
	visibleOnly.AllowPassthrough = "visible"
	if support := DetectImageSupportInFullscreen(env, &visibleOnly, true); support.Protocol != "" {
		t.Fatalf("visible-only passthrough enabled images: %#v", support)
	}
	noRGB := *kitty
	noRGB.ClientTermfeatures = "kitty"
	if support := DetectImageSupportInFullscreen(env, &noRGB, true); support.Protocol != "" {
		t.Fatalf("client without RGB enabled images: %#v", support)
	}
	if support := DetectImageSupportInFullscreen(env, nil, true); support.Protocol != "" {
		t.Fatalf("missing probe enabled images: %#v", support)
	}
	if got := (PetImageSupport{Reason: PetImageUnsupportedTmux}).UnsupportedMessage(); !strings.Contains(got, "allow-passthrough all") {
		t.Fatalf("tmux notice = %q", got)
	}
}

func TestParseTmuxProbeAndImageBaseLikeRust(t *testing.T) {
	probe, ok := ParseTmuxProbe("all\txterm-kitty\tkitty(0.42)\tRGB,kitty\t%7\n")
	if !ok {
		t.Fatal("probe parse failed")
	}
	if probe.AllowPassthrough != "all" || probe.PaneID != "%7" || !probe.KittyPassthrough() {
		t.Fatalf("probe = %#v", probe)
	}
	base, ok := probe.KittyImageBase()
	if !ok || base != 0x800000+(7<<8) {
		t.Fatalf("image base = %#x ok=%t", base, ok)
	}
	if _, ok := (TmuxProbe{PaneID: "%40000"}).KittyImageBase(); ok {
		t.Fatal("out-of-range pane id produced an image base")
	}
	if _, ok := ParseTmuxProbe("all\txterm-kitty"); ok {
		t.Fatal("short probe output parsed")
	}
}
