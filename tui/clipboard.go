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
	HerdrSession bool
	SSHSession   bool
	// ForwardedX11 marks an SSH session with a forwarded X display. Rust
	// #57504 uses the forwarded display's clipboard instead of the Windows host
	// clipboard, so WSLg must not be bypassed in that case.
	ForwardedX11 bool
	WSLSession   bool
	TmuxSession  bool
	// ScreenSession marks GNU screen detected through STY while tmux is absent
	// (Rust #58716). Screen needs its own DCS passthrough chunking.
	ScreenSession bool
}

func ClipboardCopyOrder(environment ClipboardEnvironment) []ClipboardBackend {
	order := []ClipboardBackend{ClipboardNative}
	if environment.WSLSession && !environment.ForwardedX11 {
		// Rust #53453 (codex-rs/tui/src/clipboard_copy.rs:221-229): prefer the Windows
		// clipboard on WSL. Contacting WSLg for native setup can block forever, so the
		// Windows backend is attempted before the native one.
		order = []ClipboardBackend{ClipboardWSL, ClipboardNative}
	}
	if environment.TmuxSession {
		order = append(order, ClipboardTmux)
	}
	return append(order, ClipboardOSC52)
}

type ClipboardLease struct{}

type NativeClipboardCopyFunc func(string) (*ClipboardLease, error)
type ClipboardCopyFunc func(string) error

func ClipboardEnvironmentFromMap(env map[string]string, wslSession bool) ClipboardEnvironment {
	// Rust uses var_os(...).is_some(): a present-but-empty multiplexer
	// variable still identifies the session. This matters for generated
	// environments (and for STY/TMUX under wrappers) where the value may be
	// empty even though the session is real.
	sshSession := envValuePresent(env, "SSH_TTY") || envValuePresent(env, "SSH_CONNECTION")
	tmuxSession := envValuePresent(env, "TMUX") || envValuePresent(env, "TMUX_PANE")
	return ClipboardEnvironment{
		HerdrSession: env["HERDR_ENV"] == "1",
		SSHSession:   sshSession,
		// Rust #57504: a nonempty DISPLAY in an SSH session is X11 forwarding.
		ForwardedX11: sshSession && env["DISPLAY"] != "",
		WSLSession:   wslSession,
		TmuxSession:  tmuxSession,
		// Rust #58716: GNU screen advertises itself through STY; tmux keeps
		// precedence when both are set.
		ScreenSession: !tmuxSession && envValuePresent(env, "STY"),
	}
}

// ClipboardForwardingEnabled reports whether a successful native copy should
// also reach attached terminal clients. Persistent multiplexer sessions can
// gain clients after Codex starts, so tmux and Herdr forward even without
// inherited SSH variables (Rust #52557).
func ClipboardForwardingEnabled(environment ClipboardEnvironment) bool {
	return environment.TmuxSession || environment.HerdrSession || environment.SSHSession
}

func CopyToClipboardWith(
	text string,
	environment ClipboardEnvironment,
	tmuxCopy ClipboardCopyFunc,
	osc52Copy ClipboardCopyFunc,
	nativeCopy NativeClipboardCopyFunc,
	wslCopy ClipboardCopyFunc,
) (*ClipboardLease, error) {
	// Rust #45457: an empty selection fails before any clipboard is touched.
	if text == "" {
		return nil, ClipboardError("nothing to copy: the selected content is empty")
	}
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

	// Rust #53453 (codex-rs/tui/src/clipboard_copy.rs:221-229): on WSL, attempt the
	// Windows clipboard before the native one. Native (WSLg) setup can block forever, so
	// it is only used as the Windows backend's fallback. Rust #57504: an SSH session
	// with a forwarded X display copies to that display instead of the Windows host.
	var lease *ClipboardLease
	var nativeErr error
	if environment.WSLSession && !environment.ForwardedX11 {
		if wslErr := wslCopy(text); wslErr != nil {
			fallbackLease, fallbackErr := nativeCopy(text)
			if fallbackErr == nil {
				lease = fallbackLease
			} else {
				nativeErr = ClipboardError(fmt.Sprintf("Windows clipboard: %v; native clipboard: %v", wslErr, fallbackErr))
			}
		}
	} else {
		nativeLease, err := nativeCopy(text)
		if err == nil {
			lease = nativeLease
		} else {
			nativeErr = ClipboardError("native clipboard: " + err.Error())
		}
	}
	// Copy natively first: an X11 SelectionClear from a terminal write can otherwise
	// race with arboard reusing its ownership window (clipboard_copy.rs:230-236).
	// Persistent multiplexer sessions may gain remote clients after Codex starts,
	// so still forward when native copying succeeded (Rust #52557).
	if nativeErr == nil {
		if ClipboardForwardingEnabled(environment) {
			_ = terminalClipboardCopyWith(text, environment.TmuxSession, tmuxCopy, osc52Copy)
		}
		return lease, nil
	}

	terminalErr := terminalClipboardCopyWith(text, environment.TmuxSession, tmuxCopy, osc52Copy)
	if terminalErr == nil {
		return nil, nil
	}
	return nil, ClipboardError(fmt.Sprintf("%v; terminal clipboard: %v", nativeErr, terminalErr))
}

// WriteTerminalClipboard sends text to the attached terminal's clipboard.
//
// In a tmux session it forwards through tmux's own clipboard integration so
// clients attached after Codex started receive the copy (Rust #45457); the
// caller falls back to OSC 52 when that fails. Otherwise it writes an OSC 52
// sequence, wrapped for tmux passthrough when TMUX is set, as Rust's
// osc52_copy does.
func WriteTerminalClipboard(text string, tmux bool) error {
	if tmux {
		return TmuxClipboardCopy(text)
	}
	_, tmuxSet := os.LookupEnv("TMUX")
	_, screenSet := os.LookupEnv("STY")
	return writeOSC52ToTerminal(text, OSC52TargetForEnvironment(tmuxSet, screenSet))
}

// WriteTerminalClipboardForEnvironment forwards to the terminal using an
// already-resolved clipboard environment (Rust #58716).
func WriteTerminalClipboardForEnvironment(text string, environment ClipboardEnvironment) error {
	if environment.TmuxSession {
		return TmuxClipboardCopy(text)
	}
	return writeOSC52ToTerminal(text, OSC52TargetForEnvironment(environment.TmuxSession, environment.ScreenSession))
}

func writeOSC52ToTerminal(text string, target OSC52Target) error {
	sequence, err := OSC52SequenceForTarget(text, target)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if tty, openErr := os.OpenFile("/dev/tty", os.O_WRONLY, 0); openErr == nil {
			defer tty.Close()
			if writeErr := WriteOSC52ToWriter(tty, sequence); writeErr == nil {
				return nil
			}
		}
	}
	return WriteOSC52ToWriter(os.Stdout, sequence)
}

func TerminalClipboardCopyWith(text string, tmuxSession bool, tmuxCopy ClipboardCopyFunc, osc52Copy ClipboardCopyFunc) error {
	return terminalClipboardCopyWith(text, tmuxSession, tmuxCopy, osc52Copy)
}

func terminalClipboardCopyWith(text string, tmuxSession bool, tmuxCopy ClipboardCopyFunc, osc52Copy ClipboardCopyFunc) error {
	if tmuxSession {
		// Rust #45457: the terminal payload limit applies to tmux copies too.
		err := tmuxPayloadTooLarge(text)
		if err == nil {
			err = tmuxCopy(text)
		}
		if err == nil {
			return nil
		} else if oscErr := osc52Copy(text); oscErr != nil {
			return ClipboardError(fmt.Sprintf("tmux clipboard: %v; OSC 52 fallback: %v", err, oscErr))
		}
		return nil
	}
	return osc52Copy(text)
}

func tmuxPayloadTooLarge(text string) error {
	if len(text) > OSC52MaxRawBytes {
		return ClipboardError(fmt.Sprintf("tmux clipboard payload too large (%d bytes; max %d)", len(text), OSC52MaxRawBytes))
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
	return OSC52SequenceForTarget(text, OSC52TargetForEnvironment(tmux, false))
}

// OSC52Target selects how an OSC 52 clipboard sequence reaches the outer
// terminal (Rust #58716).
type OSC52Target string

const (
	OSC52TargetDirect OSC52Target = "direct"
	OSC52TargetTmux   OSC52Target = "tmux"
	OSC52TargetScreen OSC52Target = "screen"
)

// OSC52ScreenChunkSize keeps every screen DCS below its older 256-byte string
// limit.
const OSC52ScreenChunkSize = 252

// OSC52TargetForEnvironment mirrors Rust's target selection: tmux wins over
// GNU screen, and screen is only used when tmux is absent.
func OSC52TargetForEnvironment(tmux, screen bool) OSC52Target {
	switch {
	case tmux:
		return OSC52TargetTmux
	case screen:
		return OSC52TargetScreen
	default:
		return OSC52TargetDirect
	}
}

func OSC52SequenceForTarget(text string, target OSC52Target) (string, error) {
	rawBytes := len(text)
	if rawBytes > OSC52MaxRawBytes {
		return "", ClipboardError(fmt.Sprintf("OSC 52 payload too large (%d bytes; max %d)", rawBytes, OSC52MaxRawBytes))
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	sequence := "\x1b]52;c;" + encoded + "\x07"
	switch target {
	case OSC52TargetTmux:
		return "\x1bPtmux;\x1b" + sequence + "\x1b\\", nil
	case OSC52TargetScreen:
		// Screen buffers each DCS separately but forwards one continuous OSC 52
		// to the outer terminal.
		var wrapped strings.Builder
		for start := 0; start < len(sequence); start += OSC52ScreenChunkSize {
			end := min(start+OSC52ScreenChunkSize, len(sequence))
			wrapped.WriteString("\x1bP")
			wrapped.WriteString(sequence[start:end])
			wrapped.WriteString("\x1b\\")
		}
		return wrapped.String(), nil
	default:
		return sequence, nil
	}
}

type ClipboardError string

func (e ClipboardError) Error() string {
	return string(e)
}

func envValuePresent(env map[string]string, key string) bool {
	_, ok := env[key]
	return ok
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
