package tui

import (
	"fmt"
	"strings"
)

type KeymapAction struct {
	Context         string
	ContextLabel    string
	Action          string
	Label           string
	Description     string
	DefaultBindings []string
	RequiredFeature string
}

type KeymapActionFilter struct {
	FastModeEnabled bool
}

const keymapFeatureFastMode = "fast_mode"

var keymapActionCatalog = []KeymapAction{
	keymapAction("global", "Global", "open_agents", "Open the shared agents overview.", nil),
	keymapAction("global", "Global", "open_transcript", "Open the transcript overlay.", []string{"ctrl-t"}),
	keymapAction("global", "Global", "find_transcript", "Find text in the full transcript.", []string{"f3"}),
	keymapAction("global", "Global", "focus_activity", "Focus transcript activity groups to expand or collapse their details.", []string{"f4"}),
	keymapAction("global", "Global", "open_external_editor", "Open the current draft in an external editor.", []string{"ctrl-g"}),
	keymapAction("global", "Global", "copy", "Copy the last agent response to the clipboard.", []string{"ctrl-o"}),
	keymapAction("global", "Global", "clear_terminal", "Clear the terminal UI.", []string{"ctrl-l"}),
	keymapAction("global", "Global", "toggle_vim_mode", "Turn Vim composer mode on or off.", nil),
	keymapGatedAction("global", "Global", "toggle_fast_mode", "Turn Fast mode on or off.", nil, keymapFeatureFastMode),
	keymapAction("global", "Global", "toggle_raw_output", "Toggle raw scrollback mode.", []string{"alt-r"}),
	keymapAction("global", "Global", "toggle_side_conversation", "Switch between a side conversation and its parent.", []string{"ctrl-/"}),
	keymapAction("global", "Global", "leader", "Set the prefix used by shared leader shortcuts.", []string{"ctrl-x"}),
	keymapAction("global", "Global", "open_warnings", "Open the retained warnings viewer.", []string{"f2"}),
	keymapAction("chat", "Chat", "interrupt_turn", "Interrupt the active turn.", []string{"esc"}),
	keymapAction("chat", "Chat", "decrease_reasoning_effort", "Decrease reasoning effort.", []string{"alt-,", "shift-down"}),
	keymapAction("chat", "Chat", "increase_reasoning_effort", "Increase reasoning effort.", []string{"alt-.", "shift-up"}),
	keymapAction("chat", "Chat", "previous_permission_mode", "Switch to the previous available permission mode.", nil),
	keymapAction("chat", "Chat", "next_permission_mode", "Switch to the next available permission mode.", nil),
	keymapAction("chat", "Chat", "edit_queued_message", "Move forward through questions, then edit the last queued message.", []string{"shift-left", "alt-up"}),
	keymapAction("chat", "Chat", "prompt_stack_back", "Move back through questions toward the composer.", []string{"shift-right", "alt-down"}),
	keymapAction("chat", "Chat", "skip_question", "Skip the focused question.", []string{"ctrl-]"}),
	keymapAction("chat", "Chat", "toggle_voice", "Start or stop a voice conversation.", []string{"f8"}),
	keymapAction("chat", "Chat", "open_presets", "Open the saved preset menu.", []string{"leader p"}),
	keymapAction("chat", "Chat", "toggle_voice_mute", "Toggle the microphone in an active voice conversation.", []string{"f9"}),
	keymapAction("composer", "Composer", "submit", "Submit the current composer draft.", []string{"enter"}),
	keymapAction("composer", "Composer", "queue", "Queue the draft while a task is running.", []string{"tab"}),
	keymapAction("composer", "Composer", "toggle_shortcuts", "Show or hide the composer shortcut overlay.", []string{"?", "shift-?"}),
	keymapAction("composer", "Composer", "history_search_previous", "Open history search or move to the previous match.", []string{"ctrl-r"}),
	keymapAction("composer", "Composer", "history_search_next", "Move to the next history search match.", []string{"ctrl-s"}),
	keymapAction("editor", "Editor", "insert_newline", "Insert a newline in the editor.", []string{"ctrl-j", "ctrl-m", "enter", "shift-enter", "alt-enter"}),
	keymapAction("editor", "Editor", "move_left", "Move the cursor left.", []string{"left", "ctrl-b"}),
	keymapAction("editor", "Editor", "move_right", "Move the cursor right.", []string{"right", "ctrl-f"}),
	keymapAction("editor", "Editor", "move_up", "Move the cursor up.", []string{"up", "ctrl-p"}),
	keymapAction("editor", "Editor", "move_down", "Move the cursor down.", []string{"down", "ctrl-n"}),
	keymapAction("editor", "Editor", "move_word_left", "Move to the beginning of the previous word.", []string{"alt-b", "alt-left", "ctrl-left"}),
	keymapAction("editor", "Editor", "move_word_right", "Move to the end of the next word.", []string{"alt-f", "alt-right", "ctrl-right"}),
	keymapAction("editor", "Editor", "move_line_start", "Move to the beginning of the line.", []string{"home", "ctrl-a", "super-left"}),
	keymapAction("editor", "Editor", "move_line_end", "Move to the end of the line.", []string{"end", "ctrl-e", "super-right"}),
	keymapAction("editor", "Editor", "delete_backward", "Delete one grapheme to the left.", []string{"backspace", "shift-backspace", "ctrl-h"}),
	keymapAction("editor", "Editor", "delete_forward", "Delete one grapheme to the right.", []string{"delete", "shift-delete", "ctrl-d"}),
	keymapAction("editor", "Editor", "delete_backward_word", "Delete the previous word.", []string{"alt-backspace", "ctrl-backspace", "ctrl-shift-backspace", "ctrl-w", "ctrl-alt-h"}),
	keymapAction("editor", "Editor", "delete_forward_word", "Delete the next word.", []string{"alt-delete", "ctrl-delete", "ctrl-shift-delete", "alt-d"}),
	keymapAction("editor", "Editor", "kill_line_start", "Delete from cursor to line start.", []string{"ctrl-u", "super-backspace"}),
	keymapAction("editor", "Editor", "kill_whole_line", "Delete the current line.", nil),
	keymapAction("editor", "Editor", "kill_line_end", "Delete from cursor to line end.", []string{"ctrl-k"}),
	keymapAction("editor", "Editor", "yank", "Paste the kill buffer.", []string{"ctrl-y"}),
	keymapAction("vim_normal", "Vim normal", "enter_insert", "Enter insert mode at the cursor.", []string{"i", "insert"}),
	keymapAction("vim_normal", "Vim normal", "append_after_cursor", "Enter insert mode after the cursor.", []string{"a"}),
	keymapAction("vim_normal", "Vim normal", "append_line_end", "Enter insert mode at end of line.", []string{"shift-a", "A"}),
	keymapAction("vim_normal", "Vim normal", "insert_line_start", "Enter insert mode at the first non-blank character.", []string{"shift-i", "I"}),
	keymapAction("vim_normal", "Vim normal", "open_line_below", "Open a new line below and enter insert mode.", []string{"o"}),
	keymapAction("vim_normal", "Vim normal", "open_line_above", "Open a new line above and enter insert mode.", []string{"shift-o", "O"}),
	keymapAction("vim_normal", "Vim normal", "enter_replace_mode", "Enter replace mode and overwrite characters.", []string{"shift-r", "R"}),
	keymapAction("vim_normal", "Vim normal", "move_left", "Move left in Vim normal mode.", []string{"h", "left"}),
	keymapAction("vim_normal", "Vim normal", "move_right", "Move right in Vim normal mode.", []string{"l", "right"}),
	keymapAction("vim_normal", "Vim normal", "move_up", "Move up or recall older history in Vim normal mode.", []string{"k", "up"}),
	keymapAction("vim_normal", "Vim normal", "move_down", "Move down or recall newer history in Vim normal mode.", []string{"j", "down"}),
	keymapAction("vim_normal", "Vim normal", "move_word_forward", "Move to the start of the next word.", []string{"w"}),
	keymapAction("vim_normal", "Vim normal", "move_word_backward", "Move to the start of the previous word.", []string{"b"}),
	keymapAction("vim_normal", "Vim normal", "move_word_end", "Move to the end of the current or next word.", []string{"e"}),
	keymapAction("vim_normal", "Vim normal", "move_line_start", "Move to the start of the line.", []string{"0"}),
	keymapAction("vim_normal", "Vim normal", "move_line_end", "Move to the end of the line.", []string{"$", "shift-$"}),
	keymapAction("vim_normal", "Vim normal", "find_forward", "Find the next character on the current line.", []string{"f"}),
	keymapAction("vim_normal", "Vim normal", "find_backward", "Find the previous character on the current line.", []string{"shift-f", "F"}),
	keymapAction("vim_normal", "Vim normal", "till_forward", "Stop before the next character on the current line.", []string{"t"}),
	keymapAction("vim_normal", "Vim normal", "till_backward", "Stop after the previous character on the current line.", []string{"shift-t", "T"}),
	keymapAction("vim_normal", "Vim normal", "jump_top", "Jump to the first buffer line.", nil),
	keymapAction("vim_normal", "Vim normal", "jump_bottom", "Jump to the last buffer line.", []string{"shift-g", "G"}),
	keymapAction("vim_normal", "Vim normal", "delete_char", "Delete the character under the cursor.", []string{"x"}),
	keymapAction("vim_normal", "Vim normal", "replace_char", "Replace the character under the cursor.", []string{"r"}),
	keymapAction("vim_normal", "Vim normal", "repeat_last_change", "Repeat the last complete edit.", []string{"."}),
	keymapAction("vim_normal", "Vim normal", "substitute_char", "Delete the character under the cursor and enter insert mode.", []string{"s"}),
	keymapAction("vim_normal", "Vim normal", "delete_to_line_end", "Delete from cursor to end of line.", []string{"shift-d", "D"}),
	keymapAction("vim_normal", "Vim normal", "change_to_line_end", "Change from cursor to end of line and enter insert mode.", []string{"shift-c", "C"}),
	keymapAction("vim_normal", "Vim normal", "yank_line", "Yank the entire line.", []string{"shift-y", "Y"}),
	keymapAction("vim_normal", "Vim normal", "paste_after", "Paste after the cursor.", []string{"p"}),
	keymapAction("vim_normal", "Vim normal", "start_delete_operator", "Begin a delete operator and wait for a motion.", []string{"d"}),
	keymapAction("vim_normal", "Vim normal", "start_yank_operator", "Begin a yank operator and wait for a motion.", []string{"y"}),
	keymapAction("vim_normal", "Vim normal", "start_change_operator", "Begin a change operator and wait for a motion or text object.", []string{"c"}),
	keymapAction("vim_normal", "Vim normal", "undo", "Undo the last complete edit.", []string{"u"}),
	keymapAction("vim_normal", "Vim normal", "redo", "Redo the last undone edit.", []string{"ctrl-r"}),
	keymapAction("vim_normal", "Vim normal", "cancel_operator", "Cancel a pending Vim operator.", []string{"esc"}),
	keymapAction("vim_search", "Vim search", "forward", "Search forward in the active buffer.", []string{"/"}),
	keymapAction("vim_search", "Vim search", "backward", "Search backward in the active buffer.", []string{"?", "shift-?"}),
	keymapAction("vim_search", "Vim search", "next", "Repeat the accepted search.", []string{"n"}),
	keymapAction("vim_search", "Vim search", "previous", "Repeat the search in the opposite direction.", []string{"shift-n", "N"}),
	keymapAction("vim_operator", "Vim operator", "delete_line", "Repeat delete operator to delete the whole line.", []string{"d"}),
	keymapAction("vim_operator", "Vim operator", "yank_line", "Repeat yank operator to yank the whole line.", []string{"y"}),
	keymapAction("vim_operator", "Vim operator", "motion_left", "Operator motion left.", []string{"h"}),
	keymapAction("vim_operator", "Vim operator", "motion_right", "Operator motion right.", []string{"l"}),
	keymapAction("vim_operator", "Vim operator", "motion_up", "Operator motion up.", []string{"k"}),
	keymapAction("vim_operator", "Vim operator", "motion_down", "Operator motion down.", []string{"j"}),
	keymapAction("vim_operator", "Vim operator", "motion_word_forward", "Operator motion to start of next word.", []string{"w"}),
	keymapAction("vim_operator", "Vim operator", "motion_word_backward", "Operator motion to start of previous word.", []string{"b"}),
	keymapAction("vim_operator", "Vim operator", "motion_word_end", "Operator motion to end of word.", []string{"e"}),
	keymapAction("vim_operator", "Vim operator", "motion_line_start", "Operator motion to line start.", []string{"0"}),
	keymapAction("vim_operator", "Vim operator", "motion_line_end", "Operator motion to line end.", []string{"$", "shift-$"}),
	keymapAction("vim_operator", "Vim operator", "motion_find_forward", "Operator motion to the next character on the current line.", []string{"f"}),
	keymapAction("vim_operator", "Vim operator", "motion_find_backward", "Operator motion to the previous character on the current line.", []string{"shift-f", "F"}),
	keymapAction("vim_operator", "Vim operator", "motion_till_forward", "Stop before the next character on the current line.", []string{"t"}),
	keymapAction("vim_operator", "Vim operator", "motion_till_backward", "Stop after the previous character on the current line.", []string{"shift-t", "T"}),
	keymapAction("vim_operator", "Vim operator", "motion_jump_top", "Operator motion to the first buffer line.", nil),
	keymapAction("vim_operator", "Vim operator", "motion_jump_bottom", "Operator motion to the last buffer line.", []string{"shift-g", "G"}),
	keymapAction("vim_operator", "Vim operator", "select_inner_text_object", "Select an inner text object.", []string{"i"}),
	keymapAction("vim_operator", "Vim operator", "select_around_text_object", "Select an around text object.", []string{"a"}),
	keymapAction("vim_operator", "Vim operator", "cancel", "Cancel the pending operator.", []string{"esc"}),
	keymapAction("vim_text_object", "Vim text object", "word", "Target the current word.", []string{"w"}),
	keymapAction("vim_text_object", "Vim text object", "big_word", "Target the current WORD.", []string{"shift-w", "W"}),
	keymapAction("vim_text_object", "Vim text object", "parentheses", "Target enclosing parentheses.", []string{"(", "shift-(", ")", "shift-)", "b"}),
	keymapAction("vim_text_object", "Vim text object", "brackets", "Target enclosing brackets.", []string{"[", "]"}),
	keymapAction("vim_text_object", "Vim text object", "braces", "Target enclosing braces.", []string{"{", "shift-{", "}", "shift-}", "shift-b", "B"}),
	keymapAction("vim_text_object", "Vim text object", "double_quote", "Target enclosing double quotes.", []string{"\"", "shift-\""}),
	keymapAction("vim_text_object", "Vim text object", "single_quote", "Target enclosing single quotes.", []string{"'"}),
	keymapAction("vim_text_object", "Vim text object", "backtick", "Target enclosing backticks.", []string{"`"}),
	keymapAction("vim_text_object", "Vim text object", "cancel", "Cancel the pending text object.", []string{"esc"}),
	keymapAction("pager", "Pager", "scroll_up", "Scroll up by one row.", []string{"up", "k"}),
	keymapAction("pager", "Pager", "scroll_down", "Scroll down by one row.", []string{"down", "j"}),
	keymapAction("pager", "Pager", "page_up", "Scroll up by one page.", []string{"page-up", "shift-space", "ctrl-b"}),
	keymapAction("pager", "Pager", "page_down", "Scroll down by one page.", []string{"page-down", "space", "ctrl-f"}),
	keymapAction("pager", "Pager", "half_page_up", "Scroll up by half a page.", []string{"ctrl-u"}),
	keymapAction("pager", "Pager", "half_page_down", "Scroll down by half a page.", []string{"ctrl-d"}),
	keymapAction("pager", "Pager", "jump_top", "Jump to the beginning.", []string{"home"}),
	keymapAction("pager", "Pager", "jump_bottom", "Jump to the end.", []string{"end"}),
	keymapAction("pager", "Pager", "close", "Close the pager overlay.", []string{"q", "ctrl-c"}),
	keymapAction("pager", "Pager", "close_transcript", "Close the transcript overlay.", []string{"ctrl-t"}),
	keymapAction("pager", "Pager", "find", "Find text in a transcript pager.", []string{"f3", "/"}),
	keymapAction("list", "List", "move_up", "Move list selection up.", []string{"up", "ctrl-p", "ctrl-k", "k"}),
	keymapAction("list", "List", "move_down", "Move list selection down.", []string{"down", "ctrl-n", "ctrl-j", "j"}),
	keymapAction("list", "List", "move_left", "Move horizontally left in list pickers.", []string{"left", "ctrl-h"}),
	keymapAction("list", "List", "move_right", "Move horizontally right in list pickers.", []string{"right", "ctrl-l"}),
	keymapAction("list", "List", "page_up", "Move list selection up by one page.", []string{"page-up", "ctrl-b"}),
	keymapAction("list", "List", "page_down", "Move list selection down by one page.", []string{"page-down", "ctrl-f"}),
	keymapAction("list", "List", "jump_top", "Jump to the first list item.", []string{"home"}),
	keymapAction("list", "List", "jump_bottom", "Jump to the last list item.", []string{"end"}),
	keymapAction("list", "List", "accept", "Accept the current list selection.", []string{"enter"}),
	keymapAction("list", "List", "cancel", "Cancel and close selection views.", []string{"esc"}),
	keymapAction("agents", "Agents", "resume", "Open the session resume picker.", []string{"o"}),
	keymapAction("agents", "Agents", "search", "Search the available agent tasks.", []string{"f3", "/"}),
	keymapAction("agents", "Agents", "new_task", "Open a new session in the selected checkout.", []string{"n"}),
	keymapAction("agents", "Agents", "new_worktree", "Open a new session in a worktree from the project default branch.", []string{"w"}),
	keymapAction("agents", "Agents", "fork", "Fork the selected conversation and open the new session.", []string{"f"}),
	keymapAction("agents", "Agents", "rename", "Rename the selected task.", []string{"r"}),
	keymapAction("agents", "Agents", "stop", "Stop the selected running task.", []string{"x"}),
	keymapAction("agents", "Agents", "archive", "Archive the selected task and its child agents.", []string{"a"}),
	keymapAction("agents", "Agents", "delete", "Permanently delete the selected task and its child agents.", []string{"backspace"}),
	keymapAction("agents", "Agents", "hide", "Hide the selected task until explicitly resumed.", []string{"h"}),
	keymapAction("agents", "Agents", "toggle_grouping", "Toggle grouping tasks by status or project.", []string{"g"}),
	keymapAction("agents", "Agents", "toggle_pin", "Pin or unpin the selected task in the shared sidebar section.", []string{"p"}),
	keymapAction("approval", "Approval", "open_fullscreen", "Open approval details fullscreen.", []string{"ctrl-a", "ctrl-shift-a"}),
	keymapAction("approval", "Approval", "open_thread", "Open the approval source thread when available.", []string{"o"}),
	keymapAction("approval", "Approval", "approve", "Approve the primary option.", []string{"y"}),
	keymapAction("approval", "Approval", "approve_for_session", "Approve for the session when available.", []string{"a"}),
	keymapAction("approval", "Approval", "approve_for_prefix", "Approve with an exec-policy prefix when available.", []string{"p"}),
	keymapAction("approval", "Approval", "deny", "Choose the explicit deny option when available.", []string{"d"}),
	keymapAction("approval", "Approval", "decline", "Decline and provide corrective guidance.", []string{"esc", "n"}),
	keymapAction("approval", "Approval", "cancel", "Cancel an elicitation request.", []string{"c"}),
	// Rust #49517 (keymap_setup/actions.rs:275, keymap.rs:1960): fork owns the plain f that agents.search used before the same change moved search to f3 and /.
}

// keymapConfigOnlyActions are bindings the Rust TuiKeymap struct exposes to
// config but deliberately keeps out of `KEYMAP_ACTIONS`, so the keymap setup
// editor and the catalog parity ledgers do not list them (Rust #57223:
// `config/src/tui_keymap.rs` has `select_transcript`, while
// `keymap_setup/actions.rs` does not). They stay remappable and unbindable.
var keymapConfigOnlyActions = []KeymapAction{
	keymapAction("global", "Global", "select_transcript", "Enter transcript selection mode.", []string{"leader ["}),
}

func KeymapActions(filter KeymapActionFilter) []KeymapAction {
	actions := make([]KeymapAction, 0, len(keymapActionCatalog))
	for _, action := range keymapActionCatalog {
		if action.RequiredFeature == keymapFeatureFastMode && !filter.FastModeEnabled {
			continue
		}
		action.DefaultBindings = append([]string(nil), action.DefaultBindings...)
		actions = append(actions, action)
	}
	return actions
}

// KeymapConfigActions returns every action the root-config target lookup
// accepts, including TuiKeymap fields such as `global.select_transcript` that
// are not part of the `/keymap` catalog.
func KeymapConfigActions(filter KeymapActionFilter) []KeymapAction {
	actions := KeymapActions(filter)
	for _, action := range keymapConfigOnlyActions {
		if action.RequiredFeature == keymapFeatureFastMode && !filter.FastModeEnabled {
			continue
		}
		action.DefaultBindings = append([]string(nil), action.DefaultBindings...)
		actions = append(actions, action)
	}
	return actions
}

func RenderKeymapCatalog(filter KeymapActionFilter) string {
	return RenderKeymapCatalogWithConfig(filter, nil)
}

func RenderKeymapCatalogWithConfig(filter KeymapActionFilter, config *KeymapConfig) string {
	var builder strings.Builder
	builder.WriteString("Codex TUI keymap:\n")

	currentContext := ""
	for _, action := range KeymapActions(filter) {
		if action.Context != currentContext {
			currentContext = action.Context
			if currentContext != "" {
				builder.WriteString("\n")
			}
			fmt.Fprintf(&builder, "  %s\n", action.ContextLabel)
		}
		resolved, source, custom := ResolvedKeymapBindings(config, action.Context, action.Action)
		bindings := "unbound"
		if len(resolved) > 0 {
			bindings = strings.Join(KeymapDisplayBindings(config, resolved), ", ")
		}
		sourceLabel := "default"
		if custom {
			sourceLabel = source
		}
		fmt.Fprintf(&builder, "    %s | %s | %s | %s\n", action.Label, bindings, sourceLabel, action.Description)
	}

	return builder.String()
}

func FindKeymapAction(context string, action string) (KeymapAction, bool) {
	for _, descriptor := range keymapActionCatalog {
		if descriptor.Context == context && descriptor.Action == action {
			descriptor.DefaultBindings = append([]string(nil), descriptor.DefaultBindings...)
			return descriptor, true
		}
	}
	for _, descriptor := range keymapConfigOnlyActions {
		if descriptor.Context == context && descriptor.Action == action {
			descriptor.DefaultBindings = append([]string(nil), descriptor.DefaultBindings...)
			return descriptor, true
		}
	}
	if context == "global" {
		switch action {
		case "submit", "queue", "toggle_shortcuts":
			if descriptor, ok := FindKeymapAction("composer", action); ok {
				descriptor.Context = "global"
				descriptor.ContextLabel = "Global"
				descriptor.DefaultBindings = nil
				return descriptor, true
			}
		}
	}
	return KeymapAction{}, false
}

func KeymapActionLabel(action string) string {
	words := strings.Split(action, "_")
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func keymapAction(context string, contextLabel string, action string, description string, defaultBindings []string) KeymapAction {
	return keymapGatedAction(context, contextLabel, action, description, defaultBindings, "")
}

func keymapGatedAction(context string, contextLabel string, action string, description string, defaultBindings []string, requiredFeature string) KeymapAction {
	return KeymapAction{
		Context:         context,
		ContextLabel:    contextLabel,
		Action:          action,
		Label:           KeymapActionLabel(action),
		Description:     description,
		DefaultBindings: append([]string(nil), defaultBindings...),
		RequiredFeature: requiredFeature,
	}
}
