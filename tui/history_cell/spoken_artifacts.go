package historycell

// Workspace-confined OSC 8 file links for spoken assistant transcript cells.
// Rust parity: codex-rs/tui/src/history_cell/spoken_artifacts.rs and the
// TrustedWorkspaceFile capability in terminal_hyperlinks.rs (#43676).
//
// A spoken caption is free-form speech recognition output, so a file reference
// is only turned into a link after proving it names an existing, inert source
// file that stays inside the session workspace. Anything else is left as plain
// text.

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"codex_go/tui"
)

// maxSpokenArtifactCandidates bounds how many file-looking tokens one caption
// may validate, matching the Rust annotation budget.
const maxSpokenArtifactCandidates = 16

// safeWorkspaceExtensions mirrors Rust SAFE_WORKSPACE_EXTENSIONS: only source
// and documentation files are inert enough to link.
var safeWorkspaceExtensions = map[string]bool{
	"rs": true, "go": true, "c": true, "cc": true, "cpp": true, "h": true,
	"hpp": true, "java": true, "kt": true, "swift": true, "toml": true,
	"json": true, "yaml": true, "yml": true, "md": true, "txt": true,
	"css": true,
}

// TrustedWorkspaceFile is an existing, inert source file proven to remain
// inside its session workspace. Rust's capability type of the same name.
type TrustedWorkspaceFile struct {
	URL string
}

// ValidateTrustedWorkspaceFile applies the Rust validation rules for spoken
// artifacts: relative, non-hidden, allowed extension, existing regular file,
// and no symlink escape out of the canonical workspace.
func ValidateTrustedWorkspaceFile(workspace string, candidate string) (TrustedWorkspaceFile, bool) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || candidate == "" || len(candidate) > 512 || strings.Contains(candidate, ":") {
		return TrustedWorkspaceFile{}, false
	}
	for _, r := range candidate {
		if unicode.IsControl(r) {
			return TrustedWorkspaceFile{}, false
		}
	}
	normalized := strings.ReplaceAll(candidate, "\\", "/")
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(normalized) {
		return TrustedWorkspaceFile{}, false
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return TrustedWorkspaceFile{}, false
		}
	}
	extension := path.Ext(normalized)
	if extension == "" || !safeWorkspaceExtensions[strings.TrimPrefix(extension, ".")] {
		return TrustedWorkspaceFile{}, false
	}
	workspaceCanonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return TrustedWorkspaceFile{}, false
	}
	workspaceCanonical, err = filepath.Abs(workspaceCanonical)
	if err != nil {
		return TrustedWorkspaceFile{}, false
	}
	target, err := filepath.EvalSymlinks(filepath.Join(workspaceCanonical, filepath.FromSlash(normalized)))
	if err != nil {
		return TrustedWorkspaceFile{}, false
	}
	relative, err := filepath.Rel(workspaceCanonical, target)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return TrustedWorkspaceFile{}, false
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return TrustedWorkspaceFile{}, false
	}
	// A symlink must not disguise another file type (Rust compares the
	// canonical extension with the requested one).
	if !strings.EqualFold(path.Ext(filepath.ToSlash(target)), extension) {
		return TrustedWorkspaceFile{}, false
	}
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if strings.HasPrefix(part, ".") {
			return TrustedWorkspaceFile{}, false
		}
	}
	return TrustedWorkspaceFile{URL: workspaceFileURL(target)}, true
}

// workspaceFileURL renders an absolute path as a file:// URL with forward
// slashes, matching Rust Url::from_file_path (file:///C:/repo/src/lib.go on
// Windows).
func workspaceFileURL(target string) string {
	slashed := filepath.ToSlash(target)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

// AnnotateSpokenArtifacts underlines and links every workspace file reference
// in already-rendered spoken caption lines. Lines that already carry OSC 8
// hyperlinks are preserved untouched.
func AnnotateSpokenArtifacts(lines []string, workspace string) []string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	inspected := 0
	for index, line := range lines {
		out[index] = line
		if inspected >= maxSpokenArtifactCandidates {
			break
		}
		if strings.Contains(line, "\x1b]8;;") || strings.TrimSpace(line) == "" {
			continue
		}
		annotated, count := annotateSpokenArtifactsInLine(line, workspace, maxSpokenArtifactCandidates-inspected)
		inspected += count
		out[index] = annotated
	}
	return out
}

// spokenArtifactEdit is one byte range that becomes an underlined link.
type spokenArtifactEdit struct {
	start int
	end   int
	url   string
}

// annotateSpokenArtifactsInLine links the file tokens of one caption line and
// reports how many candidates it inspected.
func annotateSpokenArtifactsInLine(line string, workspace string, budget int) (string, int) {
	edits := make([]spokenArtifactEdit, 0, 2)
	inspected := 0
	for index := 0; index < len(line); {
		for index < len(line) && isASCIIWhitespace(line[index]) {
			index++
		}
		start := index
		for index < len(line) && !isASCIIWhitespace(line[index]) {
			index++
		}
		if start == index {
			break
		}
		token := line[start:index]
		leading := strings.TrimLeft(token, "([{<'\"`")
		candidate := strings.TrimRight(leading, ")]}>'\"`,;.!")
		if candidate == "" {
			continue
		}
		filename := candidate[strings.LastIndex(candidate, "/")+1:]
		if !strings.Contains(filename, ".") {
			continue
		}
		// Drop up to two `:line` / `:line:column` suffixes.
		path := candidate
		for suffix := 0; suffix < 2; suffix++ {
			colon := strings.LastIndex(path, ":")
			if colon < 0 || colon+1 >= len(path) || !allDigits(path[colon+1:]) {
				break
			}
			path = path[:colon]
		}
		inspected++
		if inspected > budget {
			return line, inspected
		}
		file, ok := ValidateTrustedWorkspaceFile(workspace, path)
		if !ok {
			continue
		}
		byteStart := start + len(token) - len(leading)
		edits = append(edits, spokenArtifactEdit{start: byteStart, end: byteStart + len(candidate), url: file.URL})
	}
	if len(edits) == 0 {
		return line, inspected
	}
	var builder strings.Builder
	cursor := 0
	for _, edit := range edits {
		if edit.start < cursor || edit.end > len(line) {
			continue
		}
		builder.WriteString(line[cursor:edit.start])
		// Rust adds Modifier::UNDERLINED around the existing span style; the
		// OSC 8 destination stays outside the visible text.
		builder.WriteString("\x1b[4m")
		builder.WriteString(tui.OSC8Hyperlink(edit.url, line[edit.start:edit.end]))
		builder.WriteString("\x1b[24m")
		cursor = edit.end
	}
	builder.WriteString(line[cursor:])
	return builder.String(), inspected
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func allDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
