package config

import (
	"codex_go/sandbox"
	"codex_go/utils"
)

// FilesystemDenyReadEntriesFromPermissions converts a resolved
// `[permissions.filesystem] deny_read` value into policy entries. Every denial
// is validated before any entry is returned, literal paths retain their native
// spelling, glob spellings are preserved, and duplicate entries are removed.
func FilesystemDenyReadEntriesFromPermissions(
	permissions map[string]any,
	convention utils.PathConvention,
) ([]sandbox.FileSystemSandboxEntry, error) {
	filesystem, ok := permissions["filesystem"].(map[string]any)
	if !ok {
		return nil, nil
	}
	raw, ok := filesystem["deny_read"]
	if !ok {
		return nil, nil
	}
	patterns, err := denyReadPatterns(raw)
	if err != nil {
		return nil, err
	}
	entries := make([]sandbox.FileSystemSandboxEntry, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, pattern := range patterns {
		path, err := sandbox.ParseDenyReadPathForConvention(pattern, convention)
		if err != nil {
			return nil, err
		}
		key := filesystemPathKey(path)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, sandbox.FileSystemSandboxEntry{
			Path:   path,
			Access: sandbox.FileSystemAccessDeny,
		})
	}
	return entries, nil
}

// AppendFilesystemDenyReadEntries applies denials without changing existing
// entries and removes duplicates. It is intentionally separate from validation
// so callers can validate every denial before mutating a policy.
func AppendFilesystemDenyReadEntries(
	entries []sandbox.FileSystemSandboxEntry,
	denials []sandbox.FileSystemSandboxEntry,
) []sandbox.FileSystemSandboxEntry {
	seen := make(map[string]struct{}, len(entries)+len(denials))
	for _, entry := range entries {
		seen[filesystemEntryKey(entry)] = struct{}{}
	}
	for _, entry := range denials {
		key := filesystemEntryKey(entry)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, entry)
	}
	return entries
}

func filesystemEntryKey(entry sandbox.FileSystemSandboxEntry) string {
	return string(entry.Access) + "\x00" + filesystemPathKey(entry.Path)
}

func filesystemPathKey(path sandbox.FileSystemPath) string {
	switch path.Type {
	case "glob_pattern":
		return "glob\x00" + path.Pattern
	case "special":
		if path.Value == nil {
			return "special\x00"
		}
		subpath := ""
		if path.Value.Subpath != nil {
			subpath = *path.Value.Subpath
		}
		return "special\x00" + path.Value.Kind + "\x00" + path.Value.Path + "\x00" + subpath
	default:
		if uri, ok := utils.InferredPathURI(path.Path); ok {
			return "path\x00" + uri.IdentityKey()
		}
		return "path\x00" + path.Path
	}
}
