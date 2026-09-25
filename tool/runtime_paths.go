package tool

// Codex-owned PATH entries for a launch.
//
// Rust parity: codex-rs/core/src/tools/runtimes/mod.rs's `RuntimePathPrepends`.
// Codex adds its own directories to a command's PATH - the packaged `codex-path`
// shims (ripgrep and the apply_patch shim) and, for a zsh-fork launch, the
// forked shell's directory - and a shell snapshot must re-export them after it
// restores the user's PATH, because the snapshot carries whatever the user's
// login files produced.

import (
	"os"
	"runtime"
	"strings"

	"codex_go/install"
)

// runtimePathPrependsSupported mirrors Rust's `cfg(unix)` gate on
// apply_package_path_prepend: Windows launches inherit the package path from the
// launcher, so only Unix launches carry the entries explicitly. It is a variable
// so tests on any host can exercise the Unix behaviour.
var runtimePathPrependsSupported = runtime.GOOS != "windows"

// RuntimePathPrepends collects the runtime-owned PATH entries of one launch.
type RuntimePathPrepends struct {
	entries []string
}

// Prepend puts entry at the front of env's PATH and records it, mirroring Rust's
// `RuntimePathPrepends::prepend`. An empty entry changes nothing.
func (p *RuntimePathPrepends) Prepend(env map[string]string, entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return
	}
	prependRuntimePathEntry(env, entry)
	if p == nil {
		return
	}
	p.entries = removeRuntimePathEntry(p.entries, entry)
	p.entries = append(p.entries, entry)
}

// Entries returns the recorded entries in replay order: the most recently
// prepended one is applied last, so it ends up first in PATH again.
func (p *RuntimePathPrepends) Entries() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.entries...)
}

// ShellExportsAfterSnapshot renders the PATH re-exports a snapshot replay runs
// after sourcing the snapshot (Rust's `shell_exports_after_snapshot`). A user who
// overrides PATH explicitly keeps their value, so nothing is emitted then.
func (p *RuntimePathPrepends) ShellExportsAfterSnapshot(explicitEnvOverrides map[string]string) string {
	if p == nil || len(p.entries) == 0 {
		return ""
	}
	if _, overridden := explicitEnvOverrides["PATH"]; overridden {
		return ""
	}
	lines := make([]string, 0, len(p.entries))
	for _, entry := range p.entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		quoted := shellSingleQuote(entry)
		lines = append(lines, "if [ -n \"${PATH:-}\" ]; then export PATH='"+quoted+"':\"$PATH\"; else export PATH='"+quoted+"'; fi")
	}
	return strings.Join(lines, "\n")
}

// prependRuntimePathEntry mirrors Rust's `prepend_path_entry`: the entry first,
// then the existing entries without it, dropping empty ones.
func prependRuntimePathEntry(env map[string]string, entry string) string {
	if entry == "" || env == nil {
		return entry
	}
	existing := strings.TrimSpace(env["PATH"])
	var entries []string
	if existing != "" {
		for _, value := range strings.Split(existing, string(os.PathListSeparator)) {
			if value == "" || value == entry {
				continue
			}
			entries = append(entries, value)
		}
	}
	updated := strings.Join(append([]string{entry}, entries...), string(os.PathListSeparator))
	env["PATH"] = updated
	return updated
}

func removeRuntimePathEntry(entries []string, entry string) []string {
	if len(entries) == 0 {
		return entries
	}
	kept := entries[:0]
	for _, value := range entries {
		if value != entry {
			kept = append(kept, value)
		}
	}
	return kept
}

// ApplyPackagePathPrepends adds the current install's packaged `codex-path`
// directory to env (Rust's `apply_package_path_prepend`), so a command that
// resolves `rg` or `apply_patch` by name reaches Codex's own shims.
func ApplyPackagePathPrepends(env map[string]string, prepends *RuntimePathPrepends) {
	prepends.Prepend(env, packagePathDirEntry())
}

// ApplyZshForkPathPrepends adds the directory holding the zsh a zsh-fork launch
// uses (Rust's `apply_zsh_fork_path_prepend`), so the forked shell's helpers
// resolve inside the command.
func ApplyZshForkPathPrepends(env map[string]string, prepends *RuntimePathPrepends, shellPath string) {
	prepends.Prepend(env, zshForkShellDir(shellPath))
}

// RuntimePathEntriesForLaunch returns the Codex-owned PATH entries a launch
// needs, in replay order: the packaged shims and, for a zsh-fork launch, the
// forked shell's directory. Callers that compose the command environment later
// (the shell runner) carry these entries on the request instead of mutating an
// environment they do not own yet.
func RuntimePathEntriesForLaunch(zshForkShellPath string) []string {
	entries := make([]string, 0, 2)
	if pathDir := packagePathDirEntry(); pathDir != "" {
		entries = append(entries, pathDir)
	}
	if shellDir := zshForkShellDir(zshForkShellPath); shellDir != "" {
		entries = append(entries, shellDir)
	}
	return entries
}

// packagePathDirEntry is the packaged `codex-path` directory of this install, or
// "" when the install has no package layout.
func packagePathDirEntry() string {
	return installPackagePathDir()
}

// installPackagePathDir reads the packaged shim directory of the running install.
// It is a variable so tests can stand in for a packaged layout.
var installPackagePathDir = func() string { return install.Current().PackagePathDir() }

// zshForkShellDir is the directory holding the zsh a zsh-fork launch uses.
func zshForkShellDir(shellPath string) string {
	shellPath = strings.TrimSpace(shellPath)
	if shellPath == "" {
		return ""
	}
	separator := strings.LastIndexAny(shellPath, `/\`)
	if separator <= 0 {
		return ""
	}
	return shellPath[:separator]
}

// applyRuntimePathPrepends applies recorded entries to a command environment in
// order, so the last prepended entry ends up first.
func applyRuntimePathPrepends(env map[string]string, entries []string) {
	for _, entry := range entries {
		prependRuntimePathEntry(env, strings.TrimSpace(entry))
	}
}
