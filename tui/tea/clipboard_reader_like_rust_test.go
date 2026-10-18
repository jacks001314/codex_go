package tea

import (
	"bufio"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tuiapp "codex_go/tui/app"
)

const (
	clipboardReaderHelperEnv   = "CODEX_CLIPBOARD_READER_HELPER"
	clipboardReaderTimeoutEnv  = "CODEX_CLIPBOARD_READER_SLEEP"
	clipboardReaderFailFileEnv = "CODEX_CLIPBOARD_READER_FAIL_FILE"
)

func TestClipboardReaderHelperProcess(t *testing.T) {
	if os.Getenv(clipboardReaderHelperEnv) != "1" {
		return
	}
	payloads := clipboardReaderTestPayloads()
	input := bufio.NewReader(os.Stdin)
	for index, payload := range payloads {
		if _, err := input.ReadString('\n'); err != nil {
			os.Exit(0)
		}
		if index == 0 {
			if marker := os.Getenv(clipboardReaderFailFileEnv); marker != "" {
				if _, err := os.Stat(marker); os.IsNotExist(err) {
					_ = os.WriteFile(marker, []byte("failed"), 0o600)
					os.Exit(1)
				}
			}
		}
		if os.Getenv(clipboardReaderTimeoutEnv) == "1" {
			time.Sleep(2 * time.Second)
		}
		var header [4]byte
		binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
		if _, err := os.Stdout.Write(header[:]); err != nil {
			os.Exit(1)
		}
		if _, err := os.Stdout.WriteString(payload); err != nil {
			os.Exit(1)
		}
	}
	os.Exit(0)
}

func clipboardReaderTestPayloads() []string {
	return []string{
		" café\r\nsecond line\n",
		" \tthird\n",
		strings.Repeat("x", 128*1024) + "\n",
		"",
	}
}

func newTestClipboardCommandReader(t *testing.T, extraEnv ...string) *clipboardCommandReader {
	t.Helper()
	start := func() (*clipboardProcess, error) {
		command := exec.Command(os.Args[0], "-test.run=^TestClipboardReaderHelperProcess$")
		command.Env = append(os.Environ(), append([]string{clipboardReaderHelperEnv + "=1"}, extraEnv...)...)
		return startClipboardProcess(command)
	}
	return newClipboardCommandReader(start)
}

func TestClipboardCommandReaderReusesProcessLikeRust(t *testing.T) {
	reader := newTestClipboardCommandReader(t)
	defer reader.Close()
	reader.Warm()
	time.Sleep(50 * time.Millisecond)
	for _, want := range clipboardReaderTestPayloads() {
		got, err := reader.readUntil(time.Now().Add(5 * time.Second))
		if err != nil {
			t.Fatalf("read error: %v", err)
		}
		if got != want {
			t.Fatalf("read = %q (len %d), want %q (len %d)", got, len(got), want, len(want))
		}
	}
}

func TestClipboardCommandReaderTimesOutLikeRust(t *testing.T) {
	reader := newTestClipboardCommandReader(t, clipboardReaderTimeoutEnv+"=1")
	defer reader.Close()
	if text, err := reader.readUntil(time.Now().Add(50 * time.Millisecond)); err != tuiapp.ErrRightClickPasteReadTimeout || text != "" {
		t.Fatalf("timeout read = %q/%v, want timeout", text, err)
	}
}

func TestClipboardCommandReaderRestartsAfterErrorLikeRust(t *testing.T) {
	reader := newTestClipboardCommandReader(t, clipboardReaderFailFileEnv+"="+filepath.Join(t.TempDir(), "failed"))
	defer reader.Close()
	if _, err := reader.readUntil(time.Now().Add(5 * time.Second)); err == nil {
		t.Fatal("failed reader did not report an error")
	}
	got, err := reader.readUntil(time.Now().Add(5 * time.Second))
	if err != nil {
		t.Fatalf("restarted read error: %v", err)
	}
	if want := clipboardReaderTestPayloads()[0]; got != want {
		t.Fatalf("restarted read = %q, want %q", got, want)
	}
}

func TestClipboardReaderWarmupPolicyLikeRust(t *testing.T) {
	environment := tuiapp.PasteEnvironment{PlatformDefault: true, WSL: true}
	if !shouldWarmClipboardReader(false, true, environment, tuiapp.RightClickPasteAuto) {
		t.Fatal("eligible WSL owned-screen session did not prewarm")
	}
	for name, allowed := range map[string]bool{
		"custom reader":  shouldWarmClipboardReader(true, true, environment, tuiapp.RightClickPasteAuto),
		"inline screen":  shouldWarmClipboardReader(false, false, environment, tuiapp.RightClickPasteAuto),
		"paste disabled": shouldWarmClipboardReader(false, true, environment, tuiapp.RightClickPasteOff),
		"ssh session":    shouldWarmClipboardReader(false, true, tuiapp.PasteEnvironment{PlatformDefault: true, WSL: true, SSH: true}, tuiapp.RightClickPasteAuto),
	} {
		if allowed {
			t.Fatalf("%s unexpectedly prewarmed", name)
		}
	}
}
