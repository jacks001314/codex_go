package config

// Project trust lookup.
//
// Rust parity: codex-config's project_trust module (#47620). A lookup is the
// ordered list of project-map keys for one executor working directory: cwd
// spellings take precedence over the repository root's, and each canonical
// (executor-normalized) spelling is tried before its original spelling. The
// lookup is resolved independently of configuration layers and then applied to
// the final merged projects map, so a cwd entry — including one without a trust
// level — wins over a repository-root entry.

import (
	"runtime"
	"sort"
	"strings"
)

// ProjectTrustPath is the original and optionally executor-normalized canonical
// spelling of one path (Rust ProjectTrustPath).
type ProjectTrustPath struct {
	Original  string
	Canonical *string
}

// ProjectTrustLookup is the ordered project-key list for one working directory.
type ProjectTrustLookup struct {
	caseInsensitive bool
	keys            []string
}

// ProjectTrustLookupFromPaths builds the ordered lookup keys for a cwd and an
// optional repository root. It performs no filesystem access or path
// resolution: callers must supply executor-normalized canonical spellings.
// Windows keys are matched without ASCII case distinctions.
func ProjectTrustLookupFromPaths(windowsConvention bool, cwd ProjectTrustPath, repoRoot *ProjectTrustPath) ProjectTrustLookup {
	lookup := ProjectTrustLookup{caseInsensitive: windowsConvention}
	paths := []ProjectTrustPath{cwd}
	if repoRoot != nil {
		paths = append(paths, *repoRoot)
	}
	for _, path := range paths {
		original := lookup.normalize(path.Original)
		canonical := original
		if path.Canonical != nil {
			canonical = lookup.normalize(*path.Canonical)
		}
		if canonical != original {
			lookup.keys = append(lookup.keys, canonical)
		}
		lookup.keys = append(lookup.keys, original)
	}
	return lookup
}

// ProjectTrustLookupFromNativePath builds the lookup for one native path using
// the host convention (Rust ProjectTrustLookup::from_native_path).
func ProjectTrustLookupFromNativePath(path string) ProjectTrustLookup {
	return ProjectTrustLookupFromPaths(runtime.GOOS == "windows", nativeTrustPath(path), nil)
}

// Keys returns the ordered lookup keys (canonical before original).
func (l ProjectTrustLookup) Keys() []string {
	return append([]string(nil), l.keys...)
}

// ProjectTrustLookupForTarget builds the lookup for a working directory and an
// optional repository/project root, using the host path convention. Cwd keys
// take precedence over the root's, mirroring Rust's
// ProjectTrustLookup::from_paths at the configuration-resolution boundary.
//
// ProjectConfigEnabled's ancestor walk is a different, onboarding-specific
// check (directory_trust_persisted); this helper is the portable lookup the
// Rust change introduced.
func ProjectTrustLookupForTarget(cwd string, repoRoot string) ProjectTrustLookup {
	windows := runtime.GOOS == "windows"
	cwdPath := nativeTrustPath(cwd)
	var rootPath *ProjectTrustPath
	if strings.TrimSpace(repoRoot) != "" {
		root := nativeTrustPath(repoRoot)
		rootPath = &root
	}
	return ProjectTrustLookupFromPaths(windows, cwdPath, rootPath)
}

// nativeTrustPath pairs a path's literal spelling with its normalized canonical
// spelling.
func nativeTrustPath(path string) ProjectTrustPath {
	original := strings.TrimSpace(path)
	canonical := canonicalProjectPath(original)
	var canonicalPtr *string
	if canonical != "" && canonical != original {
		canonicalPtr = &canonical
	}
	return ProjectTrustPath{Original: original, Canonical: canonicalPtr}
}

func (l ProjectTrustLookup) normalize(key string) string {
	if l.caseInsensitive {
		return strings.ToLower(key)
	}
	return key
}

// ActiveProject selects the first matching project entry from a merged projects
// map. An exact key wins; otherwise the case-insensitive candidates are ordered
// so the selection stays deterministic on Windows. The entry may omit its trust
// level, which still counts as a match.
func (l ProjectTrustLookup) ActiveProject(projects map[string]any) (map[string]any, bool) {
	if len(projects) == 0 {
		return nil, false
	}
	keys := make([]string, 0, len(projects))
	for key := range projects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, wanted := range l.keys {
		if project, ok := projectEntry(projects[wanted]); ok {
			return project, true
		}
		for _, candidate := range keys {
			if l.normalize(candidate) != wanted {
				continue
			}
			if project, ok := projectEntry(projects[candidate]); ok {
				return project, true
			}
			break
		}
	}
	return nil, false
}

// projectEntry accepts a project table; a non-table value is not a project.
func projectEntry(raw any) (map[string]any, bool) {
	project, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	return project, true
}

// ActiveProjectForLookup applies a lookup to the effective projects map.
func (c *Config) ActiveProjectForLookup(lookup ProjectTrustLookup) (map[string]any, bool) {
	if c == nil {
		return nil, false
	}
	projects, ok := c.Values["projects"].(map[string]any)
	if !ok {
		return nil, false
	}
	return lookup.ActiveProject(projects)
}

// ProjectTrustLevelForLookup returns the trust level recorded for a lookup's
// active project. ok is false when no project matches or the matching entry has
// no trust level.
func (c *Config) ProjectTrustLevelForLookup(lookup ProjectTrustLookup) (string, bool) {
	project, ok := c.ActiveProjectForLookup(lookup)
	if !ok {
		return "", false
	}
	level, ok := project["trust_level"].(string)
	if !ok {
		return "", false
	}
	level = strings.TrimSpace(level)
	if level == "" {
		return "", false
	}
	return level, true
}
