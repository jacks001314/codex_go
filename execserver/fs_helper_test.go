package execserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestBoundedBufferRetainsBoundPrefix(t *testing.T) {
	var buf boundedBuffer
	// Writing more than the cap must not grow the retained prefix.
	for i := 0; i < 4; i++ {
		if _, err := buf.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	if got := buf.String(); len(got) > maxFSHelperStderrBytes {
		t.Fatalf("bounded buffer len = %d, want <= %d", len(got), maxFSHelperStderrBytes)
	}
	if !strings.HasPrefix(buf.String(), "0123456789") {
		t.Fatalf("bounded buffer dropped leading stderr: %q", buf.String())
	}
}

func TestReadHelperResponseReadsNewlineDelimited(t *testing.T) {
	line, err := readHelperResponse(strings.NewReader(`{"status":"ok"}` + "\n"))
	if err != nil {
		t.Fatalf("readHelperResponse() error = %v", err)
	}
	if got := string(line); got != `{"status":"ok"}` {
		t.Fatalf("line = %q", got)
	}

	// A helper that closes stdout without responding must surface an error.
	if _, err := readHelperResponse(bytes.NewReader(nil)); err == nil {
		t.Fatal("expected error for closed stdout without a response")
	}
}

// TestFSHelperEnvironmentPreservesWindowsSystemRootLikeRust mirrors Rust
// #56468's helper environment filter: Windows keeps PATH, SystemDrive,
// SystemRoot and LOCALAPPDATA regardless of casing, while unrelated variables
// stay out.
func TestFSHelperEnvironmentPreservesWindowsSystemRootLikeRust(t *testing.T) {
	got := fsHelperEnvironmentFrom([]string{
		`Path=C:\Windows\System32`,
		`LocalAppData=C:\Users\test\AppData\Local`,
		`SystemDrive=C:`,
		`systemROOT=C:\Windows`,
		`PATH_INJECTION=bad`,
		`OPENAI_API_KEY=secret`,
		`TMPDIR=C:\Temp`,
	}, "windows")
	want := map[string]string{
		"Path":         `C:\Windows\System32`,
		"LocalAppData": `C:\Users\test\AppData\Local`,
		"SystemDrive":  "C:",
		"systemROOT":   `C:\Windows`,
		"TMPDIR":       `C:\Temp`,
	}
	if len(got) != len(want) {
		t.Fatalf("helper env = %#v, want %#v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("helper env[%q] = %q, want %q", key, got[key], value)
		}
	}

	// Non-Windows hosts do not inherit the Windows platform entries.
	if env := fsHelperEnvironmentFrom([]string{`SystemRoot=C:\Windows`, `PATH=/usr/bin`}, "linux"); env["SystemRoot"] != "" || env["PATH"] != "/usr/bin" {
		t.Fatalf("linux helper env = %#v", env)
	}

	// macOS keeps the CoreFoundation locale variable the helper consults.
	if env := fsHelperEnvironmentFrom([]string{"__CF_USER_TEXT_ENCODING=0x1F5:0:0"}, "darwin"); env["__CF_USER_TEXT_ENCODING"] != "0x1F5:0:0" {
		t.Fatalf("darwin helper env = %#v", env)
	}
}
