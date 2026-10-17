package keymapschema

import "strings"

// ContextActions is the set of keymap contexts and actions the TUI accepts.
// It mirrors the strongly typed Rust keymap structs (`config/src/tui_keymap.rs`).
// Unknown keys are ignored during deserialization and reported by the shared
// strict-config policy (Rust #54766), so both the TUI parser and the strict
// validator read this one table.
var ContextActions = map[string]map[string]struct{}{
	"global": {
		"open_transcript":          {},
		"find_transcript":          {},
		"open_agents":              {},
		"open_warnings":            {},
		"open_external_editor":     {},
		"copy":                     {},
		"clear_terminal":           {},
		"toggle_vim_mode":          {},
		"toggle_fast_mode":         {},
		"toggle_raw_output":        {},
		"toggle_side_conversation": {},
		"leader":                   {},
		"submit":                   {},
		"queue":                    {},
		"toggle_shortcuts":         {},
	},
	"chat": {
		"interrupt_turn":            {},
		"toggle_voice":              {},
		"toggle_voice_mute":         {},
		"decrease_reasoning_effort": {},
		"increase_reasoning_effort": {},
		"previous_permission_mode":  {},
		"next_permission_mode":      {},
		"edit_queued_message":       {},
		"prompt_stack_back":         {},
		"skip_question":             {},
	},
	"composer": {
		"submit":                  {},
		"queue":                   {},
		"toggle_shortcuts":        {},
		"history_search_previous": {},
		"history_search_next":     {},
	},
	"editor": {
		"insert_newline":       {},
		"move_left":            {},
		"move_right":           {},
		"move_up":              {},
		"move_down":            {},
		"move_word_left":       {},
		"move_word_right":      {},
		"move_line_start":      {},
		"move_line_end":        {},
		"delete_backward":      {},
		"delete_forward":       {},
		"delete_backward_word": {},
		"delete_forward_word":  {},
		"kill_line_start":      {},
		"kill_whole_line":      {},
		"kill_line_end":        {},
		"yank":                 {},
	},
	"vim_normal": {
		"enter_insert":          {},
		"append_after_cursor":   {},
		"append_line_end":       {},
		"insert_line_start":     {},
		"open_line_below":       {},
		"open_line_above":       {},
		"move_left":             {},
		"move_right":            {},
		"move_up":               {},
		"move_down":             {},
		"move_word_forward":     {},
		"move_word_backward":    {},
		"move_word_end":         {},
		"undo":                  {},
		"redo":                  {},
		"move_line_start":       {},
		"move_line_end":         {},
		"search_forward":        {},
		"search_backward":       {},
		"search_next":           {},
		"search_prev":           {},
		"jump_top":              {},
		"jump_bottom":           {},
		"find_char_forward":     {},
		"find_char_backward":    {},
		"till_char_forward":     {},
		"till_char_backward":    {},
		"delete_char":           {},
		"replace_char":          {},
		"replace_mode":          {},
		"substitute_char":       {},
		"delete_to_line_end":    {},
		"change_to_line_end":    {},
		"yank_line":             {},
		"paste_after":           {},
		"repeat_last_change":    {},
		"start_delete_operator": {},
		"start_yank_operator":   {},
		"start_change_operator": {},
		"cancel_operator":       {},
	},
	"vim_operator": {
		"delete_line":               {},
		"yank_line":                 {},
		"motion_left":               {},
		"motion_right":              {},
		"motion_up":                 {},
		"motion_down":               {},
		"motion_word_forward":       {},
		"motion_word_backward":      {},
		"motion_word_end":           {},
		"motion_line_start":         {},
		"motion_line_end":           {},
		"motion_jump_top":           {},
		"motion_jump_bottom":        {},
		"motion_find_forward":       {},
		"motion_find_backward":      {},
		"motion_till_forward":       {},
		"motion_till_backward":      {},
		"select_inner_text_object":  {},
		"select_around_text_object": {},
		"cancel":                    {},
	},
	"vim_text_object": {
		"word":         {},
		"big_word":     {},
		"parentheses":  {},
		"brackets":     {},
		"braces":       {},
		"double_quote": {},
		"single_quote": {},
		"backtick":     {},
		"cancel":       {},
	},
	"pager": {
		"scroll_up":        {},
		"scroll_down":      {},
		"page_up":          {},
		"page_down":        {},
		"half_page_up":     {},
		"half_page_down":   {},
		"jump_top":         {},
		"jump_bottom":      {},
		"close":            {},
		"close_transcript": {},
		"find":             {},
	},
	"list": {
		"move_up":     {},
		"move_down":   {},
		"move_left":   {},
		"move_right":  {},
		"page_up":     {},
		"page_down":   {},
		"jump_top":    {},
		"jump_bottom": {},
		"accept":      {},
		"cancel":      {},
	},
	"agents": {
		"resume":          {},
		"search":          {},
		"new_task":        {},
		"new_worktree":    {},
		"fork":            {},
		"rename":          {},
		"stop":            {},
		"hide":            {},
		"archive":         {},
		"delete":          {},
		"toggle_pin":      {},
		"toggle_grouping": {},
	},
	"approval": {
		"open_fullscreen":     {},
		"open_thread":         {},
		"approve":             {},
		"approve_for_session": {},
		"approve_for_prefix":  {},
		"deny":                {},
		"decline":             {},
		"cancel":              {},
	},
}

// ActionAliases maps an upstream keymap action name to the Go catalog entry
// that implements the same behavior. Upstream (`config/src/tui_keymap.rs`)
// keeps these names, so a Rust-authored config must stay valid here even
// though Go implements the behavior under a different action (and, for vim
// search, a different context). The TUI loader applies the alias, and the
// shared strict-config policy accepts the upstream spelling instead of
// reporting it as an unknown field (Rust #54766).
var ActionAliases = map[string]string{
	"vim_normal.enter_replace_mode": "vim_normal.replace_mode",
	"vim_normal.find_forward":       "vim_normal.find_char_forward",
	"vim_normal.find_backward":      "vim_normal.find_char_backward",
	"vim_normal.till_forward":       "vim_normal.till_char_forward",
	"vim_normal.till_backward":      "vim_normal.till_char_backward",
	"vim_search.forward":            "vim_normal.search_forward",
	"vim_search.backward":           "vim_normal.search_backward",
	"vim_search.next":               "vim_normal.search_next",
	"vim_search.previous":           "vim_normal.search_prev",
}

// KnownContext reports whether the keymap context is part of the schema.
func KnownContext(context string) bool {
	if _, ok := ContextActions[context]; ok {
		return true
	}
	for key := range ActionAliases {
		if aliasContext, _, _ := strings.Cut(key, "."); aliasContext == context {
			return true
		}
	}
	return false
}

// KnownAction reports whether the action belongs to the context, either
// directly or through an upstream action alias.
func KnownAction(context, action string) bool {
	if actions, ok := ContextActions[context]; ok {
		if _, ok := actions[action]; ok {
			return true
		}
	}
	_, ok := ActionAliases[context+"."+action]
	return ok
}

// ResolveAlias maps an upstream action name to the Go catalog target.
func ResolveAlias(context, action string) (string, string, bool) {
	target, ok := ActionAliases[context+"."+action]
	if !ok {
		return context, action, false
	}
	goContext, goAction, _ := strings.Cut(target, ".")
	return goContext, goAction, true
}
