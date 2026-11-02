package tui

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/terminal_hyperlinks.rs
// `TerminalHyperlink::viewed_image` (#58446, a795f961be). A viewed image gets a
// file link only when the original and the resolved path are local regular
// image files; a UNC destination, a missing file, a directory, or a symlink
// whose target is not an image stays plain text.

func writeHyperlinkFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestViewedImageDestinationRequiresLocalImageFileLikeRust(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "diagram.png")
	writeHyperlinkFixture(t, image)

	destination, ok := ViewedImageDestination(image)
	if !ok {
		t.Fatalf("ViewedImageDestination(%q) rejected a local regular image", image)
	}
	parsed, err := url.Parse(destination)
	if err != nil {
		t.Fatalf("parse destination %q: %v", destination, err)
	}
	if parsed.Scheme != "file" || parsed.Host != "" {
		t.Fatalf("destination = %q, want a hostless file URL", destination)
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Path), "/diagram.png") {
		t.Fatalf("destination path = %q, want the image path", parsed.Path)
	}

	if _, ok := ViewedImageDestination(filepath.Join(dir, "missing.png")); ok {
		t.Fatal("a missing image produced a link")
	}
	notes := filepath.Join(dir, "notes.txt")
	writeHyperlinkFixture(t, notes)
	if _, ok := ViewedImageDestination(notes); ok {
		t.Fatal("a non-image file produced a link")
	}
	folder := filepath.Join(dir, "folder.png")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", folder, err)
	}
	if _, ok := ViewedImageDestination(folder); ok {
		t.Fatal("a directory named like an image produced a link")
	}

	upper := filepath.Join(dir, "SHOT.PNG")
	writeHyperlinkFixture(t, upper)
	if _, ok := ViewedImageDestination(upper); !ok {
		t.Fatal("an uppercase image extension was rejected")
	}

	// A UNC path would put a host in the file URL, which Rust refuses.
	if _, ok := ViewedImageDestination(`\\server\share\shot.png`); ok {
		t.Fatal("a UNC image path produced a link")
	}
}

func TestViewedImageDestinationRejectsSymlinkedNonImageLikeRust(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "payload.txt")
	writeHyperlinkFixture(t, target)
	link := filepath.Join(dir, "screenshot.png")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if destination, ok := ViewedImageDestination(link); ok {
		t.Fatalf("symlink to a non-image produced %q", destination)
	}
}

func TestOSC8FileHyperlinkSerializesLocalFilesLikeRust(t *testing.T) {
	if got := OSC8FileHyperlink("https://example.com/x", "text"); got != "text" {
		t.Fatalf("web destination serialized as a file link: %q", got)
	}
	if got := OSC8FileHyperlink("file://server/share/a.png", "text"); got != "text" {
		t.Fatalf("hosted file URL serialized: %q", got)
	}
	got := OSC8FileHyperlink("file:///tmp/a.png", "a.png")
	want := "\x1b]8;;file:///tmp/a.png\x07a.png\x1b]8;;\x07"
	if got != want {
		t.Fatalf("file hyperlink = %q, want %q", got, want)
	}
}

// Rust rejects Windows reparse points in any ancestor because a junction can
// lead to a UNC share, so the resolved file is never the one that was checked.
func TestViewedImageDestinationRejectsJunctionAncestorLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", realDir, err)
	}
	writeHyperlinkFixture(t, filepath.Join(realDir, "shot.png"))
	junction := filepath.Join(dir, "junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", junction, realDir).Run(); err != nil {
		t.Skipf("junctions unavailable: %v", err)
	}
	if destination, ok := ViewedImageDestination(filepath.Join(junction, "shot.png")); ok {
		t.Fatalf("a path through a junction produced %q", destination)
	}
}

