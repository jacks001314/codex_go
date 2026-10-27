package tui

import (
	"bytes"
	"testing"
)

// Mirrors codex-rs/tui/src/terminal_probe/startup_replay_tests.rs.
func TestStartupReplayBoundsOversizedOSCLikeRust(t *testing.T) {
	input := append([]byte("typed\x1b]10;"), bytes.Repeat([]byte("x"), maxReplayedOSCBytes)...)
	input = append(input, '1', '\r')
	want := input[:len("typed")+maxReplayedOSCBytes]
	if got := StartupReplayInput(input); !bytes.Equal(got, want) {
		t.Fatalf("oversized incomplete OSC replay = %q, want %q", got, want)
	}

	input = append(input, 0x07)
	input = append(input, []byte("suffix")...)
	if got := StartupReplayInput(input); string(got) != "typedsuffix" {
		t.Fatalf("completed oversized OSC replay = %q, want %q", got, "typedsuffix")
	}

	const partial = "typed\x1b]10;partial\x1b"
	if got := StartupReplayInput([]byte(partial)); string(got) != partial {
		t.Fatalf("incomplete OSC replay = %q, want %q", got, partial)
	}
}

func TestStartupReplayPreservesOSCLookingBracketedPasteLikeRust(t *testing.T) {
	input := append([]byte("prefix\x1b[200~\x1b]10;"), bytes.Repeat([]byte("x"), 2048)...)
	input = append(input, []byte("\x1b[201~suffix")...)
	if got := StartupReplayInput(input); !bytes.Equal(got, input) {
		t.Fatalf("bracketed paste replay = %q, want the paste preserved", got)
	}
}

func TestStartupReplayOmitsTerminalColorRepliesInterleavedWithBracketedPasteLikeRust(t *testing.T) {
	input := []byte("prefix\x1b[200~hello\x1b]10;rgb:eeee/eeee/eeee\x07 " +
		"\x1b]11;rgb:1111/1111/1111\x1b\\world\x1b[201~suffix")
	want := "prefix\x1b[200~hello world\x1b[201~suffix"
	if got := StartupReplayInput(input); string(got) != want {
		t.Fatalf("color replies inside paste = %q, want %q", got, want)
	}

	unfinishedPaste := []byte("\x1b[200~hello\x1b]10;rgb:eeee/eeee/eeee\x07 world")
	if got := StartupReplayInput(unfinishedPaste); string(got) != "\x1b[200~hello world" {
		t.Fatalf("unfinished paste replay = %q", got)
	}

	unrelatedOSC := []byte("\x1b[200~\x1b]52;preserve me\x07\x1b[201~")
	if got := StartupReplayInput(unrelatedOSC); !bytes.Equal(got, unrelatedOSC) {
		t.Fatalf("unrelated OSC inside paste = %q, want it preserved", got)
	}

	responseAfterPaste := []byte("\x1b[200~\x1b]10;unfinished\x1b[201~suffix\x07")
	if got := StartupReplayInput(responseAfterPaste); !bytes.Equal(got, responseAfterPaste) {
		t.Fatalf("response spanning the paste end = %q, want it preserved", got)
	}
}

// Keys, mouse reports, cursor-position replies, and the probe's own DA1 reply
// all survive: Rust only strips completed OSC replies.
func TestStartupReplayKeepsKeysAndProbeReportsLikeRust(t *testing.T) {
	input := []byte("\x1b[?64;1;2c\x1b[20;10R\x1b[<64;5;3Mhi\x1b]10;rgb:eeee/eeee/eeee\x07!")
	want := "\x1b[?64;1;2c\x1b[20;10R\x1b[<64;5;3Mhi!"
	if got := StartupReplayInput(input); string(got) != want {
		t.Fatalf("startup replay = %q, want %q", got, want)
	}
}
