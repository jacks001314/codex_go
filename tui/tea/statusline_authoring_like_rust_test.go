package tea

import (
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func statuslineAuthoringModel(t *testing.T, reader SkillsListReaderFunc, cloud bool) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-authoring")
	state.SetStatus("idle")
	state.CWD = "/repo"
	options := Options{
		Width:           120,
		Height:          30,
		FeatureSettings: map[string]bool{"tui_extensions_statusline": true},
		OnReadSkills:    reader,
	}
	if cloud {
		options.BackendID = codextui.NewBackendID()
		options.ServerOwnedSettings = true
	}
	model := NewModel(state, options)
	model.width, model.height = 120, 30
	model.sessionCWD = "/repo"
	return model
}

func statuslineSetupOptionIDs(model *Model) []string {
	if model == nil || model.modal == nil {
		return nil
	}
	ids := make([]string, 0, len(model.modal.options))
	for _, option := range model.modal.options {
		ids = append(ids, option.ID)
	}
	return ids
}

// Rust #62037 renames the provider action and offers the authoring action only
// outside cloud mode with status line extensions enabled.
func TestStatusLineSetupOffersCustomAuthoringLikeRust(t *testing.T) {
	model := statuslineAuthoringModel(t, nil, false)
	model.openStatusLineSetup()
	ids := statuslineSetupOptionIDs(model)
	if len(ids) < 2 || ids[0] != statuslineProviderPickerActionID || ids[1] != statuslineCreateCustomActionID {
		t.Fatalf("status line actions = %#v", ids)
	}
	if label := model.modal.options[0].Label; label != "Choose a status line provider" {
		t.Fatalf("provider action label = %q", label)
	}
	if label := model.modal.options[1].Label; label != "Create a custom status line" {
		t.Fatalf("create action label = %q", label)
	}

	cloud := statuslineAuthoringModel(t, nil, true)
	cloud.openStatusLineSetup()
	for _, id := range statuslineSetupOptionIDs(cloud) {
		if id == statuslineCreateCustomActionID {
			t.Fatal("cloud conversation offered status line authoring")
		}
	}

	disabled := statuslineAuthoringModel(t, nil, false)
	// Rust #62387: the gate is the launch value, not the live preferences.
	disabled.statuslineFeatureEnabled = false
	disabled.openStatusLineSetup()
	for _, id := range statuslineSetupOptionIDs(disabled) {
		if id == statuslineProviderPickerActionID || id == statuslineCreateCustomActionID {
			t.Fatalf("disabled extension gate offered %q", id)
		}
	}
}

// Rust #62037 start_statusline_authoring opens a fresh conversation and binds
// the enabled system scoped create-statusline skill into the composer draft.
func TestStatusLineCreateCustomBindsBundledSkillLikeRust(t *testing.T) {
	reader := func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
		return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
			CWD: cwd,
			Skills: []appserver.SkillsListEntry{
				{Name: createStatuslineSkillName, Scope: "user", Enabled: true, Path: "/home/skills/create-statusline/SKILL.md"},
				{Name: createStatuslineSkillName, Scope: "system", Enabled: true, Path: "/bundle/create-statusline/SKILL.md"},
				{Name: createStatuslineSkillName, Scope: "system", Enabled: false, Path: "/disabled/create-statusline/SKILL.md"},
			},
		}}}, nil
	}
	model := statuslineAuthoringModel(t, reader, false)
	model.openStatusLineSetup()
	model.modal.selected = 1
	cmd := model.toggleStatusSetupSelection()
	if model.modal != nil {
		t.Fatalf("authoring action left the item editor open: %#v", model.modal.id)
	}
	if model.State.ThreadID != "" {
		t.Fatalf("authoring did not open a fresh conversation: %q", model.State.ThreadID)
	}
	if cmd == nil {
		t.Fatal("authoring did not read the skill inventory")
	}
	model.Update(cmd())
	if got := model.composer.Value(); got != statuslineAuthoringSkillDraft {
		t.Fatalf("draft = %q, want %q", got, statuslineAuthoringSkillDraft)
	}
	wantBinding := "create-statusline|skill:///bundle/create-statusline/SKILL.md"
	if len(model.composerMentionBindings) != 1 || model.composerMentionBindings[0] != wantBinding {
		t.Fatalf("mention bindings = %#v, want %q", model.composerMentionBindings, wantBinding)
	}
	if column := model.composerCursor(); column != len(model.composer.Value()) {
		t.Fatalf("cursor column = %d, want %d", column, len(model.composer.Value()))
	}
}

// Without a bundled system skill Rust restores the plain request only.
func TestStatusLineCreateCustomFallsBackWithoutSkillLikeRust(t *testing.T) {
	reader := func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
		return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
			CWD: cwd,
			Skills: []appserver.SkillsListEntry{
				{Name: createStatuslineSkillName, Scope: "user", Enabled: true, Path: "/home/skills/create-statusline/SKILL.md"},
			},
		}}}, nil
	}
	model := statuslineAuthoringModel(t, reader, false)
	model.openStatusLineSetup()
	model.modal.selected = 1
	model.Update(model.toggleStatusSetupSelection()())
	if got := model.composer.Value(); got != statuslineAuthoringDraft {
		t.Fatalf("fallback draft = %q", got)
	}
	if len(model.composerMentionBindings) != 0 {
		t.Fatalf("fallback draft carried bindings: %#v", model.composerMentionBindings)
	}
}

// Rust #62037: the authoring row activates on Enter like any other action row;
// it must not fall through to committing the item selection.
func TestStatusLineCreateCustomActivatesOnEnterLikeRust(t *testing.T) {
	model := statuslineAuthoringModel(t, nil, false)
	model.openStatusLineSetup()
	model.modal.selected = 1
	model.Update(key(keymsg.KeyEnter))
	if model.modal != nil {
		t.Fatalf("Enter left the item editor open: %#v", model.modal.id)
	}
	if got := model.composer.Value(); got != statuslineAuthoringDraft {
		t.Fatalf("Enter did not restore the authoring draft: %q", got)
	}
}

// Rust #62037 keeps the previous thread's draft while the authoring
// conversation is open.
func TestStatusLineCreateCustomPreservesPreviousDraftLikeRust(t *testing.T) {
	model := statuslineAuthoringModel(t, nil, false)
	draft := "keep this draft"
	model.setComposerPrompt(draft)
	model.openStatusLineSetup()
	model.modal.selected = 1
	model.toggleStatusSetupSelection()
	if got := model.agentsOverviewDrafts["thread-authoring"]; got != draft {
		t.Fatalf("previous thread draft = %q, want %q", got, draft)
	}
	if got := model.composer.Value(); got != statuslineAuthoringDraft {
		t.Fatalf("authoring draft = %q", got)
	}
}

// A skill read that lands after the conversation changed must not overwrite the
// new conversation's composer.
func TestStatusLineCreateCustomDropsStaleSkillReadLikeRust(t *testing.T) {
	reader := func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
		return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
			CWD:    cwd,
			Skills: []appserver.SkillsListEntry{{Name: createStatuslineSkillName, Scope: "system", Enabled: true, Path: "/bundle/create-statusline/SKILL.md"}},
		}}}, nil
	}
	model := statuslineAuthoringModel(t, reader, false)
	model.openStatusLineSetup()
	model.modal.selected = 1
	cmd := model.toggleStatusSetupSelection()
	model.State.SetThreadID("thread-elsewhere")
	model.Update(cmd())
	if got := model.composer.Value(); got != "" {
		t.Fatalf("stale skill read restored a draft into another conversation: %q", got)
	}
}
