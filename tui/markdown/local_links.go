package markdown

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// localFileLink records a local file link rewritten during markdown
// preprocessing so the rendered display path can be wrapped in an OSC-8
// hyperlink pointing at the original file target (Rust trusted_file_destination).
type localFileLink struct {
	Display string
	Dest    string
}

// Rust parity: codex-rs/tui/src/markdown_render.rs local file-link display.
//
// Codex file references are markdown links whose destination is a local path
// rather than a web URL. The Rust TUI renders those links using the resolved
// destination path (optionally shortened against the session working directory)
// instead of the caller-provided label, so the transcript shows the real file
// target. These helpers parse/normalize such destinations and choose the text
// to display.

var (
	hashLocationSuffixRE  = regexp.MustCompile(`^L\d+(?:C\d+)?(?:-L\d+(?:C\d+)?)?$`)
	colonLocationSuffixRE = regexp.MustCompile(`:\d+(?::\d+)?(?:\x{2013}\d+(?::\d+)?)?$`)
)

// isLocalPathLikeLink reports whether a markdown link destination is a local
// file path (file URL, absolute/relative path, home-relative path, UNC path, or
// Windows drive path) rather than a web URL.
func isLocalPathLikeLink(dest string) bool {
	if strings.HasPrefix(dest, "file://") {
		return true
	}
	if strings.HasPrefix(dest, "/") {
		return true
	}
	if strings.HasPrefix(dest, "~/") {
		return true
	}
	if strings.HasPrefix(dest, "./") {
		return true
	}
	if strings.HasPrefix(dest, "../") {
		return true
	}
	if strings.HasPrefix(dest, `\\`) {
		return true
	}
	return isWindowsDrivePath(dest)
}

func isWindowsDrivePath(path string) bool {
	return len(path) >= 3 && isASCIIAlpha(path[0]) && path[1] == ':' && (path[2] == '/' || path[2] == '\\')
}

func isASCIIAlpha(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// parseLocalTarget splits a local link destination into normalized path text
// and an optional location suffix (for example ":10" or ":10:3-14:8").
func parseLocalTarget(dest string) (string, string, bool) {
	if strings.HasPrefix(dest, "file://") {
		parsed, err := url.Parse(dest)
		if err != nil {
			return "", "", false
		}
		pathText, ok := fileURLToLocalPathText(parsed)
		if !ok {
			return "", "", false
		}
		locationSuffix := ""
		if parsed.Fragment != "" {
			locationSuffix, _ = normalizeHashLocationSuffix("#" + parsed.Fragment)
		}
		return pathText, locationSuffix, true
	}

	pathText := dest
	locationSuffix := ""
	// Prefer "#L.." style fragments so a dest like "path#L10" is not misparsed
	// as a plain path ending in ":10".
	if fragIdx := strings.LastIndex(dest, "#"); fragIdx >= 0 {
		if normalized, ok := normalizeHashLocationSuffix(dest[fragIdx:]); ok {
			pathText = dest[:fragIdx]
			locationSuffix = normalized
		}
	}
	if locationSuffix == "" {
		if suffix, ok := extractColonLocationSuffix(pathText); ok {
			pathText = pathText[:len(pathText)-len(suffix)]
			locationSuffix = suffix
		}
	}
	if decoded, err := url.PathUnescape(pathText); err == nil {
		pathText = decoded
	}
	return expandLocalLinkPath(pathText), locationSuffix, true
}

// normalizeHashLocationSuffix converts a "#L10C3-L14C8" style fragment into the
// display suffix ":10:3-14:8". Returns false for non-location fragments so other
// "#..." fragments stay part of the path text.
func normalizeHashLocationSuffix(suffix string) (string, bool) {
	fragment := strings.TrimPrefix(suffix, "#")
	if !hashLocationSuffixRE.MatchString(fragment) {
		return "", false
	}
	start := fragment
	end := ""
	if idx := strings.IndexByte(fragment, '-'); idx >= 0 {
		start = fragment[:idx]
		end = fragment[idx+1:]
	}
	startLine, startCol, ok := parseMarkdownHashLocationPoint(start)
	if !ok {
		return "", false
	}
	var sb strings.Builder
	sb.WriteByte(':')
	sb.WriteString(startLine)
	if startCol != "" {
		sb.WriteByte(':')
		sb.WriteString(startCol)
	}
	if end != "" {
		endLine, endCol, ok := parseMarkdownHashLocationPoint(end)
		if !ok {
			return "", false
		}
		sb.WriteByte('-')
		sb.WriteString(endLine)
		if endCol != "" {
			sb.WriteByte(':')
			sb.WriteString(endCol)
		}
	}
	return sb.String(), true
}

func parseMarkdownHashLocationPoint(point string) (string, string, bool) {
	if !strings.HasPrefix(point, "L") {
		return "", "", false
	}
	rest := point[1:]
	if idx := strings.IndexByte(rest, 'C'); idx >= 0 {
		return rest[:idx], rest[idx+1:], true
	}
	return rest, "", true
}

// extractColonLocationSuffix returns a trailing ":line", ":line:col", or range
// suffix (using an en dash between range points) when it ends the path.
func extractColonLocationSuffix(pathText string) (string, bool) {
	loc := colonLocationSuffixRE.FindStringIndex(pathText)
	if loc == nil || loc[1] != len(pathText) {
		return "", false
	}
	return pathText[loc[0]:loc[1]], true
}

// expandLocalLinkPath expands "~/..." using the user home directory when
// available, and normalizes separators for display.
func expandLocalLinkPath(pathText string) string {
	if rest, ok := strings.CutPrefix(pathText, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			joined := filepath.ToSlash(filepath.Join(home, filepath.FromSlash(rest)))
			return normalizeLocalLinkPathText(joined)
		}
	}
	return normalizeLocalLinkPathText(pathText)
}

// fileURLToLocalPathText converts a file:// URL into the normalized local-path
// text used for rendering, reconstructing UNC and Windows drive forms that the
// standard library does not map directly.
func fileURLToLocalPathText(u *url.URL) (string, bool) {
	pathText := u.Path
	if host := u.Host; host != "" && host != "localhost" {
		pathText = "//" + host + pathText
	} else if isWindowsDrivePath(strings.TrimPrefix(pathText, "/")) {
		pathText = strings.TrimPrefix(pathText, "/")
	}
	return normalizeLocalLinkPathText(pathText), true
}

// normalizeLocalLinkPathText renders local link paths with forward slashes so
// display and prefix stripping are stable across mixed Windows/Unix inputs.
func normalizeLocalLinkPathText(pathText string) string {
	if rest, ok := strings.CutPrefix(pathText, `\\`); ok {
		return "//" + strings.TrimLeft(strings.ReplaceAll(rest, `\`, "/"), "/")
	}
	return strings.ReplaceAll(pathText, `\`, "/")
}

func isAbsoluteLocalLinkPath(pathText string) bool {
	return strings.HasPrefix(pathText, "/") || strings.HasPrefix(pathText, "//") || isWindowsDrivePath(pathText)
}

// trimTrailingLocalPathSeparator removes trailing separators without destroying
// root semantics ("/", "//", and "C:/" stay intact).
func trimTrailingLocalPathSeparator(pathText string) string {
	if pathText == "/" || pathText == "//" {
		return pathText
	}
	if isWindowsDrivePath(pathText) {
		return pathText
	}
	return strings.TrimRight(pathText, "/")
}

// stripLocalPathPrefix returns the remainder of pathText after removing the
// cwd prefix when pathText is strictly underneath it.
func stripLocalPathPrefix(pathText string, cwdText string) (string, bool) {
	pathText = trimTrailingLocalPathSeparator(pathText)
	cwdText = trimTrailingLocalPathSeparator(cwdText)
	if pathText == cwdText {
		return "", false
	}
	if cwdText == "/" || cwdText == "//" {
		if rest, ok := strings.CutPrefix(pathText, "/"); ok {
			return rest, true
		}
		return "", false
	}
	if rest, ok := strings.CutPrefix(pathText, cwdText); ok {
		rest = strings.TrimPrefix(rest, "/")
		return rest, true
	}
	return "", false
}

// displayLocalLinkPath chooses the visible path text for a local link. Relative
// paths stay relative; absolute paths are shortened against cwd only when they
// are lexically underneath it; otherwise the absolute path is preserved.
func displayLocalLinkPath(pathText string, cwd string) string {
	pathText = normalizeLocalLinkPathText(pathText)
	if !isAbsoluteLocalLinkPath(pathText) {
		return pathText
	}
	if cwd != "" {
		if stripped, ok := stripLocalPathPrefix(pathText, normalizeLocalLinkPathText(cwd)); ok {
			return stripped
		}
	}
	return pathText
}

// renderLocalLinkTarget returns the text to display for a local link destination.
func renderLocalLinkTarget(dest string, cwd string) (string, bool) {
	pathText, locationSuffix, ok := parseLocalTarget(dest)
	if !ok {
		return "", false
	}
	rendered := displayLocalLinkPath(pathText, cwd)
	rendered += locationSuffix
	return rendered, true
}

// localLinkFileURL converts a local link destination into an absolute file://
// URL for OSC-8 hyperlinks. Relative targets are joined against cwd when
// available; otherwise no URL is produced so the text stays un-clickable.
func localLinkFileURL(dest string, cwd string) string {
	pathText, _, ok := parseLocalTarget(dest)
	if !ok {
		return ""
	}
	if !isAbsoluteLocalLinkPath(pathText) {
		if cwd == "" {
			return ""
		}
		pathText = normalizeLocalLinkPathText(filepath.ToSlash(filepath.Join(filepath.FromSlash(cwd), filepath.FromSlash(pathText))))
	}
	return fileURLForPath(pathText)
}

func fileURLForPath(pathText string) string {
	if isWindowsDrivePath(pathText) {
		return "file:///" + pathText
	}
	if strings.HasPrefix(pathText, "//") {
		return "file:" + pathText
	}
	return "file://" + pathText
}
