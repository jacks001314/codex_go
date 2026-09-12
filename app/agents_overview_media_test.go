package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bottompane "codex_go/tui/bottom_pane"
	codextea "codex_go/tui/tea"
)

// TestAgentsOverviewTaskInputsLocalAndRemote covers Rust #44027: the first task
// prompt carries the images (up to date locally, snapshotted for a remote
// workspace) followed by the prompt text.
func TestAgentsOverviewTaskInputsLocalAndRemote(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "chart.png")
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3, 4}
	if err := os.WriteFile(imagePath, png, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	request := codextea.SubmitRequest{
		Prompt: "inspect the chart",
		Attachments: []bottompane.ComposerAttachment{
			{Kind: bottompane.AttachmentImage, Path: imagePath},
			{Kind: bottompane.AttachmentRemoteImage, URL: "https://example.com/a.png"},
		},
	}

	local, err := agentsOverviewTaskInputs(request, false)
	if err != nil {
		t.Fatalf("local inputs: %v", err)
	}
	if len(local) != 3 {
		t.Fatalf("local inputs = %#v", local)
	}
	if local[0].Type != "localImage" || local[0].Path != imagePath {
		t.Fatalf("local image input = %#v", local[0])
	}
	if local[1].Type != "image" || local[1].URL != "https://example.com/a.png" {
		t.Fatalf("remote image input = %#v", local[1])
	}
	if local[2].Type != "text" || local[2].Text != "inspect the chart" {
		t.Fatalf("prompt input = %#v", local[2])
	}

	remote, err := agentsOverviewTaskInputs(request, true)
	if err != nil {
		t.Fatalf("remote inputs: %v", err)
	}
	if remote[0].Type != "image" || !strings.HasPrefix(remote[0].URL, "data:image/png;base64,") {
		t.Fatalf("snapshotted image input = %#v", remote[0])
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(remote[0].URL, "data:image/png;base64,"))
	if err != nil || string(decoded) != string(png) {
		t.Fatalf("snapshot payload = %q (err=%v)", decoded, err)
	}
}

// TestLocalImageDataURLDetectsMIME covers the data-URL helper's extension and
// content-sniffing fallbacks.
func TestLocalImageDataURLDetectsMIME(t *testing.T) {
	dir := t.TempDir()
	for name, payload := range map[string]struct {
		file  string
		bytes []byte
		mime  string
	}{
		"png":     {"chart.png", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, "image/png"},
		"jpeg":    {"photo.JPEG", []byte{0xff, 0xd8, 0xff, 0xe0}, "image/jpeg"},
		"unknown": {"blob.bin", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, "image/png"},
	} {
		path := filepath.Join(dir, payload.file)
		if err := os.WriteFile(path, payload.bytes, 0o644); err != nil {
			t.Fatalf("%s: write: %v", name, err)
		}
		url, err := localImageDataURL(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(url, "data:"+payload.mime+";base64,") {
			t.Fatalf("%s: url = %q, want %s", name, url, payload.mime)
		}
	}
	if _, err := localImageDataURL(filepath.Join(dir, "missing.png")); err == nil {
		t.Fatal("missing file should fail")
	}
}
