package tea

import (
	"strconv"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func newSidebarReaderFindModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "hello from the main chat")
	model := NewModel(state, Options{
		Width:  120,
		Height: 30,
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			messages := make([]codextui.Message, 0, 40)
			for index := 0; index < 40; index++ {
				messages = append(messages, codextui.Message{
					Role: codextui.RoleAssistant,
					Text: "agent transcript line " + strconv.Itoa(index),
				})
			}
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID, ParentThreadID: "parent-a"},
				Messages: messages,
				Status:   "idle",
			}, nil
		},
		OnSubmit: func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.mergeSidebarAgentEntries("parent-a", []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-1", ParentThreadID: "parent-a", AgentNickname: "Scout", AgentRole: "worker"},
	})
	model.refreshTranscript()
	return model
}

func typeSidebarReaderQuery(model *Model, query string) {
	for _, r := range query {
		model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{r}, Text: string(r)})
	}
}

// Rust #62157 reuses the transcript view in the docked agent reader, so its
// Find editor searches the whole transcript, Enter keeps the match, Control+n
// steps and Escape closes it.
func TestSidebarReaderFindSearchesTranscriptLikeRust(t *testing.T) {
	model := newSidebarReaderFindModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	side := model.activeSide
	if side == nil || !side.WatchOnly {
		t.Fatalf("active side = %#v, want the watch-only reader", side)
	}
	if !model.focusSidebarReader() {
		t.Fatal("reader did not take focus")
	}
	_ = model.viewString()
	tail := side.SideViewMax
	if tail <= 0 {
		t.Fatalf("reader transcript never scrolled: SideViewMax = %d", tail)
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("/")})
	if !model.sidebarReaderFind.editing {
		t.Fatal("slash did not open the reader Find editor")
	}
	if plain := ansi.Strip(model.viewString()); !strings.Contains(plain, "Find:") {
		t.Fatalf("Find bar missing from the reader:\n%s", plain)
	}

	typeSidebarReaderQuery(model, "line 3")
	if got := len(model.sidebarReaderFind.matches); got < 2 {
		t.Fatalf("matches = %d, want the transcript matches", got)
	}
	first := model.sidebarReaderFind.matches[0]
	if side.SideScroll >= tail {
		t.Fatalf("Find left the reader at the tail %d, want it revealed row %d", side.SideScroll, first)
	}
	if plain := ansi.Strip(model.viewString()); !strings.Contains(plain, "Find: line 3") {
		t.Fatalf("Find bar lost the query:\n%s", plain)
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
	if model.sidebarReaderFind.editing {
		t.Fatal("Enter did not accept the reader Find")
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlN})
	if model.sidebarReaderFind.index != 1 {
		t.Fatalf("Control+n index = %d, want the next match", model.sidebarReaderFind.index)
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	if model.sidebarReaderFind.active() {
		t.Fatal("Escape left the reader Find open")
	}
	if !watchOnlySidePane(model.activeSide) {
		t.Fatal("Escape that closed Find also closed the reader")
	}
	// Rust's reading-mode footer says esc latest, so the reader returns to the
	// newest row.
	if side.SideScroll != side.SideViewMax {
		t.Fatalf("reading-mode Escape left the reader at %d, want latest %d", side.SideScroll, side.SideViewMax)
	}
	if plain := ansi.Strip(model.viewString()); strings.Contains(plain, "Find:") {
		t.Fatalf("Find bar survived Escape:\n%s", plain)
	}
}

// A query with no match paints Rust's No matches status and leaves the reader
// scroll where it was.
func TestSidebarReaderFindNoMatchLikeRust(t *testing.T) {
	model := newSidebarReaderFindModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	if !model.focusSidebarReader() {
		t.Fatal("reader did not take focus")
	}
	_ = model.viewString()
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("/")})
	typeSidebarReaderQuery(model, "zzzz")
	if model.sidebarReaderFind.index != -1 || len(model.sidebarReaderFind.matches) != 0 {
		t.Fatalf("index=%d matches=%d, want no match", model.sidebarReaderFind.index, len(model.sidebarReaderFind.matches))
	}
	if plain := ansi.Strip(model.viewString()); !strings.Contains(plain, "No matches") {
		t.Fatalf("No matches status missing:\n%s", plain)
	}
}
