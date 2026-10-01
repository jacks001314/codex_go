package linuxsandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codex_go/install"
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

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return true
}
