package elevated

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
	commandRunnerFileName     = "codex-command-runner.exe"
	devBuildVersionSentinel   = "0.0.0"
	resourcesDirname          = "codex-resources"
	bundledBinDirname         = "bin"
	sandboxBinDirname         = ".sandbox-bin"
	commandRunnerCacheKeyPart = "command-runner"
)

var helperPathCache sync.Map

func resolveHelperForLaunch(codexHome string, currentExe string) string {
	path, err := copyCommandRunnerIfNeeded(codexHome, currentExe)
	if err == nil {
		return path
	}
	if candidate := bundledExecutablePathForExe(currentExe, commandRunnerFileName); candidate != "" {
		return candidate
	}
	return commandRunnerFileName
}

func copyCommandRunnerIfNeeded(codexHome string, currentExe string) (string, error) {
	cacheKey := commandRunnerCacheKeyPart + "|" + codexHome
	if cached, ok := helperPathCache.Load(cacheKey); ok {
		if value, ok := cached.(string); ok {
			return value, nil
		}
	}
	source := bundledExecutablePathForExe(currentExe, commandRunnerFileName)
	if source == "" {
		// Go's single-file CLI dispatches the command-runner flag itself. Packaged
		// builds still prefer the dedicated helper when one is bundled.
		if !isRegularFile(currentExe) {
			return "", fmt.Errorf("helper not found next to current executable or under %s: %s", resourcesDirname, currentExe)
		}
		source = currentExe
	}
	suffix, err := helperVersionSuffix(source)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(codexHome, sandboxBinDirname, materializedRunnerName(suffix))
	if _, err := copyFromSourceIfNeeded(source, destination); err != nil {
		return "", err
	}
	helperPathCache.Store(cacheKey, destination)
	return destination, nil
}

func bundledExecutablePathForExe(exe string, fileName string) string {
	dir := filepath.Dir(exe)
	if dir == "." || strings.TrimSpace(fileName) == "" {
		return ""
	}
	directCandidate := filepath.Join(dir, fileName)
	if isRegularFile(directCandidate) {
		return directCandidate
	}
	if filepath.Base(dir) == bundledBinDirname {
		packageCandidate := filepath.Join(filepath.Dir(dir), resourcesDirname, fileName)
		if isRegularFile(packageCandidate) {
			return packageCandidate
		}
	}
	resourceCandidate := filepath.Join(dir, resourcesDirname, fileName)
	if isRegularFile(resourceCandidate) {
		return resourceCandidate
	}
	return ""
}

func materializedRunnerName(suffix string) string {
	ext := filepath.Ext(commandRunnerFileName)
	stem := strings.TrimSuffix(commandRunnerFileName, ext)
	return stem + "-" + suffix + ext
}

func helperVersionSuffix(source string) (string, error) {
	info, ok := debug.ReadBuildInfo()
	if ok {
		version := info.Main.Version
		if version != "" && version != "(devel)" && version != devBuildVersionSentinel {
			return version, nil
		}
	}
	stat, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%x", stat.Size(), stat.ModTime().Unix()), nil
}

func copyFromSourceIfNeeded(source string, destination string) (bool, error) {
	fresh, err := destinationIsFresh(source, destination)
	if err != nil {
		return false, err
	}
	if fresh {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return false, err
	}
	src, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".helper-*")
	if err != nil {
		return false, err
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
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.Rename(tmpName, destination); err != nil {
		fresh, freshErr := destinationIsFresh(source, destination)
		if freshErr == nil && fresh {
			return false, nil
		}
		return false, err
	}
	cleanupTmp = false
	return true, nil
}

func destinationIsFresh(source string, destination string) (bool, error) {
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
