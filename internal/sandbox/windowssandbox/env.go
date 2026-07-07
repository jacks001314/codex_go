package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"

	homedir "github.com/mitchellh/go-homedir"
)

func PrepareEnvironment(env map[string]string) map[string]string {
	out := cloneEnv(env)
	NormalizeNullDeviceEnv(out)
	EnsureNonInteractivePager(out)
	InheritPathEnv(out)
	return out
}

func NormalizeNullDeviceEnv(env map[string]string) {
	for key, value := range env {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "/dev/null" || normalized == `\\dev\\null` {
			env[key] = "NUL"
		}
	}
}

func EnsureNonInteractivePager(env map[string]string) {
	if _, ok := env["GIT_PAGER"]; !ok {
		env["GIT_PAGER"] = "more.com"
	}
	if _, ok := env["PAGER"]; !ok {
		env["PAGER"] = "more.com"
	}
	if _, ok := env["LESS"]; !ok {
		env["LESS"] = ""
	}
}

func InheritPathEnv(env map[string]string) {
	if _, ok := env["PATH"]; !ok {
		if value := os.Getenv("PATH"); value != "" {
			env["PATH"] = value
		}
	}
	if _, ok := env["PATHEXT"]; !ok {
		if value := os.Getenv("PATHEXT"); value != "" {
			env["PATHEXT"] = value
		}
	}
}

func ApplyNoNetworkToEnv(env map[string]string) error {
	env["SBX_NONET_ACTIVE"] = "1"
	setDefault(env, "HTTP_PROXY", "http://127.0.0.1:9")
	setDefault(env, "HTTPS_PROXY", "http://127.0.0.1:9")
	setDefault(env, "ALL_PROXY", "http://127.0.0.1:9")
	setDefault(env, "NO_PROXY", "localhost,127.0.0.1,::1")
	setDefault(env, "PIP_NO_INDEX", "1")
	setDefault(env, "PIP_DISABLE_PIP_VERSION_CHECK", "1")
	setDefault(env, "NPM_CONFIG_OFFLINE", "true")
	setDefault(env, "CARGO_NET_OFFLINE", "true")
	setDefault(env, "GIT_HTTP_PROXY", "http://127.0.0.1:9")
	setDefault(env, "GIT_HTTPS_PROXY", "http://127.0.0.1:9")
	setDefault(env, "GIT_SSH_COMMAND", "cmd /c exit 1")
	setDefault(env, "GIT_ALLOW_PROTOCOLS", "")

	base, err := EnsureDenybin([]string{"ssh", "scp"}, "")
	if err != nil {
		return err
	}
	for _, tool := range []string{"curl", "wget"} {
		for _, ext := range []string{".bat", ".cmd"} {
			path := filepath.Join(base, tool+ext)
			if _, err := os.Stat(path); err == nil {
				_ = os.Remove(path)
			}
		}
	}
	PrependPath(env, base)
	ReorderPATHEXTForStubs(env)
	return nil
}

func EnsureDenybin(tools []string, denybinDir string) (string, error) {
	base := denybinDir
	if strings.TrimSpace(base) == "" {
		home, err := homedir.Dir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".sbx-denybin")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	for _, tool := range tools {
		for _, ext := range []string{".bat", ".cmd"} {
			path := filepath.Join(base, tool+ext)
			if _, err := os.Stat(path); err == nil {
				continue
			}
			if err := os.WriteFile(path, []byte("@echo off\r\nexit /b 1\r\n"), 0o600); err != nil {
				return "", err
			}
		}
	}
	return base, nil
}

func PrependPath(env map[string]string, prefix string) {
	existing := env["PATH"]
	if existing == "" {
		existing = os.Getenv("PATH")
	}
	parts := strings.Split(existing, ";")
	if len(parts) > 0 && strings.EqualFold(parts[0], prefix) {
		return
	}
	if existing == "" {
		env["PATH"] = prefix
		return
	}
	env["PATH"] = prefix + ";" + existing
}

func ReorderPATHEXTForStubs(env map[string]string) {
	value := env["PATHEXT"]
	if value == "" {
		value = os.Getenv("PATHEXT")
	}
	if value == "" {
		value = ".COM;.EXE;.BAT;.CMD"
	}
	exts := splitNonEmpty(value, ";")
	upper := make([]string, len(exts))
	for i, ext := range exts {
		upper[i] = strings.ToUpper(ext)
	}
	var front []string
	for _, want := range []string{".BAT", ".CMD"} {
		for i, ext := range upper {
			if ext == want {
				front = append(front, exts[i])
				break
			}
		}
	}
	var rest []string
	for i, ext := range exts {
		up := upper[i]
		if up == ".BAT" || up == ".CMD" {
			continue
		}
		rest = append(rest, ext)
	}
	env["PATHEXT"] = strings.Join(append(front, rest...), ";")
}

func setDefault(env map[string]string, key string, value string) {
	if _, ok := env[key]; !ok {
		env[key] = value
	}
}

func splitNonEmpty(value string, sep string) []string {
	var out []string
	for _, part := range strings.Split(value, sep) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
