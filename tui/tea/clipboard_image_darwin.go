//go:build darwin

package tea

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/tiff"

	"codex_go/envutil"
	codextui "codex_go/tui"
	"codex_go/tui/imagedecode"
)

// Rust parity: codex-rs/tui/src/clipboard_paste/{macos.rs,paste}.rs (#60455).
// Rust reads the native pasteboard representations first, preferring PNG
// within each item and honoring the owner's JPEG/TIFF order, and only then
// falls back to arboard's TIFF reader. Go reaches the same pasteboard through
// JavaScript for Automation, applies the shared selection rule, decodes in
// display orientation, and hands the widget a PNG temp file.

// clipboardImageTimeout bounds every pasteboard helper so a wedged pasteboard
// owner cannot freeze the composer.
const clipboardImageTimeout = 3 * time.Second

// macosPasteboardScript lists pasteboard items in order with the representation
// types the owner advertises, the bytes Core may decode, and copied file URLs.
const macosPasteboardScript = `
ObjC.import('AppKit');
function run() {
  var pasteboard = $.NSPasteboard.generalPasteboard;
  var items = pasteboard.pasteboardItems;
  var result = [];
  if (!items) { return '[]'; }
  for (var index = 0; index < items.count; index++) {
    var item = items.objectAtIndex(index);
    var types = item.types;
    var names = [];
    var data = {};
    var files = [];
    for (var typeIndex = 0; typeIndex < types.count; typeIndex++) {
      var type = ObjC.unwrap(types.objectAtIndex(typeIndex));
      names.push(type);
      if (type === 'public.png' || type === 'public.jpeg') {
        var payload = item.dataForType(type);
        if (payload) {
          data[type] = ObjC.unwrap(payload.base64EncodedStringWithOptions(0));
        }
      }
      if (type === 'public.file-url') {
        var value = ObjC.unwrap(item.stringForType('public.file-url'));
        if (value) { files.push(value); }
      }
    }
    result.push({types: names, data: data, fileURLs: files});
  }
  return JSON.stringify(result);
}
`

// macosPasteboardItem is one pasteboard item as the helper reports it.
type macosPasteboardItem struct {
	Types    []string          `json:"types"`
	Data     map[string]string `json:"data"`
	FileURLs []string          `json:"fileURLs"`
}

// pasteImageFromClipboardPlatform captures the clipboard image on macOS.
func pasteImageFromClipboardPlatform() (string, error) {
	items, listErr := macosPasteboardItems()
	if listErr == nil {
		// A copied file takes precedence over its clipboard preview.
		if path, ok := pasteImageFromCopiedFiles(items); ok {
			return path, nil
		}
		if img, found, _ := firstPasteboardImage(macosPasteboardImageItems(items), decodeClipboardImage); found {
			return writeClipboardPNGImage(img)
		}
	}
	// Rust falls back to arboard's TIFF reader whenever no PNG/JPEG
	// representation produced an image.
	if img, ok := macosPasteboardTIFFImage(); ok {
		return writeClipboardPNGImage(img)
	}
	if listErr != nil {
		return "", codextui.PasteImageError{Kind: codextui.PasteImageClipboardUnavailable, Message: listErr.Error()}
	}
	return "", codextui.PasteImageError{Kind: codextui.PasteImageNoImage, Message: "no image on the macOS pasteboard"}
}

func macosPasteboardItems() ([]macosPasteboardItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardImageTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", macosPasteboardScript)
	envutil.SuppressConsoleWindow(command)
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
	var items []macosPasteboardItem
	if err := json.Unmarshal(bytes.TrimSpace(output), &items); err != nil {
		return nil, fmt.Errorf("decode pasteboard description: %w", err)
	}
	return items, nil
}

func macosPasteboardImageItems(items []macosPasteboardItem) []pasteboardImageItem {
	out := make([]pasteboardImageItem, 0, len(items))
	for _, item := range items {
		data := make(map[string][]byte, len(item.Data))
		for typ, payload := range item.Data {
			decoded, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				continue
			}
			data[typ] = decoded
		}
		out = append(out, pasteboardImageItem{Types: item.Types, Data: data})
	}
	return out
}

func decodeClipboardImage(data []byte) (image.Image, error) {
	return imagedecode.DecodeBytes(data, 0)
}

func pasteImageFromCopiedFiles(items []macosPasteboardItem) (string, bool) {
	for _, item := range items {
		for _, value := range item.FileURLs {
			path, ok := pasteboardFilePath(value)
			if !ok {
				continue
			}
			info, statErr := os.Stat(path)
			if statErr != nil || !info.Mode().IsRegular() {
				continue
			}
			img, decodeErr := imagedecode.DecodeFile(path, 0)
			if decodeErr != nil {
				continue
			}
			if path, writeErr := writeClipboardPNGImage(img); writeErr == nil {
				return path, true
			}
		}
	}
	return "", false
}

func pasteboardFilePath(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil || path == "" {
		return "", false
	}
	return path, true
}

// macosPasteboardTIFFImage reads the representation arboard would have used.
func macosPasteboardTIFFImage() (image.Image, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardImageTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "pbpaste", "-Prefer", "tiff")
	envutil.SuppressConsoleWindow(command)
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil || len(bytes.TrimSpace(output)) == 0 {
		return nil, false
	}
	img, err := tiff.Decode(bytes.NewReader(output))
	if err != nil {
		return nil, false
	}
	return img, true
}

func writeClipboardPNGImage(img image.Image) (string, error) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return "", codextui.PasteImageError{Kind: codextui.PasteImageEncodeFailed, Message: err.Error()}
	}
	dir, err := os.MkdirTemp("", "codex-paste-")
	if err != nil {
		return "", codextui.PasteImageError{Kind: codextui.PasteImageIOError, Message: err.Error()}
	}
	path := filepath.Join(dir, "clipboard.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", codextui.PasteImageError{Kind: codextui.PasteImageIOError, Message: err.Error()}
	}
	return path, nil
}
