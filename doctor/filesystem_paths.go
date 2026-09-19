package doctor

// Rust parity: codex-rs/cli/src/doctor/filesystem_paths.rs (#46543).
//
// Filesystem calls run in disposable copies of this executable: a blocked mount
// must not occupy a worker and delay doctor's shutdown. Budgets bound helper
// waits, not synchronous process launch; the sandbox policy is unchanged.
// Windows only lists paths (local-looking paths can redirect to network shares),
// and restricted-read policies only list paths because lexical checks cannot
// constrain symlinks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
)

const (
	filesystemProbeTimeout = 2 * time.Second
	filesystemTotalTimeout = 8 * time.Second
	filesystemSlowProbe    = 1 * time.Second
	maxFilesystemPaths     = 32
)

// Hidden probe-helper exit codes (Rust filesystem_paths::probe_exit_code).
const (
	filesystemProbeResolved = 0
	filesystemProbeMissing  = 2
	filesystemProbeDenied   = 3
	filesystemProbeFailed   = 4
	filesystemProbeWindows  = 5
)

// ProbeFilesystemPathExitCode resolves one path for the hidden
// `doctor --probe-filesystem-path` helper. It never loads configuration, so a
// blocked filesystem call cannot delay the parent doctor run.
func ProbeFilesystemPathExitCode(path string) int {
	if runtime.GOOS == "windows" {
		// Even drive-qualified paths can traverse reparse points to network shares.
		// Do not initiate filesystem access on Windows from this helper.
		return filesystemProbeWindows
	}
	if _, err := filepath.EvalSymlinks(path); err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return filesystemProbeMissing
		case errors.Is(err, fs.ErrPermission):
			return filesystemProbeDenied
		default:
			return filesystemProbeFailed
		}
	}
	return filesystemProbeResolved
}

type filesystemProbeOutcome int

const (
	filesystemOutcomeResolved filesystemProbeOutcome = iota
	filesystemOutcomeMissing
	filesystemOutcomeDenied
	filesystemOutcomeTimedOut
	filesystemOutcomeFailed
	filesystemOutcomeSkippedWindows
	filesystemOutcomeSkippedReadRestrictions
)

func (o filesystemProbeOutcome) description() string {
	switch o {
	case filesystemOutcomeResolved:
		return "path resolved (read/write access not tested)"
	case filesystemOutcomeMissing:
		return "missing (may be intentional)"
	case filesystemOutcomeDenied:
		return "access denied to doctor"
	case filesystemOutcomeTimedOut:
		return "path probe timed out"
	case filesystemOutcomeSkippedWindows:
		return "not probed on Windows (network authentication risk)"
	case filesystemOutcomeSkippedReadRestrictions:
		return "not probed (filesystem read restrictions apply)"
	default:
		return "path probe failed"
	}
}

func (o filesystemProbeOutcome) acceptable() bool {
	switch o {
	case filesystemOutcomeResolved, filesystemOutcomeMissing, filesystemOutcomeSkippedWindows, filesystemOutcomeSkippedReadRestrictions:
		return true
	default:
		return false
	}
}

type filesystemProbeTarget struct {
	Path     string
	Accesses []string
}

// filesystemPathsCheck reports the configured literal filesystem grants and
// whether resolving them responds promptly.
func filesystemPathsCheck(codexHome string, opts *Options) *DoctorCheck {
	check := NewCheck("sandbox.filesystem_paths", "sandbox", CheckStatusOK, "configured filesystem paths respond promptly")
	cfg, err := loadEffectiveConfigForDoctor(codexHome, opts)
	if err != nil || cfg == nil {
		check.Summary = "no explicit filesystem paths to probe"
		return check
	}
	cwd := doctorCWD(opts)
	targets, profileID, readRestricted := literalFilesystemProbeTargets(cfg, cwd)
	if len(targets) == 0 {
		check.Summary = "no explicit filesystem paths to probe"
		return check
	}
	if profileID != "" {
		check.Detail("permission profile: " + profileID)
	}
	check.Detail("operation: resolve paths only; read/write access is not tested")
	check.Detail("probe budgets: 2 seconds per path, 8 seconds total, at most 32 paths")
	switch {
	case readRestricted:
		check.Summary = "configured filesystem paths listed; read restrictions prevent probes"
	case runtime.GOOS == "windows":
		check.Summary = "configured filesystem paths listed; probes disabled on Windows"
	}

	started := time.Now()
	checked := 0
	for _, target := range targets {
		if checked >= maxFilesystemPaths {
			break
		}
		remaining := filesystemTotalTimeout - time.Since(started)
		if remaining <= 0 {
			break
		}
		label := fmt.Sprintf("path %d", checked+1)
		probeStarted := time.Now()
		outcome := filesystemOutcomeSkippedReadRestrictions
		switch {
		case readRestricted:
		case runtime.GOOS == "windows":
			outcome = filesystemOutcomeSkippedWindows
		default:
			budget := filesystemProbeTimeout
			if remaining < budget {
				budget = remaining
			}
			outcome = runFilesystemProbe(target.Path, budget)
		}
		slow := time.Since(probeStarted) >= filesystemSlowProbe
		checked++
		check.Detail(fmt.Sprintf("%s: %s (%s); %s", label, target.Path, strings.Join(target.Accesses, ", "), outcome.description()))
		if slow {
			check.Detail(label + " latency: over 1 second (includes helper startup)")
		}
		check.Detail(fmt.Sprintf("%s source: %s", label, filesystemPathSource()))
		if !outcome.acceptable() || slow {
			check.Status = CheckStatusWarning
			check.Summary = "some configured filesystem paths are slow or could not be checked"
			cause := label + ": " + outcome.description()
			if slow {
				cause += "; slow path probe"
			}
			check.Issue(NewIssue(CheckStatusWarning, cause).
				WithField(label).
				WithRemedy("Check this path's mount and host permissions. Ask your administrator whether the entry is needed on this machine; do not remove required restrictions."))
		}
	}
	if checked < len(targets) {
		check.Status = CheckStatusWarning
		check.Summary = "filesystem path check is incomplete"
		check.Detail(fmt.Sprintf("unchecked paths: %d (probe budget exhausted)", len(targets)-checked))
	}
	check.Detail(fmt.Sprintf("paths checked: %d of %d", checked, len(targets)))
	return check
}

// filesystemPathSource mirrors Rust's provenance fallback. Go's effective
// config does not retain per-entry layer origins, which is exactly the case
// Rust reports as "entry provenance unavailable"; Rust also uses that string for
// managed, legacy, and runtime-added entries.
func filesystemPathSource() string {
	return "effective filesystem policy (entry provenance unavailable)"
}

func runFilesystemProbe(path string, budget time.Duration) filesystemProbeOutcome {
	executable, err := os.Executable()
	if err != nil {
		return filesystemOutcomeFailed
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "doctor", "--probe-filesystem-path", path)
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return filesystemOutcomeTimedOut
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case filesystemProbeMissing:
				return filesystemOutcomeMissing
			case filesystemProbeDenied:
				return filesystemOutcomeDenied
			case filesystemProbeWindows:
				return filesystemOutcomeSkippedWindows
			}
		}
		return filesystemOutcomeFailed
	}
	return filesystemOutcomeResolved
}

// literalFilesystemProbeTargets mirrors Rust's literal_paths: it reports literal
// grants, never deny rules, globs, or special paths, and skips paths the profile
// denies reading. The second result is the resolved permission-profile id and
// the third reports whether read restrictions apply.
func literalFilesystemProbeTargets(cfg *config.Config, cwd string) ([]filesystemProbeTarget, string, bool) {
	if cfg == nil {
		return nil, "", false
	}
	resolution, err := cfg.ResolveSandboxPermissionProfile("", cwd)
	if err != nil || resolution == nil || resolution.Profile == nil {
		return nil, "", false
	}
	readRestricted := resolution.Profile.HasDenyReadEntries()
	accessesByPath := map[string]map[string]bool{}
	for _, entry := range filesystemEntriesFromResolution(resolution) {
		if strings.EqualFold(strings.TrimSpace(string(entry.Access)), string(sandbox.FileSystemAccessDeny)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(entry.Path.Type), "path") {
			continue
		}
		path := strings.TrimSpace(entry.Path.Path)
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if resolution.Profile.DeniesReadPath(path) {
			continue
		}
		if accessesByPath[path] == nil {
			accessesByPath[path] = map[string]bool{}
		}
		accessesByPath[path][string(entry.Access)] = true
	}
	paths := make([]string, 0, len(accessesByPath))
	for path := range accessesByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	targets := make([]filesystemProbeTarget, 0, len(paths))
	for _, path := range paths {
		accesses := make([]string, 0, len(accessesByPath[path]))
		for access := range accessesByPath[path] {
			accesses = append(accesses, access)
		}
		sort.Strings(accesses)
		targets = append(targets, filesystemProbeTarget{Path: path, Accesses: accesses})
	}
	return targets, resolution.ID, readRestricted
}

// filesystemEntriesFromResolution reads the materialized entries of a restricted
// runtime permission profile (Rust PermissionProfile::file_system_sandbox_policy
// entries). Unrestricted or disabled profiles have no explicit paths.
func filesystemEntriesFromResolution(resolution *config.SandboxPermissionProfileResolution) []sandbox.FileSystemSandboxEntry {
	if resolution == nil || strings.TrimSpace(resolution.ProfileJSON) == "" {
		return nil
	}
	var wire struct {
		FileSystem *struct {
			Type    string                           `json:"type"`
			Entries []sandbox.FileSystemSandboxEntry `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal([]byte(resolution.ProfileJSON), &wire); err != nil || wire.FileSystem == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(wire.FileSystem.Type), "restricted") {
		return nil
	}
	return wire.FileSystem.Entries
}
