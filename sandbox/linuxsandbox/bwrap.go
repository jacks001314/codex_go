package linuxsandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"codex_go/install"
	"codex_go/sandbox/sandboxpath"
)

// Bubblewrap launcher selection and argv preparation (Rust
// linux-sandbox/src/launcher.rs and bundled_bwrap.rs).
//
// The sandbox prefers a system `bwrap` on PATH, but only one that advertises the
// capabilities this launcher relies on (`--as-pid-1` and `--perms`; `--argv0`
// appeared in bubblewrap 0.9 and older distro builds reject it). Anything else
// falls back to the bubblewrap bundled in the package's `codex-resources`
// directory, which is digest-checked against the release before it runs.

// LinuxSandboxArg0 is the argv[0] an inner sandbox stage reports
// (Rust landlock::CODEX_LINUX_SANDBOX_ARG0).
const LinuxSandboxArg0 = "codex-linux-sandbox"

// BundledBwrapDigestFailureExitCode is the status a sandbox exits with when the
// packaged bubblewrap does not match the digest the release pinned at build time
// (Rust BUNDLED_BWRAP_DIGEST_VERIFICATION_FAILURE_EXIT_CODE).
const BundledBwrapDigestFailureExitCode = 121

// bundledBwrapSHA256 is the digest of the packaged bubblewrap, stamped at build
// time from third_party/bwrap/prepare_bwrap.py's receipt (Rust build.rs stamps
// CODEX_BWRAP_SHA256 the same way). Empty means the release did not pin one.
var bundledBwrapSHA256 = ""

// BwrapUnavailableMessage mirrors Rust's panic text when neither launcher works.
const BwrapUnavailableMessage = "bubblewrap is unavailable: no system bwrap was found on PATH and no bundled codex-resources/bwrap binary was found next to the Codex executable"

// GlobScanProgram is the external deny-glob scanner inventoried by sandbox
// integrity checks (Rust #51840).
const GlobScanProgram = "rg"

// maxUnreadableGlobMatches mirrors Rust MAX_UNREADABLE_GLOB_MATCHES.
const maxUnreadableGlobMatches = 8192

// bwrapCapabilities are the launcher features probed from `bwrap --help`.
type bwrapCapabilities struct {
	SupportsArgv0    bool
	SupportsPerms    bool
	SupportsRoBindFD bool
}

// parseBwrapHelp mirrors Rust system_bwrap_capabilities: a build that does not
// advertise `--as-pid-1` is not usable at all, and the remaining flags decide
// which compatibility path the launcher takes.
func parseBwrapHelp(help string) (bwrapCapabilities, bool) {
	if !strings.Contains(help, "--as-pid-1") {
		return bwrapCapabilities{}, false
	}
	return bwrapCapabilities{
		SupportsArgv0:    strings.Contains(help, "--argv0"),
		SupportsPerms:    strings.Contains(help, "--perms"),
		SupportsRoBindFD: strings.Contains(help, "--ro-bind-fd"),
	}, true
}

// bwrapLauncher is the launcher the sandbox runs. The zero value means none.
type bwrapLauncher struct {
	Program       string
	Bundled       bool
	SupportsArgv0 bool
}

func (l bwrapLauncher) available() bool {
	return strings.TrimSpace(l.Program) != ""
}

// selectBwrapLauncher mirrors Rust preferred_bwrap_launcher: a system bwrap that
// passes the capability probe wins, the packaged launcher is the fallback, and
// having neither is a hard failure.
func selectBwrapLauncher(lookPath func(string) (string, error), probe func(string) (bwrapCapabilities, bool), bundled func() (string, bool)) bwrapLauncher {
	if lookPath != nil {
		if path, err := lookPath("bwrap"); err == nil && strings.TrimSpace(path) != "" {
			if capabilities, ok := probe(path); ok && capabilities.SupportsPerms {
				return bwrapLauncher{Program: path, SupportsArgv0: capabilities.SupportsArgv0}
			}
		}
	}
	if bundled != nil {
		if path, ok := bundled(); ok {
			// The packaged launcher is built from pinned sources, so it supports
			// every flag this launcher uses.
			return bwrapLauncher{Program: path, Bundled: true, SupportsArgv0: true}
		}
	}
	return bwrapLauncher{}
}

// selectBwrapLauncherForPolicy mirrors Rust preferred_bwrap_launcher: the first
// system bwrap the command's own filesystem policy leaves eligible wins, and the
// packaged launcher is the fallback (Rust #51211).
func selectBwrapLauncherForPolicy(policy sandboxpath.FilesystemPolicy, pathEnv string, cwd string, probe func(string) (bwrapCapabilities, bool), bundled func() (string, bool)) bwrapLauncher {
	lookPath := func(program string) (string, error) {
		if found := sandboxpath.FindExecutableInPath(program, pathEnv, cwd, policy); found != "" {
			return found, nil
		}
		return "", os.ErrNotExist
	}
	return selectBwrapLauncher(lookPath, probe, bundled)
}

// injectAsPid1 mirrors Rust exec_bwrap: a sandbox that unshares the pid
// namespace must also make the sandboxed process PID 1, otherwise it cannot reap
// or signal its own children.
func injectAsPid1(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	commandSeparator := len(argv)
	for index, argument := range argv {
		if argument == "--" {
			commandSeparator = index
			break
		}
	}
	for _, argument := range argv[:commandSeparator] {
		if argument == "--unshare-pid" {
			out := make([]string, 0, len(argv)+1)
			out = append(out, argv[0], "--as-pid-1")
			out = append(out, argv[1:]...)
			return out
		}
	}
	return argv
}

// applyInnerCommandArgv0 mirrors Rust apply_inner_command_argv0: when bubblewrap
// understands `--argv0`, the inner stage reports the canonical sandbox name;
// older builds reject the flag, so the inner command is run through the caller's
// own argv[0] instead.
func applyInnerCommandArgv0(argv []string, supportsArgv0 bool, argv0FallbackCommand string) ([]string, error) {
	commandSeparator := -1
	for index, argument := range argv {
		if argument == "--" {
			commandSeparator = index
			break
		}
	}
	if commandSeparator < 0 {
		return nil, errors.New("bubblewrap argv is missing command separator '--'")
	}
	if supportsArgv0 {
		out := make([]string, 0, len(argv)+2)
		out = append(out, argv[:commandSeparator]...)
		out = append(out, "--argv0", LinuxSandboxArg0)
		out = append(out, argv[commandSeparator:]...)
		return out, nil
	}
	commandIndex := commandSeparator + 1
	if commandIndex >= len(argv) {
		return nil, errors.New("bubblewrap argv is missing inner command after '--'")
	}
	if strings.TrimSpace(argv0FallbackCommand) == "" {
		return nil, errors.New("bubblewrap argv0 fallback command is empty")
	}
	out := append([]string(nil), argv...)
	out[commandIndex] = argv0FallbackCommand
	return out, nil
}

// verifyBundledBwrapDigest mirrors Rust bundled_bwrap::verify_digest: a packaged
// launcher only runs when its bytes match the digest the release pinned.
func verifyBundledBwrapDigest(path string, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return nil
	}
	if len(expected) != 64 {
		return fmt.Errorf("bundled bubblewrap digest %q is not a SHA-256 digest", expected)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open bundled bubblewrap %s: %w", path, err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("failed to read bundled bubblewrap %s: %w", path, err)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expected {
		return fmt.Errorf("bundled bubblewrap %s does not match the digest pinned for this release (expected %s, found %s)", path, expected, actual)
	}
	return nil
}

// bundledBwrapResource resolves the packaged launcher from the CLI's own package
// (Rust bundled_resource("bwrap") plus the legacy executable-directory fallback
// find_legacy_for_exe).
func bundledBwrapResource() (string, bool) {
	for _, candidate := range bundledBwrapCandidates() {
		if isExecutableFile(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// bundledBwrapCandidates lists where a packaged launcher may live: the install
// context's resources directory first, then the directory beside the running
// executable, which an unpacked or legacy layout uses.
func bundledBwrapCandidates() []string {
	var candidates []string
	if context := install.Current(); context != nil {
		if resource := context.BundledResource("bwrap"); resource != nil && strings.TrimSpace(*resource) != "" {
			candidates = append(candidates, *resource)
		}
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "codex-resources", "bwrap"))
	}
	return candidates
}

// FindBundledBwrapForExecutable resolves the packaged bubblewrap relative to an
// explicit launcher executable rather than the running process. The integrity
// backend uses this to inventory the same bundled candidate the command's
// sandbox would probe (Rust #51840).
func FindBundledBwrapForExecutable(executable, commandCWD string) (string, bool) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return "", false
	}
	if !filepath.IsAbs(executable) {
		executable = filepath.Join(commandCWD, executable)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		resolved = executable
	}
	var candidates []string
	if context := install.FromExe(false, resolved, false, false, false, false, ""); context != nil {
		if resource := context.BundledResource("bwrap"); resource != nil && strings.TrimSpace(*resource) != "" {
			candidates = append(candidates, *resource)
		}
	}
	candidates = append(candidates,
		filepath.Join(filepath.Dir(resolved), "codex-resources", "bwrap"),
		filepath.Join(filepath.Dir(resolved), "bwrap"),
	)
	for _, candidate := range candidates {
		if isExecutableFile(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// ExpandUnreadableGlobsInEnvironment mirrors Rust
// `expand_unreadable_globs_in_environment` (#51840): resolve the deny-glob
// scanner with the command's explicit PATH and run it with that exact
// environment and working directory. A missing scanner falls back to the
// in-process walker so sandbox construction never silently loses a denial.
func ExpandUnreadableGlobsInEnvironment(
	patterns []string,
	cwd string,
	policy sandboxpath.FilesystemPolicy,
	maxDepth *int,
	env map[string]string,
	commandCWD string,
) ([]string, error) {
	if len(patterns) == 0 || (maxDepth != nil && *maxDepth == 0) {
		return nil, nil
	}
	scanner := ""
	if pathEnv, ok := env["PATH"]; ok {
		scanner = sandboxpath.FindExecutableInPath(GlobScanProgram, pathEnv, commandCWD, policy)
	}
	if scanner == "" {
		return expandUnreadableGlobsInProcess(patterns, cwd, maxDepth)
	}

	type scanGroup struct {
		root  string
		globs []string
	}
	groupIndex := map[string]int{}
	var groups []scanGroup
	for _, pattern := range patterns {
		root, glob, ok := splitUnreadableGlobForRipgrep(pattern, cwd)
		if !ok {
			continue
		}
		usable, err := inspectUnreadableGlobSearchRoot(root)
		if err != nil {
			return nil, err
		}
		if !usable {
			continue
		}
		index, exists := groupIndex[root]
		if !exists {
			index = len(groups)
			groupIndex[root] = index
			groups = append(groups, scanGroup{root: root})
		}
		groups[index].globs = append(groups[index].globs, glob)
	}

	expanded := map[string]bool{}
	for _, group := range groups {
		args := []string{
			"--no-config",
			"--files",
			"--hidden",
			"--no-ignore",
			"--null",
		}
		if maxDepth != nil {
			args = append(args, "--max-depth", fmt.Sprintf("%d", *maxDepth))
		}
		for _, glob := range group.globs {
			args = append(args, "--glob", glob)
		}
		args = append(args, "--", group.root)
		command := exec.Command(scanner, args...)
		command.Dir = commandCWD
		command.Env = environmentEntries(env)
		output, err := command.Output()
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return expandUnreadableGlobsInProcess(patterns, cwd, maxDepth)
			}
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && len(exitErr.Stderr) == 0 {
				continue
			}
			if exitErr, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("ripgrep unreadable glob scan failed for %s: %s", group.root, strings.TrimSpace(string(exitErr.Stderr)))
			}
			return nil, err
		}
		for _, raw := range strings.Split(string(output), "\x00") {
			if raw == "" {
				continue
			}
			path := raw
			if !filepath.IsAbs(path) {
				path = filepath.Join(group.root, path)
			}
			expanded[filepath.Clean(path)] = true
			if canonical, err := filepath.EvalSymlinks(path); err == nil && canonical != path {
				expanded[filepath.Clean(canonical)] = true
			}
			if len(expanded) > maxUnreadableGlobMatches {
				return nil, fmt.Errorf("unreadable glob expansion for %s matched more than %d paths", group.root, maxUnreadableGlobMatches)
			}
		}
	}
	paths := make([]string, 0, len(expanded))
	for path := range expanded {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// inspectUnreadableGlobSearchRoot reports whether a deny-glob search root can
// be scanned. Following symlink roots is fine, but an inspection failure other
// than a missing or non-directory root must reject the expansion so a deny is
// never silently discarded (Rust #57186). Only absent or non-directory roots
// have no matches.
func inspectUnreadableGlobSearchRoot(root string) (bool, error) {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect unreadable glob search root %s: %w", root, err)
	}
	return info.IsDir(), nil
}

func splitUnreadableGlobForRipgrep(pattern, cwd string) (string, string, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", "", false
	}
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(cwd, pattern)
	}
	pattern = filepath.Clean(pattern)
	firstGlob := -1
	for index, char := range pattern {
		if strings.ContainsRune("*?[]", char) {
			firstGlob = index
			break
		}
	}
	if firstGlob <= 0 {
		return "", "", false
	}
	staticPrefix := pattern[:firstGlob]
	rootEnd := strings.LastIndex(staticPrefix, string(filepath.Separator))
	if strings.HasSuffix(staticPrefix, string(filepath.Separator)) {
		rootEnd = len(staticPrefix) - 1
	}
	if rootEnd <= 0 {
		return "", "", false
	}
	root := pattern[:rootEnd]
	if !filepath.IsAbs(root) {
		return "", "", false
	}
	glob := escapeUnclosedGlobClasses(pattern[rootEnd+1:])
	return root, glob, glob != ""
}

func escapeUnclosedGlobClasses(glob string) string {
	var out strings.Builder
	for index := 0; index < len(glob); index++ {
		if glob[index] != '[' {
			out.WriteByte(glob[index])
			continue
		}
		end := strings.IndexByte(glob[index+1:], ']')
		if end < 0 {
			out.WriteString(`\[`)
			continue
		}
		out.WriteString(glob[index : index+end+2])
		index += end + 1
	}
	return out.String()
}

func environmentEntries(env map[string]string) []string {
	entries := make([]string, 0, len(env))
	for key, value := range env {
		entries = append(entries, key+"="+value)
	}
	sort.Strings(entries)
	return entries
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return true
}
