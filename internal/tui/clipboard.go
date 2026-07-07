package tui

import (
	"encoding/base64"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// Rust parity: codex-rs/tui/src/clipboard_copy.rs and clipboard_paste.rs.

const OSC52MaxRawBytes = 100_000

type ClipboardBackend string

const (
	ClipboardNative ClipboardBackend = "native"
	ClipboardWSL    ClipboardBackend = "wsl-powershell"
	ClipboardTmux   ClipboardBackend = "tmux"
	ClipboardOSC52  ClipboardBackend = "osc52"
)

type ClipboardEnvironment struct {
	SSHSession  bool
	WSLSession  bool
	TmuxSession bool
}

func ClipboardCopyOrder(environment ClipboardEnvironment) []ClipboardBackend {
	if environment.SSHSession {
		if environment.TmuxSession {
			return []ClipboardBackend{ClipboardTmux, ClipboardOSC52}
		}
		return []ClipboardBackend{ClipboardOSC52}
	}
	order := []ClipboardBackend{ClipboardNative}
	if environment.WSLSession {
		order = append(order, ClipboardWSL)
	}
	if environment.TmuxSession {
		return append(order, ClipboardTmux, ClipboardOSC52)
	}
	return append(order, ClipboardOSC52)
}

func OSC52Sequence(text string, tmux bool) (string, error) {
	rawBytes := len(text)
	if rawBytes > OSC52MaxRawBytes {
		return "", ClipboardError("OSC 52 payload too large")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if tmux {
		return "\x1bPtmux;\x1b\x1b]52;c;" + encoded + "\x07\x1b\\", nil
	}
	return "\x1b]52;c;" + encoded + "\x07", nil
}

type ClipboardError string

func (e ClipboardError) Error() string {
	return string(e)
}

type EncodedImageFormat int

const (
	EncodedImagePNG EncodedImageFormat = iota
	EncodedImageJPEG
	EncodedImageOther
)

func (f EncodedImageFormat) Label() string {
	switch f {
	case EncodedImagePNG:
		return "PNG"
	case EncodedImageJPEG:
		return "JPEG"
	default:
		return "IMG"
	}
}

func NormalizePastedSearchQuery(pasted string) (string, bool) {
	normalized := strings.Join(strings.Fields(pasted), " ")
	return normalized, normalized != ""
}

func NormalizePastedPath(pasted string) (string, bool) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return "", false
	}
	unquoted := stripSimpleQuotes(pasted)
	if parsed, err := url.Parse(unquoted); err == nil && parsed.Scheme == "file" {
		if path, err := url.PathUnescape(parsed.Path); err == nil && path != "" {
			if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") && len(path) >= 3 && path[2] == ':' {
				path = path[1:]
			}
			return filepath.Clean(path), true
		}
	}
	if isWindowsPath(unquoted) {
		return filepath.Clean(unquoted), true
	}
	parts := shellSplitOne(pasted)
	if len(parts) != 1 {
		return "", false
	}
	part := parts[0]
	if isWindowsPath(part) {
		return filepath.Clean(part), true
	}
	return filepath.Clean(part), true
}

func PastedImageFormat(path string) EncodedImageFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return EncodedImagePNG
	case ".jpg", ".jpeg":
		return EncodedImageJPEG
	default:
		return EncodedImageOther
	}
}

func stripSimpleQuotes(text string) string {
	if len(text) >= 2 {
		if (text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'') {
			return text[1 : len(text)-1]
		}
	}
	return text
}

func isWindowsPath(text string) bool {
	if strings.HasPrefix(text, `\\`) {
		return true
	}
	return len(text) >= 3 &&
		((text[0] >= 'a' && text[0] <= 'z') || (text[0] >= 'A' && text[0] <= 'Z')) &&
		text[1] == ':' &&
		(text[2] == '\\' || text[2] == '/')
}

func shellSplitOne(text string) []string {
	var parts []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range text {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if quote == 0 && r == '\\' {
			escaped = true
			continue
		}
		if r == '\'' || r == '"' {
			if quote == 0 {
				quote = r
				continue
			}
			if quote == r {
				quote = 0
				continue
			}
		}
		if quote == 0 && (r == ' ' || r == '\t' || r == '\n' || r == '\r') {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if escaped {
		current.WriteRune('\\')
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}
