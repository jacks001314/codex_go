package appserver

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
	"codex_go/turn"
)

// SkillFileSystem is the borrowed, permission-scoped view of the filesystem the
// skill loaders read through (Rust #59278-#59280 EnvironmentAccess). Host roots
// keep host authority, while repository roots use the turn's captured
// filesystem permissions.
type SkillFileSystem interface {
	// Key identifies the filesystem and captured permissions for cache keys.
	Key() string
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (os.FileInfo, error)
	EvalSymlinks(path string) (string, error)
	// WatchSupported reports whether unsandboxed executor watches may observe
	// these paths. A restricted accessor falls back to live discovery.
	WatchSupported() bool
}

// HostSkillFileSystem reads through the process filesystem with no additional
// restriction.
type HostSkillFileSystem struct{}

// HostSkillFileSystemKey is the cache identity of unrestricted host reads.
const HostSkillFileSystemKey = "host"

func (HostSkillFileSystem) Key() string                                { return HostSkillFileSystemKey }
func (HostSkillFileSystem) ReadFile(path string) ([]byte, error)       { return os.ReadFile(path) }
func (HostSkillFileSystem) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
func (HostSkillFileSystem) Stat(path string) (os.FileInfo, error)      { return os.Stat(path) }
func (HostSkillFileSystem) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
func (HostSkillFileSystem) WatchSupported() bool { return true }

// nilSkillFileSystem lets callers leave the accessor unset.
func skillFileSystemOrHost(fs SkillFileSystem) SkillFileSystem {
	if fs == nil {
		return HostSkillFileSystem{}
	}
	return fs
}

// PermissionSkillFileSystem enforces the resolved readable roots, concrete
// denials and deny globs of one captured filesystem policy.
type PermissionSkillFileSystem struct {
	key             string
	fullRead        bool
	readableRoots   []string
	unreadableRoots []string
	unreadableGlobs []skillGlob
}

type skillGlob struct {
	pattern string
	re      *regexp.Regexp
}

// NewPermissionSkillFileSystem resolves the policy once and returns an accessor
// that answers reads with the captured permissions (Rust #59279).
func NewPermissionSkillFileSystem(profile *sandbox.PermissionProfile, cwd string, workspaceRoots []string) (SkillFileSystem, error) {
	if profile == nil || profile.Disabled {
		return HostSkillFileSystem{}, nil
	}
	resolved, err := sandbox.ResolveFileSystemPermissionsForEnforcement(profile, cwd, sandboxpath.ProjectionNative)
	if err != nil {
		return nil, err
	}
	access := &PermissionSkillFileSystem{
		fullRead:        resolved.HasFullDiskReadAccess,
		readableRoots:   normalizeSkillRoots(resolved.ReadableRoots),
		unreadableRoots: normalizeSkillRoots(resolved.UnreadableRoots),
	}
	for _, pattern := range resolved.UnreadableGlobs {
		if compiled, err := compileSkillGlob(pattern); err == nil {
			access.unreadableGlobs = append(access.unreadableGlobs, compiled)
		}
	}
	if len(access.readableRoots) == 0 && !access.fullRead {
		for _, root := range workspaceRoots {
			if cleaned := cleanSkillPath(root); cleaned != "" {
				access.readableRoots = append(access.readableRoots, cleaned)
			}
		}
	}
	access.key = access.cacheKey(cwd)
	return access, nil
}

// Key identifies the captured permissions so a changed profile rescans.
func (a *PermissionSkillFileSystem) Key() string { return a.key }

func (a *PermissionSkillFileSystem) WatchSupported() bool {
	return a != nil && a.fullRead && len(a.unreadableRoots) == 0 && len(a.unreadableGlobs) == 0
}

func (a *PermissionSkillFileSystem) ReadFile(path string) ([]byte, error) {
	if err := a.checkRead(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (a *PermissionSkillFileSystem) ReadDir(path string) ([]os.DirEntry, error) {
	if err := a.checkRead(path); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := entries[:0]
	for _, entry := range entries {
		if a.allowed(filepath.Join(path, entry.Name())) {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (a *PermissionSkillFileSystem) Stat(path string) (os.FileInfo, error) {
	if err := a.checkRead(path); err != nil {
		return nil, err
	}
	return os.Stat(path)
}

func (a *PermissionSkillFileSystem) EvalSymlinks(path string) (string, error) {
	if err := a.checkRead(path); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func (a *PermissionSkillFileSystem) checkRead(path string) error {
	if a == nil {
		return nil
	}
	if a.allowed(path) {
		return nil
	}
	return &fs.PathError{Op: "read", Path: path, Err: fs.ErrPermission}
}

// allowed mirrors the policy precedence: the deepest readable grant reopens a
// concrete deny root, and a deny glob masks anything it matches.
func (a *PermissionSkillFileSystem) allowed(path string) bool {
	cleaned := cleanSkillPath(path)
	if cleaned == "" {
		return false
	}
	for _, glob := range a.unreadableGlobs {
		if globMatches(glob, cleaned) {
			return false
		}
	}
	deniedLen := deepestRootLen(a.unreadableRoots, cleaned)
	if deniedLen < 0 {
		if a.fullRead {
			return true
		}
		return deepestRootLen(a.readableRoots, cleaned) >= 0
	}
	if a.fullRead {
		return false
	}
	// A narrower readable grant under a deny root reopens that subtree.
	return deepestRootLen(a.readableRoots, cleaned) > deniedLen
}

func (a *PermissionSkillFileSystem) cacheKey(cwd string) string {
	parts := []string{"permissions", "cwd=" + cleanSkillPath(cwd)}
	if a.fullRead {
		parts = append(parts, "full-read")
	}
	parts = append(parts, "read="+strings.Join(a.readableRoots, ";"))
	parts = append(parts, "deny="+strings.Join(a.unreadableRoots, ";"))
	globs := make([]string, 0, len(a.unreadableGlobs))
	for _, glob := range a.unreadableGlobs {
		globs = append(globs, glob.pattern)
	}
	parts = append(parts, "globs="+strings.Join(globs, ";"))
	return strings.Join(parts, "|")
}

func normalizeSkillRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if cleaned := cleanSkillPath(root); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) < len(out[j]) })
	return out
}

func cleanSkillPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func deepestRootLen(roots []string, path string) int {
	best := -1
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			if len(root) > best {
				best = len(root)
			}
		}
	}
	return best
}

func compileSkillGlob(pattern string) (skillGlob, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return skillGlob{}, errors.New("empty deny glob")
	}
	normalized := filepath.ToSlash(pattern)
	var expression strings.Builder
	expression.WriteString("^")
	for i := 0; i < len(normalized); i++ {
		ch := normalized[i]
		switch ch {
		case '*':
			if i+1 < len(normalized) && normalized[i+1] == '*' {
				expression.WriteString(".*")
				i++
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	expression.WriteString("$")
	re, err := regexp.Compile(expression.String())
	if err != nil {
		return skillGlob{}, err
	}
	return skillGlob{pattern: pattern, re: re}, nil
}

func globMatches(glob skillGlob, path string) bool {
	if glob.re == nil {
		return false
	}
	return glob.re.MatchString(filepath.ToSlash(path))
}

// skillFileSystemForTurn returns the turn's permission-scoped accessor for
// repository skill roots (Rust #59278/#59279/#59280). Unrestricted turns keep
// host access, and user/plugin/system roots always use host authority.
func (r *RuntimeRouter) skillFileSystemForTurn(cfg *config.Config, params *turn.TurnStartParams) SkillFileSystem {
	if r == nil || cfg == nil || params == nil {
		return nil
	}
	cwd := turnCWD(params)
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil || resolution == nil || resolution.Profile == nil || resolution.Profile.Disabled {
		return nil
	}
	if resolution.Profile.LegacySandboxPolicy().HasFullDiskReadAccess() {
		return nil
	}
	access, err := NewPermissionSkillFileSystem(resolution.Profile, cwd, nil)
	if err != nil {
		return nil
	}
	return access
}
