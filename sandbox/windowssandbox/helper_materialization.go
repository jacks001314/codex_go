package windowssandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
)

const (
	DevBuildVersionSentinel = "0.0.0"
	BinDirname              = "bin"
	ResourcesDirname        = "codex-resources"
)

type HelperExecutable string

const HelperCommandRunner HelperExecutable = "command-runner"

type CopyOutcome string

const (
	CopyOutcomeReused   CopyOutcome = "reused"
	CopyOutcomeReCopied CopyOutcome = "recopied"
)

var helperPathCache sync.Map

func (h HelperExecutable) FileName() string {
	switch h {
	case HelperCommandRunner:
		return "codex-command-runner.exe"
	default:
		return string(h)
	}
}

func HelperBinDir(codexHome string) string {
	return SandboxBinDir(codexHome)
}

func ResolveCurrentExeForLaunch(currentExe string) (string, error) {
	if strings.TrimSpace(currentExe) == "" {
		return "", ErrInvalidRequest
	}
	return ResolveExeForLaunch(currentExe, filepath.Dir(currentExe))
}

func ResolveExeForLaunch(source string, codexHome string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", ErrInvalidRequest
	}
	fileName := filepath.Base(source)
	if fileName == "." || fileName == string(filepath.Separator) {
		return source, nil
	}
	destination := filepath.Join(HelperBinDir(codexHome), fileName)
	if _, err := CopyFromSourceIfNeeded(source, destination); err != nil {
		return source, nil
	}
	return destination, nil
}

func ResolveHelperForLaunch(kind HelperExecutable, codexHome string, currentExe string) string {
	path, err := CopyHelperIfNeeded(kind, codexHome, currentExe)
	if err == nil {
		return path
	}
	return LegacyLookup(kind, currentExe)
}

func CopyHelperIfNeeded(kind HelperExecutable, codexHome string, currentExe string) (string, error) {
	cacheKey := kind.FileName() + "|" + codexHome
	if cached, ok := helperPathCache.Load(cacheKey); ok {
		if value, ok := cached.(string); ok {
			return value, nil
		}
	}
	source, err := SiblingSourcePath(kind, currentExe)
	if err != nil {
		return "", err
	}
	destination, err := HelperDestinationForSource(kind, codexHome, source)
	if err != nil {
		return "", err
	}
	if _, err := CopyFromSourceIfNeeded(source, destination); err != nil {
		return "", err
	}
	helperPathCache.Store(cacheKey, destination)
	return destination, nil
}

func LegacyLookup(kind HelperExecutable, currentExe string) string {
	if candidate := BundledExecutablePathForExe(currentExe, kind.FileName()); candidate != "" {
		return candidate
	}
	return kind.FileName()
}

func SiblingSourcePath(kind HelperExecutable, currentExe string) (string, error) {
	if candidate := BundledExecutablePathForExe(currentExe, kind.FileName()); candidate != "" {
		return candidate, nil
	}
	return "", fmt.Errorf("helper not found next to current executable or under %s: %s", ResourcesDirname, currentExe)
}

func BundledExecutablePathForExe(exe string, fileName string) string {
	dir := filepath.Dir(exe)
	if dir == "." || strings.TrimSpace(fileName) == "" {
		return ""
	}
	directCandidate := filepath.Join(dir, fileName)
	if isRegularFile(directCandidate) {
		return directCandidate
	}
	if filepath.Base(dir) == BinDirname {
		packageDir := filepath.Dir(dir)
		packageResourceCandidate := filepath.Join(packageDir, ResourcesDirname, fileName)
		if isRegularFile(packageResourceCandidate) {
			return packageResourceCandidate
		}
	}
	resourceCandidate := filepath.Join(dir, ResourcesDirname, fileName)
	if isRegularFile(resourceCandidate) {
		return resourceCandidate
	}
	return ""
}

func HelperDestinationForSource(kind HelperExecutable, codexHome string, source string) (string, error) {
	suffix, err := HelperVersionSuffix(source)
	if err != nil {
		return "", err
	}
	return filepath.Join(HelperBinDir(codexHome), MaterializedFileName(kind, suffix)), nil
}

func MaterializedFileName(kind HelperExecutable, suffix string) string {
	sourceName := kind.FileName()
	ext := filepath.Ext(sourceName)
	stem := strings.TrimSuffix(sourceName, ext)
	return stem + "-" + suffix + ext
}

func HelperVersionSuffix(source string) (string, error) {
	version := mainModuleVersion()
	if version == "" || version == "(devel)" || version == DevBuildVersionSentinel {
		return DevBuildSuffix(source)
	}
	return version, nil
}

func DevBuildSuffix(source string) (string, error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	mtime := info.ModTime().Unix()
	return fmt.Sprintf("%d-%x", info.Size(), mtime), nil
}

func CopyFromSourceIfNeeded(source string, destination string) (CopyOutcome, error) {
	fresh, err := DestinationIsFresh(source, destination)
	if err != nil {
		return "", err
	}
	if fresh {
		return CopyOutcomeReused, nil
	}
	destinationDir := filepath.Dir(destination)
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return "", err
	}
	src, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(destinationDir, ".helper-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tmpName, destination); err != nil {
		fresh, freshErr := DestinationIsFresh(source, destination)
		if freshErr == nil && fresh {
			return CopyOutcomeReused, nil
		}
		return "", err
	}
	cleanupTmp = false
	return CopyOutcomeReCopied, nil
}

func DestinationIsFresh(source string, destination string) (bool, error) {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	destinationInfo, err := os.Stat(destination)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if sourceInfo.Size() != destinationInfo.Size() {
		return false, nil
	}
	return !destinationInfo.ModTime().Before(sourceInfo.ModTime()), nil
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func mainModuleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Version
}
