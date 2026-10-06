package appserverdaemon

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// daemonEnvPathKeys are the process-scoped paths made absolute before a managed
// child changes its working directory (Rust #49819; CA names match
// CUSTOM_CA_ENV_KEYS). CODEX_SQLITE_HOME is Windows-only, matching Rust.
func daemonEnvPathKeys() []string {
	keys := []string{
		"CODEX_CA_CERTIFICATE",
		"SSL_CERT_FILE",
		"REQUESTS_CA_BUNDLE",
		"CURL_CA_BUNDLE",
		"NODE_EXTRA_CA_CERTS",
		"GIT_SSL_CAINFO",
		"CARGO_HTTP_CAINFO",
		"PIP_CERT",
		"BUNDLE_SSL_CA_CERT",
		"npm_config_cafile",
		"NPM_CONFIG_CAFILE",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
	}
	if runtime.GOOS == "windows" {
		keys = append([]string{"CODEX_SQLITE_HOME"}, keys...)
	}
	return keys
}

// setWorkingDirectory prepares a managed child's working directory: it
// absolutizes process-scoped environment paths so they survive a cwd change, and
// recovers a deleted Unix cwd by falling back to the daemon state directory.
// Mirrors Rust app-server-daemon/src/background_command.rs (#49819).
func setWorkingDirectory(command *exec.Cmd, stateDir string) error {
	if command == nil {
		return nil
	}
	cwdMissing := false
	if runtime.GOOS != "windows" {
		if _, err := os.Getwd(); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				cwdMissing = true
			} else {
				return err
			}
		}
	}
	if command.Env == nil {
		command.Env = os.Environ()
	}
	if value, ok := os.LookupEnv("CODEX_HOME"); ok && value != "" {
		if absolute, err := filepath.Abs(value); err == nil {
			command.Env = setCommandEnv(command.Env, "CODEX_HOME", absolute)
		}
	}
	for _, name := range daemonEnvPathKeys() {
		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		if name == "CODEX_SQLITE_HOME" || name == "npm_config_cafile" || name == "NPM_CONFIG_CAFILE" {
			value = strings.TrimSpace(value)
		}
		// These consumers expand `~` independently of the working directory.
		expandsHome := false
		switch name {
		case "npm_config_cafile", "NPM_CONFIG_CAFILE":
			expandsHome = strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "~\\")
		case "CODEX_SQLITE_HOME", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE":
			expandsHome = strings.HasPrefix(value, "~")
		}
		if value == "" || expandsHome {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		command.Env = setCommandEnv(command.Env, name, absolute)
	}
	if value, ok := os.LookupEnv("SSL_CERT_DIR"); ok {
		var absolute []string
		for _, entry := range filepath.SplitList(value) {
			if entry == "" {
				continue
			}
			abs, err := filepath.Abs(entry)
			if err != nil {
				return err
			}
			absolute = append(absolute, abs)
		}
		command.Env = setCommandEnv(command.Env, "SSL_CERT_DIR", strings.Join(absolute, string(os.PathListSeparator)))
	}
	if runtime.GOOS == "windows" || cwdMissing {
		command.Dir = stateDir
	}
	return nil
}

// setCommandEnv replaces an existing entry for key or appends a new one.
func setCommandEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
