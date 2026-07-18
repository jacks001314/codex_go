package notifications

import "testing"

func TestOSC9PostNotificationWritesPlainSequenceMatchRust(t *testing.T) {
	command := OSC9PostNotification{
		Message:        "hello",
		DCSPassthrough: false,
	}

	if got, want := command.ANSI(), "\x1b]9;hello\x07"; got != want {
		t.Fatalf("ANSI() = %q, want %q", got, want)
	}
}

func TestOSC9PostNotificationWritesTmuxWrappedSequenceMatchRust(t *testing.T) {
	command := OSC9PostNotification{
		Message:        "done",
		DCSPassthrough: true,
	}

	if got, want := command.ANSI(), "\x1bPtmux;\x1b\x1b]9;done\x07\x1b\\"; got != want {
		t.Fatalf("ANSI() = %q, want %q", got, want)
	}
}

func TestOSC9PostNotificationEscapesEscapeBytesInsideTmuxPayloadMatchRust(t *testing.T) {
	command := OSC9PostNotification{
		Message:        "danger\x1b[31m",
		DCSPassthrough: true,
	}

	if got, want := command.ANSI(), "\x1bPtmux;\x1b\x1b]9;danger\x1b\x1b[31m\x07\x1b\\"; got != want {
		t.Fatalf("ANSI() = %q, want %q", got, want)
	}
}

func TestNotificationBackendsAndCompatibilityHelpers(t *testing.T) {
	osc9 := NewOSC9Backend(true)
	if got, want := osc9.Notify("ship"), "\x1bPtmux;\x1b\x1b]9;ship\x07\x1b\\"; got != want {
		t.Fatalf("OSC9Backend.Notify() = %q, want %q", got, want)
	}

	var bel BELBackend
	if got := bel.Notify("ignored"); got != BEL {
		t.Fatalf("BELBackend.Notify() = %q, want BEL", got)
	}

	if got := OSC9Notification(" hello "); got != "\x1b]9; hello \x07" {
		t.Fatalf("OSC9Notification compatibility helper = %q", got)
	}
	if got := OSC9Notification(""); got != "\x1b]9;\x07" {
		t.Fatalf("OSC9Notification empty message = %q", got)
	}
	if got := BELNotification(false); got != "" {
		t.Fatalf("BELNotification(false) = %q, want empty", got)
	}
}
