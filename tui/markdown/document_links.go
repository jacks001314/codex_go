package markdown

import (
	"net/url"
	"strings"
	"unicode"
)

// Rust parity: codex-rs/tui/src/markdown_render/local_links.rs::sidebar_file_link
// (#61880). A transcript link whose destination names a local Markdown document
// is kept as document metadata so an enabled workspace can open it in the
// reader instead of handing the path to the terminal.
//
// The gate matches the Rust helper: the destination must name a `.md` or
// `.markdown` file with no location suffix, no fragment, no query, no host
// qualified file URL, and no home-relative or UNC path. The returned text is the
// path the reader addresses; a POSIX `file://` target keeps literal backslashes
// because they are filename characters on that convention.
func DocumentLinkTarget(destination string) (string, bool) {
	if destination == "" || len(destination) > 8192 || strings.ContainsAny(destination, "#?") {
		return "", false
	}
	// Home-relative targets need a frontend home directory the session may not
	// share, so they are never document targets (Rust rejects `~`).
	if strings.HasPrefix(destination, "~") {
		return "", false
	}
	if strings.HasPrefix(destination, "file://") {
		parsed, err := url.Parse(destination)
		if err != nil || parsed.Host != "" {
			return "", false
		}
	}
	pathText, locationSuffix, ok := parseLocalTarget(destination)
	if !ok || locationSuffix != "" {
		return "", false
	}
	if posixPath, ok := posixFileURIPathText(destination); ok {
		pathText = posixPath
	}
	dot := strings.LastIndex(pathText, ".")
	if dot < 0 {
		return "", false
	}
	extension := pathText[dot+1:]
	if !strings.EqualFold(extension, "md") && !strings.EqualFold(extension, "markdown") {
		return "", false
	}
	if len(pathText) > 4096 ||
		strings.HasPrefix(pathText, "~") ||
		strings.HasPrefix(pathText, "//") ||
		strings.ContainsFunc(pathText, unicode.IsControl) {
		return "", false
	}
	if !isAbsoluteLocalLinkPath(pathText) && hasURLScheme(pathText) {
		return "", false
	}
	if !isLocalPathLikeLink(destination) && hasURLScheme(destination) {
		return "", false
	}
	return pathText, true
}

// posixFileURIPathText returns the literal POSIX path a file URI names. Rust
// infers the convention from the URI and only normalizes separators for native
// path conventions, so `file:///tmp/Guide%5Cnotes.md` addresses the file named
// `Guide\notes.md` (Rust #61880).
func posixFileURIPathText(destination string) (string, bool) {
	if !strings.HasPrefix(destination, "file://") {
		return "", false
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Host != "" {
		return "", false
	}
	raw, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil || raw == "" {
		return "", false
	}
	if isWindowsDrivePath(strings.TrimPrefix(raw, "/")) {
		return "", false
	}
	return raw, true
}

// hasURLScheme reports whether text parses as a URL with a scheme. Rust uses
// `Url::parse(...).is_err()`; Go's parser accepts relative text, so the scheme
// is the discriminator that reproduces the same verdict.
func hasURLScheme(text string) bool {
	parsed, err := url.Parse(text)
	return err == nil && parsed.Scheme != ""
}
