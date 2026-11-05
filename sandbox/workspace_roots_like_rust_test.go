package sandbox

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	json "github.com/goccy/go-json"
)

func workspaceBoundaryTestProfile(t *testing.T, deniedPath string) *PermissionProfile {
	t.Helper()
	subpath := "build"
	wire := rustPermissionProfileWire{
		Type:    "managed",
		Network: string(NetworkRestricted),
		FileSys: &rustPermissionFilesystem{
			Type: "restricted",
			Entries: []rustPermissionFilesystemEntry{
				{Path: rustPermissionFilesystemPath{Type: "special", Value: rustPermissionFilesystemSpecial{Kind: "project_roots"}}, Access: string(FileSystemAccessWrite)},
				{Path: rustPermissionFilesystemPath{Type: "special", Value: rustPermissionFilesystemSpecial{Kind: "project_roots", Subpath: &subpath}}, Access: string(FileSystemAccessRead)},
				{Path: rustPermissionFilesystemPath{Type: "glob_pattern", Pattern: workspaceRootsGlobPrefix + "**/*.env"}, Access: string(FileSystemAccessDeny)},
				{Path: rustPermissionFilesystemPath{Type: "path", Path: deniedPath}, Access: string(FileSystemAccessDeny)},
			},
		},
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	profile, err := canonicalPermissionProfileFromJSON(data)
	if err != nil {
		t.Fatalf("canonical profile: %v", err)
	}
	return &profile
}

func workspaceBoundaryEntryStrings(profile *PermissionProfile) []string {
	var out []string
	for _, entry := range profile.RuntimeFileSystemEntries() {
		value := entry.Path.Path
		if entry.Path.Type == "special" && entry.Path.Value != nil {
			value = "special:" + entry.Path.Value.Kind
			if entry.Path.Value.Subpath != nil {
				value += ":" + *entry.Path.Value.Subpath
			}
		}
		if entry.Path.Type == "glob_pattern" {
			value = "glob:" + entry.Path.Pattern
		}
		out = append(out, string(entry.Access)+"="+value)
	}
	return out
}

func TestMaterializePermissionProfileWorkspaceRootsLikeRust(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	denied := filepath.Join(base, "denied")
	profile := workspaceBoundaryTestProfile(t, denied)

	resolved, err := MaterializePermissionProfileWorkspaceRoots(profile, base, []string{first, second})
	if err != nil {
		t.Fatalf("MaterializePermissionProfileWorkspaceRoots() error = %v", err)
	}
	want := []string{
		"write=" + first,
		"write=" + second,
		"read=" + filepath.Join(first, "build"),
		"read=" + filepath.Join(second, "build"),
		"deny=glob:" + filepath.Join(first, "**", "*.env"),
		"deny=glob:" + filepath.Join(second, "**", "*.env"),
		"deny=" + denied,
	}
	got := workspaceBoundaryEntryStrings(resolved)
	if !slices.Equal(got, want) {
		t.Fatalf("materialized entries = %#v, want %#v", got, want)
	}

	noWorkspace, err := MaterializePermissionProfileWorkspaceRoots(profile, base, []string{})
	if err != nil {
		t.Fatalf("MaterializePermissionProfileWorkspaceRoots(empty) error = %v", err)
	}
	if got, want := workspaceBoundaryEntryStrings(noWorkspace), []string{"deny=" + denied}; !slices.Equal(got, want) {
		t.Fatalf("empty-root entries = %#v, want %#v", got, want)
	}

	alreadyResolved, err := MaterializePermissionProfileWorkspaceRoots(profile, base, nil)
	if err != nil {
		t.Fatalf("MaterializePermissionProfileWorkspaceRoots(nil) error = %v", err)
	}
	if !slices.Contains(workspaceBoundaryEntryStrings(alreadyResolved), "write=special:project_roots") {
		t.Fatalf("nil roots unexpectedly rewrote the profile: %#v", workspaceBoundaryEntryStrings(alreadyResolved))
	}
}

func TestMaterializePermissionProfileWorkspaceRootsRejectsUnsafeRules(t *testing.T) {
	base := t.TempDir()
	profile := workspaceBoundaryTestProfile(t, filepath.Join(base, "denied"))
	if _, err := MaterializePermissionProfileWorkspaceRoots(profile, base, []string{"relative-root"}); err == nil {
		t.Fatal("relative workspace root was accepted")
	}

	if _, err := MaterializePermissionProfileWorkspaceRoots(profile, "", []string{base}); err == nil {
		t.Fatal("empty policy cwd was accepted")
	}
}

func TestMaterializePermissionProfileWorkspaceRootsClearsUnsafeHomeDenyLikeRust(t *testing.T) {
	base := t.TempDir()
	subpath := "~/.ssh"
	wire := rustPermissionProfileWire{
		Type: "managed", Network: string(NetworkRestricted),
		FileSys: &rustPermissionFilesystem{Type: "restricted", Entries: []rustPermissionFilesystemEntry{
			{Path: rustPermissionFilesystemPath{Type: "special", Value: rustPermissionFilesystemSpecial{Kind: "project_roots"}}, Access: string(FileSystemAccessWrite)},
			{Path: rustPermissionFilesystemPath{Type: "special", Value: rustPermissionFilesystemSpecial{Kind: "project_roots", Subpath: &subpath}}, Access: string(FileSystemAccessDeny)},
		}},
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	profile, err := canonicalPermissionProfileFromJSON(data)
	if err != nil {
		t.Fatalf("canonical profile: %v", err)
	}
	materialized, err := MaterializePermissionProfileWorkspaceRoots(&profile, base, []string{base})
	if err != nil {
		t.Fatalf("MaterializePermissionProfileWorkspaceRoots() error = %v", err)
	}
	if entries := materialized.RuntimeFileSystemEntries(); len(entries) != 0 {
		t.Fatalf("unsafe home-relative deny kept workspace grants: %#v", entries)
	}
}

func TestBuildCommandRunPlanBindsEmptyWorkspaceRootsAtLaunchBoundary(t *testing.T) {
	base := t.TempDir()
	policyCWD := filepath.Join(base, "policy")
	denied := filepath.Join(base, "denied")
	profile := workspaceBoundaryTestProfile(t, denied)
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		ResolvedPermissionProfile:     profile,
		ResolvedPermissionProfileID:   "workspace-boundary",
		ResolvedPermissionProfileJSON: mustRustPermissionProfileJSON(*profile),
		CWD:                           policyCWD,
		WorkspaceRoots:                []string{},
		Command:                       []string{"pwd"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan() error = %v", err)
	}
	if plan.WorkspaceRoots == nil || len(plan.WorkspaceRoots) != 0 {
		t.Fatalf("plan workspace roots = %#v, want non-nil empty", plan.WorkspaceRoots)
	}
	if got, want := workspaceBoundaryEntryStrings(plan.PermissionProfile), []string{"deny=" + denied}; !slices.Equal(got, want) {
		t.Fatalf("launch-boundary entries = %#v, want %#v", got, want)
	}
	if strings.Contains(plan.PermissionProfileJSON, `"project_roots"`) {
		t.Fatalf("launch-boundary profile kept a symbolic entry: %s", plan.PermissionProfileJSON)
	}
}
