package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	binDirname                = "bin"
	packageMetadataFilename   = "codex-package.json"
	pathDirname               = "codex-path"
	releasesDirname           = "releases"
	resourcesDirname          = "codex-resources"
	standalonePackagesDirname = "standalone"
	zshDirname                = "zsh"
)

func codeModeHostExecutableName() string {
	if runtime.GOOS == "windows" {
		return "codex-code-mode-host.exe"
	}
	return "codex-code-mode-host"
}

type StandalonePlatform string

const (
	StandaloneUnix    StandalonePlatform = "unix"
	StandaloneWindows StandalonePlatform = "windows"
)

type InstallMethodKind string

const (
	InstallStandalone InstallMethodKind = "standalone"
	InstallNPM        InstallMethodKind = "npm"
	InstallBun        InstallMethodKind = "bun"
	InstallBrew       InstallMethodKind = "brew"
	InstallOther      InstallMethodKind = "other"
)

type CodexPackageLayout struct {
	PackageDir   string  `json:"packageDir"`
	BinDir       string  `json:"binDir"`
	ResourcesDir *string `json:"resourcesDir,omitempty"`
	PathDir      *string `json:"pathDir,omitempty"`
}

type InstallMethod struct {
	Kind         InstallMethodKind   `json:"kind"`
	ReleaseDir   string              `json:"releaseDir,omitempty"`
	ResourcesDir *string             `json:"resourcesDir,omitempty"`
	Platform     *StandalonePlatform `json:"platform,omitempty"`
}

type InstallContext struct {
	Method        InstallMethod       `json:"method"`
	PackageLayout *CodexPackageLayout `json:"packageLayout,omitempty"`
}

type ExecutableIdentity struct {
	Digest string `json:"digest"`
}

type DaemonSettings struct {
	RemoteControlEnabled bool `json:"remoteControlEnabled"`
}

var (
	currentOnce sync.Once
	current     *InstallContext
)

func Current() *InstallContext {
	currentOnce.Do(func() {
		exe, _ := os.Executable()
		codexHome := os.Getenv("CODEX_HOME")
		current = FromExe(runtime.GOOS == "darwin", exe, os.Getenv("CODEX_MANAGED_BY_NPM") != "", os.Getenv("CODEX_MANAGED_BY_BUN") != "", codexHome)
	})
	return cloneContext(current)
}

func FromExe(isMacOS bool, currentExe string, managedByNPM bool, managedByBun bool, codexHome string) *InstallContext {
	layout := PackageLayoutFromExe(currentExe)
	method := InstallMethod{Kind: InstallOther}
	switch {
	case managedByNPM:
		method = InstallMethod{Kind: InstallNPM}
	case managedByBun:
		method = InstallMethod{Kind: InstallBun}
	case currentExe != "":
		method = installMethodFromExe(currentExe, codexHome, layout, isMacOS)
	}
	return &InstallContext{Method: method, PackageLayout: layout}
}

func (c *InstallContext) RGCommand() string {
	if c != nil && c.PackageLayout != nil && c.PackageLayout.PathDir != nil {
		candidate := filepath.Join(*c.PackageLayout.PathDir, defaultRGCommand())
		if isFile(candidate) {
			return candidate
		}
	}
	if c != nil && c.Method.Kind == InstallStandalone && c.Method.ResourcesDir != nil {
		candidate := filepath.Join(*c.Method.ResourcesDir, defaultRGCommand())
		if isFile(candidate) {
			return candidate
		}
	}
	return defaultRGCommand()
}

// CodeModeHostProgram resolves the standalone host next to the installed
// Codex executable without requiring the host to be started.
func (c *InstallContext) CodeModeHostProgram() string {
	exe, _ := os.Executable()
	return c.CodeModeHostProgramFromExe(exe)
}

func (c *InstallContext) CodeModeHostProgramFromExe(currentExe string) string {
	if resource := c.BundledResource(codeModeHostExecutableName()); resource != nil {
		return *resource
	}
	executableDir := ""
	if c != nil && c.PackageLayout != nil {
		executableDir = c.PackageLayout.BinDir
	} else if c != nil && c.Method.Kind == InstallStandalone {
		executableDir = c.Method.ReleaseDir
	} else if currentExe != "" {
		if parent, err := filepath.Abs(filepath.Dir(currentExe)); err == nil {
			if resolved, resolveErr := filepath.EvalSymlinks(parent); resolveErr == nil {
				parent = resolved
			}
			executableDir = parent
		}
	}
	if executableDir != "" {
		candidate := filepath.Join(executableDir, codeModeHostExecutableName())
		if isFile(candidate) {
			return candidate
		}
	}
	if currentExe != "" {
		return filepath.Join(filepath.Dir(currentExe), codeModeHostExecutableName())
	}
	return codeModeHostExecutableName()
}

func (c *InstallContext) BundledResource(fileName string) *string {
	if strings.TrimSpace(fileName) == "" {
		return nil
	}
	if c != nil && c.PackageLayout != nil && c.PackageLayout.ResourcesDir != nil {
		candidate := filepath.Join(*c.PackageLayout.ResourcesDir, fileName)
		if isFile(candidate) {
			return &candidate
		}
	}
	if c != nil && c.Method.Kind == InstallStandalone && c.Method.ResourcesDir != nil {
		candidate := filepath.Join(*c.Method.ResourcesDir, fileName)
		if isFile(candidate) {
			return &candidate
		}
	}
	return nil
}

func (c *InstallContext) BundledResourceDir(dirName string) *string {
	if strings.TrimSpace(dirName) == "" {
		return nil
	}
	if c != nil && c.PackageLayout != nil && c.PackageLayout.ResourcesDir != nil {
		candidate := filepath.Join(*c.PackageLayout.ResourcesDir, dirName)
		if dir := existingDir(candidate); dir != nil {
			return dir
		}
	}
	if c != nil && c.Method.Kind == InstallStandalone && c.Method.ResourcesDir != nil {
		candidate := filepath.Join(*c.Method.ResourcesDir, dirName)
		if dir := existingDir(candidate); dir != nil {
			return dir
		}
	}
	return nil
}

func (c *InstallContext) BundledZshPath() *string {
	if runtime.GOOS == "windows" {
		return nil
	}
	return c.BundledResource(filepath.Join(zshDirname, binDirname, "zsh"))
}

func (c *InstallContext) BundledZshBinDir() *string {
	zsh := c.BundledZshPath()
	if zsh == nil {
		return nil
	}
	dir := filepath.Dir(*zsh)
	return &dir
}

func ManagedCodexBin(codexHome string) string {
	return filepath.Join(codexHome, "packages", standalonePackagesDirname, "current", managedCodexFileName())
}

func ParseCodexVersion(output string) (string, error) {
	fields := strings.Fields(output)
	if len(fields) < 2 || fields[1] == "" {
		return "", fmt.Errorf("managed Codex version output was malformed")
	}
	return fields[1], nil
}

func ExecutableIdentityFromBytes(data []byte) ExecutableIdentity {
	sum := sha256.Sum256(data)
	return ExecutableIdentity{Digest: hex.EncodeToString(sum[:])}
}

func PackageLayoutFromExe(exePath string) *CodexPackageLayout {
	if exePath == "" {
		return nil
	}
	canonicalExe, err := filepath.Abs(exePath)
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(canonicalExe); err == nil {
		canonicalExe = resolved
	}
	exeDir := filepath.Dir(canonicalExe)
	if filepath.Base(exeDir) != binDirname {
		return nil
	}
	packageDir := filepath.Dir(exeDir)
	if !isFile(filepath.Join(packageDir, packageMetadataFilename)) {
		return nil
	}
	return &CodexPackageLayout{
		PackageDir:   packageDir,
		BinDir:       exeDir,
		ResourcesDir: existingDir(filepath.Join(packageDir, resourcesDirname)),
		PathDir:      existingDir(filepath.Join(packageDir, pathDirname)),
	}
}

func installMethodFromExe(exePath string, codexHome string, layout *CodexPackageLayout, isMacOS bool) InstallMethod {
	if method, ok := standaloneInstallMethod(exePath, codexHome, layout); ok {
		return method
	}
	if isMacOS && (strings.HasPrefix(exePath, "/opt/homebrew") || strings.HasPrefix(exePath, "/usr/local")) {
		return InstallMethod{Kind: InstallBrew}
	}
	return InstallMethod{Kind: InstallOther}
}

func standaloneInstallMethod(exePath string, codexHome string, layout *CodexPackageLayout) (InstallMethod, bool) {
	if codexHome == "" {
		return InstallMethod{}, false
	}
	canonicalHome, err := filepath.Abs(codexHome)
	if err != nil {
		return InstallMethod{}, false
	}
	if resolved, err := filepath.EvalSymlinks(canonicalHome); err == nil {
		canonicalHome = resolved
	}
	releaseDir := ""
	if layout != nil {
		releaseDir = layout.PackageDir
	} else {
		canonicalExe, err := filepath.Abs(exePath)
		if err != nil {
			return InstallMethod{}, false
		}
		if resolved, err := filepath.EvalSymlinks(canonicalExe); err == nil {
			canonicalExe = resolved
		}
		releaseDir = filepath.Dir(canonicalExe)
	}
	releasesRoot := filepath.Join(canonicalHome, "packages", standalonePackagesDirname, releasesDirname)
	rel, err := filepath.Rel(releasesRoot, releaseDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return InstallMethod{}, false
	}
	resourcesDir := filepath.Join(releaseDir, resourcesDirname)
	method := InstallMethod{Kind: InstallStandalone, ReleaseDir: releaseDir, ResourcesDir: existingDir(resourcesDir)}
	platform := StandaloneUnix
	if runtime.GOOS == "windows" {
		platform = StandaloneWindows
	}
	method.Platform = &platform
	return method, true
}

func managedCodexFileName() string {
	if runtime.GOOS == "windows" {
		return "codex.exe"
	}
	return "codex"
}

func defaultRGCommand() string {
	if runtime.GOOS == "windows" {
		return "rg.exe"
	}
	return "rg"
}

func existingDir(path string) *string {
	stat, err := os.Stat(path)
	if err != nil || !stat.IsDir() {
		return nil
	}
	return &path
}

func isFile(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}

func cloneContext(value *InstallContext) *InstallContext {
	if value == nil {
		return &InstallContext{Method: InstallMethod{Kind: InstallOther}}
	}
	return &InstallContext{Method: cloneMethod(value.Method), PackageLayout: cloneLayout(value.PackageLayout)}
}

func cloneMethod(value InstallMethod) InstallMethod {
	return InstallMethod{
		Kind:         value.Kind,
		ReleaseDir:   value.ReleaseDir,
		ResourcesDir: cloneString(value.ResourcesDir),
		Platform:     clonePlatform(value.Platform),
	}
}

func cloneLayout(value *CodexPackageLayout) *CodexPackageLayout {
	if value == nil {
		return nil
	}
	return &CodexPackageLayout{
		PackageDir:   value.PackageDir,
		BinDir:       value.BinDir,
		ResourcesDir: cloneString(value.ResourcesDir),
		PathDir:      cloneString(value.PathDir),
	}
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePlatform(value *StandalonePlatform) *StandalonePlatform {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
