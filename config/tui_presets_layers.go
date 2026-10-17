package config

import (
	"path/filepath"
	"strconv"
)

// TUIPresetLayerAccess reports which preset slots cannot be authored in the
// user config. setReadOnly follows Rust #54380's highest-layer ownership rule;
// clearReadOnly is true when any non-user layer defines the slot, because a
// user-layer deletion would merely reveal the shadowed value.
func TUIPresetLayerAccess(layers []Layer, userConfigPath string) (map[string]bool, map[string]bool) {
	setReadOnly := map[string]bool{}
	clearReadOnly := map[string]bool{}
	userConfigPath = filepath.Clean(userConfigPath)
	for slot := 0; slot <= 9; slot++ {
		key := strconv.Itoa(slot)
		highestOutsideUser := false
		anyOutsideUser := false
		for index := len(layers) - 1; index >= 0; index-- {
			layer := layers[index]
			if !layerPresetDefined(layer, key) {
				continue
			}
			userLayer := layer.Name.Type == LayerSourceUser &&
				(userConfigPath == "." || filepath.Clean(layer.Name.File) == userConfigPath)
			if index == highestDefiningLayer(layers, key) {
				highestOutsideUser = !userLayer
			}
			if !userLayer {
				anyOutsideUser = true
			}
		}
		if highestOutsideUser {
			setReadOnly[key] = true
		}
		if anyOutsideUser {
			clearReadOnly[key] = true
		}
	}
	return setReadOnly, clearReadOnly
}

func highestDefiningLayer(layers []Layer, slot string) int {
	for index := len(layers) - 1; index >= 0; index-- {
		if layerPresetDefined(layers[index], slot) {
			return index
		}
	}
	return -1
}

func layerPresetDefined(layer Layer, slot string) bool {
	values, ok := layer.Config.(map[string]any)
	if !ok {
		return false
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return false
	}
	presets, ok := tui["presets"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = presets[slot]
	return ok
}
