package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Rust #61724: clip.exe only takes single-line printable ASCII and tabs; an
// even byte count is padded with a trailing NUL so clip.exe does not guess
// UTF-16.
func TestWSLClipInputEligibilityLikeRust(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{name: "odd ascii", text: "abc", want: "abc", ok: true},
		{name: "even ascii is padded", text: "abcd", want: "abcd\x00", ok: true},
		{name: "tabs and spaces", text: "a\tb c", want: "a\tb c", ok: true},
		{name: "empty", text: "", ok: false},
		{name: "newline", text: "a\nb", ok: false},
		{name: "carriage return", text: "a\rb", ok: false},
		{name: "unicode", text: "héllo", ok: false},
		{name: "control character", text: "a\x01b", ok: false},
		{name: "del", text: "a\x7fb", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := WSLClipInput(test.text)
			if ok != test.ok || got != test.want {
				t.Fatalf("WSLClipInput(%q) = %q, %v; want %q, %v", test.text, got, ok, test.want, test.ok)
			}
		})
	}
}

type wslRunCall struct {
	executable string
	args       []string
	path       string
	text       string
}

// wslTestWriter records writer invocations and answers per executable.
type wslTestWriter struct {
	mu       sync.Mutex
	calls    []wslRunCall
	failures map[string]error
}

func (w *wslTestWriter) run(_ context.Context, executable string, args []string, path string, text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, wslRunCall{executable: executable, args: append([]string(nil), args...), path: path, text: text})
	return w.failures[executable]
}

func (w *wslTestWriter) recorded() []wslRunCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]wslRunCall(nil), w.calls...)
}

func wslTestClipboard(t *testing.T, writer *wslTestWriter, windows map[string]string, linux map[string]string, discovered *int) *WSLClipboard {
	t.Helper()
	return NewWSLClipboard(WSLClipboardOptions{
		SystemPath: func() (string, bool) { return "/usr/local/bin:/usr/bin", true },
		WindowsExecutable: func(name string) (string, bool) {
			if discovered != nil {
				*discovered++
			}
			path, ok := windows[name]
			return path, ok
		},
		LinuxExecutable: func(name string) (string, bool) {
			path, ok := linux[name]
			return path, ok
		},
		Run: writer.run,
	})
}

// An installed Linux powershell.exe adapter wins over both Windows writers and
// keeps the released command signature.
func TestWSLClipboardPrefersLinuxAdapterLikeRust(t *testing.T) {
	writer := &wslTestWriter{}
	discovered := 0
	clipboard := wslTestClipboard(t, writer,
		map[string]string{"clip.exe": "/mnt/c/Windows/System32/clip.exe", "powershell.exe": "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"},
		map[string]string{"powershell.exe": "/usr/bin/powershell.exe"}, &discovered)
	text := "héllo\nworld"
	if err := clipboard.Copy(text); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	calls := writer.recorded()
	if len(calls) != 1 {
		t.Fatalf("writer calls = %#v, want one", calls)
	}
	call := calls[0]
	if call.executable != "/usr/bin/powershell.exe" || call.text != text {
		t.Fatalf("adapter call = %#v", call)
	}
	if strings.Join(call.args, " ") != strings.Join(WSLClipboardPowerShellArgs, " ") {
		t.Fatalf("adapter args = %#v", call.args)
	}
	if call.path != "/usr/local/bin:/usr/bin" {
		t.Fatalf("adapter PATH = %q", call.path)
	}
	if discovered != 0 {
		t.Fatalf("Windows discovery ran %d times for an adapter copy", discovered)
	}
}

// Single-line ASCII copies use clip.exe with the padded payload and no extra
// arguments.
func TestWSLClipboardUsesClipForASCIILikeRust(t *testing.T) {
	writer := &wslTestWriter{}
	clipboard := wslTestClipboard(t, writer,
		map[string]string{"clip.exe": "/mnt/c/Windows/System32/clip.exe", "powershell.exe": "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"},
		nil, nil)
	if err := clipboard.Copy("abcd"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	calls := writer.recorded()
	if len(calls) != 1 {
		t.Fatalf("writer calls = %#v, want one", calls)
	}
	if calls[0].executable != "/mnt/c/Windows/System32/clip.exe" || calls[0].text != "abcd\x00" || len(calls[0].args) != 0 {
		t.Fatalf("clip call = %#v", calls[0])
	}
}

// A failing clip.exe falls back to PowerShell with the untouched text, and a
// double failure reports both writers.
func TestWSLClipboardFallsBackToPowerShellLikeRust(t *testing.T) {
	powershell := "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
	writer := &wslTestWriter{failures: map[string]error{"/mnt/c/Windows/System32/clip.exe": errors.New("exit status 1")}}
	clipboard := wslTestClipboard(t, writer, map[string]string{"clip.exe": "/mnt/c/Windows/System32/clip.exe", "powershell.exe": powershell}, nil, nil)
	if err := clipboard.Copy("abcd"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	calls := writer.recorded()
	if len(calls) != 2 || calls[0].executable != "/mnt/c/Windows/System32/clip.exe" || calls[1].executable != powershell {
		t.Fatalf("writer calls = %#v", calls)
	}
	if calls[1].text != "abcd" {
		t.Fatalf("PowerShell text = %q, want the unpadded copy", calls[1].text)
	}

	failing := &wslTestWriter{failures: map[string]error{
		"/mnt/c/Windows/System32/clip.exe": errors.New("exit status 1"),
		powershell:                         errors.New("exit status 9"),
	}}
	err := wslTestClipboard(t, failing, map[string]string{"clip.exe": "/mnt/c/Windows/System32/clip.exe", "powershell.exe": powershell}, nil, nil).Copy("abcd")
	if err == nil || !strings.Contains(err.Error(), "clip.exe") || !strings.Contains(err.Error(), "powershell.exe") {
		t.Fatalf("double failure = %v, want both writers named", err)
	}
}

// Text clip.exe cannot carry skips it entirely and copies through PowerShell.
func TestWSLClipboardSkipsClipForUnicodeLikeRust(t *testing.T) {
	powershell := "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
	writer := &wslTestWriter{}
	clipboard := wslTestClipboard(t, writer, map[string]string{"clip.exe": "/mnt/c/Windows/System32/clip.exe", "powershell.exe": powershell}, nil, nil)
	if err := clipboard.Copy("héllo\nworld"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	calls := writer.recorded()
	if len(calls) != 1 || calls[0].executable != powershell || calls[0].text != "héllo\nworld" {
		t.Fatalf("writer calls = %#v", calls)
	}
}

// Missing writers report the PowerShell gap after clip.exe is unavailable.
func TestWSLClipboardReportsMissingPowerShellLikeRust(t *testing.T) {
	writer := &wslTestWriter{}
	err := wslTestClipboard(t, writer, nil, nil, nil).Copy("abc")
	if err == nil || !strings.Contains(err.Error(), "PowerShell is unavailable") {
		t.Fatalf("missing writer error = %v", err)
	}
	if len(writer.recorded()) != 0 {
		t.Fatal("no writer should run without a resolved executable")
	}
}

// An expired budget fails the copy before any writer runs, and the outstanding
// discovery result is reused by the next copy instead of repeating the work.
func TestWSLClipboardExpiredSetupAndReuseLikeRust(t *testing.T) {
	var mu sync.Mutex
	discoveries := 0
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	var nowCalls int
	writer := &wslTestWriter{}
	clipboard := NewWSLClipboard(WSLClipboardOptions{
		SystemPath: func() (string, bool) {
			mu.Lock()
			discoveries++
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			return "/usr/bin", true
		},
		WindowsExecutable: func(name string) (string, bool) {
			return "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe", name == "powershell.exe"
		},
		Run: writer.run,
		Now: func() time.Time {
			nowCalls++
			if nowCalls > 1 {
				// Every later observation is past the five-second deadline.
				return start.Add(time.Hour)
			}
			return start
		},
	})
	err := clipboard.Copy("abc")
	if err == nil || !strings.Contains(err.Error(), "clipboard setup timed out") {
		t.Fatalf("expired setup error = %v", err)
	}

	// A fresh writer with a working clock shares the completed discovery.
	clipboard.options.Now = time.Now
	if err := clipboard.Copy("abc"); err != nil {
		t.Fatalf("second copy: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if discoveries != 1 {
		t.Fatalf("PATH discoveries = %d, want one shared setup", discoveries)
	}
}