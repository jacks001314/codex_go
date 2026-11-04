package windowssandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	coresandbox "codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
)

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
	if profile == nil {
		return nil, nil
	}
	resolved, err := coresandbox.ResolveFileSystemPermissionsForEnforcement(profile, cwd, sandboxpath.ProjectionNative)
	if err != nil {
		return nil, err
	}
	return ResolveDenyReadPathsFromResolvedPermissions(resolved, cwd)
}

// ResolveDenyReadPathsFromResolvedPermissions mirrors Rust
// expand_windows_deny_read_paths (#59233): the resolved enforcement projection
// owns the concrete denials and deny globs the native ACL pass expands, so a
// narrower grant that reopens a deny path is not masked again.
func ResolveDenyReadPathsFromResolvedPermissions(resolved sandboxpath.ResolvedFileSystemPermissions, cwd string) ([]string, error) {
	if len(resolved.UnreadableRoots) == 0 && len(resolved.UnreadableGlobs) == 0 {
		return nil, nil
	}
	return resolveWindowsDenyReadPolicyPaths(DenyReadPolicy{
		Paths:            append([]string(nil), resolved.UnreadableRoots...),
		Globs:            append([]string(nil), resolved.UnreadableGlobs...),
		GlobScanMaxDepth: resolved.GlobScanMaxDepth,
	}, cwd)
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
	return resolveWindowsDenyReadPolicyPaths(policy, cwd)
}

// ResolveWindowsDenyReadPathsInEnvironment keeps the launcher call shape while
// expansion runs in-process. The command environment no longer selects an
// external scanner (Rust #59250).
func ResolveWindowsDenyReadPathsInEnvironment(
	policy DenyReadPolicy,
	cwd string,
	_ map[string]string,
	_ string,
) ([]string, error) {
	return resolveWindowsDenyReadPolicyPaths(policy, cwd)
}

func resolveWindowsDenyReadPolicyPaths(
	policy DenyReadPolicy,
	cwd string,
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
		seenScanDirs := map[string]bool{}
		if err := collectExistingGlobMatches(plan.Root, matcher, &paths, seen, seenScanDirs, plan.MaxDepth, 0); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func GlobScanPlanForPattern(pattern string, configuredMaxDepth *int) GlobScanPlan {
	// Rust #43903 keeps the lexical scan bound in the shared permission
	// protocol so policy validation and ACL expansion agree without filesystem
	// access.
	scan := coresandbox.WindowsDenyReadGlobScanForPattern(pattern, configuredMaxDepth)
	return GlobScanPlan{Root: scan.Root, MaxDepth: scan.MaxDepth}
}

func collectExistingGlobMatches(path string, matcher func(string) bool, paths *[]string, seenPaths map[string]bool, seenScanDirs map[string]bool, maxDepth *int, depth int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	isLink := info.Mode()&os.ModeSymlink != 0
	if matcher(path) {
		// A matching symlink is itself denied only when its target exists. A
		// dangling link is skipped like ignore::WalkBuilder's metadata check.
		if !isLink {
			pushDenyReadPath(paths, seenPaths, path)
		} else if _, targetErr := os.Stat(path); targetErr == nil {
			pushDenyReadPath(paths, seenPaths, path)
		}
	}
	if isLink {
		// Explicitly linked scan roots are followed; directory links below the
		// root are not traversed (Rust #59250).
		if depth != 0 {
			return nil
		}
		target, targetErr := os.Stat(path)
		if targetErr != nil || !target.IsDir() {
			return nil
		}
	} else if !info.IsDir() {
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
	entries, readErr := os.ReadDir(path)
	if readErr != nil {
		return nil
	}
	for _, entry := range entries {
		if err := collectExistingGlobMatches(filepath.Join(path, entry.Name()), matcher, paths, seenPaths, seenScanDirs, maxDepth, depth+1); err != nil {
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
			if end <= 0 {
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
