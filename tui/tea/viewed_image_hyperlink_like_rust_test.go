package tea

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/app/history_ui.rs link_viewed_image (#58446):
// a viewed image is linked only for a local workspace outside an SSH session.

func viewedImageLinkModel(t *testing.T, options Options) (*Model, string, string) {
	t.Helper()
	return viewedImageLinkModelWithEnvironment(t, options, codextui.ClipboardEnvironment{})
}

// viewedImageLinkModelWithEnvironment pins the clipboard environment. The
// production probe reads the host's SSH_*/TMUX/STY variables, so without the pin
// these rows render one way locally and another way for anyone running the
// tests over SSH.
func viewedImageLinkModelWithEnvironment(t *testing.T, options Options, environment codextui.ClipboardEnvironment) (*Model, string, string) {
	t.Helper()
	path := writeTranscriptTestPNG(t, 8, 8)
	options.Width = 100
	options.Height = 30
	options.ClipboardEnvironment = &environment
	model := NewModel(codextui.NewState(nil), options)
	model.applyImageViewItem(&protocol.ThreadItem{ID: "view-1", Type: "imageView", Path: path})
	destination, ok := codextui.ViewedImageDestination(path)
	if !ok {
		t.Fatalf("the fixture image produced no destination: %q", path)
	}
	return model, path, destination
}

func viewedImageRowText(t *testing.T, model *Model) string {
	t.Helper()
	if len(model.State.Messages) != 1 {
		t.Fatalf("messages = %#v, want one viewed-image row", model.State.Messages)
	}
	return model.State.Messages[0].Text
}

func TestViewedImageRowLinksLocalFileLikeRust(t *testing.T) {
	model, path, destination := viewedImageLinkModel(t, Options{})
	text := viewedImageRowText(t, model)
	if !strings.Contains(text, "\x1b]8;;"+destination+"\x07"+filepath.Base(path)) {
		t.Fatalf("viewed image row = %q, want an OSC-8 file link to %q", text, destination)
	}

	// A local background daemon keeps the workspace local, so the link stays.
	daemon, _, daemonDestination := viewedImageLinkModel(t, Options{LocalDaemonSession: true})
	if text := viewedImageRowText(t, daemon); !strings.Contains(text, "\x1b]8;;"+daemonDestination+"\x07") {
		t.Fatalf("local daemon row = %q, want a file link", text)
	}
}

func TestViewedImageRowStaysPlainForRemoteWorkspaceLikeRust(t *testing.T) {
	model, path, _ := viewedImageLinkModel(t, Options{RemoteAppServer: true})
	text := viewedImageRowText(t, model)
	if strings.Contains(text, "\x1b]8;;") {
		t.Fatalf("remote workspace row = %q, want plain text", text)
	}
	if !strings.Contains(text, "Viewed image "+filepath.Base(path)) {
		t.Fatalf("remote workspace row = %q, want the file name still shown", text)
	}
}

func TestViewedImageRowStaysPlainOverSSHLikeRust(t *testing.T) {
	model, _, _ := viewedImageLinkModelWithEnvironment(t, Options{}, codextui.ClipboardEnvironment{SSHSession: true})
	if text := viewedImageRowText(t, model); strings.Contains(text, "\x1b]8;;") {
		t.Fatalf("SSH row = %q, want plain text", text)
	}
}

func TestViewedImageRowSkipsMissingImageLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.applyImageViewItem(&protocol.ThreadItem{ID: "view-2", Type: "imageView", Path: "missing/diagram.png"})
	text := viewedImageRowText(t, model)
	if strings.Contains(text, "\x1b]8;;") {
		t.Fatalf("missing image row = %q, want plain text", text)
	}
}
