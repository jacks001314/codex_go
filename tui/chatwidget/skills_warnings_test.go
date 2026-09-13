package chatwidget

import (
	"reflect"
	"testing"

	"codex_go/appserver"
)

// Rust parity: codex-rs/tui/src/app/startup_prompts.rs skill-load warnings.

func skillWarningTestError(path string, message string) appserver.SkillErrorInfo {
	return appserver.SkillErrorInfo{Path: path, Message: message}
}

func TestSkillLoadWarningStateSuppressesRepeatedActiveErrorsMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	skillError := skillWarningTestError("/repo/.gcode/skills/abc/SKILL.md", "invalid description")

	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{skillError}) {
		t.Fatalf("first NewlyActiveErrors() = %#v, want error", got)
	}
	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); len(got) != 0 {
		t.Fatalf("repeated NewlyActiveErrors() = %#v, want empty", got)
	}
}

func TestSkillLoadWarningStateReemitsAfterErrorClearsMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	skillError := skillWarningTestError("/repo/.gcode/skills/abc/SKILL.md", "invalid description")

	state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError})
	if got := state.NewlyActiveErrors(nil); len(got) != 0 {
		t.Fatalf("cleared NewlyActiveErrors() = %#v, want empty", got)
	}
	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{skillError}) {
		t.Fatalf("reemitted NewlyActiveErrors() = %#v, want error", got)
	}
}

func TestSkillLoadWarningStateDisplaysNewMessageForActivePathMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	initial := skillWarningTestError("/repo/.gcode/skills/abc/SKILL.md", "invalid description")
	changed := skillWarningTestError("/repo/.gcode/skills/abc/SKILL.md", "invalid frontmatter")

	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{initial}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{initial}) {
		t.Fatalf("initial NewlyActiveErrors() = %#v, want initial", got)
	}
	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{changed}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{changed}) {
		t.Fatalf("changed NewlyActiveErrors() = %#v, want changed", got)
	}
}

func TestSkillLoadWarningStateClearAllowsActiveErrorAgainMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	skillError := skillWarningTestError("/repo/.gcode/skills/abc/SKILL.md", "invalid description")

	state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError})
	state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError})
	state.Clear()

	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{skillError}) {
		t.Fatalf("after clear NewlyActiveErrors() = %#v, want error", got)
	}
}

func TestSkillLoadWarningMessagesMatchRust(t *testing.T) {
	errors := []appserver.SkillErrorInfo{
		skillWarningTestError("/repo/.gcode/skills/abc/SKILL.md", "invalid description"),
		skillWarningTestError("/repo/.gcode/skills/xyz/SKILL.md", "missing name"),
	}
	want := []string{
		"Skipped loading 2 skill(s) due to invalid SKILL.md files.",
		"/repo/.gcode/skills/abc/SKILL.md: invalid description",
		"/repo/.gcode/skills/xyz/SKILL.md: missing name",
	}
	if got := SkillLoadWarningMessages(errors); !reflect.DeepEqual(got, want) {
		t.Fatalf("SkillLoadWarningMessages() = %#v, want %#v", got, want)
	}
	if got := SkillLoadWarningMessages(nil); got != nil {
		t.Fatalf("SkillLoadWarningMessages(nil) = %#v, want nil", got)
	}
}

// Mirrors Rust's errors_for_cwd.
func TestSkillErrorsForCWDLikeRust(t *testing.T) {
	response := appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{
		{CWD: "/other", Errors: []appserver.SkillErrorInfo{{Path: "/other/SKILL.md", Message: "x"}}},
		{CWD: "/repo", Errors: []appserver.SkillErrorInfo{{Path: "/repo/.gcode/skills/a/SKILL.md", Message: "invalid description"}}},
	}}
	got := SkillErrorsForCWD(response, "/repo")
	if len(got) != 1 || got[0].Path != "/repo/.gcode/skills/a/SKILL.md" {
		t.Fatalf("SkillErrorsForCWD() = %#v", got)
	}
	if got := SkillErrorsForCWD(response, "/missing"); got != nil {
		t.Fatalf("SkillErrorsForCWD(missing) = %#v, want nil", got)
	}
}
