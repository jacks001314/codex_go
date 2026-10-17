package sandbox

import (
	"net/url"
	"path"
	"sort"
	"strings"
)

// PermissionPromptExecutorContext carries the selected environment's filesystem
// facts so permission paths render in the executor's native form (Rust #47867).
// An empty UserHomeDir leaves home-relative entries unresolved, and an empty
// CWD leaves relative entries unresolved.
type PermissionPromptExecutorContext struct {
	CWD         string
	UserHomeDir string
	PlatformOS  string
}

type permissionPromptPathConvention struct {
	windows bool
}

func (c permissionPromptPathConvention) separator() string {
	if c.windows {
		return `\`
	}
	return "/"
}

// permissionPromptExecutorConvention picks the executor's path convention from
// its reported platform, falling back to the shape of its working directory.
func permissionPromptExecutorConvention(context *PermissionPromptExecutorContext) permissionPromptPathConvention {
	if context == nil {
		return permissionPromptPathConvention{}
	}
	platform := strings.ToLower(strings.TrimSpace(context.PlatformOS))
	if platform == "windows" || strings.HasPrefix(platform, "windows") {
		return permissionPromptPathConvention{windows: true}
	}
	if platform != "" {
		return permissionPromptPathConvention{}
	}
	return permissionPromptPathConvention{windows: permissionPromptLooksLikeWindowsPath(permissionPromptNativePath(context.CWD))}
}

func permissionPromptLooksLikeWindowsPath(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 2 && permissionPromptDriveLetter(value[0]) && value[1] == ':'
}

func permissionPromptDriveLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

// permissionPromptNativePath projects file URIs to the same native spelling the
// executor uses. Non-URI spellings are preserved verbatim.
func permissionPromptNativePath(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(trimmed), "file://") {
		return trimmed
	}
	rest := trimmed[len("file://"):]
	// file:///C:/workspace -> C:/workspace, file:///workspace -> /workspace.
	if len(rest) >= 3 && rest[0] == '/' && permissionPromptDriveLetter(rest[1]) && rest[2] == ':' {
		rest = rest[1:]
	}
	if decoded, err := url.PathUnescape(rest); err == nil {
		rest = decoded
	}
	return rest
}

func permissionPromptIsAbsolutePath(value string, convention permissionPromptPathConvention) bool {
	if !convention.windows {
		return strings.HasPrefix(value, "/")
	}
	if strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 3 && permissionPromptDriveLetter(value[0]) && value[1] == ':' &&
		(value[2] == '\\' || value[2] == '/')
}

// permissionPromptIsDriveRelativePath reports C:foo spellings, which cannot be
// resolved without more executor facts.
func permissionPromptIsDriveRelativePath(value string, convention permissionPromptPathConvention) bool {
	return convention.windows && len(value) >= 2 && permissionPromptDriveLetter(value[0]) &&
		value[1] == ':' && !permissionPromptIsAbsolutePath(value, convention)
}

// resolvePermissionPromptExecutorPath resolves a path or glob against the
// executor's cwd and home directory, returning ok=false when the entry cannot
// be resolved with the supplied facts (Rust #47867).
func resolvePermissionPromptExecutorPath(value string, context *PermissionPromptExecutorContext, glob bool) (string, bool) {
	if context == nil {
		return "", false
	}
	convention := permissionPromptExecutorConvention(context)
	resolved := permissionPromptNativePath(value)
	if resolved == "" {
		return "", false
	}
	switch {
	case resolved == "~" || strings.HasPrefix(resolved, "~/") || strings.HasPrefix(resolved, `~\`):
		home := permissionPromptNativePath(context.UserHomeDir)
		if strings.TrimSpace(home) == "" {
			return "", false
		}
		resolved = permissionPromptJoinPath(home, strings.TrimLeft(resolved[1:], `/\`), convention, glob)
	case permissionPromptIsAbsolutePath(resolved, convention):
		resolved = permissionPromptCleanPath(resolved, convention, glob)
	case permissionPromptIsDriveRelativePath(resolved, convention):
		return "", false
	default:
		cwd := permissionPromptNativePath(context.CWD)
		if strings.TrimSpace(cwd) == "" {
			return "", false
		}
		resolved = permissionPromptJoinPath(cwd, resolved, convention, glob)
	}
	if strings.TrimSpace(resolved) == "" {
		return "", false
	}
	return resolved, true
}

func permissionPromptJoinPath(base string, relative string, convention permissionPromptPathConvention, glob bool) string {
	separator := convention.separator()
	if convention.windows {
		base = strings.ReplaceAll(base, "/", separator)
		relative = strings.ReplaceAll(relative, "/", separator)
		joined := strings.TrimRight(base, separator) + separator + strings.TrimLeft(relative, separator)
		return permissionPromptCleanPath(joined, convention, glob)
	}
	joined := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(relative, "/")
	return permissionPromptCleanPath(joined, convention, glob)
}

// permissionPromptCleanPath normalizes separators and dot segments. Glob
// patterns keep every component verbatim because `path.Clean` would treat
// wildcards as ordinary segments and could drop meaningful spellings.
func permissionPromptCleanPath(value string, convention permissionPromptPathConvention, glob bool) string {
	if glob {
		if convention.windows {
			return strings.ReplaceAll(value, "/", `\`)
		}
		return strings.ReplaceAll(value, `\`, "/")
	}
	if !convention.windows {
		return path.Clean(strings.ReplaceAll(value, `\`, "/"))
	}
	unc := strings.HasPrefix(value, `\\`)
	cleaned := path.Clean(strings.ReplaceAll(value, `\`, "/"))
	if unc && !strings.HasPrefix(cleaned, "//") {
		cleaned = "/" + cleaned
	}
	return strings.ReplaceAll(cleaned, "/", `\`)
}

// permissionPromptExecutorSpecialPath resolves a special deny-read entry with
// executor facts. Temp-directory and project-root facts are not part of the
// environment info snapshot, so those entries stay unresolved and are omitted.
func permissionPromptExecutorSpecialPath(value *FileSystemSpecialPath, context *PermissionPromptExecutorContext) (string, bool) {
	if value == nil || context == nil {
		return "", false
	}
	convention := permissionPromptExecutorConvention(context)
	switch strings.ToLower(strings.TrimSpace(value.Kind)) {
	case "root":
		cwd := permissionPromptNativePath(context.CWD)
		if convention.windows && len(cwd) >= 2 && permissionPromptDriveLetter(cwd[0]) && cwd[1] == ':' {
			return strings.ToUpper(cwd[:1]) + `:\`, true
		}
		if convention.windows {
			return `\`, true
		}
		return "/", true
	case "current_working_directory":
		resolved, ok := resolvePermissionPromptExecutorPath(context.CWD, context, false)
		if !ok {
			return "", false
		}
		if value.Subpath != nil && strings.TrimSpace(*value.Subpath) != "" {
			return permissionPromptJoinPath(resolved, *value.Subpath, convention, false), true
		}
		return resolved, true
	default:
		return "", false
	}
}

// permissionPromptSandboxModeForExecutorContext mirrors Rust's context-aware
// profile adapter: the mode follows the executor's writable roots instead of
// the controller's host filesystem.
func permissionPromptSandboxModeForExecutorContext(profile *PermissionProfile, context *PermissionPromptExecutorContext) (PermissionPromptSandboxMode, []string) {
	if profile == nil {
		return PermissionPromptWorkspaceWrite, nil
	}
	policy := profile.LegacySandboxPolicy()
	if profile.Disabled || (policy != nil && policy.HasFullDiskWriteAccess()) {
		return PermissionPromptDangerFullAccess, nil
	}
	if policy == nil || policy.Kind != SandboxWorkspaceWrite {
		return PermissionPromptReadOnly, nil
	}
	roots := permissionPromptExecutorWritableRoots(policy, context)
	if len(roots) == 0 {
		return PermissionPromptReadOnly, nil
	}
	return PermissionPromptWorkspaceWrite, roots
}

func permissionPromptExecutorWritableRoots(policy *SandboxPolicy, context *PermissionPromptExecutorContext) []string {
	if policy == nil || context == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var roots []string
	add := func(value string) {
		resolved, ok := resolvePermissionPromptExecutorPath(value, context, false)
		if !ok {
			return
		}
		if _, duplicate := seen[resolved]; duplicate {
			return
		}
		seen[resolved] = struct{}{}
		roots = append(roots, resolved)
	}
	for _, root := range policy.WritableRoots {
		add(root)
	}
	if cwd := strings.TrimSpace(context.CWD); cwd != "" {
		add(cwd)
	}
	sort.Strings(roots)
	return roots
}

// permissionPromptDeniedReadsForExecutorContext resolves each deny entry
// independently, so one entry with missing executor facts does not hide the
// others (Rust #47867).
func permissionPromptDeniedReadsForExecutorContext(profile *PermissionProfile, context *PermissionPromptExecutorContext) ([]string, []string) {
	if profile == nil || context == nil {
		return nil, nil
	}
	var paths, globs []string
	seenPaths := map[string]struct{}{}
	seenGlobs := map[string]struct{}{}
	add := func(destination *[]string, seen map[string]struct{}, value string) {
		if _, duplicate := seen[value]; duplicate {
			return
		}
		seen[value] = struct{}{}
		*destination = append(*destination, value)
	}
	for _, entry := range profile.DeniedReadEntries {
		if entry.Access != FileSystemAccessDeny {
			continue
		}
		switch entry.Path.Type {
		case "glob_pattern":
			resolved, ok := resolvePermissionPromptExecutorPath(entry.Path.Pattern, context, true)
			if ok {
				add(&globs, seenGlobs, resolved)
			}
		case "special":
			resolved, ok := permissionPromptExecutorSpecialPath(entry.Path.Value, context)
			if ok {
				add(&paths, seenPaths, resolved)
			}
		default:
			resolved, ok := resolvePermissionPromptExecutorPath(entry.Path.Path, context, false)
			if ok {
				add(&paths, seenPaths, resolved)
			}
		}
	}
	sort.Strings(paths)
	sort.Strings(globs)
	return paths, globs
}
