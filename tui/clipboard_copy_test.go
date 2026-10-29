package tui

import (
	"bytes"
	"reflect"
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

// Rust #45457 tmux_clipboard_copy_ready_accepts_forwarding_configuration: the
// most recently active client of the current session is selected.
func TestTmuxClipboardTargetAcceptsForwardingConfigurationLikeRust(t *testing.T) {
	client, err := TmuxClipboardTarget(
		func() (string, error) { return "external\n", nil },
		func() (string, error) { return "42 /dev/pts/7\n1 /dev/pts/8\n", nil },
		func(client string) (string, error) {
			if client != "/dev/pts/7" {
				t.Fatalf("capability probe client = %q", client)
			}
			return "193: Ms: (string) \\033]52;%p1%s;%p2%s\\a\n", nil
		},
	)
	if err != nil || client != "/dev/pts/7" {
		t.Fatalf("target = %q err = %v", client, err)
	}
}

func TestTmuxClipboardTargetRejectsDisabledForwardingLikeRust(t *testing.T) {
	_, err := TmuxClipboardTarget(
		func() (string, error) { return "off\n", nil },
		func() (string, error) {
			t.Fatal("clients must not be queried when forwarding is disabled")
			return "", nil
		},
		func(string) (string, error) {
			t.Fatal("capabilities must not be queried when forwarding is disabled")
			return "", nil
		},
	)
	if err == nil || err.Error() != "tmux clipboard forwarding is disabled" {
		t.Fatalf("disabled err = %v", err)
	}
}

func TestTmuxClipboardTargetRequiresNonemptyMsCapabilityLikeRust(t *testing.T) {
	for _, info := range []string{"", "193: Ms: [missing]\n", "193: Ms: (string) \n"} {
		_, err := TmuxClipboardTarget(
			func() (string, error) { return "external\n", nil },
			func() (string, error) { return "42 /dev/pts/7\n", nil },
			func(client string) (string, error) {
				if client != "/dev/pts/7" {
					t.Fatalf("capability probe client = %q", client)
				}
				return info, nil
			},
		)
		if err == nil || err.Error() != "tmux clipboard forwarding is unavailable: missing Ms capability" {
			t.Fatalf("info %q err = %v", info, err)
		}
	}
}

func TestTmuxClipboardTargetRejectsNoAttachedClientLikeRust(t *testing.T) {
	_, err := TmuxClipboardTarget(
		func() (string, error) { return "external\n", nil },
		func() (string, error) { return "\n", nil },
		func(string) (string, error) { t.Fatal("capabilities require an attached client"); return "", nil },
	)
	if err == nil || err.Error() != "tmux clipboard forwarding is unavailable: no attached client" {
		t.Fatalf("no client err = %v", err)
	}
}

// Rust #45457 tmux.rs::copy: every probe and the buffer load target the same
// client, and the text travels over stdin.
func TestTmuxClipboardCopyTargetsMostActiveClientAndSendsStdinLikeRust(t *testing.T) {
	var calls [][]string
	payload := ""
	run := func(args []string, stdin *string) (string, error) {
		calls = append(calls, args)
		switch args[0] {
		case "show-options":
			return "external\n", nil
		case "display-message":
			return " $3 \n", nil
		case "list-clients":
			return "42 /dev/pts/7\n1 /dev/pts/8\n", nil
		case "show-messages":
			return "193: Ms: (string) \\033]52;%p1%s;%p2%s\\a\n", nil
		case "load-buffer":
			if stdin == nil {
				t.Fatal("load-buffer requires a stdin payload")
			}
			payload = *stdin
			return "", nil
		}
		return "", ClipboardError("unexpected tmux command")
	}
	if err := tmuxClipboardCopyWith("hello", "%1", true, run); err != nil {
		t.Fatalf("tmux copy err = %v", err)
	}
	if payload != "hello" {
		t.Fatalf("stdin payload = %q", payload)
	}
	want := [][]string{
		{"show-options", "-gv", "set-clipboard"},
		{"display-message", "-p", "-t", "%1", "#{session_id}"},
		{"list-clients", "-t", "$3", "-F", "#{client_activity} #{client_name}"},
		{"show-messages", "-T", "-t", "/dev/pts/7"},
		{"load-buffer", "-w", "-t", "/dev/pts/7", "-"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("tmux calls = %#v, want %#v", calls, want)
	}
}

func TestTmuxClipboardCopyRequiresCurrentPaneLikeRust(t *testing.T) {
	err := tmuxClipboardCopyWith("hello", "", false, func([]string, *string) (string, error) {
		t.Fatal("tmux must not run without a current pane")
		return "", nil
	})
	if err == nil || err.Error() != "tmux clipboard forwarding is unavailable: no current pane" {
		t.Fatalf("no pane err = %v", err)
	}
}

// Rust #45457 empty_copy_reports_failure_without_touching_clipboards.
func TestClipboardCopyRejectsEmptySelectionWithoutTouchingClipboardsLikeRust(t *testing.T) {
	_, err := CopyToClipboardWith(
		"",
		ClipboardEnvironment{TmuxSession: true, SSHSession: true, WSLSession: true},
		func(string) error { t.Fatal("empty input must not reach tmux"); return nil },
		func(string) error { t.Fatal("empty input must not reach OSC 52"); return nil },
		func(string) (*ClipboardLease, error) {
			t.Fatal("empty input must not reach the native clipboard")
			return nil, nil
		},
		func(string) error { t.Fatal("empty input must not reach PowerShell"); return nil },
	)
	if err == nil || err.Error() != "nothing to copy: the selected content is empty" {
		t.Fatalf("empty copy err = %v", err)
	}
}

// Rust #45457 oversized_tmux_copy_preserves_native_copy: the terminal payload
// limit applies to tmux copies but never discards a successful native copy.
func TestClipboardCopyWithOversizedTmuxPayloadMatchesRust(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
		want []string
	}{
		{name: "at limit", size: OSC52MaxRawBytes, want: []string{"native", "tmux"}},
		{name: "over limit", size: OSC52MaxRawBytes + 1, want: []string{"native", "osc52"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Repeat("x", tc.size)
			var order []string
			lease, err := CopyToClipboardWith(
				text,
				ClipboardEnvironment{TmuxSession: true},
				func(string) error { order = append(order, "tmux"); return nil },
				func(string) error {
					order = append(order, "osc52")
					return ClipboardError("payload too large")
				},
				func(string) (*ClipboardLease, error) {
					order = append(order, "native")
					return &ClipboardLease{}, nil
				},
				func(string) error { t.Fatal("PowerShell fallback should not be needed"); return nil },
			)
			if err != nil || lease == nil {
				t.Fatalf("lease = %#v err = %v", lease, err)
			}
			if !reflect.DeepEqual(order, tc.want) {
				t.Fatalf("call order = %v, want %v", order, tc.want)
			}
		})
	}
}

// Rust #45457 copy_uses_osc52_when_tmux_fails_even_if_native_copy_succeeds: a
// failed tmux send still falls back to OSC 52 while the native lease survives.
func TestClipboardCopyWithTmuxFailingFallsBackToOSC52LikeRust(t *testing.T) {
	var order []string
	lease, err := CopyToClipboardWith(
		"hello",
		ClipboardEnvironment{TmuxSession: true},
		func(string) error { order = append(order, "tmux"); return ClipboardError("tmux unavailable") },
		func(string) error { order = append(order, "osc52"); return nil },
		func(string) (*ClipboardLease, error) { order = append(order, "native"); return &ClipboardLease{}, nil },
		func(string) error { t.Fatal("native copy succeeded"); return nil },
	)
	if err != nil || lease == nil {
		t.Fatalf("lease = %#v err = %v", lease, err)
	}
	want := []string{"native", "tmux", "osc52"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("call order = %v, want %v", order, want)
	}
}

// Rust #45457: the terminal payload limit error is reported with the tmux
// prefix and the OSC 52 fallback failure.
func TestClipboardCopyWithOversizedTmuxPayloadReportsBothBackends(t *testing.T) {
	text := strings.Repeat("x", OSC52MaxRawBytes+1)
	_, err := CopyToClipboardWith(
		text,
		ClipboardEnvironment{TmuxSession: true},
		func(string) error { t.Fatal("oversized payloads must not reach tmux"); return nil },
		func(string) error { return ClipboardError("OSC 52 payload too large") },
		func(string) (*ClipboardLease, error) { return nil, ClipboardError("native unavailable") },
		func(string) error { t.Fatal("PowerShell fallback should not be needed"); return nil },
	)
	want := "native clipboard: native unavailable; terminal clipboard: tmux clipboard: tmux clipboard payload too large (100001 bytes; max 100000); OSC 52 fallback: OSC 52 payload too large"
	if err == nil || err.Error() != want {
		t.Fatalf("oversized tmux err = %v", err)
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
