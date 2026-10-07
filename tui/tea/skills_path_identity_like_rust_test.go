package tea

import (
	"testing"

	"codex_go/appserver"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust #51482 (codex-rs/tui/src/bottom_pane/skills_toggle_view.rs +
// chatwidget::update_skill_enabled): the manage-skills state is keyed by parsed
// path identity (`SkillsToggleItem.path: PathUri`,
// `skills_initial_state: HashMap<PathUri, bool>`), so two spellings of one
// Windows path are a single entry and a write reported for either spelling flips
// it. Counterpart Rust tests: the PathUri identity tests
// (codex-utils-path-uri) plus the skills_toggle_view item paths in this commit.
func TestManageSkillsStateKeysByPathIdentityLikeRust(t *testing.T) {
	response := appserver.SkillsListResponse{Skills: []appserver.SkillsListEntry{
		{Name: "foo", Path: `C:\Skills\Foo\SKILL.md`, Description: "Foo skill.", Enabled: true},
	}}
	model := NewModel(nil, Options{})
	model.skillsInventory = &response
	model.openManageSkillsModal(response, "")
	if model.modal == nil || model.modal.manageSkills == nil {
		t.Fatal("manage skills modal did not open")
	}
	state := model.modal.manageSkills
	identity := chatwidget.SkillPathIdentity(`C:\Skills\Foo\SKILL.md`)
	if len(state.initial) != 1 || len(state.applied) != 1 {
		t.Fatalf("initial/applied = %#v / %#v, want one identity entry each", state.initial, state.applied)
	}
	if !state.initial[identity] {
		t.Fatalf("initial = %#v, want the path identity key", state.initial)
	}

	// The checklist still carries the native catalog path.
	if len(state.view.Items) != 1 || state.view.Items[0].Path != `C:\Skills\Foo\SKILL.md` {
		t.Fatalf("toggle items = %#v, want the native path", state.view.Items)
	}

	// A write reported for the equivalent spelling updates that one entry and the
	// inventory entry it denotes.
	state.active = &skillToggleOperation{path: `c:/skills/foo/SKILL.md`, enabled: false}
	state.requestID = 42
	updated, _ := model.Update(SkillEnabledWriteResultMsg{
		RequestID: 42,
		Path:      `c:/skills/foo/SKILL.md`,
		Enabled:   false,
	})
	model = updated.(*Model)
	state = model.modal.manageSkills
	if len(state.applied) != 1 {
		t.Fatalf("applied = %#v, want one identity entry", state.applied)
	}
	if state.applied[identity] {
		t.Fatal("write for the equivalent spelling did not update the identity entry")
	}
	if response.Skills[0].Enabled {
		t.Fatal("inventory entry was not disabled by path identity")
	}
	enabled, disabled, summary, changed := chatwidget.ManageSkillsChangeSummary(state.initial, state.applied)
	if !changed || enabled != 0 || disabled != 1 || summary != "0 skills enabled, 1 skills disabled" {
		t.Fatalf("summary = (%d,%d,%q,%v), want one disabled skill", enabled, disabled, summary, changed)
	}
}

// Rust #51482 matches TUI skill selections by parsed path identity, so the popup
// candidate key (used to restore the selection) is the identity of the native
// path rather than its text.
func TestSkillPopupItemKeyUsesPathIdentityLikeRust(t *testing.T) {
	backslashes := skillPopupItem{Name: "foo", Path: `C:\Skills\Foo\SKILL.md`}
	forward := skillPopupItem{Name: "foo", Path: `c:/skills/foo/SKILL.md`}
	if skillPopupItemKey(backslashes) != skillPopupItemKey(forward) {
		t.Fatalf("popup keys differ: %q vs %q", skillPopupItemKey(backslashes), skillPopupItemKey(forward))
	}
	// A different POSIX path keeps its own key.
	other := skillPopupItem{Name: "foo", Path: "/tmp/skills/foo/SKILL.md"}
	if skillPopupItemKey(other) == skillPopupItemKey(backslashes) {
		t.Fatal("distinct POSIX paths shared one popup key")
	}
	// Without a path the name is the key.
	if got := skillPopupItemKey(skillPopupItem{Name: "foo"}); got != "foo" {
		t.Fatalf("pathless popup key = %q, want foo", got)
	}
}
