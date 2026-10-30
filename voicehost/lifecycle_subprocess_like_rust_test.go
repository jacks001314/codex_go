package voicehost

import (
	"bytes"
	"encoding/binary"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestHelperLifecycleSubprocessLikeRust mirrors Rust #41897
// (codex-rs/voice-host/tests/lifecycle.rs): the real helper binary is exercised
// as a subprocess for the same-build handshake, an explicit close, parent pipe
// loss, malformed frames, an incompatible build and out-of-order messages.
func TestHelperLifecycleSubprocessLikeRust(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the Go toolchain is unavailable")
	}
	const buildCommit = "lifecycle-commit"
	root := buildPackagedHelper(t, buildCommit)
	executable := filepath.Join(root, "codex-resources", "voice", "bin", voiceHelperName())

	frames := func(messages ...Message) []byte {
		t.Helper()
		var buffer bytes.Buffer
		for _, message := range messages {
			if err := WriteMessage(&buffer, message); err != nil {
				t.Fatalf("encode %s: %v", message.Type, err)
			}
		}
		return buffer.Bytes()
	}
	// Message refuses to encode a protocol other than 1, so a wire-level
	// invalid hello is framed by hand.
	rawFrame := func(payload string) []byte {
		t.Helper()
		frame := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
		return append(frame, payload...)
	}

	run := func(t *testing.T, input []byte) (stdout []byte, stderr []byte, success bool) {
		t.Helper()
		command := exec.Command(executable)
		stdin, err := command.StdinPipe()
		if err != nil {
			t.Fatalf("stdin pipe: %v", err)
		}
		var out, errOut bytes.Buffer
		command.Stdout = &out
		command.Stderr = &errOut
		if err := command.Start(); err != nil {
			t.Fatalf("start helper: %v", err)
		}
		if len(input) > 0 {
			if _, err := stdin.Write(input); err != nil {
				t.Fatalf("write control frames: %v", err)
			}
		}
		if err := stdin.Close(); err != nil {
			t.Fatalf("close stdin: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case err := <-done:
			return out.Bytes(), errOut.Bytes(), err == nil
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			t.Fatal("helper exceeded the lifecycle deadline")
			return nil, nil, false
		}
	}

	hello := NewHello(1, buildCommit)

	t.Run("explicit close is acknowledged", func(t *testing.T) {
		stdout, stderr, success := run(t, frames(hello, NewSimpleMessage(TypeClose)))
		if !success {
			t.Fatalf("helper failed: stderr %q", stderr)
		}
		if want := frames(NewSimpleMessage(TypeReady), NewSimpleMessage(TypeClosed)); !bytes.Equal(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
		if len(stderr) != 0 {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("parent pipe loss retires cleanly", func(t *testing.T) {
		stdout, stderr, success := run(t, frames(hello))
		if !success {
			t.Fatalf("helper failed: stderr %q", stderr)
		}
		if want := frames(NewSimpleMessage(TypeReady)); !bytes.Equal(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
		if len(stderr) != 0 {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})

	invalidJSON := []byte{0, 0, 0, 22}
	invalidJSON = append(invalidJSON, []byte("sensitive-invalid-json")...)
	for _, tc := range []struct {
		name  string
		input []byte
	}{
		{name: "oversized frame", input: []byte{0xff, 0xff, 0xff, 0xff}},
		{name: "truncated header", input: []byte{0, 0}},
		{name: "malformed json", input: invalidJSON},
		{name: "wrong build", input: frames(NewHello(1, "wrong-build"))},
		{name: "wrong protocol", input: rawFrame(`{"type":"hello","protocol":99,"buildCommit":"` + buildCommit + `"}`)},
		{name: "close before hello", input: frames(NewSimpleMessage(TypeClose))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, success := run(t, tc.input)
			if success {
				t.Fatalf("helper accepted invalid input (stdout %q)", stdout)
			}
			if len(stdout) != 0 || len(stderr) != 0 {
				t.Fatalf("invalid input leaked output: stdout %q stderr %q", stdout, stderr)
			}
		})
	}
}
