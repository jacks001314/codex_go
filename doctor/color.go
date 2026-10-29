package doctor

import (
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ansiReset      = "\x1b[0m"
	ansiDim        = "\x1b[2m"
	ansiItalic     = "\x1b[3m"
	ansiForeground = "\x1b[39m"
)

// doctorColor256 mirrors the xterm-256 foreground styling used by the Rust
// doctor renderer. Colors are emitted only when the report opted into color so
// piped output and tests stay byte-stable.
func doctorColor256(text string, code int, opts *Options) string {
	if opts == nil || !opts.Color {
		return text
	}
	return "\x1b[38;5;" + strconv.Itoa(code) + "m" + text + ansiForeground
}

func doctorDim(text string, opts *Options) string {
	if opts == nil || !opts.Color {
		return text
	}
	return ansiDim + text + ansiReset
}

func doctorRed(text string, opts *Options) string {
	return doctorColor256(text, 196, opts)
}

func doctorCyan(text string, opts *Options) string {
	return doctorColor256(text, 117, opts)
}

func doctorGreen(text string, opts *Options) string {
	return doctorColor256(text, 10, opts)
}

// highlightActions colors copyable paths and URLs in a check description or
// remedy (Rust #47358): red while the check fails, cyan otherwise. Backticked
// spans are treated as code and keep their spaces; plain text is tokenized on
// whitespace with trailing punctuation and whitespace preserved verbatim.
func highlightActions(text string, status doctorDisplayStatus, opts *Options) string {
	if opts == nil || !opts.Color {
		return text
	}
	return highlightSegments(text, func(part string, code bool) string {
		if code {
			return highlightCodeSpan(part, status, opts)
		}
		return highlightPlainText(part, status, opts)
	})
}

// styleDetailText applies the same status-aware highlight to detail values,
// using the richer per-token rules of Rust's doctor detail renderer.
func styleDetailText(text string, status doctorDisplayStatus, opts *Options) string {
	if opts == nil || !opts.Color {
		return text
	}
	return highlightSegments(text, func(part string, code bool) string {
		if code {
			return highlightCodeSpan(part, status, opts)
		}
		return styleDetailPlainText(part, status, opts)
	})
}

func highlightSegments(text string, style func(part string, code bool) string) string {
	parts := strings.Split(text, "`")
	var out strings.Builder
	out.WriteString(style(parts[0], false))
	inCode := true
	for _, part := range parts[1:] {
		out.WriteString(style(part, inCode))
		inCode = !inCode
	}
	return out.String()
}

func highlightCodeSpan(part string, status doctorDisplayStatus, opts *Options) string {
	if status == doctorDisplayStatusFail && looksCopyable(part) {
		return doctorRed(part, opts)
	}
	return doctorCyan(part, opts)
}

func highlightPlainText(text string, status doctorDisplayStatus, opts *Options) string {
	return styleTokens(text, func(bare string) string {
		switch {
		case status == doctorDisplayStatusFail && looksCopyable(bare):
			return doctorRed(bare, opts)
		case strings.HasPrefix(bare, "--") || looksCopyable(bare):
			return doctorCyan(bare, opts)
		default:
			return bare
		}
	})
}

func styleDetailPlainText(text string, status doctorDisplayStatus, opts *Options) string {
	return styleTokens(text, func(bare string) string {
		return styleDetailBareToken(bare, status, opts)
	})
}

func styleDetailBareToken(bare string, status doctorDisplayStatus, opts *Options) string {
	if bare == "" {
		return ""
	}
	if bare == "<redacted>" {
		if opts == nil || !opts.Color {
			return bare
		}
		return "\x1b[38;5;244m" + ansiItalic + bare + ansiReset + ansiForeground
	}
	if strings.Contains(bare, "(missing)") || doctorDetailIsFalsy(bare) {
		return doctorColor256(bare, 240, opts)
	}
	if label, value, ok := strings.Cut(bare, ":"); ok && doctorDetailIsFalsy(value) {
		return label + ":" + doctorColor256(value, 240, opts)
	}
	if bare == "ok" {
		return doctorGreen(bare, opts)
	}
	switch {
	case status == doctorDisplayStatusFail && looksCopyable(bare):
		return doctorRed(bare, opts)
	case strings.HasPrefix(bare, "--") || looksCopyable(bare):
		return doctorCyan(bare, opts)
	case isDoctorByteUnitToken(bare):
		return doctorDim(bare, opts)
	default:
		return bare
	}
}

func isDoctorByteUnitToken(bare string) bool {
	switch bare {
	case "B", "KB", "MB", "GB", "TB", "files", "file":
		return true
	default:
		return false
	}
}

// styleTokens walks whitespace-terminated tokens (Rust
// split_inclusive(char::is_whitespace)), strips trailing punctuation from the
// styled part, and reattaches it so the visible text is unchanged.
func styleTokens(text string, styleBare func(bare string) string) string {
	var out strings.Builder
	start := 0
	for i, r := range text {
		if !unicode.IsSpace(r) {
			continue
		}
		end := i + utf8.RuneLen(r)
		out.WriteString(styleToken(text[start:end], styleBare))
		start = end
	}
	if start < len(text) {
		out.WriteString(styleToken(text[start:], styleBare))
	}
	return out.String()
}

func styleToken(token string, styleBare func(bare string) string) string {
	trimmed := strings.TrimRightFunc(token, unicode.IsSpace)
	suffix := token[len(trimmed):]
	bare := strings.TrimRight(trimmed, ",.:;)")
	punctuation := trimmed[len(bare):]
	return styleBare(bare) + punctuation + suffix
}

// looksCopyable reports whether text is a path or URL the renderer should
// highlight. Windows paths are recognized only on Windows, matching Rust's
// cfg!(target_os = "windows") gate.
func looksCopyable(text string) bool {
	return looksCopyableOnGOOS(text, runtime.GOOS)
}

func looksCopyableOnGOOS(text string, goos string) bool {
	if strings.HasPrefix(text, "http://") ||
		strings.HasPrefix(text, "https://") ||
		strings.HasPrefix(text, "wss://") ||
		strings.HasPrefix(text, "~/") ||
		strings.HasPrefix(text, "/") ||
		strings.HasPrefix(text, "./") ||
		strings.HasPrefix(text, "../") {
		return true
	}
	if goos != "windows" {
		return false
	}
	if strings.HasPrefix(text, `\\`) || strings.HasPrefix(text, `\??\`) {
		return true
	}
	return len(text) >= 3 &&
		text[1] == ':' &&
		(text[2] == '/' || text[2] == '\\') &&
		isASCIIAlpha(text[0])
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// doctorColorEnabled decides whether the human report should be styled,
// mirroring Rust's should_enable_color: --no-color, NO_COLOR, TERM=dumb and
// non-terminal output all disable it.
func doctorColorEnabled(opts *Options, stdout io.Writer) bool {
	if opts == nil || opts.NoColor || opts.JSON {
		return false
	}
	file, ok := stdout.(*os.File)
	if !ok || !fileIsTerminal(file) {
		return false
	}
	return doctorColorEnabledForEnv(currentEnvMap(), true)
}

func doctorColorEnabledForEnv(env map[string]string, stdoutIsTerminal bool) bool {
	if env == nil {
		return false
	}
	if _, noColor := env["NO_COLOR"]; noColor {
		return false
	}
	if strings.TrimSpace(env["TERM"]) == "dumb" {
		return false
	}
	return terminalStreamSupportsColor(env, stdoutIsTerminal)
}
