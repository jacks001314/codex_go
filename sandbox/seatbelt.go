package sandbox

import (
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

const macOSSeatbeltExecutable = "/usr/bin/sandbox-exec"

type seatbeltParameter struct {
	Name  string
	Value string
}

func createSeatbeltCommandArgs(command []string, cwd string, profile *PermissionProfile, allowUnixSockets []string) ([]string, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("%w: command is required", ErrInvalidSandboxRunRequest)
	}
	policy, parameters, err := buildSeatbeltPolicy(cwd, profile, allowUnixSockets)
	if err != nil {
		return nil, err
	}
	args := []string{macOSSeatbeltExecutable}
	for _, parameter := range parameters {
		args = append(args, "-D", parameter.Name+"="+parameter.Value)
	}
	args = append(args, "-p", policy, "/usr/bin/env", "CODEX_SANDBOX=seatbelt")
	return append(args, command...), nil
}

func buildSeatbeltPolicy(cwd string, profile *PermissionProfile, allowUnixSockets []string) (string, []seatbeltParameter, error) {
	if profile == nil || profile.Disabled {
		return "(version 1)\n(allow default)\n", nil, nil
	}
	policy := profile.LegacySandboxPolicy()
	lines := []string{"(version 1)", "(allow default)"}
	var parameters []seatbeltParameter

	if !policy.HasFullDiskWriteAccess() {
		lines = append(lines, "(deny file-write*)", `(allow file-write-data (literal "/dev/null"))`)
		for _, root := range policy.GetWritableRootsWithCWD(cwd) {
			name := fmt.Sprintf("WRITABLE_ROOT_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: cleanSeatbeltPath(root.Root)})
			lines = append(lines, fmt.Sprintf(`(allow file-write* (subpath (param "%s")))`, name))
			for _, protected := range append(append([]string(nil), root.ReadOnlySubpaths...), protectedMetadataPaths(root.Root, root.ProtectedMetadataNames)...) {
				protectedName := fmt.Sprintf("PROTECTED_WRITE_%d", len(parameters))
				parameters = append(parameters, seatbeltParameter{Name: protectedName, Value: cleanSeatbeltPath(protected)})
				lines = append(lines, fmt.Sprintf(`(deny file-write* (subpath (param "%s")))`, protectedName))
			}
		}
	}

	for _, denied := range seatbeltDeniedReadPaths(profile.DeniedReadEntries) {
		name := fmt.Sprintf("DENIED_READ_%d", len(parameters))
		parameters = append(parameters, seatbeltParameter{Name: name, Value: denied})
		lines = append(lines, fmt.Sprintf(`(deny file-read* (subpath (param "%s")))`, name))
	}

	if !profile.AllowsNetwork() {
		lines = append(lines, "(deny network*)", "(deny system-socket)")
	}
	for _, socket := range cleanAbsoluteSeatbeltPaths(allowUnixSockets) {
		name := fmt.Sprintf("UNIX_SOCKET_%d", len(parameters))
		parameters = append(parameters, seatbeltParameter{Name: name, Value: socket})
		lines = append(lines,
			"(allow system-socket (socket-domain AF_UNIX))",
			fmt.Sprintf(`(allow network-bind (local unix-socket (subpath (param "%s"))))`, name),
			fmt.Sprintf(`(allow network-outbound (remote unix-socket (subpath (param "%s"))))`, name),
		)
	}
	return strings.Join(lines, "\n") + "\n", parameters, nil
}

func protectedMetadataPaths(root string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			out = append(out, joinSeatbeltPath(root, name))
		}
	}
	return out
}

func seatbeltDeniedReadPaths(entries []FileSystemSandboxEntry) []string {
	var paths []string
	for _, entry := range entries {
		if entry.Access != FileSystemAccessDeny || entry.Path.Type == "glob_pattern" || entry.Path.Value != nil {
			continue
		}
		if path := strings.TrimSpace(entry.Path.Path); isSeatbeltAbsolutePath(path) {
			paths = append(paths, cleanSeatbeltPath(path))
		}
	}
	return cleanAbsoluteSeatbeltPaths(paths)
}

func cleanAbsoluteSeatbeltPaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		if !isSeatbeltAbsolutePath(path) {
			continue
		}
		path = cleanSeatbeltPath(path)
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func isSeatbeltAbsolutePath(path string) bool {
	path = strings.TrimSpace(path)
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\\`) || (len(path) >= 3 && path[1] == ':' && (path[2] == '/' || path[2] == '\\'))
}

func cleanSeatbeltPath(value string) string {
	if strings.HasPrefix(value, "/") {
		return pathpkg.Clean(value)
	}
	return filepath.Clean(value)
}

func joinSeatbeltPath(root string, element string) string {
	if strings.HasPrefix(root, "/") {
		return pathpkg.Join(root, element)
	}
	return filepath.Join(root, element)
}
