package pets

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// LoadAmbientPet resolves a built-in pet id, ensures its spritesheet is cached
// under codexHome, extracts the per-frame PNG cache, and returns an
// AmbientPetState ready for draw requests. The returned state reports whether
// the terminal image support snapshot (from env) actually allows rendering.
func LoadAmbientPet(petID string, codexHome string, animationsEnabled bool, env map[string]string, fetch AssetFetchFunc) (AmbientPetState, error) {
	petID = strings.ToLower(strings.TrimSpace(petID))
	if petID == "" || petID == DisabledPetID {
		return AmbientPetState{}, errors.New("no pet selected")
	}
	catalogPet, ok := BuiltinPet(petID)
	if !ok {
		return AmbientPetState{}, errors.New("unknown pet " + petID)
	}
	if strings.TrimSpace(codexHome) == "" {
		return AmbientPetState{}, errors.New("GCODE_HOME is not available")
	}
	if _, err := EnsureBuiltinPet(codexHome, catalogPet, fetch); err != nil {
		return AmbientPetState{}, err
	}
	pet, err := BuiltinPetModel(petID, codexHome)
	if err != nil {
		return AmbientPetState{}, err
	}
	cacheKey, err := FrameCacheKey(pet.SpritesheetPath, pet.FrameWidth, pet.FrameHeight, pet.Columns, pet.Rows)
	if err != nil {
		return AmbientPetState{}, err
	}
	cacheDir := filepath.Join(codexHome, "cache", "tui-pets", "frame-cache", pet.ID, cacheKey)
	frames, err := PreparePNGFrames(pet.SpritesheetPath, filepath.Join(cacheDir, "frames"), pet.FrameWidth, pet.FrameHeight, pet.Columns, pet.Rows)
	if err != nil {
		return AmbientPetState{}, err
	}
	support := DetectImageSupport(env)
	return NewAmbientPetState(pet, frames, filepath.Join(cacheDir, "sixel"), support, animationsEnabled, time.Now()), nil
}
