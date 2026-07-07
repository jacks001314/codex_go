package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func FindCodexHome() (string, error) {
	value := os.Getenv("CODEX_HOME")
	if value == "" {
		return FindCodexHomeFromEnv("")
	}
	return FindCodexHomeFromEnv(value)
}

func FindCodexHomeFromEnv(codexHomeEnv string) (string, error) {
	if codexHomeEnv != "" {
		info, err := os.Stat(codexHomeEnv)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("CODEX_HOME points to %q, but that path does not exist", codexHomeEnv)
			}
			return "", fmt.Errorf("failed to read CODEX_HOME %q: %w", codexHomeEnv, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("CODEX_HOME points to %q, but that path is not a directory", codexHomeEnv)
		}
		canonical, err := filepath.Abs(codexHomeEnv)
		if err != nil {
			return "", fmt.Errorf("failed to canonicalize CODEX_HOME %q: %w", codexHomeEnv, err)
		}
		if evaluated, err := filepath.EvalSymlinks(canonical); err == nil {
			canonical = evaluated
		}
		return canonical, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("Could not find home directory")
	}
	return filepath.Join(home, ".codex"), nil
}
