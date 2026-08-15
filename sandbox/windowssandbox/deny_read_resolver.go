package windowssandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type DenyReadPolicy struct {
	Paths            []string
	Globs            []string
	GlobScanMaxDepth *int
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
	firstGlob := len(pattern)
	for index, ch := range pattern {
		if ch == '*' || ch == '?' || ch == '[' {
			firstGlob = index
			break
		}
	}
	literalPrefix := pattern[:firstGlob]
	separatorIndex := strings.LastIndexAny(literalPrefix, `/\`)
	if separatorIndex < 0 {
		return GlobScanPlan{Root: ".", MaxDepth: effectiveGlobScanMaxDepth(pattern, configuredMaxDepth)}
	}
	patternSuffix := pattern[separatorIndex+1:]
	isDriveRootSeparator := separatorIndex > 0 && literalPrefix[separatorIndex-1] == ':'
	if separatorIndex == 0 || isDriveRootSeparator {
		return GlobScanPlan{Root: literalPrefix[:separatorIndex+1], MaxDepth: effectiveGlobScanMaxDepth(patternSuffix, configuredMaxDepth)}
	}
	return GlobScanPlan{Root: literalPrefix[:separatorIndex], MaxDepth: effectiveGlobScanMaxDepth(patternSuffix, configuredMaxDepth)}
}

func effectiveGlobScanMaxDepth(patternSuffix string, configuredMaxDepth *int) *int {
	components := splitGlobComponents(patternSuffix)
	for _, component := range components {
		if component == "**" {
			if configuredMaxDepth == nil {
				return nil
			}
			value := *configuredMaxDepth
			return &value
		}
	}
	value := len(components)
	if configuredMaxDepth != nil && *configuredMaxDepth < value {
		value = *configuredMaxDepth
	}
	return &value
}

func splitGlobComponents(pattern string) []string {
	raw := strings.FieldsFunc(pattern, func(r rune) bool { return r == '/' || r == '\\' })
	out := raw[:0]
	for _, component := range raw {
		if component != "" {
			out = append(out, component)
		}
	}
	return out
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
