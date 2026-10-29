package tea

import (
	"testing"

	"codex_go/appserver"
)

// TestManageSkillsCloseRefreshesCurrentCWDLikeRust covers Rust #54050: closing
// the skills management popup reloads the configured working directory rather
// than the directory captured when the popup opened.
func TestManageSkillsCloseRefreshesCurrentCWDLikeRust(t *testing.T) {
	var gotCWD string
	var gotForce bool
	model := NewModel(nil, Options{
		Width:  100,
		Height: 30,
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
			gotCWD = cwd
			gotForce = forceReload
			return appserver.SkillsListResponse{}, nil
		},
	})
	model.sessionCWD = "/work/new"
	model.openManageSkillsModal(appserver.SkillsListResponse{Skills: []appserver.SkillsListEntry{{
		Name:    "alpha",
		Path:    "/work/old/SKILL.md",
		Enabled: true,
	}}}, "/work/old")
	if model.modal == nil || model.modal.manageSkills == nil {
		t.Fatal("manage skills modal did not open")
	}
	cmd := model.finishManageSkillsModal(model.modal.manageSkills)
	if cmd == nil {
		t.Fatal("closing skills management returned no refresh")
	}
	runTeaCmd(t, model, cmd)
	if gotCWD != "/work/new" || !gotForce {
		t.Fatalf("skills refresh = (%q, force=%v), want (/work/new, true)", gotCWD, gotForce)
	}
}

// TestStaleSkillsInventoryResultIgnoredLikeRust covers the second half of Rust
// #54050: a queued skills refresh for a directory that is no longer current
// must not issue or apply a skills/list result.
func TestStaleSkillsInventoryResultIgnoredLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 100, Height: 30})
	model.sessionCWD = "/work/new"
	stale := appserver.SkillsListResponse{Skills: []appserver.SkillsListEntry{{
		Name:    "stale",
		Path:    "/work/old/SKILL.md",
		Enabled: true,
	}}}
	model.applySkillsInventoryResult(SkillsInventoryResultMsg{CWD: "/work/old", Response: stale})
	if model.skillsInventory != nil || model.skillsInventoryCWD != "" {
		t.Fatalf("stale skills result applied: inventory=%#v cwd=%q", model.skillsInventory, model.skillsInventoryCWD)
	}

	fresh := appserver.SkillsListResponse{Skills: []appserver.SkillsListEntry{{
		Name:    "fresh",
		Path:    "/work/new/SKILL.md",
		Enabled: true,
	}}}
	model.applySkillsInventoryResult(SkillsInventoryResultMsg{CWD: "/work/new", Response: fresh})
	if model.skillsInventory == nil || model.skillsInventoryCWD != "/work/new" {
		t.Fatalf("current skills result not applied: inventory=%#v cwd=%q", model.skillsInventory, model.skillsInventoryCWD)
	}
}
