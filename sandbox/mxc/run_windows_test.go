//go:build windows

package mxc

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestRunMainReportsUndecodableLauncherRequestsLikeRust(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunMain(strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("RunMain() = %d, want 1", code)
	}
	want := LauncherFailurePrefix + "invalid MXC launcher request\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunMainFailsClosedAfterValidatingNativeRequestLikeRust(t *testing.T) {
	t.Setenv("SystemDrive", "C:")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	t.Setenv("SystemRoot", `C:\Windows`)

	policyCWD := t.TempDir()
	env := map[string]string{"CUSTOM": "value"}
	if _, err := CreateCommandArgs(
		[]string{"cmd.exe", "/c", "echo", "hi"},
		sandbox.WorkspaceWritePermissionProfile(),
		policyCWD,
		nil,
		env,
	); err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	for key, value := range env {
		t.Setenv(key, value)
	}

	var stdout, stderr bytes.Buffer
	if code := RunMain(strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("RunMain() = %d, want 1", code)
	}
	// The request decodes and builds; only the native launch is unavailable.
	want := LauncherFailurePrefix + NativeUnavailableMessage + "\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestDecodeEnvironmentBlockRejectsLossyValuesLikeRust(t *testing.T) {
	block := func(entries ...[]uint16) []uint16 {
		var units []uint16
		for _, entry := range entries {
			units = append(units, entry...)
			units = append(units, 0)
		}
		return append(units, 0)
	}
	ascii := func(value string) []uint16 {
		units := make([]uint16, 0, len(value))
		for _, ch := range value {
			units = append(units, uint16(ch))
		}
		return units
	}

	t.Run("accepts valid pairs", func(t *testing.T) {
		units := block(ascii("KEY=value"), ascii("=C:=C:\\dir"))
		buffer := make([]uint16, len(units))
		copy(buffer, units)
		env, err := decodeEnvironmentBlock(&buffer[0])
		if err != nil {
			t.Fatalf("decodeEnvironmentBlock() error = %v", err)
		}
		if env["KEY"] != "value" {
			t.Fatalf("env = %#v", env)
		}
		if _, ok := env[""]; ok {
			t.Fatalf("per-drive entry became a variable: %#v", env)
		}
	})

	t.Run("rejects a lone high surrogate name", func(t *testing.T) {
		name := append([]uint16{0xD800}, ascii("K")...)
		units := block(append(append(name, '='), ascii("v")...))
		buffer := make([]uint16, len(units))
		copy(buffer, units)
		if _, err := decodeEnvironmentBlock(&buffer[0]); err == nil ||
			!strings.Contains(err.Error(), "environment variable names") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("rejects a lone low surrogate value", func(t *testing.T) {
		units := block(append(ascii("K="), 0xDC00))
		buffer := make([]uint16, len(units))
		copy(buffer, units)
		if _, err := decodeEnvironmentBlock(&buffer[0]); err == nil ||
			!strings.Contains(err.Error(), "environment variable values") {
			t.Fatalf("err = %v", err)
		}
	})
}
