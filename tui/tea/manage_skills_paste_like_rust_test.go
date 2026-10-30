package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
)

// TestManageSkillsPasteAppendsSearchQueryLikeRust covers the TUI wiring of Rust
// #58130: a paste into the skills toggle view extends the single-line search
// query instead of being replayed as individual keys.
func TestManageSkillsPasteAppendsSearchQueryLikeRust(t *testing.T) {
	response := appserver.SkillsListResponse{Skills: []appserver.SkillsListEntry{
		{Name: "Repo Scout", Path: "/skills/repo/SKILL.md", Description: "Summarize", Enabled: true},
		{Name: "Completion Helper", Path: "/skills/completion/SKILL.md", Description: "Complete", Enabled: false},
	}}
	model := NewModel(nil, Options{})
	model.skillsInventory = &response
	model.openManageSkillsModal(response, "")
	if model.modal == nil || model.modal.manageSkills == nil {
		t.Fatal("manage skills modal did not open")
	}
	state := model.modal.manageSkills
	state.view.HandleKey("c")

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("  ompletion\r\n\t "), Paste: true})
	model = updated.(*Model)
	state = model.modal.manageSkills
	if got, want := state.view.SearchQuery, "completion"; got != want {
		t.Fatalf("search query = %q, want %q", got, want)
	}
	if len(state.view.FilteredIndices) != 1 {
		t.Fatalf("paste did not filter the visible skills: %#v", state.view.FilteredIndices)
	}
	if got, want := state.view.Items[state.view.FilteredIndices[0]].Name, "Completion Helper"; got != want {
		t.Fatalf("visible skill = %q, want %q", got, want)
	}
}
