package applypatch

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestParseErrorTextMatchesRustLikeRust pins the messages produced by
// codex-rs/apply-patch/src/parser.rs and streaming_parser.rs, including the two
// distinct renderings: the standalone entry point uses "Invalid patch[: hunk on
// line N]:" while the tool reports thiserror's Display form.
func TestParseErrorTextMatchesRustLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		patch string
		cli   string
		tool  string
	}{
		{
			name:  "missing begin marker",
			patch: "bad",
			cli:   "Invalid patch: The first line of the patch must be '*** Begin Patch'",
			tool:  "invalid patch: The first line of the patch must be '*** Begin Patch'",
		},
		{
			name:  "missing end marker",
			patch: "*** Begin Patch\nbad",
			cli:   "Invalid patch: The last line of the patch must be '*** End Patch'",
			tool:  "invalid patch: The last line of the patch must be '*** End Patch'",
		},
		{
			name:  "empty update hunk",
			patch: "*** Begin Patch\n*** Update File: foo.txt\n*** End Patch",
			cli:   "Invalid patch hunk on line 2: Update file hunk for path 'foo.txt' is empty",
			tool:  "invalid hunk at line 2, Update file hunk for path 'foo.txt' is empty",
		},
		{
			name:  "move without change lines",
			patch: "*** Begin Patch\n*** Update File: foo.txt\n*** Move to: bar.txt\n*** End Patch",
			cli:   "Invalid patch hunk on line 2: Update file hunk for path 'foo.txt' is empty",
			tool:  "invalid hunk at line 2, Update file hunk for path 'foo.txt' is empty",
		},
		{
			name:  "invalid hunk header",
			patch: "*** Begin Patch\n*** Frobnicate File: foo\n*** End Patch",
			cli:   "Invalid patch hunk on line 2: '*** Frobnicate File: foo' is not a valid hunk header. Valid hunk headers: '*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'",
			tool:  "invalid hunk at line 2, '*** Frobnicate File: foo' is not a valid hunk header. Valid hunk headers: '*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'",
		},
		{
			name:  "add hunk without plus prefix",
			patch: "*** Begin Patch\n*** Add File: x.txt\nnotplus\n*** End Patch",
			cli:   "Invalid patch hunk on line 3: 'notplus' is not a valid hunk header. Valid hunk headers: '*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'",
			tool:  "invalid hunk at line 3, 'notplus' is not a valid hunk header. Valid hunk headers: '*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'",
		},
		{
			name:  "empty change chunk at end marker",
			patch: "*** Begin Patch\n*** Update File: foo.txt\n@@\n*** End Patch",
			cli:   "Invalid patch hunk on line 4: Update hunk does not contain any lines",
			tool:  "invalid hunk at line 4, Update hunk does not contain any lines",
		},
		{
			name:  "empty change chunk at end of file marker",
			patch: "*** Begin Patch\n*** Update File: foo.txt\n@@\n*** End of File\n*** End Patch",
			cli:   "Invalid patch hunk on line 4: Update hunk does not contain any lines",
			tool:  "invalid hunk at line 4, Update hunk does not contain any lines",
		},
		{
			name:  "consecutive context markers",
			patch: "*** Begin Patch\n*** Update File: foo.txt\n@@\n@@\n*** End Patch",
			cli:   "Invalid patch hunk on line 4: Unexpected line found in update hunk: '@@'. Every line should start with ' ' (context line), '+' (added line), or '-' (removed line)",
			tool:  "invalid hunk at line 4, Unexpected line found in update hunk: '@@'. Every line should start with ' ' (context line), '+' (added line), or '-' (removed line)",
		},
		{
			name:  "missing context marker after content",
			patch: "*** Begin Patch\n*** Update File: foo.txt\n@@\n-old\nnewline\n*** End Patch",
			cli:   "Invalid patch hunk on line 5: Expected update hunk to start with a @@ context marker, got: 'newline'",
			tool:  "invalid hunk at line 5, Expected update hunk to start with a @@ context marker, got: 'newline'",
		},
		{
			name:  "empty environment id",
			patch: "*** Begin Patch\n*** Environment ID:   \n*** Add File: x.txt\n+hi\n*** End Patch",
			cli:   "Invalid patch: apply_patch environment_id cannot be empty",
			tool:  "invalid patch: apply_patch environment_id cannot be empty",
		},
		{
			name:  "duplicate environment id",
			patch: "*** Begin Patch\n*** Environment ID: a\n*** Environment ID: b\n*** Add File: x.txt\n+hi\n*** End Patch",
			cli:   "Invalid patch: apply_patch environment_id cannot be specified more than once",
			tool:  "invalid patch: apply_patch environment_id cannot be specified more than once",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Parse(testCase.patch)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want parse failure", testCase.patch)
			}
			if !errors.Is(err, ErrInvalidPatch) {
				t.Fatalf("Parse(%q) error = %v, want ErrInvalidPatch", testCase.patch, err)
			}
			if ClassifyError(err) != ErrorKindGrammar {
				t.Fatalf("ClassifyError(%v) = %q, want grammar", err, ClassifyError(err))
			}
			if got := FormatCLIError(err); got != testCase.cli {
				t.Fatalf("FormatCLIError = %q, want %q", got, testCase.cli)
			}
			if got := FormatError(err); got != testCase.tool {
				t.Fatalf("FormatError = %q, want %q", got, testCase.tool)
			}
		})
	}
}

// TestEmptyAddHunkCreatesEmptyFileLikeRust pins Rust streaming_parser behavior:
// an add hunk with no "+" lines is valid and creates an empty file.
func TestEmptyAddHunkCreatesEmptyFileLikeRust(t *testing.T) {
	dir := t.TempDir()
	patch := "*** Begin Patch\n*** Add File: empty.txt\n*** End Patch"
	result, err := Apply(patch, &ApplyOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Apply(%q) error = %v", patch, err)
	}
	info, err := os.Stat(filepath.Join(dir, "empty.txt"))
	if err != nil {
		t.Fatalf("empty.txt was not created: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("empty.txt size = %d, want 0", info.Size())
	}
	if got := result.Summary(); got != "Success. Updated the following files:\nA empty.txt\n" {
		t.Fatalf("Summary() = %q", got)
	}
}

// TestUpdateReadErrorUsesRustIOTextLikeRust pins the runtime error text from
// codex-rs/apply-patch/src/file_update.rs: capitalized context, Rust's localized
// io message, and no Go PathError "open <path>:" prefix.
func TestUpdateReadErrorUsesRustIOTextLikeRust(t *testing.T) {
	dir := t.TempDir()
	patch := "*** Begin Patch\n*** Update File: missing.txt\n@@\n-old\n+new\n*** End Patch"
	var stdout, stderr strings.Builder
	if code := RunCLI([]string{patch}, strings.NewReader(""), &stdout, &stderr, dir); code != 1 {
		t.Fatalf("RunCLI() code = %d, want 1", code)
	}
	message := strings.TrimSpace(stderr.String())
	wantPrefix := "Failed to read file to update " + filepath.Join(dir, "missing.txt") + ": "
	if !strings.HasPrefix(message, wantPrefix) {
		t.Fatalf("error = %q, want prefix %q", message, wantPrefix)
	}
	if strings.Contains(message, "open ") {
		t.Fatalf("error = %q leaks Go PathError formatting", message)
	}
	if !regexp.MustCompile(`\(os error \d+\)$`).MatchString(message) {
		t.Fatalf("error = %q, want Rust io suffix \" (os error N)\"", message)
	}
}
// TestHeredocWrappedPatchParsesLikeRust pins parser.rs's lenient boundary
// handling for gpt-4.1 local_shell heredoc arguments.
func TestHeredocWrappedPatchParsesLikeRust(t *testing.T) {
	patch := "<<'EOF'\n*** Begin Patch\n*** Add File: h.txt\n+hi\n*** End Patch\nEOF\n"
	if _, err := Parse(patch); err != nil {
		t.Fatalf("Parse(heredoc) error = %v", err)
	}
}