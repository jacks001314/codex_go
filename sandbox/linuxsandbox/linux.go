//go:build linux

package linuxsandbox

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	seccomp "github.com/elastic/go-seccomp-bpf"
	json "github.com/goccy/go-json"
	"github.com/landlock-lsm/go-landlock/landlock"
	"github.com/spf13/pflag"
	"golang.org/x/sys/unix"
)

const (
	proxySocketDirPrefix = "codex-linux-sandbox-proxy-"
)

var proxyEnvKeys = map[string]bool{
	"HTTP_PROXY":             true,
	"HTTPS_PROXY":            true,
	"WS_PROXY":               true,
	"WSS_PROXY":              true,
	"ALL_PROXY":              true,
	"FTP_PROXY":              true,
	"YARN_HTTP_PROXY":        true,
	"YARN_HTTPS_PROXY":       true,
	"NPM_CONFIG_HTTP_PROXY":  true,
	"NPM_CONFIG_HTTPS_PROXY": true,
	"NPM_CONFIG_PROXY":       true,
	"BUNDLE_HTTP_PROXY":      true,
	"BUNDLE_HTTPS_PROXY":     true,
	"PIP_PROXY":              true,
	"DOCKER_HTTP_PROXY":      true,
	"DOCKER_HTTPS_PROXY":     true,
}

type linuxNetworkSeccompMode int

const (
	linuxNetworkSeccompNone linuxNetworkSeccompMode = iota
	linuxNetworkSeccompRestricted
	linuxNetworkSeccompProxyRouted
	// linuxNetworkSeccompVmSocketRestricted mirrors the upstream
	// `NetworkSeccompMode::VmSocketRestricted` mode: it denies `AF_VSOCK`
	// sockets while leaving ordinary networking untouched.
	linuxNetworkSeccompVmSocketRestricted
)

type linuxPermissionProfile struct {
	Filesystem     *linuxFilesystemPolicy
	NetworkEnabled bool
}

type linuxFilesystemPolicy struct {
	Kind             string
	Entries          []linuxFilesystemEntry
	GlobScanMaxDepth *int
}

type linuxFilesystemEntry struct {
	Access  string
	Path    string
	Pattern string
	Special linuxSpecialPath
}

type linuxSpecialPath struct {
	Kind    string
	Subpath string
}

// linuxManagedNetworkContext is the portable managed-network context the
// seccomp filter consults, mirroring the Rust
// `codex_network_proxy::ManagedNetworkSandboxContext` wire shape transported by
// the helper's `--managed-network` flag (camelCase, see
// `linux-sandbox/src/linux_run_main.rs`).
//
// Only `dangerouslyAllowAllUnixSockets` changes enforcement today; the other
// fields are decoded so a context produced by the Rust side round-trips
// unchanged.
type linuxManagedNetworkContext struct {
	LoopbackPorts                  []uint16 `json:"loopbackPorts"`
	AllowLocalBinding              bool     `json:"allowLocalBinding"`
	AllowUnixSockets               []string `json:"allowUnixSockets"`
	DangerouslyAllowAllUnixSockets bool     `json:"dangerouslyAllowAllUnixSockets"`
}

// linuxSandboxCommand is the CLI surface of the Linux sandbox helper, matching
// the upstream Rust `LinuxSandboxCommand`. Bubblewrap provides filesystem
// isolation and seccomp provides syscall filtering; the retained Landlock
// backend is selected with `--use-legacy-landlock`.
type linuxSandboxCommand struct {
	SandboxPolicyCWD      string
	CommandCWD            string
	PermissionProfileJSON string
	PermissionProfile     *linuxPermissionProfile
	UseLegacyLandlock     bool
	ApplySeccompThenExec  bool
	AllowNetworkForProxy  bool
	ManagedNetworkJSON    string
	ManagedNetwork        *linuxManagedNetworkContext
	ProxyRouteSpec        string
	NoProc                bool
	Command               []string
}

// managedNetworkEnabled reports whether the helper runs in managed-proxy mode.
//
// The Rust helper derives this from the presence of its managed-network context
// (`allow_network_for_proxy = managed_network.is_some()`), so a context supplied
// without the legacy flag still enables proxy mode; callers that only pass the
// legacy flag keep their existing behaviour.
func (cmd *linuxSandboxCommand) managedNetworkEnabled() bool {
	return cmd.AllowNetworkForProxy || strings.TrimSpace(cmd.ManagedNetworkJSON) != ""
}

func RunHelper(args []string, stdout, stderr io.Writer) int {
	_ = stdout
	if len(args) > 0 {
		switch args[0] {
		case "--proxy-host-bridge":
			if err := runProxyHostBridgeHelper(args[1:], os.Stdout); err != nil {
				fmt.Fprintln(stderr, "codex-linux-sandbox proxy host bridge:", err)
				return 1
			}
			return 0
		case "--proxy-local-bridge":
			if err := runProxyLocalBridgeHelper(args[1:], os.Stdout); err != nil {
				fmt.Fprintln(stderr, "codex-linux-sandbox proxy local bridge:", err)
				return 1
			}
			return 0
		}
	}
	cmd, err := parseLinuxSandboxCommand(args, stderr)
	if err == nil {
		err = runLinuxSandboxCommand(cmd)
	}
	if err != nil {
		fmt.Fprintln(stderr, "codex-linux-sandbox:", err)
		return 1
	}
	// Successful sandbox paths exec and never return.
	return 1
}

func parseLinuxSandboxCommand(args []string, stderr io.Writer) (*linuxSandboxCommand, error) {
	var cmd linuxSandboxCommand
	flags := pflag.NewFlagSet("codex-linux-sandbox", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cmd.SandboxPolicyCWD, "sandbox-policy-cwd", "", "")
	flags.StringVar(&cmd.CommandCWD, "command-cwd", "", "")
	flags.StringVar(&cmd.PermissionProfileJSON, "permission-profile", "", "")
	flags.BoolVar(&cmd.UseLegacyLandlock, "use-legacy-landlock", false, "")
	flags.BoolVar(&cmd.ApplySeccompThenExec, "apply-seccomp-then-exec", false, "")
	flags.BoolVar(&cmd.AllowNetworkForProxy, "allow-network-for-proxy", false, "")
	flags.StringVar(&cmd.ManagedNetworkJSON, "managed-network", "", "")
	flags.StringVar(&cmd.ProxyRouteSpec, "proxy-route-spec", "", "")
	flags.BoolVar(&cmd.NoProc, "no-proc", false, "")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	cmd.Command = append([]string(nil), flags.Args()...)
	if len(cmd.Command) == 0 {
		return nil, errors.New("no command specified to execute")
	}
	if strings.TrimSpace(cmd.SandboxPolicyCWD) == "" {
		return nil, errors.New("--sandbox-policy-cwd is required")
	}
	if cmd.CommandCWD == "" {
		cmd.CommandCWD = cmd.SandboxPolicyCWD
	}
	profile, err := parseLinuxPermissionProfile(cmd.PermissionProfileJSON)
	if err != nil {
		return nil, err
	}
	cmd.PermissionProfile = profile
	if strings.TrimSpace(cmd.ManagedNetworkJSON) != "" {
		context, err := parseLinuxManagedNetworkContext(cmd.ManagedNetworkJSON)
		if err != nil {
			return nil, err
		}
		cmd.ManagedNetwork = context
	}
	return &cmd, nil
}

// parseLinuxManagedNetworkContext decodes the `--managed-network` payload. The
// Rust helper rejects the same payload with
// "invalid managed network context JSON"; keep the failure mode aligned.
func parseLinuxManagedNetworkContext(raw string) (*linuxManagedNetworkContext, error) {
	var context linuxManagedNetworkContext
	if err := json.Unmarshal([]byte(raw), &context); err != nil {
		return nil, fmt.Errorf("invalid managed network context JSON: %w", err)
	}
	return &context, nil
}

func runLinuxSandboxCommand(cmd *linuxSandboxCommand) error {
	if cmd.ApplySeccompThenExec && cmd.UseLegacyLandlock {
		return errors.New("--apply-seccomp-then-exec is incompatible with --use-legacy-landlock")
	}
	if err := ensureLegacyLandlockSupportsPolicy(cmd); err != nil {
		return err
	}
	managedNetwork := cmd.managedNetworkEnabled()
	if cmd.ApplySeccompThenExec {
		if managedNetwork {
			if strings.TrimSpace(cmd.ProxyRouteSpec) == "" {
				return errors.New("managed proxy mode requires --proxy-route-spec")
			}
			if err := activateProxyRoutesInNetns(cmd.ProxyRouteSpec); err != nil {
				return fmt.Errorf("error activating Linux proxy routing bridge: %w", err)
			}
		}
		if err := applyLinuxPermissionProfile(cmd.PermissionProfile, cmd.SandboxPolicyCWD, false, managedNetwork, managedNetwork, cmd.ManagedNetwork); err != nil {
			return fmt.Errorf("error applying Linux sandbox restrictions: %w", err)
		}
		return execvpInDir(cmd.Command, cmd.CommandCWD)
	}
	if cmd.PermissionProfile.Filesystem.hasFullDiskWriteAccess() && !cmd.PermissionProfile.Filesystem.hasDenyReadEntries() && !managedNetwork {
		if err := applyLinuxPermissionProfile(cmd.PermissionProfile, cmd.SandboxPolicyCWD, false, managedNetwork, false, cmd.ManagedNetwork); err != nil {
			return fmt.Errorf("error applying Linux sandbox restrictions: %w", err)
		}
		return execvpInDir(cmd.Command, cmd.CommandCWD)
	}
	if !cmd.UseLegacyLandlock {
		if managedNetwork {
			spec, err := prepareHostProxyRouteSpec()
			if err != nil {
				return fmt.Errorf("failed to prepare host proxy routing bridge: %w", err)
			}
			cmd.ProxyRouteSpec = spec
		}
		inner, err := buildInnerLinuxSandboxCommand(cmd)
		if err != nil {
			return err
		}
		return execBubblewrap(cmd, inner)
	}
	if err := applyLinuxPermissionProfile(cmd.PermissionProfile, cmd.SandboxPolicyCWD, true, managedNetwork, false, cmd.ManagedNetwork); err != nil {
		return fmt.Errorf("error applying legacy Linux sandbox restrictions: %w", err)
	}
	return execvpInDir(cmd.Command, cmd.CommandCWD)
}

func parseLinuxPermissionProfile(raw string) (*linuxPermissionProfile, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("missing permission profile configuration")
	}
	var rust rustLinuxPermissionProfile
	if err := json.Unmarshal([]byte(raw), &rust); err == nil && rust.Type != "" {
		return rust.toLinuxPermissionProfile()
	}
	var goProfile legacyPermissionProfile
	if err := json.Unmarshal([]byte(raw), &goProfile); err != nil {
		return nil, fmt.Errorf("invalid permission profile JSON: %w", err)
	}
	return linuxPermissionProfileFromLegacy(&goProfile), nil
}

type legacyPermissionProfile struct {
	Disabled       bool                 `json:"Disabled"`
	SandboxPolicy  *legacySandboxPolicy `json:"SandboxPolicy"`
	NetworkEnabled bool                 `json:"NetworkEnabled"`
}

type legacySandboxPolicy struct {
	Kind                string   `json:"Kind"`
	Type                string   `json:"type"`
	WritableRoots       []string `json:"writableRoots"`
	NetworkAccess       any      `json:"networkAccess"`
	ExternalNetwork     string   `json:"ExternalNetwork"`
	ExcludeTmpdirEnvVar bool     `json:"excludeTmpdirEnvVar"`
	ExcludeSlashTmp     bool     `json:"excludeSlashTmp"`
}

type rustLinuxPermissionProfile struct {
	Type        string                 `json:"type"`
	FileSys     *rustManagedFilesystem `json:"file_system"`
	Network     string                 `json:"network"`
	ExtraFields map[string]interface{} `json:"-"`
}

type rustManagedFilesystem struct {
	Type             string                 `json:"type"`
	Entries          []rustFilesystemEntry  `json:"entries"`
	GlobScanMaxDepth *int                   `json:"glob_scan_max_depth"`
	ExtraFields      map[string]interface{} `json:"-"`
}

type rustFilesystemEntry struct {
	Path   rustFilesystemPath `json:"path"`
	Access string             `json:"access"`
}

type rustFilesystemPath struct {
	Type    string                 `json:"type"`
	Path    string                 `json:"path"`
	Pattern string                 `json:"pattern"`
	Value   rustFilesystemSpecial  `json:"value"`
	Extra   map[string]interface{} `json:"-"`
}

type rustFilesystemSpecial struct {
	Kind    string  `json:"kind"`
	Subpath *string `json:"subpath"`
	Path    string  `json:"path"`
}

func (r *rustLinuxPermissionProfile) toLinuxPermissionProfile() (*linuxPermissionProfile, error) {
	switch r.Type {
	case "disabled":
		return &linuxPermissionProfile{Filesystem: linuxUnrestrictedFilesystemPolicy(), NetworkEnabled: true}, nil
	case "external":
		return &linuxPermissionProfile{Filesystem: linuxUnrestrictedFilesystemPolicy(), NetworkEnabled: r.Network == "enabled"}, nil
	case "managed":
		if r.FileSys == nil {
			return nil, errors.New("managed permission profile missing file_system")
		}
		fs, err := r.FileSys.toLinuxFilesystemPolicy()
		if err != nil {
			return nil, err
		}
		return &linuxPermissionProfile{Filesystem: fs, NetworkEnabled: r.Network == "enabled"}, nil
	default:
		return nil, fmt.Errorf("unsupported permission profile type %q", r.Type)
	}
}

func (r *rustManagedFilesystem) toLinuxFilesystemPolicy() (*linuxFilesystemPolicy, error) {
	switch r.Type {
	case "unrestricted":
		return linuxUnrestrictedFilesystemPolicy(), nil
	case "restricted", "":
		policy := &linuxFilesystemPolicy{
			Kind:             "restricted",
			GlobScanMaxDepth: cloneIntPtr(r.GlobScanMaxDepth),
		}
		for _, entry := range r.Entries {
			policy.Entries = append(policy.Entries, entry.toLinuxFilesystemEntry())
		}
		return policy, nil
	default:
		return nil, fmt.Errorf("unsupported managed filesystem type %q", r.Type)
	}
}

func (r *rustFilesystemEntry) toLinuxFilesystemEntry() linuxFilesystemEntry {
	entry := linuxFilesystemEntry{Access: strings.ToLower(r.Access)}
	switch r.Path.Type {
	case "path":
		entry.Path = r.Path.Path
	case "glob_pattern":
		entry.Pattern = r.Path.Pattern
	case "special":
		entry.Special.Kind = r.Path.Value.Kind
		if r.Path.Value.Subpath != nil {
			entry.Special.Subpath = *r.Path.Value.Subpath
		}
		if entry.Special.Kind == "unknown" {
			entry.Special.Kind = r.Path.Value.Path
		}
	}
	return entry
}

func linuxPermissionProfileFromLegacy(profile *legacyPermissionProfile) *linuxPermissionProfile {
	if profile == nil {
		return &linuxPermissionProfile{Filesystem: linuxReadOnlyFilesystemPolicy()}
	}
	if profile.Disabled {
		return &linuxPermissionProfile{Filesystem: linuxUnrestrictedFilesystemPolicy(), NetworkEnabled: true}
	}
	return &linuxPermissionProfile{
		Filesystem:     linuxFilesystemPolicyFromLegacy(profile.SandboxPolicy),
		NetworkEnabled: profile.NetworkEnabled || legacyPolicyHasNetwork(profile.SandboxPolicy),
	}
}

func linuxFilesystemPolicyFromLegacy(policy *legacySandboxPolicy) *linuxFilesystemPolicy {
	if policy == nil {
		return linuxReadOnlyFilesystemPolicy()
	}
	switch legacyPolicyKind(policy) {
	case "danger-full-access", "dangerFullAccess", "external-sandbox", "externalSandbox":
		return linuxUnrestrictedFilesystemPolicy()
	case "workspace-write", "workspaceWrite":
		entries := []linuxFilesystemEntry{
			{Access: "read", Special: linuxSpecialPath{Kind: "root"}},
			{Access: "write", Special: linuxSpecialPath{Kind: "project_roots"}},
			{Access: "read", Special: linuxSpecialPath{Kind: "project_roots", Subpath: ".git"}},
			{Access: "read", Special: linuxSpecialPath{Kind: "project_roots", Subpath: ".agents"}},
			{Access: "read", Special: linuxSpecialPath{Kind: "project_roots", Subpath: ".gcode"}},
			// Rust #48176: the legacy workspace-write conversion also protects
			// `.aws` under each writable root.
			{Access: "read", Special: linuxSpecialPath{Kind: "project_roots", Subpath: ".aws"}},
		}
		if !policy.ExcludeSlashTmp {
			entries = append(entries, linuxFilesystemEntry{Access: "write", Special: linuxSpecialPath{Kind: "slash_tmp"}})
		}
		if !policy.ExcludeTmpdirEnvVar {
			entries = append(entries, linuxFilesystemEntry{Access: "write", Special: linuxSpecialPath{Kind: "tmpdir"}})
		}
		for _, root := range policy.WritableRoots {
			entries = append(entries, linuxFilesystemEntry{Access: "write", Path: root})
		}
		return &linuxFilesystemPolicy{Kind: "restricted", Entries: entries}
	case "read-only", "readOnly":
		return linuxReadOnlyFilesystemPolicy()
	default:
		return linuxReadOnlyFilesystemPolicy()
	}
}

func legacyPolicyKind(policy *legacySandboxPolicy) string {
	if policy == nil {
		return ""
	}
	if strings.TrimSpace(policy.Type) != "" {
		return strings.TrimSpace(policy.Type)
	}
	return strings.TrimSpace(policy.Kind)
}

func legacyPolicyHasNetwork(policy *legacySandboxPolicy) bool {
	if policy == nil {
		return false
	}
	switch value := policy.NetworkAccess.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "enabled") || strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return strings.EqualFold(strings.TrimSpace(policy.ExternalNetwork), "enabled")
	}
}

func linuxReadOnlyFilesystemPolicy() *linuxFilesystemPolicy {
	return &linuxFilesystemPolicy{
		Kind:    "restricted",
		Entries: []linuxFilesystemEntry{{Access: "read", Special: linuxSpecialPath{Kind: "root"}}},
	}
}

func linuxUnrestrictedFilesystemPolicy() *linuxFilesystemPolicy {
	return &linuxFilesystemPolicy{Kind: "unrestricted"}
}

func (p *linuxFilesystemPolicy) hasFullDiskReadAccess() bool {
	if p == nil || p.Kind == "unrestricted" || p.Kind == "external" {
		return true
	}
	hasRootRead := false
	for _, entry := range p.Entries {
		if entry.isRootSpecial() && entry.canRead() {
			hasRootRead = true
		}
		if entry.Access == "deny" {
			return false
		}
	}
	return hasRootRead
}

// linuxPlatformDefaultReadRoots mirrors Rust `LINUX_PLATFORM_DEFAULT_READ_ROOTS`
// (`bwrap.rs:53-62`): the read-only roots a restricted policy opts into with a
// `minimal` read entry, instead of reading `/`.
var linuxPlatformDefaultReadRoots = []string{
	"/bin",
	"/sbin",
	"/usr",
	"/etc",
	"/lib",
	"/lib64",
	"/nix/store",
	"/run/current-system/sw",
}

// includePlatformDefaults mirrors Rust `include_platform_defaults`
// (`permissions.rs:962-974`): only a restricted-read policy that lists the
// `minimal` read entry picks up the platform default read roots.
func (p *linuxFilesystemPolicy) includePlatformDefaults() bool {
	if p == nil || p.Kind != "restricted" || p.hasFullDiskReadAccess() {
		return false
	}
	for _, entry := range p.Entries {
		if entry.Special.Kind == "minimal" && entry.canRead() {
			return true
		}
	}
	return false
}

// readableRoots returns the roots a restricted-read policy may read, mirroring
// Rust `get_readable_roots_with_cwd` (`permissions.rs:1455-1478`) plus the
// platform-default and procfs augmentation `create_filesystem_args`
// (`bwrap.rs:522-556`) applies before layering the scoped mounts. A policy with
// unrestricted reads has no roots to layer: the caller binds `/` instead.
func (p *linuxFilesystemPolicy) readableRoots(cwd string) []string {
	if p == nil || p.hasFullDiskReadAccess() {
		return nil
	}
	seen := map[string]bool{}
	roots := []string{}
	add := func(path string) {
		path = cleanLinuxPath(path, cwd)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		roots = append(roots, path)
	}
	for _, entry := range p.Entries {
		if !entry.canRead() {
			continue
		}
		for _, path := range entry.resolvedPaths(cwd) {
			add(path)
		}
	}
	if p.includePlatformDefaults() {
		for _, path := range linuxPlatformDefaultReadRoots {
			add(path)
		}
		// Rust also reuses the caller's procfs here, but only when the pid
		// namespace is inherited; this CLI always unshares pid and mounts a
		// fresh procfs after the filesystem arguments instead.
	}
	sort.Strings(roots)
	return roots
}

func (p *linuxFilesystemPolicy) hasFullDiskWriteAccess() bool {
	if p == nil || p.Kind == "unrestricted" || p.Kind == "external" {
		return true
	}
	hasRootWrite := false
	for _, entry := range p.Entries {
		if entry.isRootSpecial() && entry.canWrite() {
			hasRootWrite = true
			continue
		}
		if !entry.canWrite() {
			return false
		}
	}
	return hasRootWrite
}

func (p *linuxFilesystemPolicy) writableRoots(cwd string) []string {
	if p == nil || p.hasFullDiskWriteAccess() {
		return []string{"/"}
	}
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		path = cleanLinuxPath(path, cwd)
		if path != "" && !seen[path] {
			seen[path] = true
			roots = append(roots, path)
		}
	}
	for _, entry := range p.Entries {
		if !entry.canWrite() {
			continue
		}
		for _, path := range entry.resolvedPaths(cwd) {
			add(path)
		}
	}
	return roots
}

func (p *linuxFilesystemPolicy) protectedReadOnlySubpaths(cwd string) []string {
	if p == nil {
		return nil
	}
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		path = cleanLinuxPath(path, cwd)
		if path == "" || seen[path] {
			return
		}
		if _, err := os.Stat(path); err == nil {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	for _, root := range p.writableRoots(cwd) {
		if root == "/" {
			continue
		}
		// `.aws` joins the protected metadata names: AWS profiles can select
		// credential helpers the application executes (Rust #48176).
		for _, name := range []string{".git", ".agents", ".gcode", ".aws"} {
			add(filepath.Join(root, name))
		}
	}
	for _, entry := range p.Entries {
		if entry.Access == "read" {
			for _, path := range entry.resolvedPaths(cwd) {
				add(path)
			}
		}
	}
	return paths
}

func (p *linuxFilesystemPolicy) hasDenyReadEntries() bool {
	if p == nil {
		return false
	}
	for _, entry := range p.Entries {
		if entry.Access == "deny" {
			return true
		}
	}
	return false
}

func (p *linuxFilesystemPolicy) unreadableRoots(cwd string) ([]string, error) {
	if p == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		path = cleanLinuxPath(path, cwd)
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	for _, entry := range p.Entries {
		if entry.Access != "deny" {
			continue
		}
		if strings.TrimSpace(entry.Pattern) != "" {
			matches, err := expandLinuxDenyGlob(entry.Pattern, cwd, p.GlobScanMaxDepth)
			if err != nil {
				return nil, err
			}
			for _, match := range matches {
				add(match)
			}
			continue
		}
		for _, path := range entry.resolvedPaths(cwd) {
			add(path)
		}
	}
	return paths, nil
}

func (e *linuxFilesystemEntry) canRead() bool {
	return e.Access == "read" || e.Access == "write"
}

func (e *linuxFilesystemEntry) canWrite() bool {
	return e.Access == "write"
}

func (e *linuxFilesystemEntry) isRootSpecial() bool {
	return e.Special.Kind == "root"
}

func (e *linuxFilesystemEntry) resolvedPaths(cwd string) []string {
	if e.Path != "" {
		return []string{e.Path}
	}
	switch e.Special.Kind {
	case "root":
		return []string{"/"}
	case "project_roots", "current_working_directory":
		if e.Special.Subpath != "" {
			return []string{filepath.Join(cwd, e.Special.Subpath)}
		}
		return []string{cwd}
	case "tmpdir":
		if tmp := os.Getenv("TMPDIR"); tmp != "" {
			return []string{tmp}
		}
	case "slash_tmp":
		return []string{"/tmp"}
	case "minimal":
		return []string{"/bin", "/sbin", "/usr", "/etc", "/lib", "/lib64", "/nix/store", "/run/current-system/sw"}
	}
	return nil
}

func cleanLinuxPath(path string, cwd string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path)
}

func expandLinuxDenyGlob(pattern string, cwd string, maxDepth *int) ([]string, error) {
	pattern = cleanLinuxPath(pattern, cwd)
	if pattern == "" {
		return nil, nil
	}
	matches, err := doublestar.FilepathGlob(pattern, doublestar.WithNoFollow(), doublestar.WithFilesOnly(), doublestar.WithFailOnIOErrors())
	if err != nil {
		if errors.Is(err, doublestar.ErrPatternNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("expand deny-read glob %s: %w", pattern, err)
	}
	if maxDepth == nil {
		return matches, nil
	}
	root := globLiteralRoot(pattern)
	if root == "" {
		return matches, nil
	}
	out := matches[:0]
	for _, match := range matches {
		rel, err := filepath.Rel(root, match)
		if err != nil {
			continue
		}
		if pathComponentDepth(rel) <= *maxDepth {
			out = append(out, match)
		}
	}
	return out, nil
}

func globLiteralRoot(pattern string) string {
	firstGlob := len(pattern)
	for _, marker := range []string{"*", "?", "["} {
		if index := strings.Index(pattern, marker); index >= 0 && index < firstGlob {
			firstGlob = index
		}
	}
	if firstGlob == len(pattern) {
		return filepath.Dir(pattern)
	}
	prefix := pattern[:firstGlob]
	separator := strings.LastIndex(prefix, string(filepath.Separator))
	if separator < 0 {
		return "."
	}
	if separator == 0 {
		return string(filepath.Separator)
	}
	return filepath.Clean(prefix[:separator])
}

func pathComponentDepth(path string) int {
	if path == "." || path == "" {
		return 0
	}
	depth := 0
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part != "" && part != "." {
			depth++
		}
	}
	return depth
}

func ensureLegacyLandlockSupportsPolicy(cmd *linuxSandboxCommand) error {
	return ensureLegacyLandlockSupportsPolicyWithInterop(cmd, wslInteropDir)
}

func ensureLegacyLandlockSupportsPolicyWithInterop(cmd *linuxSandboxCommand, interopDir string) error {
	if !cmd.UseLegacyLandlock || cmd.PermissionProfile == nil || cmd.PermissionProfile.Filesystem == nil {
		return nil
	}
	fs := cmd.PermissionProfile.Filesystem
	if !fs.hasFullDiskReadAccess() && !fs.hasFullDiskWriteAccess() {
		return errors.New("permission profiles requiring direct runtime enforcement are incompatible with --use-legacy-landlock")
	}
	if cmd.PermissionProfile.NetworkEnabled &&
		!cmd.managedNetworkEnabled() &&
		!fs.hasFullDiskWriteAccess() &&
		pathIsDirectory(interopDir) {
		return errors.New("legacy Landlock cannot isolate WSL Windows interop with network access enabled; use bubblewrap or restrict network access")
	}
	return nil
}

func applyLinuxPermissionProfile(profile *linuxPermissionProfile, cwd string, applyLandlockFS bool, allowNetworkForProxy bool, proxyRoutedNetwork bool, managedNetwork *linuxManagedNetworkContext) error {
	if profile == nil {
		return errors.New("missing permission profile")
	}
	mode := linuxNetworkSeccompModeForProfile(profile, allowNetworkForProxy, proxyRoutedNetwork)
	if mode != linuxNetworkSeccompNone {
		if err := installLinuxNetworkSeccompFilter(mode, managedNetwork); err != nil {
			return err
		}
	}
	if applyLandlockFS && profile.Filesystem != nil && !profile.Filesystem.hasFullDiskWriteAccess() {
		if !profile.Filesystem.hasFullDiskReadAccess() {
			return errors.New("restricted read-only access is not supported by the legacy Linux Landlock filesystem backend")
		}
		rules := []landlock.Rule{
			landlock.RODirs("/"),
			landlock.RWFiles("/dev/null").IgnoreIfMissing(),
		}
		writableRoots := profile.Filesystem.writableRoots(cwd)
		if len(writableRoots) > 0 {
			rules = append(rules, landlock.RWDirs(writableRoots...))
		}
		if err := landlock.V5.RestrictPaths(rules...); err != nil {
			return err
		}
	}
	return nil
}

func linuxNetworkSeccompModeFor(networkEnabled bool, allowNetworkForProxy bool, proxyRoutedNetwork bool) linuxNetworkSeccompMode {
	if networkEnabled && !allowNetworkForProxy {
		return linuxNetworkSeccompNone
	}
	if proxyRoutedNetwork {
		return linuxNetworkSeccompProxyRouted
	}
	return linuxNetworkSeccompRestricted
}

// linuxNetworkSeccompModeForProfile selects the seccomp filter for one
// permission profile, mirroring the upstream Rust
// `apply_permission_profile_to_current_thread`.
//
// The configured network policy decides the mode. When that policy installs no
// network filter at all, a filesystem-restricted policy still installs the
// VM-socket filter: `AF_VSOCK` can reach host services outside the filesystem
// sandbox, and under WSL2 it aliases the Windows interop socket even when
// `/run/WSL` is masked. A policy with full disk write access keeps unrestricted
// networking and installs no filter.
func linuxNetworkSeccompModeForProfile(profile *linuxPermissionProfile, allowNetworkForProxy bool, proxyRoutedNetwork bool) linuxNetworkSeccompMode {
	if profile == nil {
		return linuxNetworkSeccompNone
	}
	if mode := linuxNetworkSeccompModeFor(profile.NetworkEnabled, allowNetworkForProxy, proxyRoutedNetwork); mode != linuxNetworkSeccompNone {
		return mode
	}
	if !profile.Filesystem.hasFullDiskWriteAccess() {
		return linuxNetworkSeccompVmSocketRestricted
	}
	return linuxNetworkSeccompNone
}

// installLinuxNetworkSeccompFilter loads the seccomp filter for one
// `linuxNetworkSeccompMode`.
//
// Every mode that installs socket-family restrictions also blocks
// `io_uring_setup`/`io_uring_enter`/`io_uring_register`, because io_uring can
// create sockets (including `AF_VSOCK`) without going through `socket`.
func installLinuxNetworkSeccompFilter(mode linuxNetworkSeccompMode, managedNetwork *linuxManagedNetworkContext) error {
	groups := []seccomp.SyscallGroup{}
	if mode != linuxNetworkSeccompVmSocketRestricted {
		groups = append(groups, seccomp.SyscallGroup{Action: seccomp.ActionErrno, Names: []string{
			"ptrace",
			"process_vm_readv",
			"process_vm_writev",
		}})
	}
	groups = append(groups, seccomp.SyscallGroup{Action: seccomp.ActionErrno, Names: []string{
		"io_uring_setup",
		"io_uring_enter",
		"io_uring_register",
	}})
	switch mode {
	case linuxNetworkSeccompRestricted:
		groups = append(groups,
			seccomp.SyscallGroup{Action: seccomp.ActionErrno, Names: []string{
				"connect",
				"accept",
				"accept4",
				"bind",
				"listen",
				"getpeername",
				"getsockname",
				"shutdown",
				"sendto",
				"sendmmsg",
				"recvmmsg",
				"getsockopt",
				"setsockopt",
			}},
			seccomp.SyscallGroup{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{
				{Name: "socket", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(unix.AF_UNIX)}}},
				{Name: "socketpair", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(unix.AF_UNIX)}}},
			}},
		)
	case linuxNetworkSeccompProxyRouted:
		// IP sockets stay allowed inside the isolated namespace so the command
		// can reach the local routing bridge. A standalone AF_UNIX socket is
		// denied unless the effective managed-network policy grants
		// `dangerously_allow_all_unix_sockets`; AF_UNIX socket pairs stay
		// allowed either way (see the socketpair rule below).
		deniedSocketFamilies := []seccomp.Condition{
			{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(unix.AF_INET)},
			{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(unix.AF_INET6)},
		}
		if managedNetwork != nil && managedNetwork.DangerouslyAllowAllUnixSockets {
			deniedSocketFamilies = append(deniedSocketFamilies, seccomp.Condition{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(unix.AF_UNIX)})
		}
		groups = append(groups,
			seccomp.SyscallGroup{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{
				{Name: "socket", Conditions: deniedSocketFamilies},
				{Name: "socketpair", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(unix.AF_UNIX)}}},
			}},
		)
	case linuxNetworkSeccompVmSocketRestricted:
		// Deny only the VM socket family; every other family, including
		// `AF_UNIX`, keeps the behaviour of an unrestricted network policy.
		groups = append(groups, seccomp.SyscallGroup{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{
			{Name: "socket", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.Equal, Value: uint64(unix.AF_VSOCK)}}},
			{Name: "socketpair", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.Equal, Value: uint64(unix.AF_VSOCK)}}},
		}})
	}
	filter := seccomp.Filter{
		NoNewPrivs: true,
		Flag:       seccomp.FilterFlagTSync,
		Policy: seccomp.Policy{
			DefaultAction: seccomp.ActionAllow,
			Syscalls:      groups,
		},
	}
	if err := seccomp.LoadFilter(filter); err != nil {
		return fmt.Errorf("failed to load seccomp filter: %w", err)
	}
	return nil
}

func buildInnerLinuxSandboxCommand(cmd *linuxSandboxCommand) ([]string, error) {
	exe := os.Args[0]
	if exe == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("failed to resolve current executable path: %w", err)
		}
		exe = resolved
	}
	inner := []string{
		exe,
		"--sandbox-policy-cwd", cmd.SandboxPolicyCWD,
		"--command-cwd", cmd.CommandCWD,
		"--permission-profile", cmd.PermissionProfileJSON,
		"--apply-seccomp-then-exec",
	}
	if cmd.managedNetworkEnabled() {
		inner = append(inner, "--allow-network-for-proxy")
		if strings.TrimSpace(cmd.ProxyRouteSpec) == "" {
			return nil, errors.New("managed proxy mode requires --proxy-route-spec")
		}
		inner = append(inner, "--proxy-route-spec", cmd.ProxyRouteSpec)
		if strings.TrimSpace(cmd.ManagedNetworkJSON) != "" {
			inner = append(inner, "--managed-network", cmd.ManagedNetworkJSON)
		}
	}
	inner = append(inner, "--")
	inner = append(inner, cmd.Command...)
	return inner, nil
}

func execBubblewrap(cmd *linuxSandboxCommand, inner []string) error {
	// Rust #51211 selects the launcher with the command's effective permissions
	// before any proc-mount preflight, which uses a synthetic policy with no
	// writable roots.
	launcher := resolveBwrapLauncher(cmd.PermissionProfile, cmd.SandboxPolicyCWD)
	if !launcher.available() {
		return bwrapUnavailableError()
	}
	var dataFDs []int
	defer func() {
		for _, fd := range dataFDs {
			_ = unix.Close(fd)
		}
	}()
	args := []string{"bwrap", "--die-with-parent", "--unshare-pid"}
	if !cmd.NoProc {
		args = append(args, "--proc", "/proc")
	}
	if !cmd.PermissionProfile.NetworkEnabled || cmd.managedNetworkEnabled() {
		args = append(args, "--unshare-net")
	}
	fs := cmd.PermissionProfile.Filesystem
	if fs == nil || fs.hasFullDiskWriteAccess() {
		args = append(args, "--bind", "/", "/")
		args = append(args, "--dev", "/dev", "--bind-try", "/dev/shm", "/dev/shm")
	} else if readRoots := fs.readableRoots(cmd.SandboxPolicyCWD); fs.hasFullDiskReadAccess() || containsPath(readRoots, "/") {
		args = append(args, "--ro-bind", "/", "/")
		// Rust create_filesystem_args mounts the minimal device tree right after
		// the read-only root bind (`bwrap.rs:511-516`): the root bind is nodev,
		// so standard device nodes such as `/dev/null` would otherwise be
		// unusable inside the sandbox (the managed-proxy bridge opens
		// `/dev/null` for a child's stdin, `linux.go` spawnProxyLocalBridge).
		args = append(args, "--dev", "/dev")
	} else {
		// `bwrap.rs:517-570`: a restricted-read policy that never reads `/`
		// starts from an empty root and layers only its approved read roots, so
		// the sandbox sees strictly less than the host root bind would expose.
		args = append(args, "--tmpfs", "/", "--dev", "/dev")
		// The empty root hides every mount made before it, including the
		// caller's `--proc /proc`, so re-issue that mount here; upstream mounts
		// the fresh procfs after the filesystem arguments (`bwrap.rs:377-381`).
		if !cmd.NoProc {
			args = append(args, "--proc", "/proc")
		}
		for _, root := range readRoots {
			if _, err := os.Stat(root); err != nil {
				// Rust skips readable roots that do not exist on the host.
				continue
			}
			args = append(args, "--ro-bind", root, root)
		}
	}
	for _, root := range fs.writableRoots(cmd.SandboxPolicyCWD) {
		if root == "/" {
			// Rebinding `/` shadows that device tree, so Rust recreates it
			// together with shared memory (`bwrap.rs:650-657`).
			args = append(args, "--bind", "/", "/")
			args = append(args, "--dev", "/dev", "--bind-try", "/dev/shm", "/dev/shm")
			continue
		}
		if _, err := os.Stat(root); err == nil {
			args = append(args, "--bind", root, root)
		}
	}
	// Rust re-applies read-only protections only for subpaths a writable
	// root would otherwise expose (`append_read_only_subpath_args`,
	// `bwrap.rs:1243-1257`): the read baseline is already read-only, and an
	// extra `--ro-bind / /` re-mounts the host root over the device tree and
	// over the writable roots bound above. Ordinary read entries that no
	// writable root covers keep the baseline protection.
	writableRoots := fs.writableRoots(cmd.SandboxPolicyCWD)
	fullWriteRoot := false
	for _, root := range writableRoots {
		if root == "/" {
			fullWriteRoot = true
			break
		}
	}
	for _, path := range fs.protectedReadOnlySubpaths(cmd.SandboxPolicyCWD) {
		if !fullWriteRoot && !linuxSandboxPathWithin(writableRoots, path) {
			continue
		}
		args = append(args, "--ro-bind", path, path)
	}
	if fs != nil {
		unreadableRoots, err := fs.unreadableRoots(cmd.SandboxPolicyCWD)
		if err != nil {
			return err
		}
		for _, path := range unreadableRoots {
			fd, err := appendUnreadableRootBwrapArgs(&args, path)
			if err != nil {
				return err
			}
			if fd >= 0 {
				dataFDs = append(dataFDs, fd)
			}
		}
	}
	args = appendWSLInteropMaskArgs(args, cmd.NoProc, !fs.hasFullDiskWriteAccess(), wslInteropDir)
	args = append(args, "--chdir", cmd.CommandCWD, "--")
	args = append(args, inner...)
	// Bubblewrap semantics: unsharing the pid namespace makes the sandboxed
	// process PID 1, and the inner stage must report the canonical sandbox name
	// (or run through this CLI's argv[0] when the launcher predates --argv0).
	args = injectAsPid1(args)
	args, err := applyInnerCommandArgv0(args, launcher.SupportsArgv0, os.Args[0])
	if err != nil {
		return err
	}
	makeFilesInheritable(dataFDs)
	if launcher.Bundled {
		return execBundledBwrap(launcher.Program, args, os.Environ())
	}
	return unix.Exec(launcher.Program, args, os.Environ())
}

// linuxSandboxPathWithin reports whether path sits inside one of the roots the
// sandbox made writable (Rust `is_within_allowed_write_paths`).
func linuxSandboxPathWithin(roots []string, path string) bool {
	for _, root := range roots {
		if pathWithinRoot(path, root) {
			return true
		}
	}
	return false
}

func appendUnreadableRootBwrapArgs(args *[]string, path string) (int, error) {
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		*args = append(*args, "--perms", "000", "--tmpfs", path)
		return -1, nil
	}
	target := path
	if os.IsNotExist(err) {
		if missing := firstMissingPathComponent(path); missing != "" {
			target = missing
		}
	} else if err != nil {
		return -1, err
	}
	fd, err := unix.MemfdCreate("codex-deny-read-empty", 0)
	if err != nil {
		return -1, fmt.Errorf("create deny-read empty file descriptor: %w", err)
	}
	*args = append(*args, "--ro-bind-data", strconv.Itoa(fd), target)
	return fd, nil
}

func firstMissingPathComponent(path string) string {
	path = filepath.Clean(path)
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if _, err := os.Stat(current); os.IsNotExist(err) {
			return current
		}
	}
	return ""
}

func execvpInDir(command []string, dir string) error {
	if len(command) == 0 {
		return errors.New("no command specified to execute")
	}
	if strings.TrimSpace(dir) != "" {
		if err := os.Chdir(dir); err != nil {
			return err
		}
	}
	program := command[0]
	if !strings.ContainsRune(program, '/') {
		resolved, err := exec.LookPath(program)
		if err != nil {
			return err
		}
		program = resolved
	}
	return unix.Exec(program, command, os.Environ())
}

type proxyRouteSpec struct {
	Routes []proxyRouteEntry `json:"routes"`
}

type proxyRouteEntry struct {
	EnvKey  string `json:"env_key"`
	UDSPath string `json:"uds_path"`
}

type plannedProxyRoute struct {
	EnvKey   string
	Endpoint string
}

func prepareHostProxyRouteSpec() (string, error) {
	routes, hasProxyConfig := planProxyRoutesFromEnv()
	if len(routes) == 0 {
		if hasProxyConfig {
			return "", errors.New("managed proxy mode requires parseable loopback proxy endpoints")
		}
		return "", errors.New("managed proxy mode requires proxy environment variables")
	}
	socketDir, err := createProxySocketDir()
	if err != nil {
		return "", err
	}
	endpointToUDS := map[string]string{}
	nextIndex := 0
	for _, route := range routes {
		if _, ok := endpointToUDS[route.Endpoint]; ok {
			continue
		}
		udsPath := filepath.Join(socketDir, fmt.Sprintf("proxy-route-%d.sock", nextIndex))
		nextIndex++
		if err := spawnProxyBridgeProcess("--proxy-host-bridge", route.Endpoint, udsPath); err != nil {
			return "", err
		}
		endpointToUDS[route.Endpoint] = udsPath
	}
	spec := proxyRouteSpec{}
	for _, route := range routes {
		spec.Routes = append(spec.Routes, proxyRouteEntry{
			EnvKey:  route.EnvKey,
			UDSPath: endpointToUDS[route.Endpoint],
		})
	}
	data, err := json.Marshal(&spec)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func activateProxyRoutesInNetns(serializedSpec string) error {
	var spec proxyRouteSpec
	if err := json.Unmarshal([]byte(serializedSpec), &spec); err != nil {
		return err
	}
	if len(spec.Routes) == 0 {
		return errors.New("proxy routing spec contained no routes")
	}
	portByUDS := map[string]uint16{}
	for _, route := range spec.Routes {
		if _, ok := portByUDS[route.UDSPath]; ok {
			continue
		}
		port, err := spawnProxyLocalBridge(route.UDSPath)
		if err != nil {
			return err
		}
		portByUDS[route.UDSPath] = port
	}
	for _, route := range spec.Routes {
		original := os.Getenv(route.EnvKey)
		if strings.TrimSpace(original) == "" {
			return fmt.Errorf("missing proxy env key %s", route.EnvKey)
		}
		rewritten, err := rewriteProxyEnvValue(original, portByUDS[route.UDSPath])
		if err != nil {
			return fmt.Errorf("could not rewrite proxy URL for env key %s: %w", route.EnvKey, err)
		}
		if err := os.Setenv(route.EnvKey, rewritten); err != nil {
			return err
		}
	}
	return nil
}

func planProxyRoutesFromEnv() ([]plannedProxyRoute, bool) {
	var routes []plannedProxyRoute
	hasProxyConfig := false
	for _, entry := range os.Environ() {
		key, value, ok := cutEnv(entry)
		if !ok || !proxyEnvKeys[strings.ToUpper(key)] {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		hasProxyConfig = true
		endpoint, ok := parseLoopbackProxyEndpoint(value)
		if !ok {
			continue
		}
		routes = append(routes, plannedProxyRoute{EnvKey: key, Endpoint: endpoint})
	}
	return routes, hasProxyConfig
}

func cutEnv(entry string) (string, string, bool) {
	key, value, ok := strings.Cut(entry, "=")
	return key, value, ok
}

func parseLoopbackProxyEndpoint(proxyURL string) (string, bool) {
	candidate := proxyURL
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", false
	}
	host := parsed.Hostname()
	if !isLoopbackHost(host) {
		return "", false
	}
	port := parsed.Port()
	if port == "" {
		port = strconv.Itoa(defaultProxyPort(parsed.Scheme))
	}
	if port == "0" {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func defaultProxyPort(scheme string) int {
	switch strings.ToLower(scheme) {
	case "https":
		return 443
	case "socks5", "socks5h", "socks4", "socks4a":
		return 1080
	default:
		return 80
	}
}

func rewriteProxyEnvValue(proxyURL string, localPort uint16) (string, error) {
	hadScheme := strings.Contains(proxyURL, "://")
	candidate := proxyURL
	if !hadScheme {
		candidate = "http://" + proxyURL
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", err
	}
	parsed.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(int(localPort)))
	rewritten := parsed.String()
	if !hadScheme {
		rewritten = strings.TrimPrefix(rewritten, "http://")
	}
	if !strings.HasSuffix(proxyURL, "/") && !strings.Contains(proxyURL, "?") && !strings.Contains(proxyURL, "#") {
		rewritten = strings.TrimSuffix(rewritten, "/")
	}
	return rewritten, nil
}

func createProxySocketDir() (string, error) {
	parent := proxySocketParentDir()
	if err := os.MkdirAll(parent, 0o711); err != nil {
		return "", err
	}
	_ = os.Chmod(parent, 0o711)
	pid := os.Getpid()
	uid := os.Geteuid()
	for attempt := 0; attempt < 128; attempt++ {
		candidate := filepath.Join(parent, fmt.Sprintf("%s%d-%d-%d", proxySocketDirPrefix, pid, uid, attempt))
		if err := os.Mkdir(candidate, 0o711); err == nil {
			_ = os.Chmod(candidate, 0o711)
			return candidate, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("failed to allocate proxy routing temp dir under %s", parent)
}

func proxySocketParentDir() string {
	home := os.Getenv("GCODE_HOME")
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home != "" {
		candidate := filepath.Join(home, "tmp")
		if len(filepath.Join(candidate, fmt.Sprintf("%s%d-%d-127", proxySocketDirPrefix, ^uint32(0), ^uint32(0)), "proxy-route-9223372036854775807.sock")) <= 107 {
			return candidate
		}
	}
	if tmp := os.TempDir(); len(filepath.Join(tmp, fmt.Sprintf("%s%d-%d-127", proxySocketDirPrefix, ^uint32(0), ^uint32(0)), "proxy-route-9223372036854775807.sock")) <= 107 {
		return tmp
	}
	return "/tmp"
}

func spawnProxyBridgeProcess(mode string, firstArg string, secondArg string) error {
	bridgePath := os.Args[0]
	cmd := exec.Command(bridgePath, mode, firstArg, secondArg)
	cmd.SysProcAttr = &unix.SysProcAttr{Pdeathsig: unix.SIGTERM}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	if strings.TrimSpace(line) != "ready" {
		_ = cmd.Process.Kill()
		return fmt.Errorf("proxy bridge did not acknowledge readiness")
	}
	return nil
}

func spawnProxyLocalBridge(udsPath string) (uint16, error) {
	bridgePath := os.Args[0]
	cmd := exec.Command(bridgePath, "--proxy-local-bridge", udsPath)
	cmd.SysProcAttr = &unix.SysProcAttr{Pdeathsig: unix.SIGTERM}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		_ = cmd.Process.Kill()
		return 0, err
	}
	port64, err := strconv.ParseUint(strings.TrimSpace(line), 10, 16)
	if err != nil {
		_ = cmd.Process.Kill()
		return 0, err
	}
	return uint16(port64), nil
}

func runProxyHostBridgeHelper(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("host bridge requires ENDPOINT and UDS_PATH")
	}
	endpoint := args[0]
	udsPath := args[1]
	_ = os.Remove(udsPath)
	listener, err := net.Listen("unix", udsPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	if _, err := fmt.Fprintln(stdout, "ready"); err != nil {
		return err
	}
	for {
		unixConn, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer unixConn.Close()
			tcpConn, err := net.Dial("tcp", endpoint)
			if err != nil {
				return
			}
			defer tcpConn.Close()
			proxyBidirectional(tcpConn, unixConn)
		}()
	}
}

func runProxyLocalBridgeHelper(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("local bridge requires UDS_PATH")
	}
	udsPath := args[0]
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if _, err := fmt.Fprintln(stdout, port); err != nil {
		return err
	}
	for {
		tcpConn, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer tcpConn.Close()
			unixConn, err := net.Dial("unix", udsPath)
			if err != nil {
				return
			}
			defer unixConn.Close()
			proxyBidirectional(tcpConn, unixConn)
		}()
	}
}

func proxyBidirectional(left net.Conn, right net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(left, right)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(right, left)
		done <- struct{}{}
	}()
	<-done
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
