package tui

import (
	"strconv"
	"strings"
)

// KeymapPageRow is one row of Rust's `/keymap` picker
// (keymap_setup/picker.rs::KeymapActionRow).
type KeymapPageRow struct {
	Context        string
	ContextLabel   string
	Action         string
	Label          string
	Description    string
	BindingSummary string
	Custom         bool
	Debug          bool
	// Placeholder marks the disabled "No ... shortcuts" stand-in Rust adds to
	// an empty tab. It renders without a context prefix.
	Placeholder         bool
	SelectedDescription string
}

// Unbound reports whether the row has no active shortcut.
func (r KeymapPageRow) Unbound() bool {
	return r.BindingSummary == "unbound"
}

// Source mirrors the searchable source column Rust appends to the search value.
func (r KeymapPageRow) Source() string {
	if r.Custom {
		return "Custom"
	}
	return "Default"
}

// SearchText mirrors keymap_selection_item's search_value.
func (r KeymapPageRow) SearchText() string {
	if r.Debug {
		return "debug inspect keypress key terminal detected actions"
	}
	return strings.Join([]string{
		r.ContextLabel,
		r.Action,
		r.Label,
		r.Description,
		r.BindingSummary,
		r.Source(),
	}, " ")
}

// KeymapBindingSummary mirrors format_action_binding_summary: the active
// single-key and chord alternatives in config order, de-duplicated, with the
// concrete leader strokes appended and `unbound` for an empty result.
func KeymapBindingSummary(config *KeymapConfig, context string, action string) string {
	bindings, _, _ := ResolvedKeymapBindings(config, context, action)
	display := KeymapDisplayBindings(config, bindings)
	seen := make(map[string]bool, len(display))
	specs := make([]string, 0, len(display))
	for _, spec := range display {
		spec = keymapDisplaySpec(strings.TrimSpace(spec))
		if spec == "" || seen[spec] || !KeymapStorableSpec(spec) {
			continue
		}
		seen[spec] = true
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		if chord, ok := keymapImplicitChordSummary(config, context, action); ok {
			return chord
		}
		return "unbound"
	}
	return strings.Join(specs, ", ")
}

// keymapImplicitChordSummary mirrors the implicit Vim `g g` buffer-top chord
// (keymap/chords.rs): jump_top and motion_jump_top have no single-key default,
// yet the picker still lists `g g` unless the action is configured or another
// binding already owns a plain `g`.
func keymapImplicitChordSummary(config *KeymapConfig, context, action string) (string, bool) {
	vimJumpTop := context == "vim_normal" && action == "jump_top"
	vimOperatorJumpTop := context == "vim_operator" && action == "motion_jump_top"
	if !vimJumpTop && !vimOperatorJumpTop {
		return "", false
	}
	if config != nil {
		if _, ok := config.Binding(context, action); ok {
			return "", false
		}
		if config.contextAliasConfigured("g") {
			return "", false
		}
	}
	return "g g", true
}

// keymapDisplaySpec normalizes a rendered binding the way Rust's
// value_of_keybinding does: an uppercase character carries the shift modifier,
// so `A` and `shift-a` are the same config spec and appear once.
func keymapDisplaySpec(spec string) string {
	if len(spec) == 1 && spec[0] >= 'A' && spec[0] <= 'Z' {
		return "shift-" + strings.ToLower(spec)
	}
	return spec
}

// KeymapStorableSpec mirrors key_parts_to_config_key_spec in Rust: /keymap
// never advertises a binding the config cannot hold, so a default such as the
// Insert key (enter_insert) is hidden instead of rendered as a shortcut that
// `tui.keymap` would reject.
func KeymapStorableSpec(spec string) bool {
	if strings.ContainsAny(spec, " (") {
		// Two-stroke chords and resolved leader summaries are rendered specs,
		// not single key names, and every stroke inside them came from the
		// config already.
		return true
	}
	base := spec
	for {
		trimmed := false
		for _, prefix := range []string{"ctrl-", "alt-", "shift-", "super-"} {
			if strings.HasPrefix(base, prefix) && len(base) > len(prefix) {
				base = strings.TrimPrefix(base, prefix)
				trimmed = true
				break
			}
		}
		if !trimmed {
			break
		}
	}
	switch base {
	case "enter", "tab", "backspace", "esc", "delete", "up", "down", "left", "right",
		"home", "end", "page-up", "page-down", "space", "minus":
		return true
	}
	if len(base) > 1 && base[0] == 'f' {
		if number, err := strconv.Atoi(base[1:]); err == nil {
			return number >= 1 && number <= 24
		}
		return false
	}
	if len(base) == 1 {
		character := base[0]
		return character >= 0x20 && character < 0x7f
	}
	return false
}

// KeymapActionMatch is one catalog action whose active binding presses the
// inspected key (keymap_setup/actions.rs::KeymapDebugActionMatch).
type KeymapActionMatch struct {
	Context     string
	Action      string
	Label       string
	Description string
	// Source mirrors KeymapDebugBindingSource::label: "Custom", "Custom
	// global" (composer falling back to a global binding) or "Default".
	Source string
}

// KeymapActionsForKey lists the catalog actions a single key spec presses, in
// catalog order, for the /keymap Debug tab's live inspector.
func KeymapActionsForKey(filter KeymapActionFilter, config *KeymapConfig, keySpec string) []KeymapActionMatch {
	keySpec = strings.TrimSpace(keySpec)
	if keySpec == "" {
		return nil
	}
	matches := []KeymapActionMatch{}
	for _, action := range KeymapActions(filter) {
		if keymapPageHiddenActions[action.Context+"."+action.Action] {
			continue
		}
		bindings, _, _ := ResolvedKeymapBindings(config, action.Context, action.Action)
		matched := false
		for _, binding := range bindings {
			if normalized, err := NormalizeKeybindingSpec(binding); err == nil && normalized == keySpec {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		matches = append(matches, KeymapActionMatch{
			Context:     action.Context,
			Action:      action.Action,
			Label:       action.Label,
			Description: action.Description,
			Source:      keymapActionMatchSource(config, action.Context, action.Action),
		})
	}
	return matches
}

// keymapActionMatchSource mirrors debug_binding_source: a root-level override
// wins, then the composer's global fallback, otherwise the built-in default.
func keymapActionMatchSource(config *KeymapConfig, context string, action string) string {
	if config == nil {
		return "Default"
	}
	if config.HasCustomBinding(context, action) {
		return "Custom"
	}
	if context == "composer" {
		if _, ok := config.Binding("global", action); ok {
			return "Custom global"
		}
	}
	return "Default"
}

// keymapPageHiddenActions are catalog entries upstream does not list in
// KEYMAP_ACTIONS. The catalog keeps them for Go-side behaviors, but Rust's
// /keymap page shows exactly 154 rows, so the page skips them.
var keymapPageHiddenActions = map[string]bool{
	"agents.toggle_pin":    true,
	"global.open_warnings": true,
}

// KeymapPageRows builds the picker rows for every action visible under filter.
func KeymapPageRows(filter KeymapActionFilter, config *KeymapConfig) []KeymapPageRow {
	actions := KeymapActions(filter)
	rows := make([]KeymapPageRow, 0, len(actions))
	for _, action := range actions {
		if keymapPageHiddenActions[action.Context+"."+action.Action] {
			continue
		}
		rows = append(rows, KeymapPageRow{
			Context:        action.Context,
			ContextLabel:   action.ContextLabel,
			Action:         action.Action,
			Label:          action.Label,
			Description:    action.Description,
			BindingSummary: KeymapBindingSummary(config, action.Context, action.Action),
			Custom:         config != nil && config.HasCustomBinding(action.Context, action.Action),
		})
	}
	return rows
}

// KeymapPageTab is one Rust SelectionTab: a header plus its rows.
type KeymapPageTab struct {
	Label       string
	Description string
	Summary     string
	Rows        []KeymapPageRow
	Debug       bool
}

// keymapCommonActions mirrors KEYMAP_COMMON_ACTIONS in picker.rs, in order.
var keymapCommonActions = [][2]string{
	{"composer", "submit"},
	{"chat", "interrupt_turn"},
	{"editor", "insert_newline"},
	{"composer", "queue"},
	{"global", "toggle_fast_mode"},
	{"global", "open_external_editor"},
	{"global", "copy"},
	{"global", "toggle_vim_mode"},
	{"editor", "delete_backward_word"},
	{"editor", "delete_forward_word"},
	{"editor", "move_word_left"},
	{"editor", "move_word_right"},
	{"global", "open_transcript"},
	{"pager", "close"},
	{"pager", "page_up"},
	{"pager", "page_down"},
	{"approval", "open_fullscreen"},
	{"approval", "approve"},
	{"approval", "approve_for_session"},
	{"approval", "decline"},
	{"approval", "cancel"},
}

type keymapContextTab struct {
	label       string
	description string
	contexts    []string
}

// keymapContextTabs mirrors KEYMAP_CONTEXT_TABS in picker.rs.
var keymapContextTabs = []keymapContextTab{
	{"App", "Global and chat-level shortcuts.", []string{"global", "chat"}},
	{"Composer", "Composer submission and queue shortcuts.", []string{"composer"}},
	{"Editor", "Inline editor movement and editing shortcuts.", []string{"editor"}},
	{"Vim", "Vim normal-mode and operator shortcuts.", []string{"vim_normal", "vim_operator", "vim_search", "vim_text_object"}},
	{"Navigation", "Pager and selection-list navigation shortcuts.", []string{"pager", "list"}},
	{"Agents", "Shared agents dashboard shortcuts.", []string{"agents"}},
	{"Approval", "Approval prompt shortcuts.", []string{"approval"}},
}

// keymapActionCountLine mirrors action_count_line.
func keymapActionCountLine(count int) string {
	if count == 1 {
		return "1 action."
	}
	return strconv.Itoa(count) + " actions."
}

// KeymapPageTabs mirrors build_keymap_picker_params_for_action's tab list.
func KeymapPageTabs(rows []KeymapPageRow) []KeymapPageTab {
	total := len(rows)
	custom := 0
	unbound := 0
	for _, row := range rows {
		if row.Custom {
			custom++
		}
		if row.Unbound() {
			unbound++
		}
	}
	tabs := make([]KeymapPageTab, 0, 12)
	tabs = append(tabs, KeymapPageTab{
		Label:       "All",
		Description: "All configurable shortcuts.",
		Summary:     strconv.Itoa(total) + " actions, " + strconv.Itoa(custom) + " customized, " + strconv.Itoa(unbound) + " unbound.",
		Rows:        rows,
	})

	commonRows := make([]KeymapPageRow, 0, len(keymapCommonActions))
	for _, pair := range keymapCommonActions {
		for _, row := range rows {
			if row.Context == pair[0] && row.Action == pair[1] {
				commonRows = append(commonRows, row)
				break
			}
		}
	}
	tabs = append(tabs, KeymapPageTab{
		Label:       "Common",
		Description: "Frequently customized shortcuts.",
		Summary:     keymapActionCountLine(len(commonRows)),
		Rows:        commonRows,
	})

	customRows := make([]KeymapPageRow, 0, custom)
	for _, row := range rows {
		if row.Custom {
			customRows = append(customRows, row)
		}
	}
	tabs = append(tabs, KeymapPageTab{
		Label:       "Customized (" + strconv.Itoa(custom) + ")",
		Description: "Root-level shortcut overrides.",
		Summary:     keymapActionCountLine(custom),
		Rows:        customRows,
	})

	unboundRows := make([]KeymapPageRow, 0, unbound)
	for _, row := range rows {
		if row.Unbound() {
			unboundRows = append(unboundRows, row)
		}
	}
	tabs = append(tabs, KeymapPageTab{
		Label:       "Unbound (" + strconv.Itoa(unbound) + ")",
		Description: "Actions without an active shortcut.",
		Summary:     keymapActionCountLine(unbound),
		Rows:        unboundRows,
	})

	for _, contextTab := range keymapContextTabs {
		tabRows := make([]KeymapPageRow, 0)
		for _, row := range rows {
			for _, context := range contextTab.contexts {
				if row.Context == context {
					tabRows = append(tabRows, row)
					break
				}
			}
		}
		tabs = append(tabs, KeymapPageTab{
			Label:       contextTab.label,
			Description: contextTab.description,
			Summary:     keymapActionCountLine(len(tabRows)),
			Rows:        tabRows,
		})
	}

	tabs = append(tabs, KeymapPageTab{
		Label:       "Debug",
		Description: "Inspect keypresses from your terminal.",
		Summary:     "See the key Codex detects and any shortcuts assigned to it.",
		Debug:       true,
		Rows: []KeymapPageRow{{
			Label:               "Inspect keypresses",
			Description:         "Press Enter, then any key to inspect it (Ctrl+C exits)",
			SelectedDescription: "Open a live inspector that shows the detected key, config key, and matching actions",
			Debug:               true,
		}},
	})
	return tabs
}

// KeymapEmptyTabRow mirrors keymap_selection_items' disabled placeholder.
func KeymapEmptyRow(tab KeymapPageTab) (KeymapPageRow, bool) {
	if len(tab.Rows) != 0 {
		return KeymapPageRow{}, false
	}
	switch tab.Label {
	case "Common":
		return KeymapPageRow{Label: "No common shortcuts", Description: "No common shortcut actions are available", Placeholder: true}, true
	case "Customized (0)":
		return KeymapPageRow{Label: "No customized shortcuts", Description: "No root-level keymap overrides have been configured", Placeholder: true}, true
	default:
		if strings.HasPrefix(tab.Label, "Unbound (") {
			return KeymapPageRow{Label: "No unbound shortcuts", Description: "Every configurable action currently has a shortcut", Placeholder: true}, true
		}
		if tab.Description == "All configurable shortcuts." {
			return KeymapPageRow{Label: "No shortcuts available", Description: "No configurable shortcuts are available", Placeholder: true}, true
		}
		return KeymapPageRow{Label: "No shortcuts in this group", Description: "No configurable actions are available in this group", Placeholder: true}, true
	}
}
