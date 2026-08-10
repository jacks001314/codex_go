package applypatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileUpdateModeFromEnv(t *testing.T) {
	old := os.Getenv(PreserveLineEndingsEnvVar)
	defer func() {
		if old == "" {
			_ = os.Unsetenv(PreserveLineEndingsEnvVar)
		} else {
			_ = os.Setenv(PreserveLineEndingsEnvVar, old)
		}
	}()
	_ = os.Unsetenv(PreserveLineEndingsEnvVar)
	if got := FileUpdateModeFromEnv(); got != UpdateModeNormalizeToLF {
		t.Fatalf("FileUpdateModeFromEnv() = %v, want NormalizeToLF", got)
	}
	_ = os.Setenv(PreserveLineEndingsEnvVar, "1")
	if got := FileUpdateModeFromEnv(); got != UpdateModePreserveLineEndings {
		t.Fatalf("FileUpdateModeFromEnv() = %v, want PreserveLineEndings", got)
	}
	_ = os.Setenv(PreserveLineEndingsEnvVar, "0")
	if got := FileUpdateModeFromEnv(); got != UpdateModeNormalizeToLF {
		t.Fatalf("FileUpdateModeFromEnv() = %v, want NormalizeToLF for non-1 value", got)
	}
}

func TestPreserveModeCLIUsesEnvVar(t *testing.T) {
	old := os.Getenv(PreserveLineEndingsEnvVar)
	defer func() {
		if old == "" {
			_ = os.Unsetenv(PreserveLineEndingsEnvVar)
		} else {
			_ = os.Setenv(PreserveLineEndingsEnvVar, old)
		}
	}()
	dir := t.TempDir()
	target := filepath.Join(dir, "crlf.txt")
	if err := os.WriteFile(target, []byte("one\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: crlf.txt\n@@\n-one\n+uno\n*** End Patch"
	_ = os.Setenv(PreserveLineEndingsEnvVar, "1")
	var stdout, stderr strings.Builder
	if code := RunCLI([]string{patch}, nil, &stdout, &stderr, dir); code != 0 {
		t.Fatalf("RunCLI() code = %d, stderr = %q", code, stderr.String())
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "uno\r\n" {
		t.Fatalf("preserve mode target = %q, want %q", data, "uno\r\n")
	}
}

func TestApplyPreservesCRLFFromTargetFile(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: crlf.txt\n@@\n-one\n+uno\n@@\n two\n+\n+between\n three\n*** End Patch"
	assertPreservingUpdate(t, "crlf.txt", "one\r\ntwo\r\nthree\r\n", patch, "uno\r\ntwo\r\n\r\nbetween\r\nthree\r\n")
}

func TestApplyPreservesAppendAfterTrailingBlankCRLFLine(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: trailing_blank.txt\n@@\n+new\n*** End Patch"
	assertPreservingUpdate(t, "trailing_blank.txt", "a\r\n\r\n", patch, "a\r\n\r\nnew\r\n")
}

func TestApplyPreservesCRFromTargetFile(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: cr.txt\n@@\n-one\n+uno\n@@\n two\n+\n+between\n three\n*** End Patch"
	assertPreservingUpdate(t, "cr.txt", "one\rtwo\rthree\r", patch, "uno\rtwo\r\rbetween\rthree\r")
}

func TestApplyPreservesChangeOrderWithRepeatedLines(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: repeated.txt\n@@\n-a\n-b\n+b\n+b\n+a\n*** End Patch"
	assertPreservingUpdate(t, "repeated.txt", "a\nb\n", patch, "b\nb\na\n")
}

func TestApplyPreservesRepeatedContextLineEnding(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: repeated_context.txt\n@@\n-same\n same\n*** End Patch"
	assertPreservingUpdate(t, "repeated_context.txt", "same\r\nsame\n", patch, "same\n")
}

func TestApplyPreservesUntouchedMixedLineEndings(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: mixed.txt\n@@\n one\n two\n-three\n+THREE\n four\n*** End Patch"
	assertPreservingUpdate(t, "mixed.txt", "one\r\ntwo\rthree\r\nfour\r\n", patch, "one\r\ntwo\rTHREE\r\nfour\r\n")
}

func TestApplyPreserveUsesCRLFForNewTrailingNewline(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: no_trailing_newline.txt\n@@\n-one\n+ONE\n two\n*** End Patch"
	assertPreservingUpdate(t, "no_trailing_newline.txt", "one\r\ntwo", patch, "ONE\r\ntwo\r\n")
}

func TestApplyPreserveRejectsOverlappingEndOfFileChunks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "overlapping.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: overlapping.txt\n@@\n-one\n+first\n@@\n-one\n+second\n*** End of File\n*** End Patch"
	_, err := Apply(patch, &ApplyOptions{CWD: dir, FileUpdateMode: UpdateModePreserveLineEndings})
	if err == nil {
		t.Fatal("Apply() error = nil, want overlapping chunk failure")
	}
	if !strings.Contains(err.Error(), "failed to find expected lines") {
		t.Fatalf("Apply() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\n" {
		t.Fatalf("target mutated = %q", data)
	}
}

func TestApplyDefaultModeNormalizesLineEndings(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crlf.txt")
	if err := os.WriteFile(target, []byte("one\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: crlf.txt\n@@\n-one\n+uno\n*** End Patch"
	if _, err := Apply(patch, &ApplyOptions{CWD: dir}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "uno\n" {
		t.Fatalf("normalize mode target = %q, want %q", data, "uno\n")
	}
}

func assertPreservingUpdate(t *testing.T, name string, original string, patch string, expected string) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(patch, &ApplyOptions{CWD: dir, FileUpdateMode: UpdateModePreserveLineEndings})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(result.Updated) != 1 || result.Updated[0].Path != filepath.FromSlash(name) {
		t.Fatalf("result.Updated = %#v", result.Updated)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expected {
		t.Fatalf("target = %q, want %q", data, expected)
	}
}

func TestPreserveModeVerificationMatchesApplication(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crlf.txt")
	if err := os.WriteFile(target, []byte("before\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: crlf.txt\n@@\n-before\n+after\n*** End Patch"
	action, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	options := &ApplyOptions{CWD: dir, FileUpdateMode: UpdateModePreserveLineEndings}
	if err := action.Verify(options); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if _, err := action.ApplyVerified(options); err != nil {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after\r\n" {
		t.Fatalf("target = %q, want %q", data, "after\r\n")
	}
}

func TestParseUpdateChunksRecordsContextAndEOF(t *testing.T) {
	chunks, err := parseUpdateChunks("@@\n one\n-two\n+two\n three\n*** End of File")
	if err != nil {
		t.Fatalf("parseUpdateChunks() error = %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	chunk := chunks[0]
	if !chunk.isEndOfFile {
		t.Fatalf("isEndOfFile = false, want true")
	}
	if len(chunk.contextIndices) != 2 {
		t.Fatalf("contextIndices = %v, want 2 entries", chunk.contextIndices)
	}
	if chunk.contextIndices[0] != [2]int{0, 0} || chunk.contextIndices[1] != [2]int{2, 2} {
		t.Fatalf("contextIndices = %v", chunk.contextIndices)
	}
	if len(chunk.oldLines) != 3 || len(chunk.newLines) != 3 {
		t.Fatalf("oldLines = %v newLines = %v", chunk.oldLines, chunk.newLines)
	}
}

func TestParseUpdateChunksRecordsChangeContext(t *testing.T) {
	chunks, err := parseUpdateChunks("@@ def f():\n-old\n+new\n")
	if err != nil {
		t.Fatalf("parseUpdateChunks() error = %v", err)
	}
	if len(chunks) != 1 || chunks[0].changeContext != "def f():" {
		t.Fatalf("changeContext = %q, want %q", chunks[0].changeContext, "def f():")
	}
}
