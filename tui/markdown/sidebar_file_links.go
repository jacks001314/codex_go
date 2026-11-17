package markdown

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
)

// Rust parity: codex-rs/tui/src/markdown_render/local_links.rs
// `sidebar_file_link` and markdown_render::file_links (#61920).
//
// The sidebar inventory lists files a transcript mentions. Only destinations
// that denote a local file on the app-server host qualify: web URLs, host-qualified
// file URLs, bare fragments and placeholder paths are dropped, while local paths
// keep their optional `:line[:col]` / `#L..C..` location suffix.

// SidebarSourceLocation is a one-based line/column target inside a file.
type SidebarSourceLocation struct {
	Line   int
	Column int
}

// SidebarFileLink is a local file destination a transcript mentions.
type SidebarFileLink struct {
	Path     string
	Location *SidebarSourceLocation
}

// SidebarFileLinks collects the local file links a Markdown document mentions,
// in document order. Both links and images count (Rust Tag::Link/Tag::Image).
func SidebarFileLinks(source string) []SidebarFileLink {
	document := gfmMarkdownParser.Parse(gmtext.NewReader([]byte(source)))
	var links []SidebarFileLink
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var destination []byte
		switch typed := node.(type) {
		case *ast.Link:
			destination = typed.Destination
		case *ast.Image:
			destination = typed.Destination
		default:
			return ast.WalkContinue, nil
		}
		if link, ok := SidebarFileLinkForDestination(string(destination)); ok {
			links = append(links, link)
		}
		return ast.WalkContinue, nil
	})
	return links
}

// SidebarFileLinkForDestination parses one link destination, returning false
// when the destination does not name a local file.
func SidebarFileLinkForDestination(destination string) (SidebarFileLink, bool) {
	if destination == "" || len(destination) > 8192 || strings.Contains(destination, "?") {
		return SidebarFileLink{}, false
	}
	// Home-relative destinations need a frontend home directory the app-server
	// host may not share, so the inventory keeps them out (Rust rejects `~`).
	if strings.HasPrefix(destination, "~") {
		return SidebarFileLink{}, false
	}
	if strings.HasPrefix(destination, "file://") {
		parsed, err := url.Parse(destination)
		if err != nil || parsed.Host != "" {
			return SidebarFileLink{}, false
		}
	}
	pathText, suffix, ok := parseLocalTarget(destination)
	if !ok {
		return SidebarFileLink{}, false
	}
	// A fragment that is not a location target identifies a section, not a file.
	if strings.Contains(destination, "#") && suffix == "" {
		return SidebarFileLink{}, false
	}
	var location *SidebarSourceLocation
	if suffix != "" {
		start := strings.TrimPrefix(suffix, ":")
		if index := strings.IndexAny(start, "-\u2013"); index >= 0 {
			start = start[:index]
		}
		lineText, columnText, hasColumn := strings.Cut(start, ":")
		line, err := strconv.Atoi(lineText)
		if err != nil {
			return SidebarFileLink{}, false
		}
		column := 1
		if hasColumn {
			column, err = strconv.Atoi(columnText)
			if err != nil {
				return SidebarFileLink{}, false
			}
		}
		location = &SidebarSourceLocation{Line: max(line, 1), Column: max(column, 1)}
	}
	if pathText == "" ||
		strings.HasSuffix(pathText, "/") ||
		len(pathText) > 4096 ||
		strings.HasPrefix(pathText, "~") ||
		strings.HasPrefix(pathText, "//") ||
		strings.ContainsFunc(pathText, unicode.IsControl) {
		return SidebarFileLink{}, false
	}
	if !isAbsoluteLocalLinkPath(pathText) {
		// Relative destinations must not parse as an absolute URL with a scheme.
		if parsed, err := url.Parse(pathText); err == nil && parsed.Scheme != "" {
			return SidebarFileLink{}, false
		}
	}
	return SidebarFileLink{Path: pathText, Location: location}, true
}
