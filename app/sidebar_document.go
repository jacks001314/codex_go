package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/rivo/uniseg"

	"codex_go/tui/markdown"
	"codex_go/tui/sidebar_image"
)

// Rust parity: codex-rs/tui/src/app/sidebar_document.rs `Document::loaded` and
// `source_target` (#61888).
//
// A sidebar document is either rendered Markdown or plain source with line
// numbers. Coordinate links always show source, and a failed read leaves the
// reason on the pane instead of retrying on its own.

const (
	sidebarDocumentMaxSourceBytes = 1024 * 1024
	sidebarDocumentMaxSourceLines = 10_000
)

type sidebarDocumentState int

const (
	sidebarDocumentLoading sidebarDocumentState = iota
	sidebarDocumentFailed
	sidebarDocumentReady
)

// sidebarDocumentTarget is the resolved source position of a link target.
type sidebarDocumentTarget struct {
	Line   int
	Column int
	Offset int
}

type sidebarDocument struct {
	source     string
	path       string
	link       int
	location   *markdown.SidebarSourceLocation
	state      sidebarDocumentState
	readErr    string
	code       bool
	language   string
	image      *sidebar_image.Image
	text       string
	lineStarts []int
	target     *sidebarDocumentTarget
}

// newSidebarDocument starts a read for one link. link is the index of the
// clicked link inside the document, or -1 when the path was opened directly.
func newSidebarDocument(source, path string, link int, location *markdown.SidebarSourceLocation) *sidebarDocument {
	return &sidebarDocument{
		source:   source,
		path:     strings.TrimSpace(path),
		link:     link,
		location: location,
		state:    sidebarDocumentLoading,
	}
}

// title is the focused tab label: the path's basename (Rust `Document::title`).
func (d *sidebarDocument) title() string {
	if d == nil {
		return ""
	}
	path := d.path
	if index := strings.LastIndex(path, "/"); index >= 0 {
		path = path[index+1:]
	}
	return path
}

// loaded applies a read result. A coordinate link or a non-Markdown file shows
// source; plain Markdown links render.
func (d *sidebarDocument) loaded(text string, err error) {
	if d == nil {
		return
	}
	if err != nil {
		d.state = sidebarDocumentFailed
		d.readErr = err.Error()
		return
	}
	title := d.title()
	code := d.location != nil || !sidebarIsMarkdownFile(title)
	if code && len(text) > sidebarDocumentMaxSourceBytes {
		d.state = sidebarDocumentFailed
		d.readErr = "Source preview limit is 1 MiB"
		return
	}
	var starts []int
	if code {
		offset := 0
		for _, line := range sidebarSourceLines(text) {
			starts = append(starts, offset)
			offset += len(line) + 1
		}
	}
	if len(starts) > sidebarDocumentMaxSourceLines {
		d.state = sidebarDocumentFailed
		d.readErr = fmt.Sprintf("Source preview limit is %d lines", sidebarDocumentMaxSourceLines)
		return
	}
	d.state = sidebarDocumentReady
	d.readErr = ""
	d.code = code
	d.text = text
	d.lineStarts = starts
	d.language = ""
	if code {
		d.language = sidebarDocumentLanguage(title)
	}
	d.target = sidebarDocumentSourceTarget(text, starts, d.location)
}

// connectionReplaced fails an in-flight read so the pane does not hang after a
// backend switch (Rust `Document::connection_replaced`).
func (d *sidebarDocument) connectionReplaced() {
	if d == nil || d.state != sidebarDocumentLoading {
		return
	}
	d.state = sidebarDocumentFailed
	d.readErr = "Connection changed. Reopen this file to try again."
}

// openSidebarDocument resolves and reads one preview for the source thread
// (Rust `open_sidebar_document` plus the `SidebarDocumentLoaded` handler).
// The document comes back in its final state; the caller keeps it only while
// the request still owns the pane.
func openSidebarDocument(ctx context.Context, client *remoteAppServerTUIClient, source, target string, link int, location *markdown.SidebarSourceLocation) *sidebarDocument {
	document := newSidebarDocument(source, target, link, location)
	resolved, err := resolveRemoteThreadFile(ctx, client, source, target)
	if err != nil {
		document.loaded("", err)
		return document
	}
	document.path = resolved
	data, err := readRemotePreviewFile(ctx, client, source, target, resolved)
	if err != nil {
		document.loaded("", err)
		return document
	}
	document.loadedBytes(data)
	return document
}

// sidebarIsMarkdownFile reports whether a basename is rendered as Markdown
// (Rust `is_markdown_file`).
func sidebarIsMarkdownFile(path string) bool {
	index := strings.LastIndex(path, ".")
	if index < 0 {
		return false
	}
	ext := path[index+1:]
	return strings.EqualFold(ext, "md") || strings.EqualFold(ext, "markdown")
}

// sidebarSourceLines splits like Rust's str::lines: one entry per LF and no
// trailing empty entry for a final newline.
func sidebarSourceLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if last := len(lines) - 1; last >= 0 && lines[last] == "" {
		lines = lines[:last]
	}
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	return lines
}

// sidebarDocumentLanguage is the fence language for a source preview: the
// extension, or the whole name when it has none.
func sidebarDocumentLanguage(title string) string {
	if index := strings.LastIndex(title, "."); index >= 0 {
		return title[index+1:]
	}
	return title
}

// sidebarDocumentSourceTarget maps a line/column link onto the preview text.
// Lines and columns clamp to the file, and the column snaps back to the start
// of its grapheme cluster (Rust `source_target`).
func sidebarDocumentSourceTarget(text string, starts []int, location *markdown.SidebarSourceLocation) *sidebarDocumentTarget {
	if location == nil || len(starts) == 0 {
		return nil
	}
	lines := sidebarSourceLines(text)
	if len(lines) == 0 {
		return nil
	}
	index := location.Line - 1
	if index > len(starts)-1 {
		index = len(starts) - 1
	}
	if index < 0 {
		index = 0
	}
	if index >= len(lines) {
		return nil
	}
	line := lines[index]
	column := sidebarRuneByteOffset(line, location.Column-1)
	if column != len(line) {
		column = sidebarGraphemeStart(line, column)
	}
	return &sidebarDocumentTarget{Line: index + 1, Column: column + 1, Offset: starts[index] + column}
}

// sidebarRuneByteOffset is the byte offset of the charIndex-th character, or
// the end of the line when the line is shorter.
func sidebarRuneByteOffset(line string, charIndex int) int {
	if charIndex <= 0 {
		return 0
	}
	index := 0
	for byteOffset := range line {
		if index == charIndex {
			return byteOffset
		}
		index++
	}
	return len(line)
}

// sidebarGraphemeStart snaps a byte offset back to the start of the grapheme
// cluster that contains it.
func sidebarGraphemeStart(line string, offset int) int {
	if offset <= 0 {
		return 0
	}
	state := -1
	position := 0
	start := 0
	rest := line
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if position > offset {
			return start
		}
		start = position
		position += len(cluster)
	}
	if position <= offset {
		return start
	}
	return start
}

// loadedBytes prefers supported image content, then falls back to text. A text
// file named .png remains readable; malformed binary preserves the decode error.
func (d *sidebarDocument) loadedBytes(data []byte) {
	if d.location == nil && sidebar_image.SupportsPath(d.path) {
		preview, err := sidebar_image.Decode(data)
		if err == nil {
			d.loaded("", nil)
			d.code = false
			d.lineStarts = nil
			d.target = nil
			d.image = preview
			return
		}
		text, textErr := documentTextFromBytes(data)
		if textErr != nil {
			d.loaded("", err)
			return
		}
		d.loaded(text, nil)
		return
	}
	text, err := documentTextFromBytes(data)
	d.loaded(text, err)
}
