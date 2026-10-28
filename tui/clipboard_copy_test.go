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
		"HERDR_ENV":      "1",
	}, true)
	if !env.SSHSession || !env.TmuxSession || !env.WSLSession || !env.HerdrSession {
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

// Rust #52557: native copying still runs over SSH, and forwarding follows a
// successful native write.
func TestClipboardCopyWithSSHForwardsAfterNativeSuccessLikeRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{SSHSession: true},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		calls.wslOK,
	)
	if err != nil || lease == nil {
		t.Fatalf("ssh copy = lease %#v err %v", lease, err)
	}
	if calls.osc != 1 || calls.tmux != 0 || calls.native != 1 || calls.wsl != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestClipboardCopyWithSSHKeepsNativeLeaseWhenForwardingFailsLikeRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
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
	if err != nil || lease == nil {
		t.Fatalf("ssh native lease = %#v err = %v", lease, err)
	}
	if calls.osc != 1 || calls.native != 1 {
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
		func(string) (*ClipboardLease, error) {
			calls.native++
			return nil, ClipboardError("native unavailable")
		},
		calls.wslOK,
	)
	if err == nil || err.Error() != "native clipboard: native unavailable; terminal clipboard: tmux clipboard: tmux unavailable; OSC 52 fallback: osc blocked" {
		t.Fatalf("ssh tmux err = %v", err)
	}
	if calls.tmux != 1 || calls.osc != 1 || calls.native != 1 {
		t.Fatalf("calls = %#v", calls)
	}
}

// Rust #52557: a Herdr session forwards through OSC 52 even when native
// copying succeeds, and keeps the native lease.
func TestClipboardCopyWithHerdrForwardsEvenAfterNativeSuccessLikeRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{HerdrSession: true},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		calls.wslOK,
	)
	if err != nil || lease == nil {
		t.Fatalf("herdr copy = lease %#v err %v", lease, err)
	}
	if calls.native != 1 || calls.osc != 1 || calls.tmux != 0 || calls.wsl != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

// Rust #53453 local_wsl_uses_windows_and_skips_osc52_on_success: in a WSL session the
// Windows clipboard must be reached without contacting the native (WSLg) backend first.
func TestClipboardCopyWithLocalWSLPrefersWindowsClipboardLikeRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{WSLSession: true},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		calls.wslOK,
	)
	if err != nil || lease != nil {
		t.Fatalf("wsl copy = lease %#v err %v", lease, err)
	}
	if calls.wsl != 1 || calls.native != 0 || calls.osc != 0 || calls.tmux != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

// Rust #53453: off WSL the native clipboard is still attempted first and its lease
// is preserved.
func TestClipboardCopyWithLocalUsesNativeFirstLikeRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		calls.wslOK,
	)
	if err != nil || lease == nil {
		t.Fatalf("local native = lease %#v err %v", lease, err)
	}
	if calls.native != 1 || calls.wsl != 0 || calls.osc != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

// Rust #53453: on WSL a failing Windows write falls through to the native backend,
// and a native lease suppresses terminal forwarding.
func TestClipboardCopyWithLocalWSLFallsBackToNativeLeaseLikeRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{WSLSession: true},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		func(string) error {
			calls.wsl++
			return ClipboardError("powershell unavailable")
		},
	)
	if err != nil || lease == nil {
		t.Fatalf("wsl fallback native = lease %#v err %v", lease, err)
	}
	if calls.wsl != 1 || calls.native != 1 || calls.osc != 0 || calls.tmux != 0 {
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
	if err == nil || err.Error() != "Windows clipboard: powershell unavailable; native clipboard: native unavailable; terminal clipboard: osc blocked" {
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

// Rust #57504: an SSH session in WSL with X11 forwarding uses the forwarded
// display's native clipboard instead of the Windows host clipboard.
func TestClipboardCopyWithWSLForwardedX11UsesNativeLikeRust(t *testing.T) {
	calls := clipboardCopyCalls{}
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{SSHSession: true, WSLSession: true, ForwardedX11: true},
		calls.tmuxOK,
		calls.oscOK,
		calls.nativeOK,
		func(string) error {
			t.Fatal("forwarded X11 must not write to the Windows host clipboard")
			return nil
		},
	)
	if err != nil || lease == nil {
		t.Fatalf("forwarded X11 copy = lease %#v err %v", lease, err)
	}
	if calls.native != 1 || calls.wsl != 0 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestClipboardEnvironmentDetectsForwardedX11LikeRust(t *testing.T) {
	ssh := map[string]string{"SSH_CONNECTION": "127.0.0.1 1 127.0.0.1 2"}
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "ssh with display", env: map[string]string{"SSH_CONNECTION": ssh["SSH_CONNECTION"], "DISPLAY": ":0"}, want: true},
		{name: "ssh with empty display", env: map[string]string{"SSH_CONNECTION": ssh["SSH_CONNECTION"], "DISPLAY": ""}, want: false},
		{name: "ssh without display", env: ssh, want: false},
		{name: "local display is not forwarded", env: map[string]string{"DISPLAY": ":0"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClipboardEnvironmentFromMap(tc.env, true).ForwardedX11; got != tc.want {
				t.Fatalf("ForwardedX11 = %v, want %v", got, tc.want)
			}
		})
	}
}
