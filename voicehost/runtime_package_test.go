package voicehost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeHelpPackage creates a package layout with the named helper contents.
func writeHelpPackage(t *testing.T, contents []byte) string {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "codex-resources", "voice", "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if contents != nil {
		if err := os.WriteFile(filepath.Join(directory, voiceHelperName()), contents, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestInspectRuntimePackageReportsMissingHelper(t *testing.T) {
	root := writeHelpPackage(t, nil)
	status, err := InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.HelperPresent || status.SupportsMedia() {
		t.Fatalf("status = %#v", status)
	}
	if _, err := VerifyRuntimePackage(root); err == nil {
		t.Fatal("a package without a helper verified successfully")
	}
}

// buildPackagedHelper builds cmd/codex-voice-host into a package layout with
// the given stamped identity and returns the package root.
func buildPackagedHelper(t *testing.T, buildCommit string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the Go toolchain is unavailable")
	}
	root := writeHelpPackage(t, nil)
	binary := filepath.Join(root, "codex-resources", "voice", "bin", voiceHelperName())
	build := exec.Command("go", "build", "-ldflags", "-X main.buildCommit="+buildCommit, "-o", binary, "codex_go/cmd/codex-voice-host")
	build.Dir = moduleRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, output)
	}
	return root
}

// TestPackagedHelperCompletesSameBuildHandshake proves the packaged layout and
// the same-build contract end to end: the inspected commit drives the parent's
// hello, a matching helper answers, and a mismatched one is refused.
func TestPackagedHelperCompletesSameBuildHandshake(t *testing.T) {
	root := buildPackagedHelper(t, "test-commit")
	status, err := InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.BuildCommit != "test-commit" {
		t.Fatalf("inspected build commit = %q", status.BuildCommit)
	}
	executable := status.HelperPath
	if !filepath.IsAbs(executable) {
		executable = filepath.Join(root, filepath.FromSlash(VoiceRuntimeDirectory), "bin", voiceHelperName())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	host, err := Connect(ctx, executable, "test-commit")
	if err != nil {
		t.Fatalf("matching handshake failed: %v", err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatalf("closing a matched helper failed: %v", err)
	}

	if _, err := Connect(ctx, executable, "other-commit"); err == nil {
		t.Fatal("a mismatched helper completed the handshake")
	}
}

// TestPackagedVoiceRuntimeCarriesCodec proves the packaged layout a release
// produces: the helper plus the codec it loads at runtime, both resolvable from
// the package root and usable.
func TestPackagedVoiceRuntimeCarriesCodec(t *testing.T) {
	prepared := preparedOpusLibrary(t)
	if _, err := os.Stat(prepared); err != nil {
		t.Skipf("prepared Opus codec is unavailable at %s; run third_party/voice/prepare_opus.py", prepared)
	}
	root := buildPackagedHelper(t, "codec-commit")
	library := OpusLibraryPath(root)
	if err := os.MkdirAll(filepath.Dir(library), 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, payload, 0o755); err != nil {
		t.Fatal(err)
	}

	status, err := InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.HelperPresent || !status.CodecPresent || !status.SupportsMedia() {
		t.Fatalf("status = %#v", status)
	}
	if status.CodecPath != library {
		t.Fatalf("codec path = %q", status.CodecPath)
	}

	// The packaged codec is directly usable, which is what the helper does at
	// startup.
	codec, err := LoadPackagedVoiceCodec(root)
	if err != nil {
		t.Fatalf("load packaged codec: %v", err)
	}
	defer func() {
		if closer, ok := codec.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()
	encoded, err := codec.Encode(toneFrame(11000, 660))
	if err != nil {
		t.Fatalf("encode with packaged codec: %v", err)
	}
	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("decode with packaged codec: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("the packaged codec decoded no samples")
	}

	// The packaged helper still completes the same-build handshake.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	host, err := Connect(ctx, status.HelperPath, "codec-commit")
	if err != nil {
		t.Fatalf("packaged helper handshake failed: %v", err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatalf("closing the packaged helper failed: %v", err)
	}
}

func TestInspectRuntimePackageReportsHelperWithoutProbe(t *testing.T) {
	root := writeHelpPackage(t, []byte("not an executable"))
	status, err := InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.HelperPresent {
		t.Fatalf("status = %#v", status)
	}
	if status.BuildCommit != "" {
		t.Fatalf("build commit = %q, want empty for an unprobeable helper", status.BuildCommit)
	}
	if status.SupportsMedia() {
		t.Fatal("a package without a codec reported media support")
	}
	if _, err := VerifyRuntimePackage(root); err != nil {
		t.Fatalf("a package with a helper failed verification: %v", err)
	}
}

func TestInspectRuntimePackageRequiresDirectory(t *testing.T) {
	if _, err := InspectRuntimePackage("   "); err == nil {
		t.Fatal("an empty package directory was accepted")
	}
}

func TestSupportsMediaRequiresCodec(t *testing.T) {
	root := writeHelpPackage(t, []byte("helper"))
	status, err := InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.CodecPresent {
		t.Fatalf("a package without a codec reported one: %#v", status)
	}
	if status.SupportsMedia() {
		t.Fatal("a package without a codec supported media")
	}

	// A packaged codec is the artifact the helper loads at runtime.
	library := OpusLibraryPath(root)
	if err := os.MkdirAll(filepath.Dir(library), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, []byte("codec"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, err = InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.CodecPresent || status.CodecPath != library || !status.SupportsMedia() {
		t.Fatalf("status = %#v", status)
	}
	if IsSupportedPackage(root) != (runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows") {
		t.Fatalf("IsSupportedPackage(%q) = %v", root, IsSupportedPackage(root))
	}
}

// TestPackagedHelperReportsBuildCommit builds the real helper and probes it, so
// the same-build handshake and the inspection path are exercised end to end.
func TestPackagedHelperReportsBuildCommit(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the Go toolchain is unavailable")
	}
	root := writeHelpPackage(t, nil)
	binary := filepath.Join(root, "codex-resources", "voice", "bin", voiceHelperName())
	build := exec.Command("go", "build", "-o", binary, "codex_go/cmd/codex-voice-host")
	build.Dir = moduleRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, output)
	}
	status, err := InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.HelperPresent || !strings.HasPrefix(status.HelperPath, root) {
		t.Fatalf("status = %#v", status)
	}
	if status.BuildCommit != "dev" {
		t.Fatalf("build commit = %q, want dev for an unstamped build", status.BuildCommit)
	}
}

// moduleRoot walks up from the test's working directory to the module root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("module root not found")
		}
		directory = parent
	}
}

// TestStagedReleaseVoiceRuntime validates a package produced by
// scripts/build.ps1 or scripts/build.sh. It runs only when
// CODEX_VOICE_STAGE_DIR points at the staged package root, which keeps the
// release layout verifiable without invoking the release scripts from a test.
func TestStagedReleaseVoiceRuntime(t *testing.T) {
	stage := strings.TrimSpace(os.Getenv("CODEX_VOICE_STAGE_DIR"))
	if stage == "" {
		t.Skip("CODEX_VOICE_STAGE_DIR is not set")
	}
	status, err := InspectRuntimePackage(stage)
	if err != nil {
		t.Fatal(err)
	}
	if !status.SupportsMedia() {
		t.Fatalf("the staged runtime carries no usable voice media: %#v", status)
	}
	if status.BuildCommit == "" {
		t.Fatal("the staged helper reported no build commit")
	}

	codec, err := LoadPackagedVoiceCodec(stage)
	if err != nil {
		t.Fatalf("load staged codec: %v", err)
	}
	encoded, err := codec.Encode(toneFrame(10000, 500))
	if err != nil {
		t.Fatalf("encode with staged codec: %v", err)
	}
	decoded, err := codec.Decode(encoded)
	if err != nil {
		t.Fatalf("decode with staged codec: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("the staged codec decoded no samples")
	}
	if closer, ok := codec.(interface{ Close() error }); ok {
		_ = closer.Close()
	}

	// The inspected commit is exactly what the parent sends, so the staged
	// helper must accept it without extra configuration.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	host, err := Connect(ctx, status.HelperPath, status.BuildCommit)
	if err != nil {
		t.Fatalf("staged helper rejected its own build commit: %v", err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatalf("closing the staged helper failed: %v", err)
	}
}

// TestPackagedHelperAdvertisesVoiceAudioTrack proves the real helper attaches
// the Opus media track before gathering its offer, so the peer negotiates audio
// and not just the control channel.
func TestPackagedHelperAdvertisesVoiceAudioTrack(t *testing.T) {
	root := buildPackagedHelper(t, "audio-commit")
	status, err := InspectRuntimePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	host, err := Connect(ctx, status.HelperPath, "audio-commit")
	if err != nil {
		t.Fatalf("handshake failed: %v", err)
	}
	description, err := host.StartTransport(ctx)
	if err != nil {
		_ = host.Close(context.Background())
		t.Fatalf("start transport: %v", err)
	}
	offer := description.SDP()
	if !strings.Contains(offer, "m=audio") {
		t.Fatalf("offer advertises no audio track:\n%s", offer)
	}
	if !strings.Contains(strings.ToLower(offer), "opus/48000") {
		t.Fatalf("offer does not negotiate Opus:\n%s", offer)
	}
	// Payload type 111 is the contract both implementations use.
	if !strings.Contains(offer, "a=rtpmap:111 opus/48000") {
		t.Fatalf("offer does not use payload type 111:\n%s", offer)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}
