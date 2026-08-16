//go:build unix

// Package processhardening mirrors Rust process-hardening (codex-rs/
// process-hardening/src/lib.rs pre_main_hardening): disable core dumps,
// disable ptrace attach on Linux and macOS, and clear dangerous loader
// environment variables (LD_* / DYLD_*). It is called at process start from
// cmd/codex and is fail-closed like Rust (exit codes 5-7 on failure).
package processhardening

import (
	"fmt"
	"os"
	"strings"
)

const (
	prctlFailedExitCode         = 5
	ptraceDenyAttachExitCode    = 6
	setRlimitCoreFailedExitCode = 7
)

func setCoreFileSizeLimitToZero() {
	if err := setRlimitCoreZero(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: setrlimit(RLIMIT_CORE) failed: %v\n", err)
		os.Exit(setRlimitCoreFailedExitCode)
	}
}

// removeEnvVarsWithPrefix clears every environment variable whose key starts
// with the given prefix.
func removeEnvVarsWithPrefix(prefix string) {
	for _, key := range envKeysWithPrefix(os.Environ(), prefix) {
		_ = os.Unsetenv(key)
	}
}

// envKeysWithPrefix returns the keys of the environment entries starting with
// prefix, operating on the raw string bytes so non-UTF8 keys are handled like
// Rust's byte-based env_keys_with_prefix.
func envKeysWithPrefix(vars []string, prefix string) []string {
	var out []string
	for _, entry := range vars {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	return out
}
