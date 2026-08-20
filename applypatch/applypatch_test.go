package applypatch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCreateFreeformTool(t *testing.T) {
	tool := CreateFreeformTool(false)
	if tool.Name != "apply_patch" || tool.Format.Syntax != "lark" {
		t.Fatalf("CreateFreeformTool() = %#v", tool)
	}
	if strings.Contains(tool.Format.Definition, "Environment ID") {
		t.Fatalf("CreateFreeformTool(false) included environment grammar")
	}
	withEnv := CreateFreeformTool(true)
	if !strings.Contains(withEnv.Format.Definition, "Environment ID") {
		t.Fatalf("CreateFreeformTool(true) missing environment grammar")
	}
}

func TestParseAddDeleteUpdate(t *testing.T) {
	action, err := Parse(`*** Begin Patch
*** Add File: a.txt
+hello
+world
*** Delete File: old.txt
*** Update File: b.txt
*** Move to: c.txt
@@ context
-old
+new
*** End of File
*** End Patch`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if action.IsEmpty() {
		t.Fatalf("Action.IsEmpty() = true, want false")
	}
	if action.Changes["a.txt"].Content != "hello\nworld\n" {
		t.Fatalf("add content = %q", action.Changes["a.txt"].Content)
	}
	if action.Changes["old.txt"].Kind != ChangeDelete {
		t.Fatalf("old.txt kind = %s, want delete", action.Changes["old.txt"].Kind)
	}
	update := action.Changes["b.txt"]
	if update.MovePath != "c.txt" || !strings.Contains(update.UnifiedDiff, "-old\n+new") {
		t.Fatalf("update = %#v", update)
	}
	if got := action.FilePaths(); !sameStrings(got, []string{"a.txt", "old.txt", "b.txt", "c.txt"}) {
		t.Fatalf("FilePaths() = %v", got)
	}
}

// TestUpdateMoveRequiresChangeLinesLikeRust pins the Rust parser contract: the
// apply-patch spec requires at least one change line per update hunk
// (`change+`), so a move without any chunk is rejected even with `*** Move
// to:`. Verified against the Rust apply_patch oracle (which reports "Update
// file hunk for path 'X' is empty" / "Update hunk does not contain any
// lines").
func TestUpdateMoveRequiresChangeLinesLikeRust(t *testing.T) {
	dir := t.TempDir()
	writeApplyFile(t, dir, "legacy.py", "def format_name(first, last):\n    return first + \" \" + last\n")

	cases := []string{
		// No chunk at all.
		"*** Begin Patch\n*** Update File: legacy.py\n*** Move to: formatter.py\n*** End Patch",
		// An empty @@ context marker without change lines.
		"*** Begin Patch\n*** Update File: legacy.py\n*** Move to: formatter.py\n@@\n*** End Patch",
	}
	for _, patch := range cases {
		if _, err := Apply(patch, &ApplyOptions{CWD: dir}); err == nil {
			t.Fatalf("Apply(%q) error = nil, want empty-hunk rejection", patch)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "legacy.py")); err != nil {
		t.Fatalf("legacy.py should remain after rejected move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "formatter.py")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("formatter.py should not exist after rejected move: %v", err)
	}
}

// TestParseToleratesWhitespacePaddedMarkersLikeRust pins Rust parser leniency
// (parser.rs: "allows for leading/trailing whitespace around patch markers"),
// covered by apply-patch scenarios 017/018/020.
func TestParseToleratesWhitespacePaddedMarkersLikeRust(t *testing.T) {
	cases := []string{
		// 017: leading-space-padded update header.
		"*** Begin Patch\n  *** Update File: foo.txt\n@@\n-old\n+new\n*** End Patch",
		// 018: leading-space-padded begin marker, trailing-space end marker.
		" *** Begin Patch\n*** Update File: file.txt\n@@\n-one\n+two\n*** End Patch ",
		// 020: trailing-space begin marker, leading-space final end marker
		// (matched by StreamingPatchParser::finish's full trim).
		"*** Begin Patch \n*** Update File: file.txt\n@@\n-one\n+two\n *** End Patch",
	}
	for _, patch := range cases {
		action, err := Parse(patch)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", patch, err)
		}
		if action.IsEmpty() {
			t.Fatalf("Parse(%q) produced empty action", patch)
		}
	}
}

// TestApplyVerifiesBeforeApplyingLikeRustTool pins the app-server tool
// contract: the tool mirrors Rust's verify_apply_patch_args (verify the whole
// patch in a shadow workspace first, then apply), so a patch that fails
// verification applies nothing — unlike the CLI's sequential partial success.
func TestApplyVerifiesBeforeApplyingLikeRustTool(t *testing.T) {
	dir := t.TempDir()
	patch := "*** Begin Patch\n*** Add File: created.txt\n+hello\n*** Update File: missing.txt\n@@\n-old\n+new\n*** End Patch"
	if _, err := Apply(patch, &ApplyOptions{CWD: dir}); err == nil {
		t.Fatal("Apply() error = nil, want verification failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created.txt exists after failed verification, stat err = %v", err)
	}
}

func TestParseRejectsEmptyUpdateWithoutMove(t *testing.T) {
	_, err := Parse(`*** Begin Patch
*** Update File: legacy.py
*** End Patch`)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("Parse() error = %v, want empty hunk error", err)
	}
}

func TestParseEnvironmentID(t *testing.T) {
	action, err := Parse(`*** Begin Patch
*** Environment ID: env-1
*** Delete File: old.txt
*** End Patch`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if action.EnvironmentID != "env-1" {
		t.Fatalf("EnvironmentID = %q, want env-1", action.EnvironmentID)
	}
}

func TestParseRejectsInvalidPatch(t *testing.T) {
	cases := []string{
		"",
		"*** Begin Patch\n*** End Patch",
		"*** Begin Patch\n*** Add File: a.txt\nno plus\n*** End Patch",
		"*** Begin Patch\n*** Update File: empty.txt\n*** End Patch",
		"*** Begin Patch\nwhat\n*** End Patch",
	}
	for _, input := range cases {
		if _, err := Parse(input); !errors.Is(err, ErrInvalidPatch) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidPatch", input, err)
		}
	}
}

func TestApplyAcceptsRelativeAndAbsolutePathsLikeRust(t *testing.T) {
	cwd := t.TempDir()
	external := t.TempDir()
	relativeUpdate := filepath.Join(cwd, "relative-update.txt")
	absoluteUpdate := filepath.Join(external, "absolute-update.txt")
	relativeDelete := filepath.Join(cwd, "relative-delete.txt")
	absoluteDelete := filepath.Join(external, "absolute-delete.txt")
	if err := os.WriteFile(relativeUpdate, []byte("relative old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absoluteUpdate, []byte("absolute old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(relativeDelete, []byte("relative delete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absoluteDelete, []byte("absolute delete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	relativeAdd := filepath.Join(cwd, "relative-add.txt")
	absoluteAdd := filepath.Join(external, "absolute-add.txt")
	patch := "*** Begin Patch\n" +
		"*** Add File: relative-add.txt\n+relative add\n" +
		"*** Add File: " + absoluteAdd + "\n+absolute add\n" +
		"*** Delete File: relative-delete.txt\n" +
		"*** Delete File: " + absoluteDelete + "\n" +
		"*** Update File: relative-update.txt\n@@\n-relative old\n+relative new\n" +
		"*** Update File: " + absoluteUpdate + "\n@@\n-absolute old\n+absolute new\n" +
		"*** End Patch"
	if _, err := Apply(patch, &ApplyOptions{CWD: cwd}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	for path, want := range map[string]string{relativeAdd: "relative add\n", absoluteAdd: "absolute add\n", relativeUpdate: "relative new\n", absoluteUpdate: "absolute new\n"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("ReadFile(%s) = %q, %v; want %q", path, data, err, want)
		}
	}
	for _, path := range []string{relativeDelete, absoluteDelete} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted path %s still exists: %v", path, err)
		}
	}
}

func TestAbsolutePathPreflightFailureDoesNotMutateWorkspace(t *testing.T) {
	cwd := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "target.txt")
	if err := os.WriteFile(target, []byte("actual\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Add File: " + filepath.Join(external, "should-not-remain.txt") + "\n+temporary\n*** Update File: " + target + "\n@@\n-missing\n+new\n*** End Patch"
	if _, err := Apply(patch, &ApplyOptions{CWD: cwd}); err == nil {
		t.Fatal("Apply() error = nil, want failure")
	}
	if _, err := os.Stat(filepath.Join(external, "should-not-remain.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preflight add escaped into workspace: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "actual\n" {
		t.Fatalf("target = %q, %v", data, err)
	}
}

func TestApplyAbsoluteMovePreservesContentLikeRust(t *testing.T) {
	cwd := t.TempDir()
	external := t.TempDir()
	source := filepath.Join(external, "source.txt")
	destination := filepath.Join(external, "destination.txt")
	if err := os.WriteFile(source, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: " + source + "\n*** Move to: " + destination + "\n@@\n-before\n+after\n*** End Patch"
	if _, err := Apply(patch, &ApplyOptions{CWD: cwd}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "after\n" {
		t.Fatalf("destination = %q, %v", data, err)
	}
}

func TestErrorClassificationAndFormatting(t *testing.T) {
	err := Validate("*** Begin Patch\n*** End Patch")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("Validate error = %v", err)
	}
	if ClassifyError(err) != ErrorKindGrammar {
		t.Fatalf("ClassifyError = %q", ClassifyError(err))
	}
	if FormatError(err) != "No files were modified." {
		t.Fatalf("FormatError = %q", FormatError(err))
	}
	if ClassifyError(os.ErrNotExist) != ErrorKindApply {
		t.Fatalf("ClassifyError(apply) = %q", ClassifyError(os.ErrNotExist))
	}
}

func TestToProtocolCopiesChanges(t *testing.T) {
	action := &Action{Changes: map[string]Change{
		"a.txt": {Kind: ChangeAdd, Path: "a.txt", Content: "hello"},
	}}
	protocol := action.ToProtocol()
	protocol["a.txt"] = Change{Kind: ChangeDelete, Path: "a.txt"}
	if action.Changes["a.txt"].Kind != ChangeAdd {
		t.Fatalf("ToProtocol() did not copy map")
	}
}

func TestApplyResultTracksCommittedContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	result, err := Apply(`*** Begin Patch
*** Update File: old.txt
@@
-old
+new
*** End Patch`, &ApplyOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Apply(update) error = %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].OldContent != "old\n" || result.Changes[0].NewContent != "new\n" {
		t.Fatalf("update changes = %#v", result.Changes)
	}

	result, err = Apply(`*** Begin Patch
*** Delete File: old.txt
*** End Patch`, &ApplyOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Apply(delete) error = %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].OldContent != "new\n" || result.Changes[0].NewContent != "" {
		t.Fatalf("delete changes = %#v", result.Changes)
	}
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func TestSplitLinesNormalizesNewlines(t *testing.T) {
	got := splitLines("a\r\nb\rc\n")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("splitLines() = %v", got)
	}
}

func TestApplyAddsUpdatesMovesAndDeletes(t *testing.T) {
	dir := t.TempDir()
	writeApplyFile(t, dir, "modify.txt", "line1\nline2\n")
	writeApplyFile(t, dir, "delete.txt", "obsolete\n")
	writeApplyFile(t, dir, "old/name.txt", "from\n")

	result, err := Apply(`*** Begin Patch
*** Add File: nested/new.txt
+created
*** Update File: modify.txt
@@
-line2
+changed
*** Delete File: delete.txt
*** Update File: old/name.txt
*** Move to: renamed/name.txt
@@
-from
+to
*** End Patch`, &ApplyOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if got := result.Summary(); got != "Success. Updated the following files:\nA nested/new.txt\nM modify.txt\nM renamed/name.txt\nD delete.txt\n" {
		t.Fatalf("Summary = %q", got)
	}
	if got := readApplyFile(t, dir, "nested/new.txt"); got != "created\n" {
		t.Fatalf("new file = %q", got)
	}
	if got := readApplyFile(t, dir, "modify.txt"); got != "line1\nchanged\n" {
		t.Fatalf("modify file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("delete stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old/name.txt")); !os.IsNotExist(err) {
		t.Fatalf("old move stat error = %v, want not exist", err)
	}
	if got := readApplyFile(t, dir, "renamed/name.txt"); got != "to\n" {
		t.Fatalf("moved file = %q", got)
	}
}

func TestApplyUpdatesFileWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeApplyFile(t, dir, "target.txt", "no newline")
	if _, err := Apply(`*** Begin Patch
*** Update File: target.txt
@@
-no newline
+with newline
*** End Patch`, &ApplyOptions{CWD: dir}); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if got := readApplyFile(t, dir, "target.txt"); got != "with newline\n" {
		t.Fatalf("target = %q", got)
	}
}

func TestApplyRejectsMissingContext(t *testing.T) {
	dir := t.TempDir()
	writeApplyFile(t, dir, "target.txt", "actual\n")
	_, err := Apply(`*** Begin Patch
*** Update File: target.txt
@@
-missing
+new
*** End Patch`, &ApplyOptions{CWD: dir})
	if err == nil {
		t.Fatal("Apply returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "failed to find expected lines") {
		t.Fatalf("error = %v", err)
	}
	if got := readApplyFile(t, dir, "target.txt"); got != "actual\n" {
		t.Fatalf("target mutated = %q", got)
	}
}

func TestApplyDoesNotPartiallyCommitWhenLaterHunkFails(t *testing.T) {
	dir := t.TempDir()
	writeApplyFile(t, dir, "target.txt", "actual\n")
	_, err := Apply(`*** Begin Patch
*** Add File: should-not-remain.txt
+temporary
*** Update File: target.txt
@@
-missing
+new
*** End Patch`, &ApplyOptions{CWD: dir})
	if err == nil {
		t.Fatal("Apply returned nil error, want failure")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "should-not-remain.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("earlier add was partially committed: %v", statErr)
	}
	if got := readApplyFile(t, dir, "target.txt"); got != "actual\n" {
		t.Fatalf("target mutated = %q", got)
	}
}

func writeApplyFile(t *testing.T, dir string, name string, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func readApplyFile(t *testing.T, dir string, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return string(data)
}

func TestApplyRejectsDuplicateResolvedPathsLikeRust(t *testing.T) {
	dir := t.TempDir()
	writeApplyFile(t, dir, "duplicate.txt", "before\n")
	action, err := Parse("*** Begin Patch\n*** Update File: duplicate.txt\n@@\n-before\n+first after\n*** Update File: ./duplicate.txt\n@@\n-before\n+second after\n*** End Patch")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	err = action.Verify(&ApplyOptions{CWD: dir})
	if err == nil || !strings.Contains(err.Error(), "multiple operations target") {
		t.Fatalf("Verify(duplicate paths) error = %v", err)
	}
	if got := readApplyFile(t, dir, "duplicate.txt"); got != "before\n" {
		t.Fatalf("duplicate.txt mutated despite rejection: %q", got)
	}

	// Distinct files remain accepted (Rust apply_patch_cli_preserves_distinct_updated_paths).
	writeApplyFile(t, dir, "first.txt", "first before\n")
	writeApplyFile(t, dir, "second.txt", "second before\n")
	distinct, err := Parse("*** Begin Patch\n*** Update File: first.txt\n@@\n-first before\n+first after\n*** Update File: second.txt\n@@\n-second before\n+second after\n*** End Patch")
	if err != nil {
		t.Fatalf("Parse(distinct) error = %v", err)
	}
	if err := distinct.Verify(&ApplyOptions{CWD: dir}); err != nil {
		t.Fatalf("Verify(distinct) error = %v", err)
	}
}

func TestNoFollowSymlinksRejectsLinkedPathComponents(t *testing.T) {
	// Rust #39659: an unsandboxed patch must not follow symlinks in any path
	// component, so a verified path cannot be swapped for a link to another
	// file.
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	action, err := Parse("*** Begin Patch\n*** Add File: linked/secret.txt\n+evil\n*** End Patch")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	err = action.Verify(&ApplyOptions{CWD: dir, NoFollowSymlinks: true})
	if err == nil || !strings.Contains(err.Error(), "traverses symlink") {
		t.Fatalf("Verify(no-follow) error = %v, want symlink rejection", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(outside, "secret.txt")); readErr != nil || string(data) != "secret\n" {
		t.Fatalf("outside file mutated: %q, %v", data, readErr)
	}
	// A leaf symlink is also rejected.
	leafTarget := filepath.Join(dir, "leaf-target.txt")
	if err := os.WriteFile(leafTarget, []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(leafTarget, filepath.Join(dir, "leaf-link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	leafAction, err := Parse("*** Begin Patch\n*** Update File: leaf-link.txt\n@@\n-target\n+changed\n*** End Patch")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if err := leafAction.Verify(&ApplyOptions{CWD: dir, NoFollowSymlinks: true}); err == nil || !strings.Contains(err.Error(), "traverses symlink") {
		t.Fatalf("leaf Verify(no-follow) error = %v, want symlink rejection", err)
	}
	// With the default follow behavior the patch still applies.
	if err := leafAction.Verify(&ApplyOptions{CWD: dir}); err != nil {
		t.Fatalf("Verify(follow) error = %v", err)
	}
}
