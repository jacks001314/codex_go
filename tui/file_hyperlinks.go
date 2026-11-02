package tui

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Rust parity: codex-rs/tui/src/terminal_hyperlinks.rs
// `TerminalHyperlink::viewed_image` (#58446). A viewed image can live outside
// the workspace, so the link is created only for a local regular file whose
// resolved extension still opens as an image: a symlink named screenshot.png may
// target a script.

// imageFileExtensions mirrors `image::ImageFormat::from_path` for the formats
// the renderer accepts.
var imageFileExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".ico":  true,
	".tif":  true,
	".tiff": true,
	".avif": true,
	".qoi":  true,
	".tga":  true,
	".dds":  true,
	".hdr":  true,
	".exr":  true,
	".pnm":  true,
	".pbm":  true,
	".pgm":  true,
	".ppm":  true,
	".ff":   true,
}

// IsImageFilePath reports whether the path carries a recognized image
// extension.
func IsImageFilePath(path string) bool {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	return extension != "" && imageFileExtensions[extension]
}

// ViewedImageDestination validates a locally viewed image and returns the OSC-8
// file destination for it (Rust `viewed_image`). The original and the resolved
// paths must both name an existing regular image file, and a Windows path may
// not traverse a reparse point: a junction can lead to a UNC share.
func ViewedImageDestination(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || !IsImageFilePath(path) {
		return "", false
	}
	absolute, err := filepath.Abs(path)
	if err != nil || !IsImageFilePath(absolute) {
		return "", false
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if pathTraversesReparsePoint(absolute) {
		return "", false
	}
	target, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	target, err = filepath.Abs(target)
	if err != nil || !IsImageFilePath(target) {
		return "", false
	}
	if pathTraversesReparsePoint(target) {
		return "", false
	}
	resolved, err := os.Stat(target)
	if err != nil || !resolved.Mode().IsRegular() {
		return "", false
	}
	return fileURLForPath(target)
}

// fileURLForPath renders an absolute local path as a file URL. A UNC path would
// put a host in the URL, which Rust rejects for viewed images.
func fileURLForPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	slashed := filepath.ToSlash(path)
	if strings.HasPrefix(slashed, "//") {
		return "", false
	}
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String(), true
}

// OSC8FileHyperlink wraps text in an OSC-8 hyperlink whose destination is a
// validated local file URL. Web destinations go through OSC8Hyperlink.
func OSC8FileHyperlink(destination string, text string) string {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return text
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Scheme != "file" || parsed.Host != "" {
		return text
	}
	if strings.ContainsAny(destination, "\x07\x1b") {
		return text
	}
	return "\x1b]8;;" + destination + "\x07" + text + "\x1b]8;;\x07"
}

// pathAncestorsRootFirst lists the path and every ancestor, outermost first.
func pathAncestorsRootFirst(path string) []string {
	ancestors := []string{}
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		ancestors = append(ancestors, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	for left, right := 0, len(ancestors)-1; left < right; left, right = left+1, right-1 {
		ancestors[left], ancestors[right] = ancestors[right], ancestors[left]
	}
	return ancestors
}
