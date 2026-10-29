package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const macOSSeatbeltExecutable = "/usr/bin/sandbox-exec"

type seatbeltParameter struct {
	Name  string
	Value string
}

// seatbeltManagedNetworkContext is the subset of the prepared managed-network
// context that shapes the Seatbelt policy. Rust #45548 reads this policy from
// the context for this command instead of falling back to a live proxy.
type seatbeltManagedNetworkContext struct {
	AllowUnixSockets               []string `json:"allowUnixSockets"`
	DangerouslyAllowAllUnixSockets bool     `json:"dangerouslyAllowAllUnixSockets"`
}

func parseSeatbeltManagedNetworkContext(raw string) (*seatbeltManagedNetworkContext, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var context seatbeltManagedNetworkContext
	if err := json.Unmarshal([]byte(raw), &context); err != nil {
		return nil, fmt.Errorf("managed network context is invalid: %w", err)
	}
	return &context, nil
}

func createSeatbeltCommandArgs(command []string, cwd string, profile *PermissionProfile, allowUnixSockets []string, allowedSymlinkedCodexHome string) ([]string, error) {
	return createSeatbeltCommandArgsWithManagedNetwork(command, cwd, profile, allowUnixSockets, nil, allowedSymlinkedCodexHome)
}

func createSeatbeltCommandArgsWithManagedNetwork(command []string, cwd string, profile *PermissionProfile, allowUnixSockets []string, managedNetwork *seatbeltManagedNetworkContext, allowedSymlinkedCodexHome string) ([]string, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("%w: command is required", ErrInvalidSandboxRunRequest)
	}
	policy, parameters, err := buildSeatbeltPolicyWithManagedNetwork(cwd, profile, allowUnixSockets, managedNetwork, allowedSymlinkedCodexHome)
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

func buildSeatbeltPolicy(cwd string, profile *PermissionProfile, allowUnixSockets []string, allowedSymlinkedCodexHome string) (string, []seatbeltParameter, error) {
	return buildSeatbeltPolicyWithManagedNetwork(cwd, profile, allowUnixSockets, nil, allowedSymlinkedCodexHome)
}

func buildSeatbeltPolicyWithManagedNetwork(cwd string, profile *PermissionProfile, allowUnixSockets []string, managedNetwork *seatbeltManagedNetworkContext, allowedSymlinkedCodexHome string) (string, []seatbeltParameter, error) {
	if profile == nil || profile.Disabled {
		return "(version 1)\n(allow default)\n", nil, nil
	}
	policy := profile.LegacySandboxPolicy()
	lines := []string{"(version 1)", "(allow default)"}
	var parameters []seatbeltParameter
	unreadable := seatbeltDeniedReadPaths(profile.DeniedReadEntries)
	// Rust #46571: the implicit scratch grants and the writable roots share the
	// same exclusion set, so a protected path (a read-only subpath or protected
	// project metadata) is never reopened by a broader allowance.
	protectedPaths := seatbeltProtectedPaths(policy, cwd)
	scratchRoots := cleanAbsoluteSeatbeltPaths([]string{"/tmp", "/var/tmp", "/private/tmp", "/private/var/tmp"})
	var scratchExclusions []string
	for _, scratchRoot := range scratchRoots {
		for _, excluded := range append(append([]string(nil), unreadable...), protectedPaths...) {
			if seatbeltSubpathWithin(excluded, scratchRoot) {
				scratchExclusions = appendUniqueSeatbeltPath(scratchExclusions, excluded)
			}
		}
	}

	if !policy.HasFullDiskWriteAccess() {
		lines = append(lines, "(deny file-write*)", `(allow file-write-data (literal "/dev/null"))`)
		// macOS #40961: ordinary sandboxed processes keep system scratch
		// directories for compatibility, while denying them to filesystem
		// helpers (which use a separate restricted policy). These grants are
		// process-only and mirror Rust's MACOS_PROCESS_PLATFORM_DEFAULTS.
		for _, scratch := range scratchRoots {
			name := fmt.Sprintf("SCRATCH_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: scratch})
			lines = append(lines, fmt.Sprintf(`(allow file-read* file-test-existence file-write* (subpath (param "%s")))`, name))
		}
		// Rust #46571: route the scratch write grants through the normal exclusions
		// so they respect unreadable paths, read-only paths, and protected
		// metadata. Scratch reads stay governed by the global deny-read policy,
		// which Rust applies to the exclusion set as well.
		for _, excluded := range scratchExclusions {
			name := fmt.Sprintf("SCRATCH_EXCLUDED_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: excluded})
			lines = append(lines, fmt.Sprintf(`(deny file-write* (subpath (param "%s")))`, name))
		}
		normalizedHome, err := normalizeSeatbeltAllowedHome(allowedSymlinkedCodexHome)
		if err != nil {
			return "", nil, err
		}
		allowedSymlinkedCodexHome = normalizedHome
		for _, root := range policy.GetWritableRootsWithCWD(cwd) {
			normalizedRoot, literal, err := normalizeSeatbeltWritableRoot(root.Root, allowedSymlinkedCodexHome)
			if err != nil {
				return "", nil, err
			}
			name := fmt.Sprintf("WRITABLE_ROOT_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: normalizedRoot})
			match := "subpath"
			if literal {
				match = "literal"
			}
			lines = append(lines, fmt.Sprintf(`(allow file-write* (%s (param "%s")))`, match, name))
			// Rust #39599: a sandboxed process must not replace the writable
			// root whose identity a later policy will reuse.
			lines = append(lines, fmt.Sprintf(`(deny file-write-unlink (require-all (literal (param "%s")) (vnode-type DIRECTORY)))`, name))
			for _, protected := range append(append([]string(nil), root.ReadOnlySubpaths...), protectedMetadataPaths(root.Root, root.ProtectedMetadataNames)...) {
				protectedName := fmt.Sprintf("PROTECTED_WRITE_%d", len(parameters))
				parameters = append(parameters, seatbeltParameter{Name: protectedName, Value: cleanSeatbeltPath(protected)})
				lines = append(lines, fmt.Sprintf(`(deny file-write* (subpath (param "%s")))`, protectedName))
			}
		}
	}

	// Rust #46583: deny XPC service lookups so sandboxed commands cannot reach
	// privileged macOS services.
	lines = append(lines, `(deny mach-lookup (xpc-service-name-prefix ""))`)

	for _, denied := range unreadable {
		name := fmt.Sprintf("DENIED_READ_%d", len(parameters))
		parameters = append(parameters, seatbeltParameter{Name: name, Value: denied})
		lines = append(lines, fmt.Sprintf(`(deny file-read* (subpath (param "%s")))`, name))
	}

	if !profile.AllowsNetwork() {
		lines = append(lines, "(deny network*)", "(deny system-socket)")
	}
	socketLines, socketParameters := seatbeltUnixSocketRules(allowUnixSockets, managedNetwork)
	lines = append(lines, socketLines...)
	parameters = append(parameters, socketParameters...)
	// Rust #45984: network grants and Unix-socket allowlists must never reopen
	// the privileged app-server control socket to filesystem-restricted
	// commands. Keep this after the allowlist so the deny wins.
	if !policy.HasFullDiskWriteAccess() {
		if directory, ok := sharedDaemonSocketDirectory(); ok {
			name := fmt.Sprintf("DAEMON_SOCKET_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: directory})
			lines = append(lines,
				fmt.Sprintf(`(deny file-read* file-write* (literal (param "%s")))`, name),
				fmt.Sprintf(`(deny file-read* file-write* (subpath (param "%s")))`, name),
				fmt.Sprintf(`(deny network-outbound (remote unix-socket (subpath (param "%s"))))`, name),
			)
			for ancestor := filepath.Dir(directory); ancestor != "."; ancestor = filepath.Dir(ancestor) {
				ancestorName := fmt.Sprintf("DAEMON_SOCKET_ANCESTOR_%d", len(parameters))
				parameters = append(parameters, seatbeltParameter{Name: ancestorName, Value: ancestor})
				lines = append(lines, fmt.Sprintf(`(deny file-write-unlink (require-all (vnode-type DIRECTORY) (literal (param "%s"))))`, ancestorName))
				if ancestor == string(filepath.Separator) {
					break
				}
			}
		}
	}
	// Rust #46571: renaming an allowed ancestor relocates its protected
	// descendants past their pathname carveouts. Keep these unlink denies last so
	// no broader allowance can reopen the rename operation.
	for index, ancestor := range seatbeltProtectedAncestors(policy, cwd) {
		name := fmt.Sprintf("PROTECTED_ANCESTOR_%d", index)
		parameters = append(parameters, seatbeltParameter{Name: name, Value: ancestor})
		lines = append(lines, fmt.Sprintf(`(deny file-write-unlink (require-all (vnode-type DIRECTORY) (literal (param "%s"))))`, name))
	}
	return strings.Join(lines, "\n") + "\n", parameters, nil
}

// seatbeltUnixSocketRules mirrors Rust #45548's prepared-context precedence:
// a prepared allow-all grant becomes broad AF_UNIX allowances, while a
// prepared allowlist is normalized and combined with explicit extra grants.
// With no prepared context the explicit extra grants are the whole policy.
func seatbeltUnixSocketRules(extraAllowUnixSockets []string, managedNetwork *seatbeltManagedNetworkContext) ([]string, []seatbeltParameter) {
	if managedNetwork != nil && managedNetwork.DangerouslyAllowAllUnixSockets {
		return []string{
			"(allow system-socket (socket-domain AF_UNIX))",
			"(allow network-bind (local unix-socket))",
			"(allow network-outbound (remote unix-socket))",
		}, nil
	}
	allowUnixSockets := append([]string(nil), extraAllowUnixSockets...)
	if managedNetwork != nil {
		allowUnixSockets = append(allowUnixSockets, managedNetwork.AllowUnixSockets...)
	}
	sockets := normalizeSeatbeltSocketPaths(allowUnixSockets)
	if len(sockets) == 0 {
		return nil, nil
	}
	lines := []string{"(allow system-socket (socket-domain AF_UNIX))"}
	parameters := make([]seatbeltParameter, 0, len(sockets))
	for index, socket := range sockets {
		name := fmt.Sprintf("UNIX_SOCKET_PATH_%d", index)
		parameters = append(parameters, seatbeltParameter{Name: name, Value: socket})
		lines = append(lines,
			fmt.Sprintf(`(allow network-bind (local unix-socket (subpath (param "%s"))))`, name),
			fmt.Sprintf(`(allow network-outbound (remote unix-socket (subpath (param "%s"))))`, name),
		)
	}
	return lines, parameters
}

// seatbeltProtectedPaths returns the protected paths (a writable root's
// read-only subpaths and protected project metadata paths) that a broader
// allowance must not reopen.
func seatbeltProtectedPaths(policy *SandboxPolicy, cwd string) []string {
	paths := []string{}
	for _, root := range policy.GetWritableRootsWithCWD(cwd) {
		for _, protected := range root.ReadOnlySubpaths {
			paths = appendUniqueSeatbeltPath(paths, cleanSeatbeltPath(protected))
		}
		for _, protected := range protectedMetadataPaths(root.Root, root.ProtectedMetadataNames) {
			paths = appendUniqueSeatbeltPath(paths, protected)
		}
	}
	return paths
}

// seatbeltProtectedAncestors returns the writable-root directories that contain
// a protected path. Unlinking one of them would relocate the protected
// descendants past their pathname carveouts (Rust #46571). A protected path that
// is a symlink protects both its logical entry and its resolved target.
func seatbeltProtectedAncestors(policy *SandboxPolicy, cwd string) []string {
	seen := map[string]bool{}
	ancestors := []string{}
	for _, root := range policy.GetWritableRootsWithCWD(cwd) {
		rootPath := cleanSeatbeltPath(root.Root)
		for _, protected := range root.ReadOnlySubpaths {
			logical := cleanSeatbeltPath(protected)
			paths := []string{logical}
			if resolved := seatbeltSymlinkResolver(logical); resolved != "" {
				paths = append(paths, resolved)
			}
			for _, protectedPath := range paths {
				ancestor := parentSeatbeltPath(protectedPath)
				for ancestor != "" && seatbeltSubpathWithin(ancestor, rootPath) {
					if !seen[ancestor] {
						seen[ancestor] = true
						ancestors = append(ancestors, ancestor)
					}
					ancestor = parentSeatbeltPath(ancestor)
				}
			}
		}
	}
	sort.Strings(ancestors)
	return ancestors
}

// seatbeltResolvedSymlinkPath returns the resolved target of a symlinked path
// when it differs from the logical path, mirroring Rust's
// `normalize_path_for_sandbox` resolution of top-level aliases (#46571).
// seatbeltSymlinkResolver indirection lets tests exercise the resolved-ancestor
// protection on hosts without symlink privileges.
var seatbeltSymlinkResolver = seatbeltResolvedSymlinkPath

func seatbeltResolvedSymlinkPath(path string) string {
	cleaned := cleanSeatbeltPath(path)
	if cleaned == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return ""
	}
	resolved = cleanSeatbeltPath(resolved)
	if resolved == "" || resolved == cleaned {
		return ""
	}
	return resolved
}

func normalizeSeatbeltAllowedHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	normalized, err := normalizeSeatbeltTopLevelAlias(path)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

// normalizeSeatbeltWritableRoot mirrors Rust
// normalize_writable_root_for_sandbox (#39599, #42716). Writable roots reject
// nested symlinks unless they are at or beneath the explicitly opted-in user
// home; the opt-in trusts the current target, while the default preserves
// mutable components and only resolves top-level system aliases.
func normalizeSeatbeltWritableRoot(root string, allowedSymlinkedCodexHome string) (string, bool, error) {
	allowSymlinks := seatbeltAllowedSymlinkHomeCovers(root, allowedSymlinkedCodexHome)
	if !allowSymlinks {
		if symlink := seatbeltNestedSymlinkComponent(root); symlink != "" {
			return "", false, fmt.Errorf(
				"writable root %s contains symlink component %s; symlinked writable roots are not supported.\n"+
					"If this writable root is at or beneath CODEX_HOME and you trust its symlink targets, "+
					"set `allow_symlinked_codex_home = true` at the top level of `$CODEX_HOME/config.toml` "+
					"(normally `~/.codex/config.toml`) on the execution host, then restart Codex or its executor. "+
					"This opt-out trusts targets outside CODEX_HOME and targets changed between commands. "+
					"It does not apply to other writable roots.",
				cleanSeatbeltPath(root),
				cleanSeatbeltPath(symlink),
			)
		}
	}

	normalized := cleanSeatbeltPath(root)
	if allowSymlinks {
		if resolved, err := filepath.EvalSymlinks(normalized); err == nil {
			normalized = cleanSeatbeltPath(resolved)
		}
	} else {
		var err error
		normalized, err = normalizeSeatbeltTopLevelAlias(normalized)
		if err != nil {
			return "", false, fmt.Errorf("failed to normalize top-level alias for path %s: %w", cleanSeatbeltPath(root), err)
		}
	}

	info, err := os.Lstat(normalized)
	if err != nil {
		if os.IsNotExist(err) {
			return normalized, false, nil
		}
		return "", false, fmt.Errorf("failed to inspect Seatbelt writable root %s: %w", normalized, err)
	}
	return normalized, !info.IsDir(), nil
}

// seatbeltAllowedSymlinkHomeCovers reports whether root is at or beneath the
// opted-in home. Both the configured spelling and its resolved target count,
// mirroring Rust's two starts_with checks.
func seatbeltAllowedSymlinkHomeCovers(root string, home string) bool {
	home = cleanSeatbeltPath(home)
	if home == "" {
		return false
	}
	root = cleanSeatbeltPath(root)
	if root == "" {
		return false
	}
	if seatbeltSubpathWithin(root, home) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return false
	}
	return seatbeltSubpathWithin(root, cleanSeatbeltPath(resolved))
}

// seatbeltNestedSymlinkComponent returns the deepest user-controlled symlink
// component, leaving top-level macOS aliases such as `/tmp -> /private/tmp`
// compatible. Rust #39599 counts a symlink at depth two or deeper as nested.
func seatbeltNestedSymlinkComponent(path string) string {
	current := cleanSeatbeltPath(path)
	for current != "" && current != "/" && current != "." {
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 && seatbeltPathDepth(current) >= 2 {
			return current
		}
		current = parentSeatbeltPath(current)
	}
	return ""
}

func seatbeltPathDepth(path string) int {
	path = cleanSeatbeltPath(path)
	if path == "" || path == "/" || path == "." {
		return 0
	}
	if strings.HasPrefix(path, "/") {
		trimmed := strings.Trim(path, "/")
		if trimmed == "" {
			return 0
		}
		return len(strings.Split(trimmed, "/"))
	}
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	trimmed := strings.Trim(rest, `/\`)
	if trimmed == "" {
		return 0
	}
	return len(strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '/' || r == '\\'
	}))
}

// normalizeSeatbeltTopLevelAlias resolves only a depth-one system alias (for
// example `/tmp -> /private/tmp`) and leaves deeper components untouched, in
// line with Rust AbsolutePathBuf::normalize_system_aliases.
func normalizeSeatbeltTopLevelAlias(path string) (string, error) {
	path = cleanSeatbeltPath(path)
	topLevel := seatbeltTopLevelPath(path)
	if topLevel == "" {
		return path, nil
	}
	info, err := os.Lstat(topLevel)
	if err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(topLevel)
	if err != nil {
		return "", err
	}
	return cleanSeatbeltPath(filepath.Join(resolved, strings.TrimPrefix(path, topLevel))), nil
}

func seatbeltTopLevelPath(path string) string {
	path = cleanSeatbeltPath(path)
	if path == "" || path == "/" || path == "." {
		return ""
	}
	if strings.HasPrefix(path, "/") {
		trimmed := strings.TrimPrefix(path, "/")
		if index := strings.Index(trimmed, "/"); index >= 0 {
			return "/" + trimmed[:index]
		}
		return path
	}
	volume := filepath.VolumeName(path)
	if volume == "" {
		return ""
	}
	return volume + string(filepath.Separator)
}

// ValidateWritableRootsForPlatform applies the platform's writable-root
// preparation checks to an in-process filesystem writer such as apply_patch.
// It is a no-op outside macOS and for full-disk-write profiles.
func ValidateWritableRootsForPlatform(cwd string, profile *PermissionProfile, allowedSymlinkedCodexHome string) error {
	if runtime.GOOS != "darwin" || profile == nil || profile.Disabled {
		return nil
	}
	policy := profile.LegacySandboxPolicy()
	return ValidateWritableRootsForPolicy(cwd, policy, allowedSymlinkedCodexHome)
}

// ValidateWritableRootsForPolicy applies the macOS writable-root checks to an
// already-resolved legacy or runtime policy.
func ValidateWritableRootsForPolicy(cwd string, policy *SandboxPolicy, allowedSymlinkedCodexHome string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if policy == nil || policy.HasFullDiskWriteAccess() {
		return nil
	}
	normalizedHome, err := normalizeSeatbeltAllowedHome(allowedSymlinkedCodexHome)
	if err != nil {
		return err
	}
	allowedSymlinkedCodexHome = normalizedHome
	for _, root := range policy.GetWritableRootsWithCWD(cwd) {
		if _, _, err := normalizeSeatbeltWritableRoot(root.Root, allowedSymlinkedCodexHome); err != nil {
			return err
		}
	}
	return nil
}

// parentSeatbeltPath returns the parent directory of an absolute seatbelt path.
func parentSeatbeltPath(value string) string {
	cleaned := cleanSeatbeltPath(value)
	if cleaned == "" || cleaned == "/" || cleaned == "." {
		return ""
	}
	if strings.HasPrefix(cleaned, "/") {
		parent := pathpkg.Dir(cleaned)
		if parent == "/" || parent == "." {
			return ""
		}
		return parent
	}
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return ""
	}
	return parent
}

// seatbeltSubpathWithin reports whether path is root itself or below it.
func seatbeltSubpathWithin(path string, root string) bool {
	path = cleanSeatbeltPath(path)
	root = cleanSeatbeltPath(root)
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	if strings.HasPrefix(root, "/") {
		return strings.HasPrefix(path, strings.TrimRight(root, "/")+"/")
	}
	return strings.HasPrefix(path, strings.TrimRight(root, `\/`)+string(filepath.Separator))
}

// appendUniqueSeatbeltPath appends a cleaned path once.
func appendUniqueSeatbeltPath(paths []string, value string) []string {
	value = cleanSeatbeltPath(value)
	if value == "" {
		return paths
	}
	for _, existing := range paths {
		if existing == value {
			return paths
		}
	}
	return append(paths, value)
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

// normalizeSeatbeltSocketPaths resolves existing socket paths on macOS, where
// /tmp and similar roots are aliases, then applies the same absolute-path and
// deduplication rules as other Seatbelt parameters.
func normalizeSeatbeltSocketPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		if !isSeatbeltAbsolutePath(path) {
			continue
		}
		path = cleanSeatbeltPath(path)
		if runtime.GOOS == "darwin" {
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				path = cleanSeatbeltPath(resolved)
			}
		}
		normalized = append(normalized, path)
	}
	return cleanAbsoluteSeatbeltPaths(normalized)
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
