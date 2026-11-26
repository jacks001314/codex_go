package tea

// Mirrors Rust #61919 App::preview_image_protocol: a herdr session's startup
// probe answer decides the image protocol, and an ordinary terminal keeps the
// environment-based detection.

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/pets"
)

func TestPetImageSupportHonorsTheKittyProbeLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:        80,
		Height:       24,
		LocalSession: true,
		PetEnv:       map[string]string{"TERM": "xterm-256color"},
	})

	restore := codextui.SetProbedKittyGraphicsForTest(nil)
	defer restore()
	if support := model.petImageSupport(); support.Supported() {
		t.Fatalf("environment detection without a probe = %#v", support)
	}

	accepted := true
	codextui.SetProbedKittyGraphics(&accepted)
	if support := model.petImageSupport(); support.Protocol != pets.ImageProtocolKitty {
		t.Fatalf("accepted probe protocol = %#v", support)
	}

	rejected := false
	codextui.SetProbedKittyGraphics(&rejected)
	if support := model.petImageSupport(); support.Supported() {
		t.Fatalf("rejected probe protocol = %#v", support)
	}
}

func TestKittyProbeOverridesTmuxDetectionLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:        80,
		Height:       24,
		LocalSession: true,
		PetEnv:       map[string]string{"TERM": "xterm-256color", "TMUX": "/tmp/tmux-0/default,1,0"},
	})
	restore := codextui.SetProbedKittyGraphicsForTest(nil)
	defer restore()
	if support := model.petImageSupport(); support.Supported() {
		t.Fatalf("tmux environment detection = %#v", support)
	}
	accepted := true
	codextui.SetProbedKittyGraphics(&accepted)
	if support := model.petImageSupport(); support.Protocol != pets.ImageProtocolKitty {
		t.Fatalf("probe did not override tmux detection: %#v", support)
	}
}
