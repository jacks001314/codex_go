package app

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestInteractiveMouseScrollSpeedLikeRust pins the host-side resolution of
// `tui.mouse_scroll_speed` (#50209). Rust validates the value while parsing the
// config (`codex-rs/config/src/tui_mouse_scroll.rs::deserialize`: a finite
// positive number or an error) and the TUI then consumes it with
// `unwrap_or(1.0)` (`local_settings.rs`); Go's `[tui]` sub-table is not
// value-validated, so the host resolves the usable value here and falls back to
// the one-row default for a missing or unusable setting.
//
// Rust #50209 (4dd51f4a5f), counterpart test
// `mouse_scroll_speed_rejects_nonpositive_and_nonfinite_values`.
func TestInteractiveMouseScrollSpeedLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]any
		want   float64
	}{
		{name: "absent_uses_the_default", values: nil, want: codextui.MouseScrollSpeedDefault},
		{name: "no_tui_table", values: map[string]any{}, want: codextui.MouseScrollSpeedDefault},
		{name: "tui_without_the_key", values: map[string]any{"tui": map[string]any{"theme": "dark"}}, want: codextui.MouseScrollSpeedDefault},
		{name: "configured_float", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": 2.0}}, want: 2.0},
		{name: "configured_fractional", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": 0.5}}, want: 0.5},
		{name: "configured_integer", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": 3}}, want: 3.0},
		{name: "triple_restores_the_old_speed", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": 3.0}}, want: 3.0},
		{name: "zero_is_not_positive", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": 0.0}}, want: codextui.MouseScrollSpeedDefault},
		{name: "negative_is_not_positive", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": -2.0}}, want: codextui.MouseScrollSpeedDefault},
		{name: "positive_infinity_is_not_finite", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": math.Inf(1)}}, want: codextui.MouseScrollSpeedDefault},
		{name: "nan_is_not_finite", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": math.NaN()}}, want: codextui.MouseScrollSpeedDefault},
		{name: "wrong_type_falls_back", values: map[string]any{"tui": map[string]any{"mouse_scroll_speed": "fast"}}, want: codextui.MouseScrollSpeedDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := interactiveMouseScrollSpeed(tc.values)
			if got == nil {
				t.Fatalf("interactiveMouseScrollSpeed(%v) = nil, want %v", tc.values, tc.want)
			}
			if *got != tc.want {
				t.Fatalf("interactiveMouseScrollSpeed(%v) = %v, want %v", tc.values, *got, tc.want)
			}
			if settings := interactiveSettingsFromConfig(&config.Config{Values: tc.values}); settings.MouseScrollSpeed == nil || *settings.MouseScrollSpeed != tc.want {
				t.Fatalf("settings mouse scroll speed = %v, want %v", settings.MouseScrollSpeed, tc.want)
			}
		})
	}
}

// TestTUIMouseScrollSpeedFromEffectiveConfigLikeRust walks the whole host path
// for #50209: a real `config.toml` with `[tui] mouse_scroll_speed = 2.0` is
// loaded through the interactive settings loader, forwarded into the TUI's
// options the way `runInteractiveTUI` does, and drives the live transcript
// wheel. Constructing `SettingsWriteResult` directly (as the TUI unit tests do)
// would not catch a missing producer or a missing host wiring, so this test
// reads the effective configuration instead.
func TestTUIMouseScrollSpeedFromEffectiveConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[tui]\nmouse_scroll_speed = 2.0\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	root := &cli.RootOptions{}

	settings := interactiveTUISettings(root)
	if settings.MouseScrollSpeed == nil || *settings.MouseScrollSpeed != 2.0 {
		t.Fatalf("interactiveTUISettings mouse scroll speed = %v, want 2.0", settings.MouseScrollSpeed)
	}
	// A new thread stages the reloaded record (Rust #51510 carries local settings
	// alongside the new-session configuration), so the live value survives.
	if staged := interactiveNewThreadLocalSettings(root); staged.Tui.MouseScrollSpeed == nil || *staged.Tui.MouseScrollSpeed != 2.0 {
		t.Fatalf("new-thread local settings mouse scroll speed = %v, want 2.0", staged.Tui.MouseScrollSpeed)
	}

	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	// The host builds these options in runInteractiveTUI / runInteractiveRemoteTUI
	// with `MouseScrollSpeed: settings.MouseScrollSpeed`.
	model := codextea.NewModel(state, codextea.Options{
		Width:            60,
		Height:           10,
		MouseScrollSpeed: settings.MouseScrollSpeed,
	})
	if got := model.MouseScrollSpeed(); got != 2.0 {
		t.Fatalf("live mouse scroll speed = %v, want 2.0", got)
	}
	before := model.TranscriptYOffset()
	if before <= 0 {
		t.Fatalf("initial transcript offset = %d, want a scrollable bottom", before)
	}
	model.Update(bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonWheelUp})
	if got, want := model.TranscriptYOffset(), before-2; got != want {
		t.Fatalf("configured wheel offset = %d, want %d (one wheel event moves `mouse_scroll_speed` rows)", got, want)
	}
	model.Update(bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonWheelDown})
	if got, want := model.TranscriptYOffset(), before; got != want {
		t.Fatalf("wheel-down offset = %d, want %d", got, want)
	}
}

// TestHostsForwardMouseScrollSpeedToTUIOptionsLikeRust guards the startup half
// of #50209: the interactive hosts must forward the resolved
// `tui.mouse_scroll_speed` into `codextea.Options`, otherwise a configured speed
// would only reach the transcript after a settings write. The options literals
// are inline in the run functions with no extraction seam, so this check reads
// the source exactly as the other host-wiring guards do.
func TestHostsForwardMouseScrollSpeedToTUIOptionsLikeRust(t *testing.T) {
	wiring := regexp.MustCompile(`MouseScrollSpeed:\s*settings\.MouseScrollSpeed,`)
	for _, path := range []string{"interactive.go", "remote_tui.go"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		found := false
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if wiring.MatchString(line) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s does not forward `MouseScrollSpeed: settings.MouseScrollSpeed` into codextea.Options; a configured tui.mouse_scroll_speed would not reach the transcript at startup", path)
		}
	}
}
