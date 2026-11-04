package tui

import (
	"strings"
	"unicode/utf8"
)

type NotificationMethod string

const (
	NotificationMethodAuto   NotificationMethod = "auto"
	NotificationMethodOSC9   NotificationMethod = "osc9"
	NotificationMethodOSC777 NotificationMethod = "osc777"
	NotificationMethodBEL    NotificationMethod = "bel"
)

type NotificationCondition string

const (
	NotificationConditionUnfocused NotificationCondition = "unfocused"
	NotificationConditionAlways    NotificationCondition = "always"
)

type NotificationSequenceOptions struct {
	Tmux         bool
	SupportsOSC9 bool
	// Ghostty limits a parsed notification body to 255 bytes (Rust #59256).
	Ghostty bool
}

func ShouldEmitNotification(condition NotificationCondition, terminalFocused bool) bool {
	switch condition {
	case NotificationConditionAlways:
		return true
	default:
		return !terminalFocused
	}
}

func NotificationSequence(method NotificationMethod, message string, options NotificationSequenceOptions) string {
	switch resolveNotificationMethod(method, options.SupportsOSC9) {
	case NotificationMethodOSC9, NotificationMethodOSC777:
		protocol := resolveNotificationMethod(method, options.SupportsOSC9)
		body := notificationBody(message, protocol == NotificationMethodOSC777)
		if options.Ghostty {
			body = truncateGhosttyNotificationBody(body)
		}
		sequence := "\x1b]9;" + body + "\x07"
		if protocol == NotificationMethodOSC777 {
			sequence = "\x1b]777;notify;Codex;" + body + "\x1b\\"
		}
		if options.Tmux {
			return "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
		}
		return sequence
	default:
		return "\x07"
	}
}

// notificationBody keeps terminal control characters out of the OSC payload;
// OSC 777 also uses ASCII semicolons as field delimiters (Rust #59256).
func notificationBody(message string, osc777 bool) string {
	var builder strings.Builder
	builder.Grow(len(message))
	for _, r := range message {
		switch {
		case osc777 && r == ';':
			builder.WriteRune('；')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			builder.WriteRune(' ')
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// truncateGhosttyNotificationBody mirrors Ghostty's 255-byte notification
// buffer, keeping the body on a UTF-8 boundary and ending with an ellipsis.
func truncateGhosttyNotificationBody(body string) string {
	const limit = 255
	const ellipsis = "…"
	if len(body) <= limit {
		return body
	}
	cut := limit - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + ellipsis
}

func NotificationSequenceForEnv(method NotificationMethod, message string, getenv func(string) string) string {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return NotificationSequence(method, message, NotificationSequenceOptions{
		Tmux:         strings.TrimSpace(getenv("TMUX")) != "",
		SupportsOSC9: NotificationEnvSupportsOSC9(getenv),
		Ghostty:      normalizeTerminalProgram(getenv("TERM_PROGRAM")) == "ghostty",
	})
}

func NotificationEnvSupportsOSC9(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	if termProgram := strings.TrimSpace(getenv("TERM_PROGRAM")); termProgram != "" {
		return terminalProgramSupportsOSC9(termProgram)
	}
	if strings.TrimSpace(getenv("WEZTERM_VERSION")) != "" {
		return true
	}
	if strings.TrimSpace(getenv("ITERM_SESSION_ID")) != "" ||
		strings.TrimSpace(getenv("ITERM_PROFILE")) != "" ||
		strings.TrimSpace(getenv("ITERM_PROFILE_NAME")) != "" {
		return true
	}
	if strings.TrimSpace(getenv("KITTY_WINDOW_ID")) != "" {
		return true
	}
	term := strings.ToLower(strings.TrimSpace(getenv("TERM")))
	return strings.Contains(term, "kitty") || term == "wezterm" || term == "wezterm-mux"
}

func terminalProgramSupportsOSC9(value string) bool {
	normalized := normalizeTerminalProgram(value)
	switch normalized {
	case "ghostty", "iterm", "iterm2", "itermapp", "kitty", "warp", "warpterminal", "wezterm":
		return true
	default:
		return false
	}
}

func normalizeTerminalProgram(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		switch r {
		case ' ', '-', '_', '.':
			continue
		default:
			builder.WriteRune(r)
		}
	}
	return strings.ToLower(builder.String())
}

func resolveNotificationMethod(method NotificationMethod, supportsOSC9 bool) NotificationMethod {
	switch method {
	case NotificationMethodOSC9, NotificationMethodOSC777, NotificationMethodBEL:
		return method
	default:
		if supportsOSC9 {
			return NotificationMethodOSC9
		}
		return NotificationMethodBEL
	}
}
