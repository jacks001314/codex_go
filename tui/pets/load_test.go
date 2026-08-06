package pets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAmbientPetPreparesFramesAndDetectsSupport(t *testing.T) {
	codexHome := t.TempDir()
	payload := testSpritesheetPNG(t, SpritesheetWidth, SpritesheetHeight)
	fetch := func(url string, maxBytes int64) ([]byte, error) {
		return payload, nil
	}
	env := map[string]string{"KITTY_WINDOW_ID": "1"}

	state, err := LoadAmbientPet("dewey", codexHome, true, env, fetch)
	if err != nil {
		t.Fatalf("LoadAmbientPet: %v", err)
	}
	if !state.Visible || !state.Support.Supported() {
		t.Fatalf("pet should be visible in kitty env: %#v", state.Support)
	}
	if state.Pet.ID != "dewey" || state.Pet.DisplayName != "Dewey" {
		t.Fatalf("pet = %#v", state.Pet)
	}
	if len(state.Frames) != DefaultFrameCount() {
		t.Fatalf("frame count = %d, want %d", len(state.Frames), DefaultFrameCount())
	}
	for _, frame := range state.Frames {
		if _, err := os.Stat(frame); err != nil {
			t.Fatalf("missing frame %s: %v", frame, err)
		}
	}
	// The downloaded spritesheet is cached under the versioned asset dir.
	assetPath := BuiltinSpritesheetPath(codexHome, "dewey-spritesheet-v4.webp")
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("missing cached spritesheet: %v", err)
	}
	if !strings.Contains(filepath.ToSlash(state.Frames[0]), "cache/tui-pets/frame-cache/dewey/sha256-") {
		t.Fatalf("frame cache layout = %q", state.Frames[0])
	}
}

func TestLoadAmbientPetHidesWhenTerminalUnsupported(t *testing.T) {
	codexHome := t.TempDir()
	payload := testSpritesheetPNG(t, SpritesheetWidth, SpritesheetHeight)
	fetch := func(url string, maxBytes int64) ([]byte, error) {
		return payload, nil
	}
	state, err := LoadAmbientPet("codex", codexHome, true, nil, fetch)
	if err != nil {
		t.Fatalf("LoadAmbientPet: %v", err)
	}
	if state.Visible {
		t.Fatal("pet should be hidden without terminal image support")
	}
	if state.Support.Supported() {
		t.Fatalf("support should be unsupported: %#v", state.Support)
	}
}

func TestLoadAmbientPetRejectsUnknownPet(t *testing.T) {
	if _, err := LoadAmbientPet("nope", t.TempDir(), true, nil, nil); err == nil {
		t.Fatal("expected unknown-pet error")
	}
}
