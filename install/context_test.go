package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectsStandaloneReleaseLayout(t *testing.T) {
	home := t.TempDir()
	releaseDir := filepath.Join(home, "packages", "standalone", "releases", "1.2.3-x86")
	resourcesDir := filepath.Join(releaseDir, resourcesDirname)
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	exePath := filepath.Join(releaseDir, "codex")
	if err := os.WriteFile(exePath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}
	hostPath := filepath.Join(releaseDir, codeModeHostExecutableName())
	if err := os.WriteFile(hostPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(host) error = %v", err)
	}
	rgPath := filepath.Join(resourcesDir, defaultRGCommand())
	if err := os.WriteFile(rgPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(rg) error = %v", err)
	}
	context := FromExe(false, exePath, false, false, home)
	if context.Method.Kind != InstallStandalone {
		t.Fatalf("method = %+v, want standalone", context.Method)
	}
	if context.RGCommand() != rgPath {
		t.Fatalf("RGCommand() = %q, want %q", context.RGCommand(), rgPath)
	}
	if got := context.CodeModeHostProgramFromExe(exePath); got != hostPath {
		t.Fatalf("CodeModeHostProgramFromExe() = %q, want %q", got, hostPath)
	}
}

func TestBundledResourceDirFindsDirectory(t *testing.T) {
	packageDir := t.TempDir()
	resourcesDir := filepath.Join(packageDir, resourcesDirname)
	systemSkillsDir := filepath.Join(resourcesDir, "skills", ".system")
	if err := os.MkdirAll(systemSkillsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(system skills) error = %v", err)
	}
	context := &InstallContext{PackageLayout: &CodexPackageLayout{ResourcesDir: &resourcesDir}}
	if got := context.BundledResourceDir(filepath.Join("skills", ".system")); got == nil || *got != systemSkillsDir {
		t.Fatalf("BundledResourceDir() = %#v, want %q", got, systemSkillsDir)
	}
	if got := context.BundledResourceDir(filepath.Join("skills", "missing")); got != nil {
		t.Fatalf("BundledResourceDir(missing) = %#v, want nil", got)
	}
}

func TestDetectsPackageLayout(t *testing.T) {
	packageDir := t.TempDir()
	binDir := filepath.Join(packageDir, binDirname)
	resourcesDir := filepath.Join(packageDir, resourcesDirname)
	pathDir := filepath.Join(packageDir, pathDirname)
	for _, dir := range []string{binDir, resourcesDir, pathDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, packageMetadataFilename), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
	exePath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(exePath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}
	hostPath := filepath.Join(binDir, codeModeHostExecutableName())
	if err := os.WriteFile(hostPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(host) error = %v", err)
	}
	resourceHostPath := filepath.Join(resourcesDir, codeModeHostExecutableName())
	if err := os.WriteFile(resourceHostPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(resource host) error = %v", err)
	}
	layout := PackageLayoutFromExe(exePath)
	if layout == nil || layout.PackageDir != packageDir || layout.ResourcesDir == nil || layout.PathDir == nil {
		t.Fatalf("layout = %+v", layout)
	}
	context := &InstallContext{Method: InstallMethod{Kind: InstallOther}, PackageLayout: layout}
	if got := context.CodeModeHostProgramFromExe(exePath); got != resourceHostPath {
		t.Fatalf("CodeModeHostProgramFromExe() = %q, want %q", got, resourceHostPath)
	}
}

func TestCodeModeHostProgramFallsBackNextToCurrentExecutable(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, managedCodexFileName())
	context := &InstallContext{Method: InstallMethod{Kind: InstallOther}}
	want := filepath.Join(dir, codeModeHostExecutableName())
	if got := context.CodeModeHostProgramFromExe(exePath); got != want {
		t.Fatalf("CodeModeHostProgramFromExe() = %q, want %q", got, want)
	}
	if got := context.CodeModeHostProgramFromExe(""); got != codeModeHostExecutableName() {
		t.Fatalf("unknown executable fallback = %q", got)
	}
}

func TestCodeModeHostProgramRejectsDirectoryCandidate(t *testing.T) {
	packageDir := t.TempDir()
	binDir := filepath.Join(packageDir, binDirname)
	if err := os.MkdirAll(filepath.Join(binDir, codeModeHostExecutableName()), 0o755); err != nil {
		t.Fatal(err)
	}
	fallbackExe := filepath.Join(packageDir, "fallback-codex")
	context := &InstallContext{PackageLayout: &CodexPackageLayout{PackageDir: packageDir, BinDir: binDir}}
	want := filepath.Join(packageDir, codeModeHostExecutableName())
	if got := context.CodeModeHostProgramFromExe(fallbackExe); got != want {
		t.Fatalf("CodeModeHostProgramFromExe() = %q, want %q", got, want)
	}
}

func TestCodeModeHostProgramAcceptsSymlinkToFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on some Windows hosts")
	}
	packageDir := t.TempDir()
	binDir := filepath.Join(packageDir, binDirname)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(packageDir, "host-target")
	if err := os.WriteFile(target, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	hostPath := filepath.Join(binDir, codeModeHostExecutableName())
	if err := os.Symlink(target, hostPath); err != nil {
		t.Fatal(err)
	}
	context := &InstallContext{PackageLayout: &CodexPackageLayout{PackageDir: packageDir, BinDir: binDir}}
	if got := context.CodeModeHostProgramFromExe(filepath.Join(binDir, "codex")); got != hostPath {
		t.Fatalf("CodeModeHostProgramFromExe() = %q, want %q", got, hostPath)
	}
}

func TestManagedByPackageManagersWins(t *testing.T) {
	if got := FromExe(false, "codex", true, false, "").Method.Kind; got != InstallNPM {
		t.Fatalf("npm method = %q", got)
	}
	if got := FromExe(false, "codex", false, true, "").Method.Kind; got != InstallBun {
		t.Fatalf("bun method = %q", got)
	}
}

func TestManagedCodexBin(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "packages", "standalone", "current", managedCodexFileName())
	if got := ManagedCodexBin(home); got != want {
		t.Fatalf("ManagedCodexBin() = %q, want %q", got, want)
	}
}

func TestParseCodexVersion(t *testing.T) {
	version, err := ParseCodexVersion("codex 1.2.3\n")
	if err != nil {
		t.Fatalf("ParseCodexVersion() error = %v", err)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q", version)
	}
	if _, err := ParseCodexVersion("codex\n"); err == nil {
		t.Fatal("ParseCodexVersion malformed output returned nil error")
	}
}

func TestExecutableIdentityUsesBinaryContents(t *testing.T) {
	old := ExecutableIdentityFromBytes([]byte("old"))
	same := ExecutableIdentityFromBytes([]byte("old"))
	newIdentity := ExecutableIdentityFromBytes([]byte("new"))
	if old != same {
		t.Fatalf("same bytes identity mismatch: %#v %#v", old, same)
	}
	if old == newIdentity {
		t.Fatalf("different bytes produced same identity: %#v", old)
	}
}

func TestDaemonSettingsUseCamelCaseJSON(t *testing.T) {
	data, err := json.Marshal(DaemonSettings{RemoteControlEnabled: true})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"remoteControlEnabled":true}` {
		t.Fatalf("settings JSON = %s", data)
	}
}
