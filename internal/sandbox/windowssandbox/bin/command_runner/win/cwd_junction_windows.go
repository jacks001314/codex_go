//go:build windows

package win

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"codex_go/internal/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
)

func createCWDJunction(cwd string, logDir string) (string, error) {
	userprofile := os.Getenv("USERPROFILE")
	if userprofile == "" {
		return "", nil
	}
	junctionRoot := JunctionRootForUserProfile(userprofile)
	if err := os.MkdirAll(junctionRoot, 0o700); err != nil {
		logJunction(logDir, fmt.Sprintf("junction: failed to create %s: %v", junctionRoot, err))
		return "", nil
	}
	junctionPath := filepath.Join(junctionRoot, JunctionNameForPath(cwd))
	if _, err := os.Lstat(junctionPath); err == nil {
		reparse, statErr := IsReparsePoint(junctionPath)
		if statErr != nil {
			logJunction(logDir, fmt.Sprintf("junction: failed to stat existing %s: %v", junctionPath, statErr))
			return "", nil
		}
		if reparse {
			logJunction(logDir, fmt.Sprintf("junction: reusing existing %s", junctionPath))
			return junctionPath, nil
		}
		logJunction(logDir, fmt.Sprintf("junction: existing path is not a reparse point, recreating %s", junctionPath))
		if err := os.Remove(junctionPath); err != nil {
			logJunction(logDir, fmt.Sprintf("junction: failed to remove existing %s: %v", junctionPath, err))
			return "", nil
		}
	} else if !os.IsNotExist(err) {
		logJunction(logDir, fmt.Sprintf("junction: failed to stat existing %s: %v", junctionPath, err))
		return "", nil
	}

	logJunction(logDir, fmt.Sprintf("junction: creating via cmd /c mklink /J %q %q", junctionPath, cwd))
	cmd := exec.Command("cmd", "/c", "mklink", "/J", junctionPath, cwd)
	output, err := cmd.CombinedOutput()
	if err == nil {
		if _, statErr := os.Lstat(junctionPath); statErr == nil {
			logJunction(logDir, fmt.Sprintf("junction: created %s -> %s", junctionPath, cwd))
			return junctionPath, nil
		}
	}
	logJunction(logDir, fmt.Sprintf("junction: mklink failed err=%v output=%s", err, string(output)))
	return "", nil
}

func IsReparsePoint(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || data == nil {
		return false, nil
	}
	return data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

func logJunction(logDir string, note string) {
	if logDir == "" {
		return
	}
	_ = windowssandbox.LogNoteInDir(logDir, note)
}
