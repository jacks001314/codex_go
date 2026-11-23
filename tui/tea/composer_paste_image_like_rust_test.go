package tea

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

func writePastedImageFixture(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pasted.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return path
}

// Rust #62687: a pasted image path is copied into a private attachment at paste
// time, so moving or deleting the original cannot break the draft.
func TestPastedImagePathCopiesIntoPrivateAttachmentLikeRust(t *testing.T) {
	path := writePastedImageFixture(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.insertComposerPaste(path)
	if len(model.attachments) != 1 {
		t.Fatalf("attachments = %#v, want one pasted image", model.attachments)
	}
	attachment := model.attachments[0]
	if attachment.Kind != bottompane.AttachmentImage {
		t.Fatalf("attachment kind = %v", attachment.Kind)
	}
	if attachment.Path == path {
		t.Fatal("attachment kept the original path; want a private copy")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove original: %v", err)
	}
	copied, err := os.ReadFile(attachment.Path)
	if err != nil {
		t.Fatalf("read copied image after removing the original: %v", err)
	}
	if !bytes.Equal(copied, original) {
		t.Fatal("copied image bytes differ from the original")
	}
	if got := model.composerText(); !strings.Contains(got, "[Image #1]") {
		t.Fatalf("composer = %q, want the image placeholder", got)
	}
}

// Rust #62687: an oversized pasted image reports an error and preserves the
// draft instead of attaching a truncated file.
func TestPastedImagePathOverLimitReportsErrorAndKeepsDraftLikeRust(t *testing.T) {
	previous := codextui.MaxPastedImageBytes
	codextui.MaxPastedImageBytes = 8
	t.Cleanup(func() { codextui.MaxPastedImageBytes = previous })

	path := writePastedImageFixture(t)
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.composer.SetValue("keep me")
	model.insertComposerPaste(path)
	if len(model.attachments) != 0 {
		t.Fatalf("attachments = %#v, want none", model.attachments)
	}
	if got := model.composerText(); got != "keep me" {
		t.Fatalf("composer = %q, want the draft preserved", got)
	}
	if view := model.viewString(); !strings.Contains(view, "Failed to paste image: image exceeds the 1 GiB input limit") {
		t.Fatalf("transcript missing the paste error: %q", view)
	}
}
