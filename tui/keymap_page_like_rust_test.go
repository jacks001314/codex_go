package tui

import "testing"

func keymapPageTabByLabel(t *testing.T, tabs []KeymapPageTab, label string) KeymapPageTab {
	t.Helper()
	for _, tab := range tabs {
		if tab.Label == label {
			return tab
		}
	}
	t.Fatalf("keymap tab %q not found", label)
	return KeymapPageTab{}
}

func keymapPageRowByAction(t *testing.T, rows []KeymapPageRow, context string, action string) KeymapPageRow {
	t.Helper()
	for _, row := range rows {
		if row.Context == context && row.Action == action {
			return row
		}
	}
	t.Fatalf("keymap row %s.%s not found", context, action)
	return KeymapPageRow{}
}

// TestKeymapPageRowsLikeRust locks the counts, labels and binding summaries
// that Rust 0.166 renders in its /keymap picker (out/km0-rust166.txt,
// out/km1-rust166.txt, out/km3-rust166.txt and the per-context tabs).
func TestKeymapPageRowsLikeRust(t *testing.T) {
	rows := KeymapPageRows(KeymapActionFilter{FastModeEnabled: true}, nil)
	tabs := KeymapPageTabs(rows)

	all := keymapPageTabByLabel(t, tabs, "All")
	if all.Summary != "154 actions, 0 customized, 6 unbound." {
		t.Fatalf("all summary = %q (rows=%d)", all.Summary, len(rows))
	}
	if got := keymapPageTabByLabel(t, tabs, "Common").Summary; got != "21 actions." {
		t.Fatalf("common summary = %q", got)
	}
	customized := keymapPageTabByLabel(t, tabs, "Customized (0)")
	if customized.Summary != "0 actions." || len(customized.Rows) != 0 {
		t.Fatalf("customized tab = %q rows:%d", customized.Summary, len(customized.Rows))
	}
	unbound := keymapPageTabByLabel(t, tabs, "Unbound (6)")
	if unbound.Summary != "6 actions." || len(unbound.Rows) != 6 {
		t.Fatalf("unbound tab = %q rows:%d", unbound.Summary, len(unbound.Rows))
	}
	wantUnbound := []string{
		"Open Agents", "Toggle Vim Mode", "Toggle Fast Mode",
		"Previous Permission Mode", "Next Permission Mode", "Kill Whole Line",
	}
	for index, label := range wantUnbound {
		if unbound.Rows[index].Label != label {
			t.Fatalf("unbound row %d = %q, want %q", index, unbound.Rows[index].Label, label)
		}
	}

	counts := map[string]int{
		"App": 23, "Composer": 5, "Editor": 17, "Vim": 69,
		"Navigation": 21, "Agents": 11, "Approval": 8,
	}
	for label, want := range counts {
		tab := keymapPageTabByLabel(t, tabs, label)
		if len(tab.Rows) != want {
			t.Fatalf("%s rows = %d, want %d", label, len(tab.Rows), want)
		}
		if tab.Summary != keymapActionCountLine(want) {
			t.Fatalf("%s summary = %q", label, tab.Summary)
		}
	}

	debug := keymapPageTabByLabel(t, tabs, "Debug")
	if debug.Summary != "See the key Codex detects and any shortcuts assigned to it." || len(debug.Rows) != 1 {
		t.Fatalf("debug tab = %q rows:%d", debug.Summary, len(debug.Rows))
	}

	for _, tc := range []struct {
		context string
		action  string
		label   string
		summary string
	}{
		{"global", "copy", "Copy", "ctrl-o"},
		{"global", "open_agents", "Open Agents", "unbound"},
		{"chat", "open_presets", "Open Presets", "leader p (ctrl-x p)"},
		{"chat", "decrease_reasoning_effort", "Decrease Reasoning Effort", "alt-,, shift-down"},
		{"editor", "insert_newline", "Insert Newline", "ctrl-j, ctrl-m, enter, shift-enter, alt-enter"},
		{"global", "toggle_side_conversation", "Toggle Side Conversation", "ctrl-/"},
	} {
		row := keymapPageRowByAction(t, rows, tc.context, tc.action)
		if row.Label != tc.label || row.BindingSummary != tc.summary {
			t.Fatalf("%s.%s = %q / %q, want %q / %q", tc.context, tc.action, row.Label, row.BindingSummary, tc.label, tc.summary)
		}
	}

	labels := make([]string, len(tabs))
	for index, tab := range tabs {
		labels[index] = tab.Label
	}
	wantLabels := []string{"All", "Common", "Customized (0)", "Unbound (6)", "App", "Composer", "Editor", "Vim", "Navigation", "Agents", "Approval", "Debug"}
	if len(labels) != len(wantLabels) {
		t.Fatalf("tabs = %#v", labels)
	}
	for index, label := range wantLabels {
		if labels[index] != label {
			t.Fatalf("tab %d = %q, want %q", index, labels[index], label)
		}
	}
}

// TestKeymapPageVimTabLikeRust locks the Vim tab against out/km7-rust166.txt:
// upstream's action spellings, the separate Vim search context, the implicit
// `g g` buffer-top chord, and the rule that a binding the config cannot store
// (the Insert key) stays out of the summary.
func TestKeymapPageVimTabLikeRust(t *testing.T) {
	rows := KeymapPageRows(KeymapActionFilter{FastModeEnabled: true}, nil)
	vim := keymapPageTabByLabel(t, KeymapPageTabs(rows), "Vim")
	if len(vim.Rows) != 69 {
		t.Fatalf("Vim rows = %d, want 69", len(vim.Rows))
	}
	want := []struct {
		contextLabel string
		label        string
		summary      string
	}{
		{"Vim normal", "Enter Insert", "i"},
		{"Vim normal", "Append After Cursor", "a"},
		{"Vim normal", "Append Line End", "shift-a"},
		{"Vim normal", "Insert Line Start", "shift-i"},
		{"Vim normal", "Open Line Below", "o"},
		{"Vim normal", "Open Line Above", "shift-o"},
		{"Vim normal", "Enter Replace Mode", "shift-r"},
		{"Vim normal", "Move Left", "h, left"},
		{"Vim normal", "Move Right", "l, right"},
		{"Vim normal", "Move Up", "k, up"},
		{"Vim normal", "Move Down", "j, down"},
		{"Vim normal", "Move Word Forward", "w"},
		{"Vim normal", "Move Word Backward", "b"},
		{"Vim normal", "Move Word End", "e"},
		{"Vim normal", "Move Line Start", "0"},
		{"Vim normal", "Move Line End", "$, shift-$"},
		{"Vim normal", "Find Forward", "f"},
		{"Vim normal", "Find Backward", "shift-f"},
		{"Vim normal", "Till Forward", "t"},
		{"Vim normal", "Till Backward", "shift-t"},
		{"Vim normal", "Jump Top", "g g"},
		{"Vim normal", "Jump Bottom", "shift-g"},
		{"Vim normal", "Delete Char", "x"},
		{"Vim normal", "Replace Char", "r"},
	}
	for index, tc := range want {
		row := vim.Rows[index]
		if row.ContextLabel != tc.contextLabel || row.Label != tc.label || row.BindingSummary != tc.summary {
			t.Fatalf("Vim row %d = %q %q / %q, want %q %q / %q", index, row.ContextLabel, row.Label, row.BindingSummary, tc.contextLabel, tc.label, tc.summary)
		}
	}
	search := []struct {
		label   string
		summary string
	}{
		{"Forward", "/"},
		{"Backward", "?, shift-?"},
		{"Next", "n"},
		{"Previous", "shift-n"},
	}
	for offset, tc := range search {
		index := 36 + offset
		row := vim.Rows[index]
		if row.Context != "vim_search" || row.ContextLabel != "Vim search" || row.Label != tc.label || row.BindingSummary != tc.summary {
			t.Fatalf("Vim row %d = %s %q / %q, want vim_search %q / %q", index, row.Context, row.Label, row.BindingSummary, tc.label, tc.summary)
		}
	}
	if row := vim.Rows[40]; row.Context != "vim_operator" || row.Label != "Delete Line" {
		t.Fatalf("Vim row 40 = %s %q, want the first Vim operator action", row.Context, row.Label)
	}
	if row := vim.Rows[55]; row.Context != "vim_operator" || row.Label != "Motion Jump Top" || row.BindingSummary != "g g" {
		t.Fatalf("Vim row 55 = %s %q / %q, want Motion Jump Top with the implicit g g chord", row.Context, row.Label, row.BindingSummary)
	}
}

// keymapActionMatchSummaries renders "<context>.<action>|<source>" so the
// assertions read like the inspector's "Assigned actions" block.
func keymapActionMatchSummaries(matches []KeymapActionMatch) []string {
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.Context+"."+match.Action+"|"+match.Source)
	}
	return out
}

// TestKeymapActionsForKeyLikeRust locks matching_actions_for_key_event: catalog
// order, hidden actions skipped, exactly the sets Rust 0.166 prints in the
// Keypress Inspector (out/kmdbg-*-go.txt, out/kmdbgx-*-go.txt).
func TestKeymapActionsForKeyLikeRust(t *testing.T) {
	cases := []struct {
		key  string
		want []string
	}{
		{
			key: "k",
			want: []string{
				"vim_normal.move_up|Default",
				"vim_operator.motion_up|Default",
				"pager.scroll_up|Default",
				"list.move_up|Default",
			},
		},
		{
			key: "ctrl-k",
			want: []string{
				"editor.kill_line_end|Default",
				"list.move_up|Default",
			},
		},
		{
			key: "esc",
			want: []string{
				"chat.interrupt_turn|Default",
				"vim_normal.cancel_operator|Default",
				"vim_operator.cancel|Default",
				"vim_text_object.cancel|Default",
				"list.cancel|Default",
				"approval.decline|Default",
			},
		},
		{
			key:  "shift-a",
			want: []string{"vim_normal.append_line_end|Default"},
		},
		{
			key: "left",
			want: []string{
				"editor.move_left|Default",
				"vim_normal.move_left|Default",
				"list.move_left|Default",
			},
		},
		{
			key:  "space",
			want: []string{"pager.page_down|Default"},
		},
		{
			key:  "f1",
			want: []string{},
		},
		{
			key: "home",
			want: []string{
				"editor.move_line_start|Default",
				"pager.jump_top|Default",
				"list.jump_top|Default",
			},
		},
		{
			key: "page-down",
			want: []string{
				"pager.page_down|Default",
				"list.page_down|Default",
			},
		},
		{
			key: "backspace",
			want: []string{
				"editor.delete_backward|Default",
				"agents.delete|Default",
			},
		},
		{
			key:  "tab",
			want: []string{"composer.queue|Default"},
		},
		{
			key:  "insert",
			want: []string{"vim_normal.enter_insert|Default"},
		},
	}
	for _, testCase := range cases {
		matches := KeymapActionsForKey(KeymapActionFilter{}, nil, testCase.key)
		got := keymapActionMatchSummaries(matches)
		if len(got) != len(testCase.want) {
			t.Fatalf("%q matches = %#v, want %#v", testCase.key, got, testCase.want)
		}
		for index := range testCase.want {
			if got[index] != testCase.want[index] {
				t.Fatalf("%q matches = %#v, want %#v", testCase.key, got, testCase.want)
			}
		}
	}
	if matches := KeymapActionsForKey(KeymapActionFilter{}, nil, ""); len(matches) != 0 {
		t.Fatalf("empty spec matches = %#v", keymapActionMatchSummaries(matches))
	}
	for _, match := range KeymapActionsForKey(KeymapActionFilter{}, nil, "f2") {
		if match.Context == "global" && match.Action == "open_warnings" {
			t.Fatalf("hidden global.open_warnings leaked into the inspector matches")
		}
	}
}

// TestKeymapActionsForKeyBindingSourceLikeRust covers debug_binding_source:
// a root-level override is Custom, and a composer action falling back to a
// root-level binding is Custom global.
func TestKeymapActionsForKeyBindingSourceLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("global", "open_transcript", []string{"ctrl-k"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range KeymapActionsForKey(KeymapActionFilter{}, config, "ctrl-k") {
		if match.Context == "global" && match.Action == "open_transcript" {
			found = true
			if match.Source != "Custom" {
				t.Fatalf("global.open_transcript source = %q, want Custom", match.Source)
			}
		}
	}
	if !found {
		t.Fatalf("custom global.open_transcript missing from ctrl-k matches")
	}

	fallback := NewKeymapConfig()
	// The composer falls back to a root-level binding with the same action
	// name; Set validates against the catalog, so seed the raw table directly.
	fallback.setRaw("global", "history_search_previous", []string{"ctrl-t"})
	found = false
	for _, match := range KeymapActionsForKey(KeymapActionFilter{}, fallback, "ctrl-t") {
		if match.Context == "composer" && match.Action == "history_search_previous" {
			found = true
			if match.Source != "Custom global" {
				t.Fatalf("composer.history_search_previous source = %q, want Custom global", match.Source)
			}
		}
	}
	if !found {
		t.Fatalf("composer fallback missing from ctrl-t matches")
	}
}
