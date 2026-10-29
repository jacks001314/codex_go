package sandbox

import (
	"testing"

	"codex_go/utils"
)

// Mirrors Rust #45852 permission_profile_summary_preserves_executor_workspace_paths.
func TestPermissionProfileSummaryPreservesExecutorWorkspacePaths(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		cwd       string
		extraRoot string
		want      string
	}{
		{
			name:      "posix",
			cwd:       "file:///workspace/repo",
			extraRoot: "file:///workspace/shared%20files",
			want:      "workspace-write [workdir, /workspace/shared files]",
		},
		{
			name:      "windows drive",
			cwd:       "file:///C:/workspace/repo",
			extraRoot: "file:///C:/workspace/shared%20files",
			want:      `workspace-write [workdir, C:\workspace\shared files]`,
		},
		{
			name:      "unc",
			cwd:       "file://server/share/repo",
			extraRoot: "file://server/share/shared%20files",
			want:      `workspace-write [workdir, \\server\share\shared files]`,
		},
		{
			name:      "case distinct roots",
			cwd:       "file:///C:/repo",
			extraRoot: "file:///C:/REPO",
			want:      `workspace-write [workdir, C:\REPO]`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cwd := mustPathURI(t, testCase.cwd)
			extraRoot := mustPathURI(t, testCase.extraRoot)
			roots := []*utils.PathURI{cwd, extraRoot}

			profile := WorkspaceWritePermissionProfile()
			profile.SandboxPolicy.ExcludeTmpdirEnvVar = true
			profile.SandboxPolicy.ExcludeSlashTmp = true
			if got := SummarizePermissionProfile(&profile, cwd, roots); got != testCase.want {
				t.Fatalf("workspace summary = %q, want %q", got, testCase.want)
			}

			// A write outside the working directory stays custom permissions.
			writeOutside := runtimeProfileWithWriteEntry(t,
				`{"type":"path","path":"`+testCase.extraRoot+`"}`)
			if got := SummarizePermissionProfile(writeOutside, cwd, roots); got != "custom permissions" {
				t.Fatalf("outside write summary = %q, want custom permissions", got)
			}
		})
	}
}

// Mirrors Rust #45852
// permission_profile_summary_keeps_opaque_workspace_subpath_writes_custom.
func TestPermissionProfileSummaryKeepsOpaqueWorkspaceSubpathWritesCustom(t *testing.T) {
	cwd := mustPathURI(t, "file:///C:/repo")
	for _, subpath := range []string{"src", "", "."} {
		profile := runtimeProfileWithWriteEntry(t,
			`{"type":"special","value":{"kind":"project_roots","subpath":"`+subpath+`"}}`)
		if got := SummarizePermissionProfile(profile, cwd, []*utils.PathURI{cwd}); got != "custom permissions" {
			t.Fatalf("subpath %q summary = %q, want custom permissions", subpath, got)
		}
	}
	unscoped := runtimeProfileWithWriteEntry(t,
		`{"type":"special","value":{"kind":"project_roots","subpath":null}}`)
	if got := SummarizePermissionProfile(unscoped, cwd, []*utils.PathURI{cwd}); got != "workspace-write [workdir]" {
		t.Fatalf("unscoped project roots summary = %q", got)
	}
}

func TestSummarizePermissionProfileNetworkAndFullAccess(t *testing.T) {
	cwd := mustPathURI(t, "file:///workspace/repo")
	fullAccess := FullAccessPermissionProfile()
	if got := SummarizePermissionProfile(&fullAccess, cwd, nil); got != "danger-full-access" {
		t.Fatalf("disabled profile summary = %q", got)
	}
	external := PermissionProfile{SandboxPolicy: NewExternalSandboxPolicy(NetworkEnabled), NetworkEnabled: true}
	if got := SummarizePermissionProfile(&external, cwd, nil); got != "external-sandbox (network access enabled)" {
		t.Fatalf("external profile summary = %q", got)
	}
	readOnly := ReadOnlyPermissionProfile()
	if got := SummarizePermissionProfile(&readOnly, cwd, nil); got != "read-only" {
		t.Fatalf("read-only profile summary = %q", got)
	}
	readOnly.NetworkEnabled = true
	if got := SummarizePermissionProfile(&readOnly, cwd, nil); got != "read-only (network access enabled)" {
		t.Fatalf("read-only network summary = %q", got)
	}
	workspace := WorkspaceWritePermissionProfile()
	workspace.SandboxPolicy.ExcludeTmpdirEnvVar = true
	workspace.SandboxPolicy.ExcludeSlashTmp = true
	workspace.NetworkEnabled = true
	if got := SummarizePermissionProfile(&workspace, cwd, []*utils.PathURI{cwd}); got != "workspace-write [workdir] (network access enabled)" {
		t.Fatalf("workspace network summary = %q", got)
	}
}

func runtimeProfileWithWriteEntry(t *testing.T, entryPathJSON string) *PermissionProfile {
	t.Helper()
	raw := `{"type":"managed","file_system":{"type":"restricted","entries":[{"path":` +
		entryPathJSON + `,"access":"write"}]},"network":"restricted"}`
	profile, err := ParseRuntimePermissionProfileJSON(raw)
	if err != nil {
		t.Fatalf("parse runtime permission profile: %v", err)
	}
	return profile
}

func mustPathURI(t *testing.T, raw string) *utils.PathURI {
	t.Helper()
	path, err := utils.Parse(raw)
	if err != nil {
		t.Fatalf("parse path URI %q: %v", raw, err)
	}
	return path
}
