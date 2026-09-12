package voicehost

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVoicePackageDirBesideExecutableFindsDevelopmentLayout(t *testing.T) {
	root := t.TempDir()
	helperDir := filepath.Join(root, "codex-resources", "voice", "bin")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "codex-voice-host"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(helperDir, name), []byte("helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "codex.exe")
	if err := os.WriteFile(exe, []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := voicePackageDirBesideExecutable(exe); got != root {
		t.Fatalf("development package dir = %q, want %q", got, root)
	}
	empty := t.TempDir()
	if got := voicePackageDirBesideExecutable(filepath.Join(empty, "codex.exe")); got != "" {
		t.Fatalf("layout without helper = %q, want empty", got)
	}
	if got := voicePackageDirBesideExecutable(""); got != "" {
		t.Fatalf("empty executable = %q, want empty", got)
	}
}

func TestSupportMessageAgreesWithIsSupported(t *testing.T) {
	message := SupportMessage()
	if (message == "") != IsSupported() {
		t.Fatalf("SupportMessage()=%q disagrees with IsSupported()=%v", message, IsSupported())
	}
	if message == "" || !supportedPlatform() {
		return
	}
	if message == "Voice requires macOS, an MSVC-based Windows build, or a glibc-based Linux build." {
		t.Fatalf("supported platform reported the platform sentence: %q", message)
	}
}
