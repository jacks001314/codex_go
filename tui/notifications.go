package tui

import "strings"

type NotificationMethod string

const (
	NotificationMethodAuto NotificationMethod = "auto"
	NotificationMethodOSC9 NotificationMethod = "osc9"
	NotificationMethodBEL  NotificationMethod = "bel"
)

type NotificationCondition string

const (
	NotificationConditionUnfocused NotificationCondition = "unfocused"
	NotificationConditionAlways    NotificationCondition = "always"
)

type NotificationSequenceOptions struct {
	Tmux         bool
	SupportsOSC9 bool
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
	case NotificationMethodOSC9:
		if options.Tmux {
			return "\x1bPtmux;\x1b\x1b]9;" + strings.ReplaceAll(message, "\x1b", "\x1b\x1b") + "\x07\x1b\\"
		}
		return "\x1b]9;" + message + "\x07"
	default:
		return "\x07"
	}
}

func NotificationSequenceForEnv(method NotificationMethod, message string, getenv func(string) string) string {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return NotificationSequence(method, message, NotificationSequenceOptions{
		Tmux:         strings.TrimSpace(getenv("TMUX")) != "",
		SupportsOSC9: NotificationEnvSupportsOSC9(getenv),
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
	case NotificationMethodOSC9, NotificationMethodBEL:
		return method
	default:
		if supportsOSC9 {
			return NotificationMethodOSC9
		}
		return NotificationMethodBEL
	}
}
