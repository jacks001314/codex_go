package windowssandbox

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func EnsureCodexHomeExists(codexHome string) error {
	if codexHome == "" {
		return ErrInvalidRequest
	}
	return os.MkdirAll(codexHome, 0o700)
}

func FindGitWorktreeRootForSafeDirectory(start string) string {
	if strings.TrimSpace(start) == "" {
		return ""
	}
	cur, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	if evaluated, err := filepath.EvalSymlinks(cur); err == nil {
		cur = evaluated
	}
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func InjectGitSafeDirectory(envMap map[string]string, cwd string) {
	if envMap == nil {
		return
	}
	gitRoot := FindGitWorktreeRootForSafeDirectory(cwd)
	if gitRoot == "" {
		return
	}
	count, _ := strconv.Atoi(envMap["GIT_CONFIG_COUNT"])
	envMap["GIT_CONFIG_KEY_"+strconv.Itoa(count)] = "safe.directory"
	envMap["GIT_CONFIG_VALUE_"+strconv.Itoa(count)] = strings.ReplaceAll(gitRoot, `\`, "/")
	envMap["GIT_CONFIG_COUNT"] = strconv.Itoa(count + 1)
}
