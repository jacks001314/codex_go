package agentsoverview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGroupingCycleIncludesDirectoryLikeRust covers Rust #56308: Directory
// joins the Project/Status/Model cycle but is never persisted because older
// clients reject the value.
func TestGroupingCycleIncludesDirectoryLikeRust(t *testing.T) {
	want := []Grouping{GroupingDirectory, GroupingStatus, GroupingModel, GroupingProject}
	grouping := GroupingProject
	for _, expected := range want {
		grouping = grouping.Next()
		if grouping != expected {
			t.Fatalf("Next() from the cycle = %v, want %v", grouping, expected)
		}
	}
	if GroupingDirectory.ConfigValue() != "" {
		t.Fatalf("Directory ConfigValue = %q, want empty", GroupingDirectory.ConfigValue())
	}
	if GroupingDirectory.Persisted() {
		t.Fatal("Directory grouping must not be persisted")
	}
	for _, persisted := range []Grouping{GroupingProject, GroupingStatus, GroupingModel} {
		if !persisted.Persisted() || persisted.ConfigValue() == "" {
			t.Fatalf("%v should be persisted with a config value", persisted)
		}
		if got := ParseGroupingConfig(persisted.ConfigValue()); got != persisted {
			t.Fatalf("round trip %v -> %v", persisted, got)
		}
	}
	if GroupingDirectory.DisplayLabel() != "Directory" || GroupingDirectory.Label() != "group: directory" {
		t.Fatalf("Directory labels = %q / %q", GroupingDirectory.DisplayLabel(), GroupingDirectory.Label())
	}
}

// TestDirectoryGroupingCanonicalizesAliasesLikeRust covers Rust #56308: with
// linked worktrees enabled, aliases of one directory form a single group while
// distinct directories stay separate, and group headings are home-relative.
func TestDirectoryGroupingCanonicalizesAliasesLikeRust(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	alias := sub + string(filepath.Separator) + ".."

	rows := []Row{
		{ThreadID: "t-1", CWD: base, Group: GroupReady},
		{ThreadID: "t-2", CWD: alias, Group: GroupReady},
		{ThreadID: "t-3", CWD: sub, Group: GroupReady},
	}
	view := New(rows, "", false)
	view.SetWorktreesEnabled(true)
	view.State.Grouping = GroupingDirectory

	if !view.sameGroup(GroupingDirectory, 0, 1) {
		t.Fatalf("aliases %q and %q did not group together", base, alias)
	}
	if view.sameGroup(GroupingDirectory, 0, 2) {
		t.Fatalf("%q and %q grouped together", base, sub)
	}

	// Without worktrees the reported cwd is used verbatim, so the alias stays a
	// separate group (Rust directory_group_key).
	view.SetWorktreesEnabled(false)
	if view.sameGroup(GroupingDirectory, 0, 1) {
		t.Fatalf("aliases grouped together without worktrees enabled")
	}

	t.Setenv("HOME", base)
	t.Setenv("USERPROFILE", base)
	project := filepath.Join(base, "project")
	view = New([]Row{{ThreadID: "t-4", CWD: project, Group: GroupReady}}, "", false)
	view.State.Grouping = GroupingDirectory
	want := "~" + string(filepath.Separator) + "project"
	if got := view.groupHeading(GroupingDirectory, 0); got != want {
		t.Fatalf("directory heading = %q, want %q", got, want)
	}
	if got := formatDirectoryDisplay(base); got != "~" {
		t.Fatalf("home heading = %q, want ~", got)
	}
	if got := formatDirectoryDisplay(""); got != "" {
		t.Fatalf("empty heading = %q", got)
	}
	if got := formatDirectoryDisplay(project); !strings.HasPrefix(got, "~") {
		t.Fatalf("heading %q should be home-relative", got)
	}
}
