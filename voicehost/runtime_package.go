package voicehost

// Packaged voice runtime inspection. Rust gates voice on a private native
// runtime that ships inside the package (helper plus bundled GStreamer). The Go
// helper ships the process and its device backend, and reports whether an audio
// codec is present so callers can distinguish "no helper" from "no codec".

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// VoiceRuntimeDirectory is the package-relative directory that owns the voice
// runtime.
const VoiceRuntimeDirectory = "codex-resources/voice"

// voiceHelperName is the packaged helper executable.
func voiceHelperName() string {
	if runtime.GOOS == "windows" {
		return "codex-voice-host.exe"
	}
	return "codex-voice-host"
}

// RuntimePackage describes what a package directory provides for voice.
type RuntimePackage struct {
	// PackageDir is the resolved package root.
	PackageDir string
	// HelperPath is the resolved helper executable path.
	HelperPath string
	// HelperPresent reports whether the helper exists and is not a directory.
	HelperPresent bool
	// CodecPath is the resolved audio codec path when the package carries one.
	CodecPath string
	// CodecPresent reports whether the package carries an audio codec. The
	// helper still runs the control plane without one.
	CodecPresent bool
	// BuildCommit is the helper's stamped build identity, empty when unknown.
	BuildCommit string
}

// SupportsMedia reports whether both a helper and an audio codec are present.
func (p RuntimePackage) SupportsMedia() bool {
	return p.HelperPresent && p.CodecPresent
}

// InspectRuntimePackage reports what a package directory provides for voice. It
// never loads native code or opens an audio device.
func InspectRuntimePackage(packageDir string) (RuntimePackage, error) {
	trimmed := strings.TrimSpace(packageDir)
	if trimmed == "" {
		return RuntimePackage{}, errors.New("voice package directory is required")
	}
	root, err := filepath.Abs(trimmed)
	if err != nil {
		return RuntimePackage{}, fmt.Errorf("resolve voice package: %w", err)
	}
	status := RuntimePackage{
		PackageDir: root,
	}
	helperPath := filepath.Join(root, filepath.FromSlash(VoiceRuntimeDirectory), "bin", voiceHelperName())
	if info, statErr := os.Stat(helperPath); statErr == nil && !info.IsDir() {
		status.HelperPresent = true
		status.HelperPath = helperPath
		status.BuildCommit = helperBuildCommit(helperPath)
	}
	codecPath := OpusLibraryPath(root)
	if info, statErr := os.Stat(codecPath); statErr == nil && !info.IsDir() {
		status.CodecPresent = true
		status.CodecPath = codecPath
	}
	return status, nil
}

// VerifyRuntimePackage inspects the package and fails when the helper needed to
// start a voice session is missing.
func VerifyRuntimePackage(packageDir string) (RuntimePackage, error) {
	status, err := InspectRuntimePackage(packageDir)
	if err != nil {
		return RuntimePackage{}, err
	}
	if !status.HelperPresent {
		return status, fmt.Errorf("voice helper is missing from %s", filepath.Join(packageDir, filepath.FromSlash(VoiceRuntimeDirectory), "bin"))
	}
	return status, nil
}

// helperBuildCommit asks the helper for its stamped build identity. A helper
// that cannot answer reports an empty commit instead of failing inspection.
func helperBuildCommit(helperPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), helperProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, helperPath, "--build-commit")
	configureHiddenProcess(command)
	command.Dir = filepath.Dir(helperPath)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// helperProbeTimeout bounds the build-identity probe.
const helperProbeTimeout = 5 * time.Second
