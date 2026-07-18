package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestClipboardEnvironmentFromMapMatchesRust(t *testing.T) {
	env := ClipboardEnvironmentFromMap(map[string]string{
		"SSH_CONNECTION": "127.0.0.1 1 127.0.0.1 2",
		"TMUX_PANE":      "%1",
	}, true)
	if !env.SSHSession || !env.TmuxSession || !env.WSLSession {
		t.Fatalf("environment = %#v", env)
	}
}

func TestOSC52SequenceMatchesRustErrorsAndWriter(t *testing.T) {
	if _, err := OSC52Sequence(strings.Repeat("x", OSC52MaxRawBytes+1), false); err == nil || err.Error() != "OSC 52 payload too large (100001 bytes; max 100000)" {
		t.Fatalf("oversized OSC52 err = %v", err)
	}

	var out bytes.Buffer
	if err := WriteOSC52ToWriter(&out, "\x1b]52;c;aGVsbG8=\x07"); err != nil {
		t.Fatalf("WriteOSC52ToWriter error = %v", err)
	}
	if out.String() != "\x1b]52;c;aGVsbG8=\x07" {
		t.Fatalf("writer output = %q", out.String())
	}
}

func TestClipboardCopyWithSSHUsesTerminalAndSkipsNative(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{SSHSession: true},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		calls.wslOK,
	)
	if err != nil || lease != nil {
		t.Fatalf("ssh copy = lease %#v err %v", lease, err)
	}
	if calls.osc != 1 || calls.tmux != 0 || calls.native != 0 || calls.wsl != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestClipboardCopyWithSSHReportsOSC52Error(t *testing.T) {
	calls := clipboardCopyCalls{}
	_, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{SSHSession: true},
		calls.tmuxOK,
		func(string) error {
			calls.osc++
			return ClipboardError("blocked")
		},
		calls.nativeOK,
		calls.wslOK,
	)
	if err == nil || err.Error() != "OSC 52 clipboard copy failed over SSH: blocked" {
		t.Fatalf("ssh err = %v", err)
	}
	if calls.osc != 1 || calls.native != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestClipboardCopyWithSSHTmuxFallbackMatchesRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	_, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{SSHSession: true, TmuxSession: true},
		func(string) error {
			calls.tmux++
			return ClipboardError("tmux unavailable")
		},
		func(string) error {
			calls.osc++
			return ClipboardError("osc blocked")
		},
		calls.nativeOK,
		calls.wslOK,
	)
	if err == nil || err.Error() != "terminal clipboard copy failed over SSH: tmux clipboard: tmux unavailable; OSC 52 fallback: osc blocked" {
		t.Fatalf("ssh tmux err = %v", err)
	}
	if calls.tmux != 1 || calls.osc != 1 || calls.native != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestClipboardCopyWithLocalUsesNativeFirst(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{WSLSession: true},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		calls.wslOK,
	)
	if err != nil || lease == nil {
		t.Fatalf("local native = lease %#v err %v", lease, err)
	}
	if calls.native != 1 || calls.osc != 0 || calls.wsl != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestClipboardCopyWithLocalFallbacksMatchRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	_, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{WSLSession: true},
		calls.tmuxOK,
		func(string) error {
			calls.osc++
			return ClipboardError("osc blocked")
		},
		func(string) (*ClipboardLease, error) {
			calls.native++
			return nil, ClipboardError("native unavailable")
		},
		func(string) error {
			calls.wsl++
			return ClipboardError("powershell unavailable")
		},
	)
	if err == nil || err.Error() != "native clipboard: native unavailable; WSL fallback: powershell unavailable; OSC 52 fallback: osc blocked" {
		t.Fatalf("local fallback err = %v", err)
	}
	if calls.native != 1 || calls.wsl != 1 || calls.osc != 1 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestClipboardCopyWithLocalTmuxFallbackMatchesRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{TmuxSession: true},
		calls.tmuxOK,
		calls.oscOK,
		func(string) (*ClipboardLease, error) {
			calls.native++
			return nil, ClipboardError("native unavailable")
		},
		calls.wslOK,
	)
	if err != nil || lease != nil {
		t.Fatalf("local tmux fallback = lease %#v err %v", lease, err)
	}
	if calls.native != 1 || calls.tmux != 1 || calls.osc != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestTmuxClipboardCopyReadyMatchesRust(t *testing.T) {
	if err := TmuxClipboardCopyReady("external\n", "193: Ms: (string) \\033]52;%p1%s;%p2%s\\a\n"); err != nil {
		t.Fatalf("ready err = %v", err)
	}
	if err := TmuxClipboardCopyReady("off\n", ""); err == nil || err.Error() != "tmux clipboard forwarding is disabled" {
		t.Fatalf("disabled err = %v", err)
	}
	if err := TmuxClipboardCopyReady("external\n", "193: Ms: [missing]\n"); err == nil || err.Error() != "tmux clipboard forwarding is unavailable: missing Ms capability" {
		t.Fatalf("missing Ms err = %v", err)
	}
}

type clipboardCopyCalls struct {
	tmux   int
	osc    int
	native int
	wsl    int
}

func (c *clipboardCopyCalls) tmuxOK(string) error {
	c.tmux++
	return nil
}

func (c *clipboardCopyCalls) oscOK(string) error {
	c.osc++
	return nil
}

func (c *clipboardCopyCalls) nativeOK(string) (*ClipboardLease, error) {
	c.native++
	return &ClipboardLease{}, nil
}

func (c *clipboardCopyCalls) wslOK(string) error {
	c.wsl++
	return nil
}
