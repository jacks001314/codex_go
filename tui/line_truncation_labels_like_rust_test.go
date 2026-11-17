package tui

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/line_truncation.rs middle_label /
// filename_label (the sidebar chooser's file rows).

func TestMiddleLabelKeepsBothEndsLikeRust(t *testing.T) {
	if got := MiddleLabel("short.txt", 20); got != "short.txt" {
		t.Fatalf("MiddleLabel(short) = %q", got)
	}
	if got := MiddleLabel("", 5); got != "" {
		t.Fatalf("MiddleLabel(empty) = %q", got)
	}
	if got := MiddleLabel("abcdefghij", 0); got != "" {
		t.Fatalf("MiddleLabel(width 0) = %q", got)
	}
	got := MiddleLabel("src/components/sidebar/chooser.tsx", 20)
	if DisplayWidth(got) > 20 {
		t.Fatalf("MiddleLabel width = %d, want <= 20 (%q)", DisplayWidth(got), got)
	}
	if !hasPrefixSuffix(got, "src/", ".tsx") || !containsEllipsis(got) {
		t.Fatalf("MiddleLabel = %q, want both ends and an ellipsis", got)
	}
}

func TestFilenameLabelKeepsExtensionLikeRust(t *testing.T) {
	got := FilenameLabel("sidebar_chooser_extra_long_name.tsx", 16)
	if DisplayWidth(got) > 16 {
		t.Fatalf("FilenameLabel width = %d, want <= 16 (%q)", DisplayWidth(got), got)
	}
	if !hasPrefixSuffix(got, "", ".tsx") || !containsEllipsis(got) {
		t.Fatalf("FilenameLabel = %q, want the extension kept", got)
	}
	// A dot inside a path component is not an extension.
	got = FilenameLabel("a.b/c", 12)
	if got != "a.b/c" {
		t.Fatalf("FilenameLabel(path dot) = %q", got)
	}
}

func TestDocumentLabelJoinsPartsTowardRootLikeRust(t *testing.T) {
	parts := []string{"chooser.tsx", "sidebar", "src"}
	if got := DocumentLabel(parts, 40); got != "src/sidebar/chooser.tsx" {
		t.Fatalf("DocumentLabel(wide) = %q", got)
	}
	if got := DocumentLabel(parts, 0); got != "" {
		t.Fatalf("DocumentLabel(width 0) = %q", got)
	}
	if got := DocumentLabel(nil, 10); got != "" {
		t.Fatalf("DocumentLabel(empty) = %q", got)
	}
}

func TestDocumentLabelKeepsDirectorySuffixWhenNarrowLikeRust(t *testing.T) {
	parts := []string{"sidebar_chooser_extra_long_name.tsx", "very_long_directory_name", "src"}
	got := DocumentLabel(parts, 24)
	if DisplayWidth(got) > 24 {
		t.Fatalf("DocumentLabel width = %d, want <= 24 (%q)", DisplayWidth(got), got)
	}
	if !strings.Contains(got, ".tsx") {
		t.Fatalf("DocumentLabel = %q, want the filename extension kept", got)
	}
	if !strings.Contains(got, "/") {
		t.Fatalf("DocumentLabel = %q, want a directory/filename split", got)
	}
	// Too narrow to keep a directory falls back to filename_label, which at
	// width 3 cannot even keep the extension (Rust yields "s…x" for this path).
	got = DocumentLabel(parts, 3)
	if DisplayWidth(got) > 3 || !containsEllipsis(got) {
		t.Fatalf("DocumentLabel(narrowest) = %q", got)
	}
	// One part is just a filename, so no directory join happens.
	if got := DocumentLabel([]string{"README.md"}, 4); DisplayWidth(got) > 4 {
		t.Fatalf("DocumentLabel(single part) = %q, want width <= 4", got)
	}
}

func hasPrefixSuffix(value, prefix, suffix string) bool {
	return len(value) >= len(prefix)+len(suffix) && value[:len(prefix)] == prefix && value[len(value)-len(suffix):] == suffix
}

func containsEllipsis(value string) bool {
	for _, r := range value {
		if r == '…' {
			return true
		}
	}
	return false
}
