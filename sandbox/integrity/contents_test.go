package integrity

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/sandbox"
)

// policyEntry mirrors the `(&AbsolutePathBuf, FileSystemAccessMode)` pair
// upstream's `policy(..)` helper takes (contents_tests.rs:10-17).
type policyEntry struct {
	path   string
	access sandbox.FileSystemAccessMode
}

// restrictedPolicy mirrors upstream `policy(..)`: a restricted policy built from
// concrete path entries.
func restrictedPolicy(entries []policyEntry) []sandbox.FileSystemSandboxEntry {
	out := make([]sandbox.FileSystemSandboxEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, sandbox.FileSystemSandboxEntry{
			Path:   sandbox.FileSystemPath{Type: "path", Path: entry.path},
			Access: entry.access,
		})
	}
	return out
}

// withDenyGlob appends a glob deny entry, like the upstream tests push
// `FileSystemPath::GlobPattern { .. }` onto a policy (contents_tests.rs:78-84).
func withDenyGlob(entries []sandbox.FileSystemSandboxEntry, pattern string) []sandbox.FileSystemSandboxEntry {
	return append(append([]sandbox.FileSystemSandboxEntry(nil), entries...), sandbox.FileSystemSandboxEntry{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: pattern},
		Access: sandbox.FileSystemAccessDeny,
	})
}

func canonicalDirectory(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

func writeFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func wantFinding(target string, resolvedTarget string, writeRoots []string) *Finding {
	if writeRoots == nil {
		return nil
	}
	return &Finding{
		Target:     target,
		WriteRoots: writeRoots,
		Details:    FindingDetails{ResolvedTarget: resolvedTarget},
	}
}

func checkLikeRust(t *testing.T, entries []sandbox.FileSystemSandboxEntry, cwd string, target string) *Finding {
	t.Helper()
	checker, err := NewFileContentsChecker(entries, cwd)
	if err != nil {
		t.Fatalf("NewFileContentsChecker(): %v", err)
	}
	finding, err := checker.Check(target)
	if err != nil {
		t.Fatalf("Check(%q): %v", target, err)
	}
	return finding
}

// TestContentsRespectWriteGrantsAndCarveoutsLikeRust mirrors
// contents_tests.rs:19-57.
func TestContentsRespectWriteGrantsAndCarveoutsLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	target := filepath.Join(root, "sandbox-helper")
	writeFixture(t, target)

	write := sandbox.FileSystemAccessWrite
	read := sandbox.FileSystemAccessRead
	deny := sandbox.FileSystemAccessDeny

	cases := []struct {
		name           string
		entries        []policyEntry
		wantWriteRoots []string
	}{
		{"write grant", []policyEntry{{root, write}}, []string{root}},
		{"read carveout", []policyEntry{{root, write}, {target, read}}, nil},
		{"narrow write grant", []policyEntry{{root, read}, {target, write}}, []string{target}},
		{"deny carveout", []policyEntry{{root, write}, {target, deny}}, nil},
		{"overlapping write grants", []policyEntry{{root, write}, {target, write}}, []string{root, target}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			finding := checkLikeRust(t, restrictedPolicy(testCase.entries), root, target)
			if want := wantFinding(target, target, testCase.wantWriteRoots); !reflect.DeepEqual(finding, want) {
				t.Fatalf("Check(%q) = %#v, want %#v", target, finding, want)
			}
		})
	}
}

// TestDenyReadGlobsProtectMatchingContentsLikeRust mirrors
// contents_tests.rs:59-94: a deny glob suppresses the finding for the contents it
// matches without disabling checks for anything else.
func TestDenyReadGlobsProtectMatchingContentsLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	target := filepath.Join(root, "sandbox-helper")
	unrelated := filepath.Join(root, "other-helper")
	writeFixture(t, target)
	writeFixture(t, unrelated)

	entries := withDenyGlob(
		restrictedPolicy([]policyEntry{{root, sandbox.FileSystemAccessWrite}}),
		filepath.Join(root, "sandbox-*"),
	)
	if finding := checkLikeRust(t, entries, root, target); finding != nil {
		t.Fatalf("Check(%q) = %#v, want no finding", target, finding)
	}
	finding := checkLikeRust(t, entries, root, unrelated)
	if want := wantFinding(unrelated, unrelated, []string{root}); !reflect.DeepEqual(finding, want) {
		t.Fatalf("Check(%q) = %#v, want %#v", unrelated, finding, want)
	}
}

// TestContentsFollowLinksAndResolvedWriteGrantsLikeRust mirrors
// contents_tests.rs:96-137, which upstream gates behind #[cfg(unix)]. The Go case
// runs wherever the host can create a symlink and skips with the reason
// otherwise (a Windows node needs developer mode or a privileged node).
func TestContentsFollowLinksAndResolvedWriteGrantsLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	writable := filepath.Join(root, "workspace")
	destination := filepath.Join(root, "sandbox-helper")
	link := filepath.Join(writable, "helper-link")
	if err := os.Mkdir(writable, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", writable, err)
	}
	writeFixture(t, destination)
	if err := os.Symlink(destination, link); err != nil {
		t.Skipf("host cannot create a symlink (%v); upstream gates this case behind #[cfg(unix)] and a Windows node needs developer mode or a privileged node", err)
	}

	write := sandbox.FileSystemAccessWrite
	read := sandbox.FileSystemAccessRead
	cases := []struct {
		name           string
		entries        []sandbox.FileSystemSandboxEntry
		wantWriteRoots []string
	}{
		{
			"writable directory holding the link",
			restrictedPolicy([]policyEntry{{writable, write}}),
			nil,
		},
		{
			"link is the writable root",
			restrictedPolicy([]policyEntry{{link, write}}),
			[]string{destination},
		},
		{
			"link is a read carveout",
			restrictedPolicy([]policyEntry{{root, write}, {link, read}}),
			nil,
		},
		{
			"denying the alias still reports the writable root",
			withDenyGlob(
				restrictedPolicy([]policyEntry{{root, write}}),
				filepath.Join(writable, "helper-*"),
			),
			[]string{root},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			checker, err := NewFileContentsChecker(testCase.entries, root)
			if err != nil {
				t.Fatalf("NewFileContentsChecker(): %v", err)
			}
			finding, err := checker.Check(link)
			if err != nil {
				t.Fatalf("Check(%q): %v", link, err)
			}
			if want := wantFinding(link, destination, testCase.wantWriteRoots); !reflect.DeepEqual(finding, want) {
				t.Fatalf("Check(%q) = %#v, want %#v", link, finding, want)
			}
		})
	}
}

// TestProtectedMetadataContentsRemainReadOnlyLikeRust mirrors
// contents_tests.rs:139-150. Upstream protects `<root>/.codex`
// (PROTECTED_METADATA_CODEX_PATH_NAME, permissions.rs:42); this repository
// renames the Codex home and workspace metadata directory to `.gcode`
// (sandbox/sandboxpath/sandboxpath.go:78), so the Go case uses that name.
func TestProtectedMetadataContentsRemainReadOnlyLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	metadataDirectory := filepath.Join(root, ".gcode")
	if err := os.Mkdir(metadataDirectory, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", metadataDirectory, err)
	}
	target := filepath.Join(metadataDirectory, "config.toml")
	writeFixture(t, target)

	finding := checkLikeRust(t, restrictedPolicy([]policyEntry{{root, sandbox.FileSystemAccessWrite}}), root, target)
	if finding != nil {
		t.Fatalf("Check(%q) = %#v, want no finding", target, finding)
	}
}

// TestMissingTargetHasNoContentsToMutateLikeRust mirrors
// contents_tests.rs:152-160.
func TestMissingTargetHasNoContentsToMutateLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	missing := filepath.Join(root, "missing")
	finding := checkLikeRust(t, restrictedPolicy([]policyEntry{{root, sandbox.FileSystemAccessWrite}}), root, missing)
	if finding != nil {
		t.Fatalf("Check(%q) = %#v, want no finding", missing, finding)
	}
}

// TestNonFileTargetIsRejectedLikeRust pins the documented upstream error for an
// existing dependency that is not a file (contents.rs:58-64).
func TestNonFileTargetIsRejectedLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	checker, err := NewFileContentsChecker(restrictedPolicy([]policyEntry{{root, sandbox.FileSystemAccessWrite}}), root)
	if err != nil {
		t.Fatalf("NewFileContentsChecker(): %v", err)
	}
	_, err = checker.Check(root)
	if !errors.Is(err, ErrTargetNotAFile) {
		t.Fatalf("Check(%q) error = %v, want ErrTargetNotAFile", root, err)
	}
	if message := err.Error(); message != "containment dependency is not a file" {
		t.Fatalf("Check(%q) error = %q, want the upstream message", root, message)
	}
}

// TestWriteEntryShadowedByDenyYieldsNoWritableRootLikeRust pins the entry filter
// upstream applies before collecting writable roots: a write entry whose own
// path resolves to a deny is not a writable root
// (permissions.rs:1574-1587).
func TestWriteEntryShadowedByDenyYieldsNoWritableRootLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	target := filepath.Join(root, "sandbox-helper")
	writeFixture(t, target)

	entries := restrictedPolicy([]policyEntry{
		{root, sandbox.FileSystemAccessWrite},
		{root, sandbox.FileSystemAccessDeny},
	})
	checker, err := NewFileContentsChecker(entries, root)
	if err != nil {
		t.Fatalf("NewFileContentsChecker(): %v", err)
	}
	if len(checker.policy.Roots) != 0 {
		t.Fatalf("PreparedPolicy.Roots = %#v, want no writable root", checker.policy.Roots)
	}
	finding, err := checker.Check(target)
	if err != nil {
		t.Fatalf("Check(%q): %v", target, err)
	}
	if finding != nil {
		t.Fatalf("Check(%q) = %#v, want no finding", target, finding)
	}
}

// TestInvalidDenyReadGlobIsRejectedLikeRust pins upstream's ReturnError behavior
// for a malformed deny-read glob (permissions.rs:1116-1137): the checker refuses
// to build instead of silently treating the pattern as if it matched.
func TestInvalidDenyReadGlobIsRejectedLikeRust(t *testing.T) {
	root := canonicalDirectory(t, t.TempDir())
	entries := withDenyGlob(
		restrictedPolicy([]policyEntry{{root, sandbox.FileSystemAccessWrite}}),
		filepath.Join(root, "sandbox-["),
	)
	if _, err := NewFileContentsChecker(entries, root); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewFileContentsChecker() error = %v, want ErrInvalidPolicy", err)
	}
}

// TestExplicitMetadataWriteGrantIsReportedLikeRust mirrors upstream's rule that a
// workspace metadata name stays protected until the policy grants a write rule
// for that metadata path itself (permissions.rs:2526-2549), and that an explicit
// entry replaces the default read-only carveout for the same resolved path
// (permissions.rs:1687, 2519-2524). Upstream protects `.codex`
// (permissions.rs:42); this repository uses the `.gcode` rename for the Codex
// metadata directory (sandbox/sandboxpath/sandboxpath.go:78), so both spellings
// are pinned here.
func TestExplicitMetadataWriteGrantIsReportedLikeRust(t *testing.T) {
	for _, name := range []string{".gcode", ".git"} {
		t.Run(name, func(t *testing.T) {
			root := canonicalDirectory(t, t.TempDir())
			metadataDirectory := filepath.Join(root, name)
			if err := os.Mkdir(metadataDirectory, 0o755); err != nil {
				t.Fatalf("mkdir %q: %v", metadataDirectory, err)
			}
			target := filepath.Join(metadataDirectory, "config.toml")
			writeFixture(t, target)

			entries := restrictedPolicy([]policyEntry{
				{root, sandbox.FileSystemAccessWrite},
				{metadataDirectory, sandbox.FileSystemAccessWrite},
			})
			checker, err := NewFileContentsChecker(entries, root)
			if err != nil {
				t.Fatalf("NewFileContentsChecker(): %v", err)
			}
			finding, err := checker.Check(target)
			if err != nil {
				t.Fatalf("Check(%q): %v", target, err)
			}
			if want := wantFinding(target, target, []string{root, metadataDirectory}); !reflect.DeepEqual(finding, want) {
				t.Fatalf("Check(%q) = %#v, want %#v", target, finding, want)
			}
		})
	}
}
