package mcp

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// mcpSandboxStateProbeTimeout bounds one capability probe (Rust
// sandbox_executable::PROBE_TIMEOUT, #52182).
const mcpSandboxStateProbeTimeout = 5 * time.Second

// mcpSandboxStateProbe runs the capability check. It is a variable so tests can
// substitute a probe instead of spawning a process.
var mcpSandboxStateProbe = probeSandboxStateExecutable

type mcpSandboxStateProbeEntry struct {
	once sync.Once
	ok   bool
}

// mcpSandboxStateProbes caches completed probes by absolute executable path, so
// a path is probed at most once for the process lifetime (Rust's
// PROBES/OnceCell map).
var mcpSandboxStateProbes = struct {
	sync.Mutex
	entries map[string]*mcpSandboxStateProbeEntry
}{entries: map[string]*mcpSandboxStateProbeEntry{}}

// supportsSandboxState reports whether an executable accepts the sandbox-state
// option. The capability is never inferred from the binary's name or PATH: only
// a completed probe of that exact path answers true.
func supportsSandboxState(executable string) bool {
	key := strings.TrimSpace(executable)
	if key == "" {
		return false
	}
	key = filepath.Clean(key)
	mcpSandboxStateProbes.Lock()
	entry := mcpSandboxStateProbes.entries[key]
	if entry == nil {
		entry = &mcpSandboxStateProbeEntry{}
		mcpSandboxStateProbes.entries[key] = entry
	}
	mcpSandboxStateProbes.Unlock()
	entry.once.Do(func() {
		if mcpSandboxStateProbeSelfGuard(key) {
			entry.ok = false
			return
		}
		entry.ok = mcpSandboxStateProbe(key)
	})
	return entry.ok
}

// mcpSandboxStateProbeSelfGuard refuses to probe this very process when it is a
// Go test binary. A test binary stops flag parsing at the first non-flag
// argument and then runs the whole suite, so probing ourselves would re-enter
// the test run. A production codex binary is not affected and is probed
// normally.
func mcpSandboxStateProbeSelfGuard(executable string) bool {
	if flag.Lookup("test.v") == nil {
		return false
	}
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return false
	}
	return sameExecutablePath(executable, self)
}

func sameExecutablePath(a string, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// probeSandboxStateExecutable checks the binary without launching a sandbox.
//
// A plain `sandbox --help` also succeeds for unrelated binaries, including Rust
// test harnesses, so `--sandbox-state-json` goes before `--help`: parsers that
// do not know the option reject it. Codex handles help before interpreting the
// placeholder `{}` policy or loading configuration, and only the exit status
// matters, not the help text.
func probeSandboxStateExecutable(executable string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), mcpSandboxStateProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "sandbox", "--sandbox-state-json", "{}", "--help")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// sandboxLauncherExecutable mirrors PreparedMcpCall::sandbox_codex_executable
// (Rust #52182): only a local stdio server on the default (local) environment
// receives a launcher, and only when the host supplied an absolute, UTF-8
// encodable path to a CLI that passes the sandbox-state probe. An HTTP endpoint
// (even on loopback) or a remote environment never does, and an absent launcher
// never authorizes a PATH lookup or an unsandboxed fallback.
func sandboxLauncherExecutable(executable string, environmentID string, localStdio bool) string {
	exe := strings.TrimSpace(executable)
	if exe == "" || !filepath.IsAbs(exe) || !utf8.ValidString(exe) {
		return ""
	}
	if !localStdio {
		return ""
	}
	envID := strings.TrimSpace(environmentID)
	if envID != "" && envID != DefaultMCPServerEnvironmentID {
		return ""
	}
	if !supportsSandboxState(exe) {
		return ""
	}
	return exe
}
