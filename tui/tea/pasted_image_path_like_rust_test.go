package tea

import (
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: #57774 `handle_paste_image_path`. A pasted path that resolves to
// a regular image file attaches instead of inserting the text; anything a stat
// says is not a regular file stays plain text so the input loop cannot block.

func pastedImagePNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer file.Close()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

func pasteText(t *testing.T, model *Model, text string) *Model {
	t.Helper()
	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune(text), Paste: true})
	return updated.(*Model)
}

func newPastedImageTestModel(t *testing.T) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
}

func TestPastedImagePathAttachesRegularFileLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	pastedImagePNG(t, path)

	model := pasteText(t, newPastedImageTestModel(t), path)
	if len(model.attachments) != 1 || model.attachments[0].Kind != bottompane.AttachmentImage || model.attachments[0].Path != path {
		t.Fatalf("attachments = %#v, want one local image for %s", model.attachments, path)
	}
	if got := model.composer.Value(); got != "[Image #1] " {
		t.Fatalf("composer value = %q, want the placeholder and a trailing space", got)
	}
	if len(model.composerElements) != 1 || model.composerElements[0].Placeholder != "[Image #1]" {
		t.Fatalf("composer elements = %#v", model.composerElements)
	}
}

func TestPastedImagePathKeepsQuotedAndFileURLFormsLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my shot.png")
	pastedImagePNG(t, path)

	for name, pasted := range map[string]string{
		"quoted":   `"` + path + `"`,
		"file URL": (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String(),
	} {
		model := pasteText(t, newPastedImageTestModel(t), pasted)
		if len(model.attachments) != 1 || filepath.Clean(model.attachments[0].Path) != filepath.Clean(path) {
			t.Fatalf("%s: attachments = %#v, want %s", name, model.attachments, path)
		}
	}
}

func TestPastedImagePathSkipsNonRegularFilesLikeRust(t *testing.T) {
	dir := t.TempDir()
	// A directory is not a regular file; probing it must not attach.
	model := pasteText(t, newPastedImageTestModel(t), dir)
	if len(model.attachments) != 0 {
		t.Fatalf("attachments = %#v, want none for a directory", model.attachments)
	}
	if got := model.composer.Value(); got != dir {
		t.Fatalf("composer value = %q, want the pasted directory path as text", got)
	}
}

func TestPastedImagePathSkipsNonImageFilesLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	model := pasteText(t, newPastedImageTestModel(t), path)
	if len(model.attachments) != 0 {
		t.Fatalf("attachments = %#v, want none for a text file", model.attachments)
	}
	if got := model.composer.Value(); got != path {
		t.Fatalf("composer value = %q, want the path as text", got)
	}
}

func TestNormalizePastedPathLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		pasted string
		want   string
		ok     bool
	}{
		{name: "plain", pasted: "/tmp/a.png", want: "/tmp/a.png", ok: true},
		{name: "quoted with space", pasted: `"/tmp/my shot.png"`, want: "/tmp/my shot.png", ok: true},
		{name: "shell escaped", pasted: `/tmp/my\ shot.png`, want: "/tmp/my shot.png", ok: true},
		{name: "two tokens", pasted: "/tmp/a.png /tmp/b.png", ok: false},
		{name: "blank", pasted: "   ", ok: false},
	}
	for _, testCase := range cases {
		got, ok := normalizePastedPath(testCase.pasted)
		if ok != testCase.ok || got != testCase.want {
			t.Fatalf("%s: normalizePastedPath(%q) = %q, %v; want %q, %v", testCase.name, testCase.pasted, got, ok, testCase.want, testCase.ok)
		}
	}
	if got, ok := normalizePastedPath("C:\\Temp\\a.png"); !ok || !strings.HasSuffix(got, "a.png") {
		t.Fatalf("windows path = %q, %v", got, ok)
	}
}
