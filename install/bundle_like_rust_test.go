package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// canonicalBundleTestPath resolves a fixture path the way the install context
// does, so assertions survive temp-directory symlinks (macOS /var) and Windows
// short names.
func canonicalBundleTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", path, err)
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatalf("Abs(%s) error = %v", resolved, err)
	}
	return filepath.Clean(absolute)
}

// TestBundleExecutablePreservesPackageLayoutAndInstallMethodLikeRust ports Rust
// `bundle_executable_preserves_package_layout_and_install_method` (#42068): an
// executable inside CodexCLI.app/Contents/MacOS still resolves the surrounding
// standalone package layout and install method from the sibling bin directory.
func TestBundleExecutablePreservesPackageLayoutAndInstallMethodLikeRust(t *testing.T) {
	home := t.TempDir()
	packageDir := filepath.Join(home, "packages", standalonePackagesDirname, releasesDirname, "test")
	executable := filepath.Join(packageDir, bundleAppDirname, bundleContentsDirname, bundleMacOSDirname, "codex")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatalf("MkdirAll(bundle) error = %v", err)
	}
	if err := os.WriteFile(executable, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(executable) error = %v", err)
	}
	for _, directory := range []string{binDirname, resourcesDirname, pathDirname} {
		if err := os.MkdirAll(filepath.Join(packageDir, directory), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", directory, err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, packageMetadataFilename), []byte(`{"version":"1.2.3"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}

	context := FromExe(true, executable, false, false, false, false, home)
	canonicalPackage := canonicalBundleTestPath(t, packageDir)
	binDir := filepath.Join(canonicalPackage, binDirname)
	resourcesDir := filepath.Join(canonicalPackage, resourcesDirname)
	pathDir := filepath.Join(canonicalPackage, pathDirname)

	layout := context.PackageLayout
	if layout == nil {
		t.Fatal("PackageLayout = nil, want the outer package layout")
	}
	if got := canonicalBundleTestPath(t, layout.PackageDir); got != canonicalPackage {
		t.Fatalf("PackageDir = %q, want %q", got, canonicalPackage)
	}
	if got := canonicalBundleTestPath(t, layout.BinDir); got != binDir {
		t.Fatalf("BinDir = %q, want %q", got, binDir)
	}
	if layout.ResourcesDir == nil || canonicalBundleTestPath(t, *layout.ResourcesDir) != resourcesDir {
		t.Fatalf("ResourcesDir = %#v, want %q", layout.ResourcesDir, resourcesDir)
	}
	if layout.PathDir == nil || canonicalBundleTestPath(t, *layout.PathDir) != pathDir {
		t.Fatalf("PathDir = %#v, want %q", layout.PathDir, pathDir)
	}

	method := context.Method
	if method.Kind != InstallStandalone {
		t.Fatalf("method = %+v, want standalone", method)
	}
	if got := canonicalBundleTestPath(t, method.ReleaseDir); got != canonicalPackage {
		t.Fatalf("release dir = %q, want %q", got, canonicalPackage)
	}
	if method.ResourcesDir == nil || canonicalBundleTestPath(t, *method.ResourcesDir) != resourcesDir {
		t.Fatalf("method resources dir = %#v, want %q", method.ResourcesDir, resourcesDir)
	}
	wantPlatform := StandaloneUnix
	if runtime.GOOS == "windows" {
		wantPlatform = StandaloneWindows
	}
	if method.Platform == nil || *method.Platform != wantPlatform {
		t.Fatalf("platform = %v, want %v", method.Platform, wantPlatform)
	}
}

// TestBundleLayoutRequiresTheCodexCLIAppContainerLikeRust pins the structural
// check: a MacOS directory only resolves a package when it belongs to
// CodexCLI.app/Contents.
func TestBundleLayoutRequiresTheCodexCLIAppContainerLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		dirs  []string
		wants bool
	}{
		{name: "codex cli bundle", dirs: []string{bundleAppDirname, bundleContentsDirname, bundleMacOSDirname}, wants: true},
		{name: "other app bundle", dirs: []string{"Other.app", bundleContentsDirname, bundleMacOSDirname}, wants: false},
		{name: "no contents directory", dirs: []string{bundleAppDirname, "MacOS"}, wants: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			packageDir := t.TempDir()
			parts := append([]string{packageDir}, testCase.dirs...)
			exeDir := filepath.Join(parts...)
			if err := os.MkdirAll(exeDir, 0o755); err != nil {
				t.Fatalf("MkdirAll(exe dir) error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(exeDir, "codex"), nil, 0o600); err != nil {
				t.Fatalf("WriteFile(exe) error = %v", err)
			}
			if err := os.MkdirAll(filepath.Join(packageDir, binDirname), 0o755); err != nil {
				t.Fatalf("MkdirAll(bin) error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(packageDir, packageMetadataFilename), []byte(`{"version":"1.2.3"}`), 0o600); err != nil {
				t.Fatalf("WriteFile(metadata) error = %v", err)
			}
			if got := PackageLayoutFromExe(filepath.Join(exeDir, "codex")); (got != nil) != testCase.wants {
				t.Fatalf("PackageLayoutFromExe() = %#v, want layout present = %v", got, testCase.wants)
			}
		})
	}
}
