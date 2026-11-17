package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/tui/markdown"
	codextea "codex_go/tui/tea"
)

// Rust sidebar_document.rs `read_document` with the embedded app-server: the
// row's path resolves against the workspace and Markdown renders as Markdown.
func TestEmbeddedSidebarDocumentHostReadsWorkspaceLikeRust(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := "# Title\n\nBody\n"
	if err := os.WriteFile(filepath.Join(workspace, "docs", "readme.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	host := newEmbeddedSidebarDocumentHost(func() string { return workspace })
	view := host.open(codextea.SidebarDocumentRequest{
		ParentThreadID: "parent-a",
		SourceThreadID: "parent-a",
		Path:           "docs/readme.md",
		Link:           -1,
	})
	if view.Err != "" {
		t.Fatalf("view error = %q", view.Err)
	}
	if view.Text != source || view.Code {
		t.Fatalf("view = %#v", view)
	}
	if !strings.HasSuffix(view.Path, "docs/readme.md") {
		t.Fatalf("resolved path = %q", view.Path)
	}
}

// Rust `Document::loaded`: source previews stop at 1 MiB and 10 000 lines.
func TestEmbeddedSidebarDocumentHostLimitsLikeRust(t *testing.T) {
	workspace := t.TempDir()
	big := strings.Repeat("a", sidebarDocumentMaxSourceBytes+1)
	if err := os.WriteFile(filepath.Join(workspace, "big.go"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := strings.Repeat("line\n", sidebarDocumentMaxSourceLines+1)
	if err := os.WriteFile(filepath.Join(workspace, "long.go"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	host := newEmbeddedSidebarDocumentHost(func() string { return workspace })

	view := host.open(codextea.SidebarDocumentRequest{SourceThreadID: "parent-a", Path: "big.go", Link: -1})
	if view.Err != "Source preview limit is 1 MiB" {
		t.Fatalf("byte limit error = %q", view.Err)
	}
	view = host.open(codextea.SidebarDocumentRequest{SourceThreadID: "parent-a", Path: "long.go", Link: -1})
	if view.Err != "Source preview limit is 10000 lines" {
		t.Fatalf("line limit error = %q", view.Err)
	}
}

// Rust `read_document` rejects content the sidebar cannot display as text.
func TestEmbeddedSidebarDocumentHostRejectsNonTextLikeRust(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "binary.bin"), []byte{0xff, 0xfe, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "control.txt"), []byte("ok\x07"), 0o644); err != nil {
		t.Fatal(err)
	}
	host := newEmbeddedSidebarDocumentHost(func() string { return workspace })
	if view := host.open(codextea.SidebarDocumentRequest{SourceThreadID: "parent-a", Path: "binary.bin", Link: -1}); view.Err != remotePreviewNotUTF8 {
		t.Fatalf("binary error = %q", view.Err)
	}
	if view := host.open(codextea.SidebarDocumentRequest{SourceThreadID: "parent-a", Path: "control.txt", Link: -1}); view.Err != remotePreviewNotText {
		t.Fatalf("control error = %q", view.Err)
	}
}

// Rust `Document::loaded` + `source_target`: a coordinate link always opens
// source, counts its lines, and resolves the target offset.
func TestSidebarDocumentViewOfLocationLikeRust(t *testing.T) {
	document := newSidebarDocument("parent-a", "src/main.go", -1, &markdown.SidebarSourceLocation{Line: 3, Column: 2})
	document.loaded("package main\n\nfunc main() {}\n", nil)
	view := sidebarDocumentViewOf(document)
	if !view.Code || view.Language != "go" {
		t.Fatalf("view = %#v", view)
	}
	if len(view.LineStarts) != 3 {
		t.Fatalf("line starts = %#v", view.LineStarts)
	}
	// Rust source_target returns (line, start + column byte offset).
	if view.Target == nil || view.Target.Line != 3 || view.Target.Offset != len("package main\n\n")+1 {
		t.Fatalf("target = %#v", view.Target)
	}
}
