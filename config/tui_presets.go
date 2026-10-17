package config

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// TUIPresetMaxInputBytes bounds a preset input to the same 8,000 UTF-8 bytes
// Rust enforces (config/src/types.rs QuickDial, #54379).
const TUIPresetMaxInputBytes = 8000

// TUIPreset is one reusable prompt or slash command submitted through the TUI
// composer (Rust `QuickDial`, #54379). Presets live under `tui.presets` keyed by
// a single digit from `0` through `9`.
type TUIPreset struct {
	// Input is the plain-text prompt or slash command (never a shell command).
	Input string `json:"input"`
	// Label is the optional short name for the picker and shortcut hints.
	Label *string `json:"label,omitempty"`
}

// Validate applies Rust `QuickDial::validate` verbatim: nonempty input, no
// shell command (a leading `!` after whitespace) and at most 8,000 UTF-8 bytes.
func (p TUIPreset) Validate() error {
	if strings.TrimSpace(p.Input) == "" {
		return fmt.Errorf("Preset input cannot be empty.")
	}
	if strings.HasPrefix(strings.TrimLeftFunc(p.Input, unicode.IsSpace), "!") {
		return fmt.Errorf("Presets support prompts and slash commands, not shell commands.")
	}
	// Rust compares the byte length (String::len), not the rune count.
	if len(p.Input) > TUIPresetMaxInputBytes {
		return fmt.Errorf("Preset input cannot exceed 8,000 UTF-8 bytes.")
	}
	return nil
}

var knownTUIPresetFields = map[string]struct{}{
	"input": {},
	"label": {},
}

// ParseTUIPresets reads and validates `tui.presets` from the raw configuration
// values. Slot keys must be a single ASCII digit; every entry must decode as a
// preset with only `input` and `label`. Validation errors use Rust's
// `tui.presets.<slot>: <reason>` shape so the CLI reports the same message.
func ParseTUIPresets(values map[string]any) (map[string]TUIPreset, error) {
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return nil, nil
	}
	raw, ok := tui["presets"]
	if !ok || raw == nil {
		return nil, nil
	}
	entries, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tui.presets must be a table")
	}
	slots := make([]string, 0, len(entries))
	for slot := range entries {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	presets := make(map[string]TUIPreset, len(entries))
	for _, slot := range slots {
		if len(slot) != 1 || slot[0] < '0' || slot[0] > '9' {
			return nil, fmt.Errorf("tui.presets.%s: slot must be a single digit from 0 to 9", slot)
		}
		entry, ok := entries[slot].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tui.presets.%s: expected a table with an input", slot)
		}
		fields := make([]string, 0, len(entry))
		for field := range entry {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			if _, known := knownTUIPresetFields[field]; !known {
				return nil, fmt.Errorf("tui.presets.%s: unknown field `%s`", slot, field)
			}
		}
		input, present := entry["input"].(string)
		if !present {
			return nil, fmt.Errorf("tui.presets.%s: missing field `input`", slot)
		}
		var label *string
		if rawLabel, ok := entry["label"]; ok && rawLabel != nil {
			value, ok := rawLabel.(string)
			if !ok {
				return nil, fmt.Errorf("tui.presets.%s: label must be a string", slot)
			}
			label = &value
		}
		preset := TUIPreset{Input: input, Label: label}
		if err := preset.Validate(); err != nil {
			return nil, fmt.Errorf("tui.presets.%s: %s", slot, err.Error())
		}
		presets[slot] = preset
	}
	return presets, nil
}

// ValidateTUIPresets rejects an invalid `tui.presets` table at config load time,
// mirroring Rust's typed ConfigToml deserialization plus the QuickDial checks
// (#54379).
func ValidateTUIPresets(values map[string]any) error {
	_, err := ParseTUIPresets(values)
	return err
}

// TUIPresets returns the configured presets keyed by slot digit.
func (c *Config) TUIPresets() map[string]TUIPreset {
	if c == nil {
		return nil
	}
	presets, err := ParseTUIPresets(c.Values)
	if err != nil {
		return nil
	}
	return presets
}
