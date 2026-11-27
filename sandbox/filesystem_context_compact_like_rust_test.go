package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderCompactFileSystemContextGroupsSharedMetadataLikeRust(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	profile := WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = []string{workspace, extra}

	full, err := RenderFileSystemContext(&profile, workspace, []string{workspace})
	if err != nil {
		t.Fatalf("RenderFileSystemContext() error = %v", err)
	}
	compact, err := RenderFileSystemContextWithFormat(&profile, workspace, []string{workspace}, FileSystemContextCompact)
	if err != nil {
		t.Fatalf("RenderFileSystemContextWithFormat(compact) error = %v", err)
	}
	for _, want := range []string{
		"Managed, restricted. Read access to :root.",
		"Writable workspace roots: " + jsonStringsForXML([]string{workspace}),
		"Other writable paths: " + jsonStringsForXML([]string{extra}),
		"Directly under each listed path, .git, .agents, .codex, .gcode and .aws have read-only entries.",
		"Separate write grants: :slash_tmp, :tmpdir",
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact filesystem context missing %q:\n%s", want, compact)
		}
	}
	if strings.Count(compact, filepath.Join(extra, ".git")) != 0 {
		t.Fatalf("compact context still listed each metadata read:\n%s", compact)
	}
	if strings.Count(full, "access=\"read\"") <= len(compactFileSystemMetadataNames) {
		t.Fatalf("full control did not list expanded metadata reads:\n%s", full)
	}
}

func TestCompactFileSystemContextFallsBackForNonuniformRulesLikeRust(t *testing.T) {
	workspace := "/work"
	extra := "/build"
	entries := []rustPermissionFilesystemEntry{
		{Path: rustPermissionFilesystemPath{Type: "special", Value: rustPermissionFilesystemSpecial{Kind: "root"}}, Access: "read"},
		{Path: rustPermissionFilesystemPath{Type: "path", Path: workspace}, Access: "write"},
		{Path: rustPermissionFilesystemPath{Type: "path", Path: extra}, Access: "write"},
	}
	for _, root := range []string{workspace, extra} {
		for _, name := range compactFileSystemMetadataNames {
			entries = append(entries, rustPermissionFilesystemEntry{
				Path:   rustPermissionFilesystemPath{Type: "path", Path: filepath.Join(root, name)},
				Access: "read",
			})
		}
	}
	if _, ok := renderCompactFileSystemContext([]string{workspace}, entries); !ok {
		t.Fatal("uniform generated metadata reads did not use the compact format")
	}

	caseVariant := append([]rustPermissionFilesystemEntry(nil), entries...)
	for index := range caseVariant {
		if caseVariant[index].Path.Path == filepath.Join(extra, ".git") {
			caseVariant[index].Path.Path = filepath.Join(extra, ".GIT")
		}
	}
	if compact, ok := renderCompactFileSystemContext([]string{workspace}, caseVariant); ok {
		t.Fatalf("case-variant metadata path used compact format and lost its spelling:\n%s", compact)
	}

	explicitRead := append(append([]rustPermissionFilesystemEntry(nil), entries...), rustPermissionFilesystemEntry{
		Path:   rustPermissionFilesystemPath{Type: "path", Path: filepath.Join(extra, ".git")},
		Access: "read",
	})
	if _, ok := renderCompactFileSystemContext([]string{workspace}, explicitRead); ok {
		t.Fatal("explicit duplicate metadata read used compact format")
	}
}
