package windowssandbox

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	coresandbox "codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
)

// GlobScanProgram is the external deny-glob scanner inventoried by sandbox
// integrity checks (Rust #51831).
const GlobScanProgram = "rg"

type DenyReadPolicy struct {
	Paths            []string
	Globs            []string
	GlobScanMaxDepth *int
}

// ResolveDenyReadPathsFromProfile resolves the managed filesystem deny rules
// from a resolved permission profile at Windows sandbox request construction
// (Rust #38660): exact-path and glob deny-read entries become the planned
// deny-read paths, with unbounded root-level globs failing closed.
func ResolveDenyReadPathsFromProfile(profile *coresandbox.PermissionProfile, cwd string) ([]string, error) {
	if profile == nil || !profile.HasDenyReadEntries() {
		return nil, nil
	}
	var policy DenyReadPolicy
	for _, entry := range profile.DeniedReadEntries {
		switch entry.Path.Type {
		case "glob_pattern":
			policy.Globs = append(policy.Globs, entry.Path.Pattern)
		case "path":
			policy.Paths = append(policy.Paths, entry.Path.Path)
		}
	}
	return ResolveWindowsDenyReadPolicyPaths(policy, cwd)
}

type GlobScanPlan struct {
	Root     string
	MaxDepth *int
}

func ResolveWindowsDenyReadPaths(paths []string) []string {
	resolved, _ := ResolveWindowsDenyReadPolicyPaths(DenyReadPolicy{Paths: paths}, "")
	return resolved
}

func ResolveWindowsDenyReadPolicyPaths(policy DenyReadPolicy, cwd string) ([]string, error) {
	return resolveWindowsDenyReadPolicyPaths(policy, cwd, nil, "")
}

// ResolveWindowsDenyReadPathsInEnvironment resolves deny paths with the
// launcher's scanner in an explicit command environment. The supplied
// environment replaces inheritance, including when it is empty.
func ResolveWindowsDenyReadPathsInEnvironment(
	policy DenyReadPolicy,
	cwd string,
	env map[string]string,
	commandCWD string,
) ([]string, error) {
	return resolveWindowsDenyReadPolicyPaths(policy, cwd, env, commandCWD)
}

func resolveWindowsDenyReadPolicyPaths(
	policy DenyReadPolicy,
	cwd string,
	env map[string]string,
	commandCWD string,
) ([]string, error) {
	var paths []string
	seen := map[string]bool{}
	for _, path := range policy.Paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		pushDenyReadPath(&paths, seen, resolveAgainstCWD(path, cwd))
	}
	for _, pattern := range policy.Globs {
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		pattern = resolveAgainstCWD(pattern, cwd)
		matcher, err := compileGlobMatcher(pattern)
		if err != nil {
			return nil, err
		}
		plan := GlobScanPlanForPattern(pattern, policy.GlobScanMaxDepth)
		// Rust #38660: a recursive glob rooted at a filesystem root cannot be
		// safely expanded without glob_scan_max_depth; fail closed instead of
		// silently dropping the deny rule.
		if plan.MaxDepth == nil && filepath.Dir(plan.Root) == plan.Root {
			return nil, fmt.Errorf(
				"unreadable glob `%s` cannot be safely expanded from a filesystem root without `glob_scan_max_depth`; configure `glob_scan_max_depth` or use a non-root directory prefix",
				pattern,
			)
		}
		if scanned, ok, err := ripgrepWindowsDenyReadMatches(pattern, plan, env, commandCWD); err != nil {
			return nil, err
		} else if ok {
			for _, path := range scanned {
				pushDenyReadPath(&paths, seen, path)
			}
			continue
		}
		seenScanDirs := map[string]bool{}
		if err := collectExistingGlobMatches(plan.Root, matcher, &paths, seen, seenScanDirs, plan.MaxDepth, 0); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func ripgrepWindowsDenyReadMatches(
	pattern string,
	plan GlobScanPlan,
	env map[string]string,
	commandCWD string,
) ([]string, bool, error) {
	if env == nil {
		return nil, false, nil
	}
	pathEnv := ""
	for key, value := range env {
		if strings.EqualFold(key, "PATH") {
			pathEnv = value
			break
		}
	}
	scanner := sandboxpath.FindExecutableInPath(GlobScanProgram, pathEnv, commandCWD, sandboxpath.FilesystemPolicy{})
	if scanner == "" {
		return nil, false, nil
	}
	relativeGlob := strings.TrimPrefix(pattern, plan.Root)
	relativeGlob = strings.TrimPrefix(relativeGlob, string(filepath.Separator))
	if relativeGlob == "" {
		return nil, false, nil
	}
	args := []string{"--no-config", "--files", "--hidden", "--no-ignore", "--null"}
	if plan.MaxDepth != nil {
		args = append(args, "--max-depth", fmt.Sprintf("%d", *plan.MaxDepth))
	}
	args = append(args, "--glob", relativeGlob, "--", plan.Root)
	command := exec.Command(scanner, args...)
	command.Dir = commandCWD
	command.Env = windowsEnvironmentEntries(env)
	output, err := command.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && len(exitErr.Stderr) == 0 {
			return nil, true, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, false, fmt.Errorf("ripgrep deny-read scan failed for %s: %s", plan.Root, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, false, err
	}
	paths := make([]string, 0)
	for _, raw := range strings.Split(string(output), "\x00") {
		if raw == "" {
			continue
		}
		path := raw
		if !filepath.IsAbs(path) {
			path = filepath.Join(plan.Root, path)
		}
		paths = append(paths, filepath.Clean(path))
		if canonical, err := filepath.EvalSymlinks(path); err == nil && canonical != path {
			paths = append(paths, filepath.Clean(canonical))
		}
	}
	return paths, true, nil
}

func windowsEnvironmentEntries(env map[string]string) []string {
	entries := make([]string, 0, len(env))
	for key, value := range env {
		entries = append(entries, key+"="+value)
	}
	sort.Strings(entries)
	return entries
}

func GlobScanPlanForPattern(pattern string, configuredMaxDepth *int) GlobScanPlan {
	// Rust #43903 keeps the lexical scan bound in the shared permission
	// protocol so policy validation and ACL expansion agree without filesystem
	// access.
	scan := coresandbox.WindowsDenyReadGlobScanForPattern(pattern, configuredMaxDepth)
	return GlobScanPlan{Root: scan.Root, MaxDepth: scan.MaxDepth}
}

func collectExistingGlobMatches(path string, matcher func(string) bool, paths *[]string, seenPaths map[string]bool, seenScanDirs map[string]bool, maxDepth *int, depth int) error {
	if _, err := os.Lstat(path); err != nil {
		return nil
	}
	if matcher(path) {
		pushDenyReadPath(paths, seenPaths, path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}
	key := CanonicalPathKey(path)
	if seenScanDirs[key] {
		return nil
	}
	seenScanDirs[key] = true
	if maxDepth != nil && depth >= *maxDepth {
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if err := collectExistingGlobMatches(filepath.Join(path, entry.Name()), matcher, paths, seenPaths, seenScanDirs, maxDepth, depth+1); err != nil && !strings.Contains(err.Error(), fs.SkipDir.Error()) {
			return err
		}
	}
	return nil
}

func pushDenyReadPath(paths *[]string, seen map[string]bool, path string) {
	path = cleanWindowsSandboxAbs(path)
	if path == "" || seen[path] {
		return
	}
	seen[path] = true
	*paths = append(*paths, path)
}

func resolveAgainstCWD(path string, cwd string) string {
	path = strings.TrimSpace(path)
	if path == "" || isWindowsSandboxAbs(path) || strings.TrimSpace(cwd) == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

func compileGlobMatcher(pattern string) (func(string) bool, error) {
	expr, err := globPatternToRegexp(pattern)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	return func(path string) bool {
		return re.MatchString(filepath.ToSlash(filepath.Clean(path)))
	}, nil
}

func globPatternToRegexp(pattern string) (string, error) {
	pattern = filepath.ToSlash(filepath.Clean(pattern))
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					out.WriteString("(?:.*/)?")
					i++
				} else {
					out.WriteString(".*")
				}
			} else {
				out.WriteString(`[^/]*`)
			}
		case '?':
			out.WriteString(`[^/]`)
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				out.WriteString(`\[`)
				continue
			}
			class := pattern[i : i+end+2]
			out.WriteString(class)
			i += end + 1
		case '/', '\\':
			out.WriteByte('/')
		default:
			out.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	out.WriteString("$")
	expr := out.String()
	_, err := regexp.Compile(expr)
	return expr, err
}
