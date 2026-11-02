//go:build linux

package tea

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/clipboard_paste.rs. Rust reads the clipboard
// through arboard (X11/Wayland), prefers a clipboard file list over raw image
// data, and hands the widget a PNG temp file. Go keeps that contract without
// cgo by driving the same selection helpers the primary-selection code uses
// (wl-paste on Wayland, xclip on X11).
//
// A verbatim image/png payload is written through byte for byte so callers can
// verify the pasted file against the clipboard source; other formats are
// decoded and re-encoded like Rust's image::DynamicImage::write_to(Png).

// clipboardImageTimeout bounds every helper invocation so a wedged selection
// owner cannot freeze the composer.
const clipboardImageTimeout = 3 * time.Second

type clipboardImageTool struct {
	name string
	// targets lists the offered clipboard MIME types.
	targets []string
	// read returns the payload for one MIME type.
	read func(mime string) []string
	// files returns a text/uri-list payload (Rust prefers file lists).
	files []string
}

func waylandClipboardTool() clipboardImageTool {
	return clipboardImageTool{
		name:    "wl-paste",
		targets: []string{"--list-types"},
		read:    func(mime string) []string { return []string{"--type", mime, "--no-newline"} },
		files:   []string{"--type", "text/uri-list", "--no-newline"},
	}
}

func x11ClipboardTool() clipboardImageTool {
	read := func(mime string) []string {
		return []string{"-selection", "clipboard", "-t", mime, "-o"}
	}
	return clipboardImageTool{
		name:    "xclip",
		targets: []string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
		read:    read,
		files:   read("text/uri-list"),
	}
}

// pasteImageFromClipboardPlatform captures the clipboard image on Linux.
func pasteImageFromClipboardPlatform() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardImageTimeout)
	defer cancel()
	tools := clipboardImageTools(os.Getenv("WAYLAND_DISPLAY") != "", os.Getenv("DISPLAY") != "")
	return pasteClipboardImage(ctx, tools)
}

func clipboardImageTools(wayland, x11 bool) []clipboardImageTool {
	var tools []clipboardImageTool
	if wayland {
		tools = append(tools, waylandClipboardTool())
	}
	if x11 {
		tools = append(tools, x11ClipboardTool())
	}
	return tools
}

// clipboardImageTimeoutError reports a wedged clipboard as unavailable so the
// TUI surfaces an error instead of silently pasting text (Rust routes the same
// case through PasteImageError::ClipboardUnavailable).
func clipboardImageTimeoutError() error {
	return codextui.PasteImageError{
		Kind:    codextui.PasteImageClipboardUnavailable,
		Message: fmt.Sprintf("clipboard read timed out after %s", clipboardImageTimeout),
	}
}

func pasteClipboardImage(ctx context.Context, tools []clipboardImageTool) (string, error) {
	if len(tools) == 0 {
		return "", codextui.PasteImageError{
			Kind:    codextui.PasteImageClipboardUnavailable,
			Message: "no X11 or Wayland display (DISPLAY and WAYLAND_DISPLAY are unset)",
		}
	}
	var unavailable []string
	var noImage []string
	for _, tool := range tools {
		if _, err := exec.LookPath(tool.name); err != nil {
			unavailable = append(unavailable, tool.name+": not found in PATH")
			continue
		}
		if path, ok := clipboardImageFromFiles(ctx, tool); ok {
			return path, nil
		}
		// A wedged selection owner is a backend failure, not "no image": Rust
		// maps it to ClipboardUnavailable and never falls back to text.
		if err := ctx.Err(); err != nil {
			return "", clipboardImageTimeoutError()
		}
		mime, ok := clipboardImageMime(ctx, tool)
		if err := ctx.Err(); err != nil {
			return "", clipboardImageTimeoutError()
		}
		if !ok {
			noImage = append(noImage, tool.name+": no image on clipboard")
			continue
		}
		data, err := runClipboardHelper(ctx, tool.name, tool.read(mime))
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return "", clipboardImageTimeoutError()
			}
			noImage = append(noImage, tool.name+": "+err.Error())
			continue
		}
		if len(data) == 0 {
			noImage = append(noImage, tool.name+": empty "+mime+" payload")
			continue
		}
		path, err := writeClipboardImagePNG(data, mime)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	if len(noImage) == 0 && len(unavailable) > 0 {
		return "", codextui.PasteImageError{
			Kind:    codextui.PasteImageClipboardUnavailable,
			Message: strings.Join(unavailable, "; "),
		}
	}
	detail := append(noImage, unavailable...)
	return "", codextui.PasteImageError{
		Kind:    codextui.PasteImageNoImage,
		Message: strings.Join(detail, "; "),
	}
}

// clipboardImageMime picks the offered image target, preferring PNG (Rust hands
// the widget a PNG in every case).
func clipboardImageMime(ctx context.Context, tool clipboardImageTool) (string, bool) {
	output, err := runClipboardHelper(ctx, tool.name, tool.targets)
	if err != nil {
		return "", false
	}
	best := ""
	for _, line := range strings.Split(string(output), "\n") {
		target := strings.TrimSpace(line)
		if len(target) > len("image/") && strings.EqualFold(target[:len("image/")], "image/") {
			if best == "" {
				best = target
			}
			if strings.EqualFold(target, "image/png") {
				return target, true
			}
		}
	}
	return best, best != ""
}

// clipboardImageFromFiles mirrors Rust's file-list preference: when the
// clipboard carries files (a file manager copy), attach the first image.
func clipboardImageFromFiles(ctx context.Context, tool clipboardImageTool) (string, bool) {
	output, err := runClipboardHelper(ctx, tool.name, tool.files)
	if err != nil || len(bytes.TrimSpace(output)) == 0 {
		return "", false
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path := line
		if parsed, parseErr := url.Parse(line); parseErr == nil && parsed.Scheme == "file" {
			path = parsed.Path
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			continue
		}
		img, _, decodeErr := image.Decode(file)
		file.Close()
		if decodeErr != nil {
			continue
		}
		var encoded bytes.Buffer
		if encodeErr := png.Encode(&encoded, img); encodeErr != nil {
			continue
		}
		if pasted, writeErr := writeClipboardPNG(encoded.Bytes()); writeErr == nil {
			return pasted, true
		}
	}
	return "", false
}

// writeClipboardImagePNG turns one clipboard payload into a PNG temp file.
func writeClipboardImagePNG(data []byte, mime string) (string, error) {
	if strings.EqualFold(mime, "image/png") && bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		if _, err := png.DecodeConfig(bytes.NewReader(data)); err == nil {
			return writeClipboardPNG(data)
		}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", codextui.PasteImageError{
			Kind:    codextui.PasteImageEncodeFailed,
			Message: fmt.Sprintf("could not decode %s: %v", mime, err),
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return "", codextui.PasteImageError{
			Kind:    codextui.PasteImageEncodeFailed,
			Message: err.Error(),
		}
	}
	return writeClipboardPNG(encoded.Bytes())
}

func writeClipboardPNG(data []byte) (string, error) {
	dir, err := os.MkdirTemp("", "codex-clipboard-")
	if err != nil {
		return "", codextui.PasteImageError{Kind: codextui.PasteImageIOError, Message: err.Error()}
	}
	path := filepath.Join(dir, "clipboard.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", codextui.PasteImageError{Kind: codextui.PasteImageIOError, Message: err.Error()}
	}
	return path, nil
}

// runClipboardHelper runs a selection helper with a bounded context and returns
// its stdout. Stderr is folded into the error so "target not available" stays
// visible in the footer.
func runClipboardHelper(ctx context.Context, name string, args []string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	// Selection helpers are usually single processes, but a wedged owner can
	// leave a grandchild holding stdout open after the context kills the
	// helper. WaitDelay keeps the composer responsive in that case.
	command.WaitDelay = time.Second
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, errors.New(detail)
	}
	return output, nil
}
