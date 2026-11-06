package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

// Rust renders chatwidget selection views through the bottom-pane
// ListSelectionView: the header paragraph holds the bold title over the dim
// subtitle, the list owns a blank header gap plus its scroll-indicator rows, and
// one blank row separates the results from the footer hint.
func TestSkillsPickerRowsMatchRustSelectionView(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.openSkillsMenu()
	rows := strings.Split(strings.TrimRight(utils.StripANSI(model.viewString()), "\n"), "\n")
	var tail []string
	for index, row := range rows {
		if strings.TrimSpace(row) == "Skills" {
			tail = rows[index:]
			break
		}
	}
	if len(tail) == 0 {
		t.Fatalf("skills picker missing:\n%s", strings.Join(rows, "\n"))
	}
	for index := range tail {
		tail[index] = strings.TrimRight(tail[index], " ")
	}
	want := []string{
		"  Skills",
		"  Choose an action",
		"",
		"",
		"› 1. List skills            Tip: press $ to open this list directly",
		"  2. Enable/Disable Skills  Enable or disable skills",
		"",
		"  enter select · esc back",
	}
	if strings.Join(tail, "\n") != strings.Join(want, "\n") {
		t.Fatalf("skills picker rows = %#v, want %#v", tail, want)
	}
}

// Rust wraps an over-long row description at the pane edge and aligns the
// continuation under the description column instead of clipping it.
func TestSelectionViewWrapsDescriptionUnderItsColumnLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 60, Height: 30})
	model.openSelectionViewModal(ModalKindPermissions, chatwidget.SelectionView{
		ViewID: "wrap-check",
		Title:  "Wrap",
		Items: []chatwidget.SelectionItem{{
			ID:          "one",
			Name:        "One",
			Description: "Read and edit workspace files and run commands, with approval required for internet access",
		}},
	})
	rows := strings.Split(strings.TrimRight(utils.StripANSI(model.viewString()), "\n"), "\n")
	for _, row := range rows {
		trimmed := strings.TrimRight(row, " ")
		if codextui.DisplayWidth(trimmed) > 60 {
			t.Fatalf("row exceeded the pane width (%d): %q", codextui.DisplayWidth(trimmed), trimmed)
		}
	}
	first := -1
	for index, row := range rows {
		if strings.Contains(row, "› 1. One") {
			first = index
			break
		}
	}
	if first < 0 {
		t.Fatalf("row missing:\n%s", strings.Join(rows, "\n"))
	}
	matchIndex := strings.Index(rows[first], "Read and edit workspace files")
	if matchIndex <= 0 || !strings.HasSuffix(strings.TrimRight(rows[first], " "), "run commands,") {
		t.Fatalf("first wrapped segment = %q", rows[first])
	}
	descStart := codextui.DisplayWidth(rows[first][:matchIndex])
	if first+1 >= len(rows) {
		t.Fatalf("missing continuation row:\n%s", strings.Join(rows, "\n"))
	}
	continuation := rows[first+1]
	if !strings.HasPrefix(continuation, strings.Repeat(" ", descStart)+"with approval required for") {
		t.Fatalf("continuation was not aligned under column %d: %q", descStart, continuation)
	}
}

// TestDaemonRestartConfirmationMatchesRustSnapshot pins the centered
// confirmation Rust builds with SelectionViewParams::confirmation()
// (tui/src/app/snapshots/codex_tui__app__daemon_menu__tests__daemon_restart_confirmation.snap).
func TestDaemonRestartConfirmationMatchesRustSnapshot(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 40})
	model.confirmDaemonRestart()
	rows := strings.Split(strings.TrimRight(utils.StripANSI(model.viewString()), "\n"), "\n")
	var block []string
	for index, row := range rows {
		if strings.TrimSpace(row) == "Restart background server?" {
			block = rows[index : index+9]
			break
		}
	}
	want := []string{
		"      Restart background server?",
		"      Active or queued work in other chats may be interrupted.",
		"      Codex will reconnect automatically.",
		"",
		"",
		"    › 1. Cancel",
		"      2. Restart",
		"",
		"      enter select · esc back",
	}
	if len(block) != len(want) {
		t.Fatalf("confirmation block missing:\n%s", strings.Join(rows, "\n"))
	}
	for index := range want {
		if strings.TrimRight(block[index], " ") != want[index] {
			t.Fatalf("row %d = %q, want %q\n%s", index, block[index], want[index], strings.Join(block, "\n"))
		}
	}
}
