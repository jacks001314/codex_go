package tui

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
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

type ClipboardLease struct{}

type NativeClipboardCopyFunc func(string) (*ClipboardLease, error)
type ClipboardCopyFunc func(string) error

func ClipboardEnvironmentFromMap(env map[string]string, wslSession bool) ClipboardEnvironment {
	return ClipboardEnvironment{
		SSHSession:  envValueSet(env, "SSH_TTY") || envValueSet(env, "SSH_CONNECTION"),
		WSLSession:  wslSession,
		TmuxSession: envValueSet(env, "TMUX") || envValueSet(env, "TMUX_PANE"),
	}
}

func CopyToClipboardWith(
	text string,
	environment ClipboardEnvironment,
	tmuxCopy ClipboardCopyFunc,
	osc52Copy ClipboardCopyFunc,
	nativeCopy NativeClipboardCopyFunc,
	wslCopy ClipboardCopyFunc,
) (*ClipboardLease, error) {
	if tmuxCopy == nil {
		tmuxCopy = func(string) error { return ClipboardError("tmux clipboard unavailable") }
	}
	if osc52Copy == nil {
		osc52Copy = func(string) error { return ClipboardError("OSC 52 clipboard unavailable") }
	}
	if nativeCopy == nil {
		nativeCopy = func(string) (*ClipboardLease, error) { return nil, ClipboardError("native clipboard unavailable") }
	}
	if wslCopy == nil {
		wslCopy = func(string) error { return ClipboardError("WSL clipboard fallback unavailable on this platform") }
	}

	if environment.SSHSession {
		err := terminalClipboardCopyWith(text, environment.TmuxSession, tmuxCopy, osc52Copy)
		if err == nil {
			return nil, nil
		}
		if environment.TmuxSession {
			return nil, ClipboardError("terminal clipboard copy failed over SSH: " + err.Error())
		}
		return nil, ClipboardError("OSC 52 clipboard copy failed over SSH: " + err.Error())
	}

	lease, nativeErr := nativeCopy(text)
	if nativeErr == nil {
		return lease, nil
	}
	if environment.WSLSession {
		wslErr := wslCopy(text)
		if wslErr == nil {
			return nil, nil
		}
		terminalErr := terminalClipboardCopyWith(text, environment.TmuxSession, tmuxCopy, osc52Copy)
		if terminalErr == nil {
			return nil, nil
		}
		if environment.TmuxSession {
			return nil, ClipboardError(fmt.Sprintf("native clipboard: %v; WSL fallback: %v; terminal fallback: %v", nativeErr, wslErr, terminalErr))
		}
		return nil, ClipboardError(fmt.Sprintf("native clipboard: %v; WSL fallback: %v; OSC 52 fallback: %v", nativeErr, wslErr, terminalErr))
	}

	terminalErr := terminalClipboardCopyWith(text, environment.TmuxSession, tmuxCopy, osc52Copy)
	if terminalErr == nil {
		return nil, nil
	}
	if environment.TmuxSession {
		return nil, ClipboardError(fmt.Sprintf("native clipboard: %v; terminal fallback: %v", nativeErr, terminalErr))
	}
	return nil, ClipboardError(fmt.Sprintf("native clipboard: %v; OSC 52 fallback: %v", nativeErr, terminalErr))
}

func TerminalClipboardCopyWith(text string, tmuxSession bool, tmuxCopy ClipboardCopyFunc, osc52Copy ClipboardCopyFunc) error {
	return terminalClipboardCopyWith(text, tmuxSession, tmuxCopy, osc52Copy)
}

func terminalClipboardCopyWith(text string, tmuxSession bool, tmuxCopy ClipboardCopyFunc, osc52Copy ClipboardCopyFunc) error {
	if tmuxSession {
		if err := tmuxCopy(text); err == nil {
			return nil
		} else if oscErr := osc52Copy(text); oscErr != nil {
			return ClipboardError(fmt.Sprintf("tmux clipboard: %v; OSC 52 fallback: %v", err, oscErr))
		}
		return nil
	}
	return osc52Copy(text)
}

func TmuxClipboardCopyReady(setClipboardOutput string, tmuxInfoOutput string) error {
	if strings.TrimSpace(setClipboardOutput) == "off" {
		return ClipboardError("tmux clipboard forwarding is disabled")
	}
	for _, line := range strings.Split(tmuxInfoOutput, "\n") {
		if strings.Contains(line, "Ms: [missing]") {
			return ClipboardError("tmux clipboard forwarding is unavailable: missing Ms capability")
		}
	}
	return nil
}

func WriteOSC52ToWriter(writer io.Writer, sequence string) error {
	if _, err := io.WriteString(writer, sequence); err != nil {
		return ClipboardError("failed to write OSC 52: " + err.Error())
	}
	return nil
}

func OSC52Sequence(text string, tmux bool) (string, error) {
	rawBytes := len(text)
	if rawBytes > OSC52MaxRawBytes {
		return "", ClipboardError(fmt.Sprintf("OSC 52 payload too large (%d bytes; max %d)", rawBytes, OSC52MaxRawBytes))
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

func envValueSet(env map[string]string, key string) bool {
	return strings.TrimSpace(env[key]) != ""
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

type PastedImageInfo struct {
	Width         uint32
	Height        uint32
	EncodedFormat EncodedImageFormat
}

type PasteImageErrorKind string

const (
	PasteImageClipboardUnavailable PasteImageErrorKind = "clipboard_unavailable"
	PasteImageNoImage              PasteImageErrorKind = "no_image"
	PasteImageEncodeFailed         PasteImageErrorKind = "encode_failed"
	PasteImageIOError              PasteImageErrorKind = "io_error"
)

type PasteImageError struct {
	Kind    PasteImageErrorKind
	Message string
}

func (e PasteImageError) Error() string {
	switch e.Kind {
	case PasteImageClipboardUnavailable:
		return "clipboard unavailable: " + e.Message
	case PasteImageNoImage:
		return "no image on clipboard: " + e.Message
	case PasteImageEncodeFailed:
		return "could not encode image: " + e.Message
	case PasteImageIOError:
		return "io error: " + e.Message
	default:
		if strings.TrimSpace(e.Message) == "" {
			return "clipboard image paste failed"
		}
		return e.Message
	}
}

func NormalizePastedSearchQuery(pasted string) (string, bool) {
	normalized := strings.Join(strings.Fields(pasted), " ")
	return normalized, normalized != ""
}

func NormalizePastedPath(pasted string) (string, bool) {
	return NormalizePastedPathWithWSL(pasted, IsProbablyWSL())
}

func NormalizePastedPathWithWSL(pasted string, wsl bool) (string, bool) {
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
	if path, ok := NormalizeWindowsPath(unquoted, wsl); ok {
		return path, true
	}
	parts := shellSplitOne(pasted)
	if len(parts) != 1 {
		return "", false
	}
	part := parts[0]
	if path, ok := NormalizeWindowsPath(part, wsl); ok {
		return path, true
	}
	return filepath.Clean(part), true
}

func IsProbablyWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	version, _ := os.ReadFile("/proc/version")
	env := map[string]string{
		"WSL_DISTRO_NAME": os.Getenv("WSL_DISTRO_NAME"),
		"WSL_INTEROP":     os.Getenv("WSL_INTEROP"),
	}
	return IsProbablyWSLFrom(string(version), env)
}

func IsProbablyWSLFrom(procVersion string, env map[string]string) bool {
	version := strings.ToLower(procVersion)
	if strings.Contains(version, "microsoft") || strings.Contains(version, "wsl") {
		return true
	}
	return strings.TrimSpace(env["WSL_DISTRO_NAME"]) != "" || strings.TrimSpace(env["WSL_INTEROP"]) != ""
}

func ConvertWindowsPathToWSL(input string) (string, bool) {
	if strings.HasPrefix(input, `\\`) {
		return "", false
	}
	if len(input) < 2 || input[1] != ':' {
		return "", false
	}
	drive := input[0]
	if drive >= 'A' && drive <= 'Z' {
		drive = drive + ('a' - 'A')
	}
	if drive < 'a' || drive > 'z' {
		return "", false
	}
	rest := strings.TrimLeft(input[2:], `\/`)
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' })
	all := append([]string{"/mnt/" + string(drive)}, parts...)
	return path.Clean(path.Join(all...)), true
}

func NormalizeWindowsPath(input string, wsl bool) (string, bool) {
	drive := len(input) >= 3 &&
		((input[0] >= 'a' && input[0] <= 'z') || (input[0] >= 'A' && input[0] <= 'Z')) &&
		input[1] == ':' &&
		(input[2] == '\\' || input[2] == '/')
	unc := strings.HasPrefix(input, `\\`)
	if !drive && !unc {
		return "", false
	}
	if wsl {
		if converted, ok := ConvertWindowsPathToWSL(input); ok {
			return converted, true
		}
	}
	return filepath.Clean(input), true
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
