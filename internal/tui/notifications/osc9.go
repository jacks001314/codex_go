package notifications

import "strings"

type OSC9Backend struct {
	DCSPassthrough bool
}

func NewOSC9Backend(dcsPassthrough bool) OSC9Backend {
	return OSC9Backend{DCSPassthrough: dcsPassthrough}
}

func (b *OSC9Backend) Notify(message string) string {
	return OSC9PostNotification{
		Message:        message,
		DCSPassthrough: b != nil && b.DCSPassthrough,
	}.ANSI()
}

type OSC9PostNotification struct {
	Message        string
	DCSPassthrough bool
}

func (p OSC9PostNotification) ANSI() string {
	if p.DCSPassthrough {
		escapedMessage := EscapeTmuxDCSPassthroughPayload(p.Message)
		return "\x1bPtmux;\x1b\x1b]9;" + escapedMessage + "\x07\x1b\\"
	}
	return "\x1b]9;" + p.Message + "\x07"
}

func (p OSC9PostNotification) String() string {
	return p.ANSI()
}

func EscapeTmuxDCSPassthroughPayload(message string) string {
	return strings.ReplaceAll(message, "\x1b", "\x1b\x1b")
}

func OSC9Notification(message string) string {
	return "\x1b]9;" + message + "\x07"
}
