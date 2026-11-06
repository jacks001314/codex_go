package tea

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"strings"
	"unicode"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer.rs
// `handle_paste_image_path` (#57774). A pasted single path that names a regular
// image file is attached instead of inserted as text.
//
// Non-regular files are skipped: opening a FIFO, socket or device while probing
// its dimensions can block the input loop.
func (m *Model) handlePasteImagePath(pasted string) bool {
	if m == nil {
		return false
	}
	path, ok := normalizePastedPath(pasted)
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if _, _, err := image.DecodeConfig(file); err != nil {
		return false
	}
	m.attachComposerImagePath(path)
	return true
}

// attachComposerImagePath inserts a fresh `[Image #N]` element at the caret and
// records the local attachment, mirroring Rust's ChatComposer::attach_image.
func (m *Model) attachComposerImagePath(path string) {
	if m == nil || strings.TrimSpace(path) == "" {
		return
	}
	placeholder := m.nextLocalImageLabel()
	insertAt := m.composerCursorByteOffset()
	m.composer.InsertString(placeholder)
	value := m.composer.Value()
	if insertAt < 0 || insertAt+len(placeholder) > len(value) || value[insertAt:insertAt+len(placeholder)] != placeholder {
		insertAt = len(value) - len(placeholder)
	}
	m.attachments = append(m.attachments, bottompane.ComposerAttachment{
		Kind: bottompane.AttachmentImage,
		Path: path,
	})
	m.registerComposerElement(value, insertAt, insertAt+len(placeholder))
}

// normalizePastedPath mirrors Rust's clipboard_paste::normalize_pasted_path: a
// trimmed, optionally quoted single token, a file:// URL, or a Windows/UNC path.
func normalizePastedPath(pasted string) (string, bool) {
	trimmed := strings.TrimSpace(pasted)
	if trimmed == "" {
		return "", false
	}
	unquoted := trimmed
	if len(unquoted) >= 2 {
		first, last := unquoted[0], unquoted[len(unquoted)-1]
		if (first == '"' && last == '"') || (first == 0x27 && last == 0x27) {
			unquoted = unquoted[1 : len(unquoted)-1]
		}
	}
	if parsed, err := url.Parse(unquoted); err == nil && parsed.Scheme == "file" {
		path := parsed.Path
		switch {
		case parsed.Host == "" || strings.EqualFold(parsed.Host, "localhost"):
		case len(parsed.Host) == 2 && parsed.Host[1] == ':':
			// file://C:/dir/file.png keeps the Windows drive in the host slot.
			path = parsed.Host + parsed.Path
		default:
			path = "//" + parsed.Host + parsed.Path
		}
		if strings.HasPrefix(path, "/") && len(path) > 2 && path[2] == ':' {
			path = path[1:]
		}
		if path == "" {
			return "", false
		}
		return path, true
	}
	if isWindowsPathToken(unquoted) {
		return unquoted, true
	}
	parts := splitShellTokens(trimmed)
	if len(parts) != 1 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

// isWindowsPathToken reports a drive-letter or UNC path without shell parsing,
// matching Rust's normalize_windows_path bypass.
func isWindowsPathToken(token string) bool {
	if strings.HasPrefix(token, `\\`) {
		return true
	}
	if len(token) >= 2 && unicode.IsLetter(rune(token[0])) && token[1] == ':' {
		rest := token[2:]
		return rest == "" || strings.HasPrefix(rest, `\`) || strings.HasPrefix(rest, "/")
	}
	return false
}

// splitShellTokens performs the subset of shlex splitting normalize_pasted_path
// needs: single/double quotes and backslash escapes, whitespace separated.
func splitShellTokens(input string) []string {
	var tokens []string
	var current strings.Builder
	inSingle, inDouble, escaped, started := false, false, false, false
	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped, started = false, true
		case r == '\\' && !inSingle:
			escaped, started = true, true
		case r == '\'' && !inDouble:
			inSingle, started = !inSingle, true
		case r == '"' && !inSingle:
			inDouble, started = !inDouble, true
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			if started {
				tokens = append(tokens, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if started && !inSingle && !inDouble {
		tokens = append(tokens, current.String())
	}
	return tokens
}
