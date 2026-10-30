package mxc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
	"codex_go/sandbox/windowssandbox"
)

// PrepareFileSystemPolicy resolves configured symbols and snapshots deny globs
// for inspection without adding native grants or protections (Rust #51831).
func PrepareFileSystemPolicy(
	profile *sandbox.PermissionProfile,
	policyCWD string,
	env map[string]string,
	commandCWD string,
) (*FileSystemPolicy, error) {
	if profile == nil || profile.SandboxPolicy == nil {
		return nil, fmt.Errorf("MXC requires a managed permission profile")
	}
	if !filepath.IsAbs(policyCWD) || !filepath.IsAbs(commandCWD) {
		return nil, fmt.Errorf("MXC requires absolute policy and command working directories")
	}
	permissions, err := windowssandbox.ResolvePermissions(profile, nil)
	if err != nil {
		return nil, err
	}
	volumes, err := VolumeRoots(policyCWD, commandCWD)
	if err != nil {
		return nil, err
	}
	implicitReads := ImplicitReadRoots(volumes, policyCWD, commandCWD)
	write := pathSet{}
	read := pathSet{}
	if profile.SandboxPolicy.HasFullDiskWriteAccess() {
		for _, volume := range volumes {
			write.add(volume)
		}
	} else {
		for _, root := range permissions.WritableRootsForCWD(policyCWD, env) {
			write.add(root.Root)
			for _, carveout := range root.ReadOnlySubpaths {
				read.add(carveout)
			}
		}
	}
	if profile.SandboxPolicy.HasFullDiskReadAccess() {
		for _, volume := range implicitReads {
			read.add(volume)
		}
	} else if !write.has(policyCWD) {
		read.add(policyCWD)
	}
	if readOnlyRoots := permissions.ReadableRootsForCWD(policyCWD); len(readOnlyRoots) > 0 {
		for _, root := range readOnlyRoots {
			read.add(root)
		}
	}
	for _, root := range platformReadRoots() {
		read.add(root)
	}

	var denyPolicy windowssandbox.DenyReadPolicy
	for _, entry := range profile.DeniedReadEntries {
		switch entry.Path.Type {
		case "glob_pattern":
			denyPolicy.Globs = append(denyPolicy.Globs, entry.Path.Pattern)
		case "path":
			denyPolicy.Paths = append(denyPolicy.Paths, entry.Path.Path)
		}
	}
	// Rust #58168: MXC requires deny paths to have existing parents, and the
	// directories must exist before deny globs are expanded.
	if err := ensureDenyParentDirectories(denyPolicy.Paths); err != nil {
		return nil, err
	}
	denied, err := windowssandbox.ResolveWindowsDenyReadPathsInEnvironment(
		denyPolicy,
		policyCWD,
		env,
		commandCWD,
	)
	if err != nil {
		return nil, err
	}
	deny := pathSet{}
	for _, path := range denied {
		deny.add(path)
	}

	expandedWrite := expandVolumeRootPaths(write.values(), volumes)
	for path := range deny.paths {
		delete(expandedWrite, path)
	}
	// Rust #57985: read-only volume-root grants already recurse in MXC, so only
	// writable roots are expanded into their current children.
	expandedRead := expandReadVolumeRoots(read.values())
	for path := range expandedWrite {
		delete(expandedRead, path)
	}
	for path := range deny.paths {
		delete(expandedRead, path)
	}
	return &FileSystemPolicy{
		ReadWritePaths: sortedPathKeys(expandedWrite),
		ReadOnlyPaths:  sortedPathKeys(expandedRead),
		DeniedPaths:    deny.values(),
	}, nil
}

// PrepareEntriesPolicy applies the same MXC policy preparation directly to a
// canonical restricted-entry list, as used by the exec-server integrity runner.
func PrepareEntriesPolicy(
	entries []sandbox.FileSystemSandboxEntry,
	policyCWD string,
	env map[string]string,
	commandCWD string,
) (*FileSystemPolicy, error) {
	if !filepath.IsAbs(policyCWD) || !filepath.IsAbs(commandCWD) {
		return nil, fmt.Errorf("MXC requires absolute policy and command working directories")
	}
	volumes, err := VolumeRoots(policyCWD, commandCWD)
	if err != nil {
		return nil, err
	}
	implicitReads := ImplicitReadRoots(volumes, policyCWD, commandCWD)
	write := pathSet{}
	read := pathSet{}
	fullWrite := false
	fullRead := false
	includePlatformDefaults := false
	var denyPolicy windowssandbox.DenyReadPolicy
	add := func(set *pathSet, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(policyCWD, path)
		}
		set.add(path)
	}
	for _, entry := range entries {
		kind := ""
		if entry.Path.Type == "special" && entry.Path.Value != nil {
			kind = strings.ToLower(strings.TrimSpace(entry.Path.Value.Kind))
		}
		switch {
		case kind == "root":
			if entry.Access == sandbox.FileSystemAccessWrite {
				fullWrite = true
			} else if entry.Access == sandbox.FileSystemAccessRead {
				fullRead = true
			}
		case kind == "tmpdir":
			for _, path := range windowsTemporaryDirectories(env) {
				switch entry.Access {
				case sandbox.FileSystemAccessWrite:
					write.add(path)
				case sandbox.FileSystemAccessRead:
					read.add(path)
				case sandbox.FileSystemAccessDeny:
					denyPolicy.Paths = append(denyPolicy.Paths, path)
				}
			}
		case kind == "slash_tmp":
			// /tmp has no special meaning on a Windows executor.
		case kind == "minimal":
			if entry.Access == sandbox.FileSystemAccessRead || entry.Access == sandbox.FileSystemAccessWrite {
				includePlatformDefaults = true
				for _, path := range platformReadRoots() {
					add(&read, path)
				}
			}
		case kind == "project_roots" || kind == "current_working_directory":
			path := policyCWD
			if entry.Path.Value.Subpath != nil && strings.TrimSpace(*entry.Path.Value.Subpath) != "" {
				path = *entry.Path.Value.Subpath
			}
			switch entry.Access {
			case sandbox.FileSystemAccessWrite:
				add(&write, path)
			case sandbox.FileSystemAccessRead:
				add(&read, path)
			case sandbox.FileSystemAccessDeny:
				denyPolicy.Paths = append(denyPolicy.Paths, path)
			}
		case entry.Path.Type == "glob_pattern":
			if entry.Access == sandbox.FileSystemAccessDeny {
				denyPolicy.Globs = append(denyPolicy.Globs, entry.Path.Pattern)
			}
		default:
			path := sandbox.ResolveFileSystemEntryPath(entry.Path, policyCWD)
			switch entry.Access {
			case sandbox.FileSystemAccessWrite:
				add(&write, path)
			case sandbox.FileSystemAccessRead:
				add(&read, path)
			case sandbox.FileSystemAccessDeny:
				denyPolicy.Paths = append(denyPolicy.Paths, path)
			}
		}
	}
	if fullWrite {
		for _, volume := range volumes {
			write.add(volume)
		}
	}
	if fullRead {
		for _, volume := range implicitReads {
			read.add(volume)
		}
	}
	if includePlatformDefaults && !fullRead {
		for _, root := range platformReadRoots() {
			read.add(root)
		}
	}
	for _, root := range sandboxpath.WritableRootsWithProtectedSubpaths(write.values()) {
		for _, carveout := range root.ReadOnlySubpaths {
			read.add(carveout)
		}
	}
	// Rust #58168: create missing deny parents before glob expansion.
	if err := ensureDenyParentDirectories(denyPolicy.Paths); err != nil {
		return nil, err
	}
	denied, err := windowssandbox.ResolveWindowsDenyReadPathsInEnvironment(
		denyPolicy,
		policyCWD,
		env,
		commandCWD,
	)
	if err != nil {
		return nil, err
	}
	deny := pathSet{}
	for _, path := range denied {
		deny.add(path)
	}
	expandedWrite := expandVolumeRootPaths(write.values(), volumes)
	for path := range deny.paths {
		delete(expandedWrite, path)
	}
	expandedRead := expandReadVolumeRoots(read.values())
	for path := range expandedWrite {
		delete(expandedRead, path)
	}
	for path := range deny.paths {
		delete(expandedRead, path)
	}
	return &FileSystemPolicy{
		ReadWritePaths: sortedPathKeys(expandedWrite),
		ReadOnlyPaths:  sortedPathKeys(expandedRead),
		DeniedPaths:    deny.values(),
	}, nil
}

func windowsTemporaryDirectories(env map[string]string) []string {
	keys := make([]string, 0, 2)
	for key := range env {
		switch {
		case strings.EqualFold(key, "TEMP"), strings.EqualFold(key, "TMP"):
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := strings.ToUpper(keys[i]), strings.ToUpper(keys[j])
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	seenKeys := map[string]bool{}
	seenPaths := map[string]bool{}
	var values []string
	for _, key := range keys {
		upper := strings.ToUpper(key)
		if seenKeys[upper] {
			continue
		}
		seenKeys[upper] = true
		value := strings.TrimSpace(env[key])
		if value == "" || !filepath.IsAbs(value) {
			continue
		}
		value = filepath.Clean(value)
		keyValue := strings.ToLower(value)
		if seenPaths[keyValue] {
			continue
		}
		seenPaths[keyValue] = true
		values = append(values, value)
	}
	return values
}

// VolumeRoots mirrors Rust's MXC Windows volume enumeration. Unc paths are
// added even though GetLogicalDrives cannot report them.
func VolumeRoots(policyCWD, commandCWD string) ([]string, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getLogicalDrives := kernel32.NewProc("GetLogicalDrives")
	mask, _, _ := getLogicalDrives.Call()
	if mask == 0 {
		return nil, fmt.Errorf("cannot enumerate Windows volumes")
	}
	volumes := make([]string, 0, 26)
	for index := 0; index < 26; index++ {
		if mask&(1<<uint(index)) == 0 {
			continue
		}
		volumes = append(volumes, fmt.Sprintf("%c:\\", 'A'+rune(index)))
	}
	for _, cwd := range []string{policyCWD, commandCWD} {
		if volume := filepath.VolumeName(filepath.Clean(cwd)); volume != "" {
			volumes = append(volumes, volume+`\`)
		}
	}
	return uniquePathKeys(volumes), nil
}

func platformReadRoots() []string {
	roots := make([]string, 0, 5)
	if windowsDir, ok := os.LookupEnv("SystemRoot"); ok {
		if strings.TrimSpace(windowsDir) != "" {
			roots = append(roots, windowsDir)
		}
	}
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
			roots = append(roots, value)
		}
	}
	return roots
}

func expandVolumeRootPaths(paths []string, volumeRoots []string) map[string]string {
	volumes := pathSet{}
	for _, volume := range volumeRoots {
		volumes.add(volume)
	}
	expanded := map[string]string{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		key := windowssandbox.CanonicalPathKey(path)
		if key != "" {
			expanded[key] = filepath.Clean(path)
		}
	}
	for _, path := range paths {
		if volumes.has(path) {
			entries, err := os.ReadDir(path)
			if err == nil {
				for _, entry := range entries {
					child := filepath.Join(path, entry.Name())
					// Rust #57515: directory entries carry the file type, so
					// only symbolic links need a resolution probe. Broken or
					// self-referential links would prevent PSEC startup.
					if entry.Type()&os.ModeSymlink != 0 {
						if _, statErr := os.Stat(child); statErr != nil {
							continue
						}
					}
					add(child)
				}
			}
		}
		add(path)
	}
	return expanded
}

// expandReadVolumeRoots keeps read-only grants as-is. MXC already grants
// recursive read access to a volume root, so enumerating its children would
// only widen the policy to directories created later (Rust #57985).
func expandReadVolumeRoots(paths []string) map[string]string {
	expanded := make(map[string]string, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if key := windowssandbox.CanonicalPathKey(path); key != "" {
			expanded[key] = filepath.Clean(path)
		}
	}
	return expanded
}

// ImplicitReadRoots filters the default read volume roots (Rust #57783):
// local volumes and remote shares that contain the policy or command cwd stay,
// while unrelated network shares are neither probed nor enumerated.
func ImplicitReadRoots(volumeRoots []string, policyCWD, commandCWD string) []string {
	roots := make([]string, 0, len(volumeRoots))
	policyCWD = filepath.Clean(policyCWD)
	commandCWD = filepath.Clean(commandCWD)
	for _, root := range volumeRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if driveTypeOfRoot(root) != driveRemote {
			roots = append(roots, root)
			continue
		}
		if pathWithinRoot(policyCWD, root) || pathWithinRoot(commandCWD, root) {
			roots = append(roots, root)
		}
	}
	return roots
}

// driveRemote is DRIVE_REMOTE from GetDriveTypeW.
const driveRemote = 4

// driveTypeOfRoot probes a volume root's drive type. It is a variable so tests
// can model network shares without a real mapped drive.
var driveTypeOfRoot = windowsDriveType

func windowsDriveType(root string) uint32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDriveType := kernel32.NewProc("GetDriveTypeW")
	pathPtr, err := syscall.UTF16PtrFromString(strings.TrimSuffix(root, `\`) + `\`)
	if err != nil {
		return 0
	}
	driveType, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(pathPtr)))
	return uint32(driveType)
}

func pathWithinRoot(path string, root string) bool {
	if path == "" || root == "" {
		return false
	}
	root = strings.TrimSuffix(filepath.Clean(root), string(filepath.Separator))
	path = filepath.Clean(path)
	if strings.EqualFold(path, root) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(root)+string(filepath.Separator))
}

// ensureDenyParentDirectories creates missing parent directories for explicit
// deny paths (Rust #58168). MXC cannot evaluate a deny path whose parent does
// not exist, so the parents are created while the path itself is left absent.
func ensureDenyParentDirectories(paths []string) error {
	for _, denied := range paths {
		denied = strings.TrimSpace(denied)
		if denied == "" {
			continue
		}
		cleaned := filepath.Clean(denied)
		parent := filepath.Dir(cleaned)
		if parent == cleaned {
			continue
		}
		if info, err := os.Stat(parent); err == nil {
			_ = info
			continue
		} else if !os.IsNotExist(err) {
			continue
		}
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("MXC currently requires deny paths to have existing parent directories. Could not create %s: %w. Create this directory manually or correct the path/permissions, then retry", parent, err)
		}
	}
	return nil
}

type pathSet struct {
	paths map[string]string
}

func (s *pathSet) add(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	key := windowssandbox.CanonicalPathKey(path)
	if key == "" {
		return
	}
	if s.paths == nil {
		s.paths = map[string]string{}
	}
	if _, exists := s.paths[key]; !exists {
		s.paths[key] = filepath.Clean(path)
	}
}

func (s *pathSet) has(path string) bool {
	if s == nil || s.paths == nil {
		return false
	}
	return s.paths[windowssandbox.CanonicalPathKey(path)] != ""
}

func (s *pathSet) values() []string {
	return sortedPathKeys(s.paths)
}

func sortedPathKeys(paths map[string]string) []string {
	values := make([]string, 0, len(paths))
	for _, path := range paths {
		values = append(values, path)
	}
	sort.Slice(values, func(i, j int) bool {
		return strings.ToLower(values[i]) < strings.ToLower(values[j])
	})
	return values
}

func uniquePathKeys(paths []string) []string {
	set := pathSet{}
	for _, path := range paths {
		set.add(path)
	}
	return set.values()
}
