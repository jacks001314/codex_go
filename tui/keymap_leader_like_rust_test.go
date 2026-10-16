package tui

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Rust #52273 (a3f3b44cd8) resolves the shared leader prefix from
// `tui.keymap.global.leader`, defaulting to ctrl-x and yielding to an existing
// ctrl-x alias anywhere in the keymap (tui/src/keymap/leader.rs:11-22).
func TestLeaderKeybindingsDefaultLikeRust(t *testing.T) {
	prefixes, err := LeaderKeybindings(nil)
	if err != nil || !reflect.DeepEqual(prefixes, []string{"ctrl-x"}) {
		t.Fatalf("prefixes = %#v err = %v", prefixes, err)
	}
}

func TestLeaderKeybindingsYieldsToExistingAliasLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("chat", "interrupt_turn", []string{"ctrl-x"}); err != nil {
		t.Fatal(err)
	}
	prefixes, err := LeaderKeybindings(config)
	if err != nil || len(prefixes) != 0 {
		t.Fatalf("prefixes = %#v err = %v", prefixes, err)
	}
	if bindings, _, _ := ResolvedKeymapBindings(config, "global", "leader"); len(bindings) != 0 {
		t.Fatalf("a shadowed default leader still resolved to %#v", bindings)
	}
}

func TestLeaderKeybindingsConfiguredPrefixReplacesDefaultLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("global", "leader", []string{"alt-x"}); err != nil {
		t.Fatal(err)
	}
	prefixes, err := LeaderKeybindings(config)
	if err != nil || !reflect.DeepEqual(prefixes, []string{"alt-x"}) {
		t.Fatalf("prefixes = %#v err = %v", prefixes, err)
	}
	bindings, source, custom := ResolvedKeymapBindings(config, "global", "leader")
	if !reflect.DeepEqual(bindings, []string{"alt-x"}) || source != "custom" || !custom {
		t.Fatalf("bindings = %#v source = %q custom = %v", bindings, source, custom)
	}
}

// Rust rejects an unusable leader prefix, so a bad value fails configuration
// resolution instead of silently falling back (keymap/leader.rs:25-66).
func TestLeaderKeybindingsRejectionsLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		want   string
	}{
		{"multi_stroke", "ctrl-x c", "use a single prefix key"},
		{"bare_character", "a", "use ctrl, alt, super, or a non-character key"},
		{"bare_space", "space", "use ctrl, alt, super, or a non-character key"},
		// Rust applies the plain-character rule before the reserved table, so
		// printable reserved keys report the text-input error
		// (keymap/leader.rs:45-70).
		{"bare_slash", "/", "use ctrl, alt, super, or a non-character key"},
		{"bare_bang", "!", "use ctrl, alt, super, or a non-character key"},
		{"bare_at", "@", "use ctrl, alt, super, or a non-character key"},
		{"bare_dollar", "$", "use ctrl, alt, super, or a non-character key"},
		{"reserved_ctrl_c", "ctrl-c", "reserved by `fixed.interrupt_or_quit`"},
		{"reserved_ctrl_d", "ctrl-d", "reserved by `fixed.quit`"},
		{"reserved_esc", "esc", "reserved by `fixed.backtrack`"},
		{"reserved_shift_tab", "shift-tab", "reserved by `fixed.cycle_collaboration_mode`"},
		{"reserved_alt_left", "alt-left", "reserved by `fixed.previous_agent`"},
		{"reserved_alt_right", "alt-right", "reserved by `fixed.next_agent`"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := NewKeymapConfig()
			if err := config.Set("global", "leader", []string{testCase.prefix}); err != nil {
				t.Fatal(err)
			}
			if _, err := LeaderKeybindings(config); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("LeaderKeybindings(%q) error = %v, want %q", testCase.prefix, err, testCase.want)
			}
			if err := config.Validate(); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Validate() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestLeaderCtrlZReservedOnUnixLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Rust only reserves ctrl-z when the target OS is not Windows")
	}
	config := NewKeymapConfig()
	if err := config.Set("global", "leader", []string{"ctrl-z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LeaderKeybindings(config); err == nil || !strings.Contains(err.Error(), "ctrl-z is reserved for suspending the terminal on Unix") {
		t.Fatalf("error = %v", err)
	}
}

// Rust parses `leader <key>` as a two-part symbolic binding and rejects any
// other arity (config/src/tui_keymap.rs:589-609, MAX_KEY_CHORD_STROKES at :33).
func TestNormalizeKeybindingSpecLeaderFormLikeRust(t *testing.T) {
	accepted := map[string]string{
		"leader c":      "leader c",
		"leader C":      "leader c",
		"leader ctrl-c": "leader ctrl-c",
		"ctrl-x ctrl-t": "ctrl-x ctrl-t",
		"ctrl-x":        "ctrl-x",
	}
	for input, want := range accepted {
		got, err := NormalizeKeybindingSpec(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeKeybindingSpec(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	rejected := map[string]string{
		"leader": "`leader` must be followed by one key",
		// Rust checks the stroke cap before the leader arity
		// (config/src/tui_keymap.rs:594-607).
		"leader c c":        "key chords may contain at most 2 strokes",
		"ctrl-x ctrl-t esc": "key chords may contain at most 2 strokes",
	}
	for input, want := range rejected {
		if _, err := NormalizeKeybindingSpec(input); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("NormalizeKeybindingSpec(%q) error = %v, want %q", input, err, want)
		}
	}
}

// The CLI form (`/keymap set global.open_transcript leader c`) splits on
// whitespace, so a `leader <key>` pair must survive as one spec.
func TestParseKeybindingListGroupsLeaderSpecLikeRust(t *testing.T) {
	got, err := ParseKeybindingList("leader c")
	if err != nil || !reflect.DeepEqual(got, []string{"leader c"}) {
		t.Fatalf("ParseKeybindingList = %#v err = %v", got, err)
	}
	got, err = ParseKeybindingList("ctrl-t, leader c")
	if err != nil || !reflect.DeepEqual(got, []string{"ctrl-t", "leader c"}) {
		t.Fatalf("ParseKeybindingList = %#v err = %v", got, err)
	}
}

// Rust renders `leader <key>` together with the concrete strokes it resolves to
// (keymap_setup/actions.rs:450-467).
func TestLeaderSpecSummaryLikeRust(t *testing.T) {
	if got := LeaderSpecSummary([]string{"ctrl-x", "alt-x"}, "leader c"); got != "leader c (ctrl-x c, alt-x c)" {
		t.Fatalf("summary = %q", got)
	}
	if got := LeaderSpecSummary(nil, "leader c"); got != "leader c (inactive)" {
		t.Fatalf("inactive summary = %q", got)
	}
	if got := LeaderSpecSummary([]string{"ctrl-x"}, "ctrl-t"); got != "ctrl-t" {
		t.Fatalf("plain summary = %q", got)
	}
}

func TestKeymapActionHasBindingMatchesLeaderPrefixLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("chat", "interrupt_turn", []string{"leader c"}); err != nil {
		t.Fatal(err)
	}
	if !KeymapActionHasBinding(config, "chat", "interrupt_turn", "leader c") {
		t.Fatal("the configured leader spec does not match itself")
	}
	if !KeymapActionHasBinding(config, "chat", "interrupt_turn", "ctrl-x c") {
		t.Fatal("`leader c` should also match the resolved `ctrl-x c` stroke")
	}
	if KeymapActionHasBinding(config, "chat", "interrupt_turn", "ctrl-x") {
		t.Fatal("the leader prefix alone must not match the completion key")
	}
}

func TestRenderKeymapCatalogShowsResolvedLeaderLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("chat", "interrupt_turn", []string{"leader c"}); err != nil {
		t.Fatal(err)
	}
	rendered := RenderKeymapCatalogWithConfig(KeymapActionFilter{}, config)
	if !strings.Contains(rendered, "leader c (ctrl-x c)") {
		t.Fatalf("catalog did not resolve the leader spec:\n%s", rendered)
	}
	if !strings.Contains(RenderKeymapAction("chat", "interrupt_turn", config), "leader c (ctrl-x c)") {
		t.Fatalf("action view did not resolve the leader spec:\n%s", RenderKeymapAction("chat", "interrupt_turn", config))
	}
}

func TestRenderKeymapCatalogMarksInactiveLeaderLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("chat", "interrupt_turn", []string{"leader c"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Set("global", "copy", []string{"ctrl-x"}); err != nil {
		t.Fatal(err)
	}
	if rendered := RenderKeymapCatalogWithConfig(KeymapActionFilter{}, config); !strings.Contains(rendered, "leader c (inactive)") {
		t.Fatalf("catalog did not mark the leader inactive:\n%s", rendered)
	}
}

// `tui.keymap.global.leader` is a real config field, so the resolver runs when
// the keymap table is loaded from config values.
func TestKeymapConfigFromValuesValidatesLeaderLikeRust(t *testing.T) {
	values := map[string]any{
		"tui": map[string]any{
			"keymap": map[string]any{
				"global": map[string]any{"leader": "ctrl-c"},
			},
		},
	}
	if _, err := KeymapConfigFromConfigValues(values); err == nil || !strings.Contains(err.Error(), "reserved by `fixed.interrupt_or_quit`") {
		t.Fatalf("error = %v", err)
	}

	values["tui"] = map[string]any{
		"keymap": map[string]any{
			"global": map[string]any{
				"leader":          "alt-x",
				"open_transcript": "leader c",
			},
		},
	}
	config, err := KeymapConfigFromConfigValues(values)
	if err != nil {
		t.Fatal(err)
	}
	prefixes, err := LeaderKeybindings(config)
	if err != nil || !reflect.DeepEqual(prefixes, []string{"alt-x"}) {
		t.Fatalf("prefixes = %#v err = %v", prefixes, err)
	}
	if got := RenderKeymapAction("global", "open_transcript", config); !strings.Contains(got, "leader c (alt-x c)") {
		t.Fatalf("action view = %q", got)
	}
}
