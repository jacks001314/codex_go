package windowssandbox

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	json "github.com/goccy/go-json"
)

const (
	SetupVersion    = 5
	OfflineUsername = "CodexSandboxOffline"
	OnlineUsername  = "CodexSandboxOnline"
)

var WindowsPlatformDefaultReadRoots = []string{
	`C:\Windows`,
	`C:\Program Files`,
	`C:\Program Files (x86)`,
	`C:\ProgramData`,
}

type SetupRootOverrides struct {
	ReadRoots                        []string
	ReadRootsSet                     bool
	ReadRootsIncludePlatformDefaults bool
	WriteRoots                       []string
	WriteRootsSet                    bool
	DenyReadPaths                    []string
	DenyWritePaths                   []string
}

type SandboxSetupRequest struct {
	RealUser             string
	CodexHome            string
	CommandCWD           string
	Env                  map[string]string
	ProxyEnforced        bool
	Permissions          *ResolvedWindowsSandboxPermissions
	Overrides            SetupRootOverrides
	OfflineProxySettings *OfflineProxySettings
}

type SetupMarker struct {
	Version           int      `json:"version"`
	OfflineUsername   string   `json:"offline_username"`
	OnlineUsername    string   `json:"online_username"`
	CreatedAt         *string  `json:"created_at,omitempty"`
	ProxyPorts        []uint16 `json:"proxy_ports,omitempty"`
	AllowLocalBinding bool     `json:"allow_local_binding,omitempty"`
}

func (m *SetupMarker) VersionMatches() bool {
	return m != nil && m.Version == SetupVersion
}

func (m *SetupMarker) OfflineProxySettings() OfflineProxySettings {
	if m == nil {
		return OfflineProxySettings{}
	}
	return OfflineProxySettings{
		ProxyPorts:        append([]uint16(nil), m.ProxyPorts...),
		AllowLocalBinding: m.AllowLocalBinding,
	}
}

func (m *SetupMarker) RequestMismatchReason(networkIdentity SandboxNetworkIdentity, desired OfflineProxySettings) string {
	if m == nil || !networkIdentity.UsesOfflineIdentity() {
		return ""
	}
	if equalUint16s(m.ProxyPorts, desired.ProxyPorts) && m.AllowLocalBinding == desired.AllowLocalBinding {
		return ""
	}
	return "offline firewall settings changed (stored_ports=" + fmtUint16s(m.ProxyPorts) +
		", desired_ports=" + fmtUint16s(desired.ProxyPorts) +
		", stored_allow_local_binding=" + strconv.FormatBool(m.AllowLocalBinding) +
		", desired_allow_local_binding=" + strconv.FormatBool(desired.AllowLocalBinding) + ")"
}

type SandboxUserRecord struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type SandboxUsersFile struct {
	Version int               `json:"version"`
	Offline SandboxUserRecord `json:"offline"`
	Online  SandboxUserRecord `json:"online"`
}

func (f *SandboxUsersFile) VersionMatches() bool {
	return f != nil && f.Version == SetupVersion
}

func RunElevatedSetup(req *SandboxSetupRequest) error {
	if req == nil {
		return ErrInvalidRequest
	}
	return runElevatedSetup(req)
}

func RunElevatedProvisioningSetup(req *SandboxSetupRequest) error {
	if req == nil {
		return ErrInvalidRequest
	}
	return runElevatedProvisioningSetup(req)
}

func RunSetupRefresh(codexHome string) error {
	return runSetupRefresh(codexHome)
}

func RunSetupRefreshForRequest(req *SandboxSetupRequest) error {
	if req == nil {
		return ErrInvalidRequest
	}
	return runSetupRefreshForRequest(req)
}

func SandboxDir(codexHome string) string {
	return filepath.Join(codexHome, ".sandbox")
}

func SandboxBinDir(codexHome string) string {
	return filepath.Join(codexHome, ".sandbox-bin")
}

func SandboxSecretsDir(codexHome string) string {
	return filepath.Join(codexHome, ".sandbox-secrets")
}

func SetupMarkerPath(codexHome string) string {
	return filepath.Join(SandboxDir(codexHome), "setup_marker.json")
}

func SandboxUsersPath(codexHome string) string {
	return filepath.Join(SandboxSecretsDir(codexHome), "sandbox_users.json")
}

func WriteSetupMarker(codexHome string, marker *SetupMarker) error {
	if marker == nil {
		return ErrInvalidRequest
	}
	if err := os.MkdirAll(SandboxDir(codexHome), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SetupMarkerPath(codexHome), data, 0o600)
}

func ReadSetupMarker(codexHome string) (*SetupMarker, error) {
	data, err := os.ReadFile(SetupMarkerPath(codexHome))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marker SetupMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, err
	}
	return &marker, nil
}

type OfflineProxySettings struct {
	ProxyPorts        []uint16
	AllowLocalBinding bool
}

type SandboxNetworkIdentity string

const (
	SandboxNetworkIdentityOffline SandboxNetworkIdentity = "offline"
	SandboxNetworkIdentityOnline  SandboxNetworkIdentity = "online"
)

var proxyEnvKeys = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"ALL_PROXY",
	"WS_PROXY",
	"WSS_PROXY",
	"http_proxy",
	"https_proxy",
	"all_proxy",
	"ws_proxy",
	"wss_proxy",
}

const allowLocalBindingEnvKey = "CODEX_NETWORK_ALLOW_LOCAL_BINDING"

var UserProfileRootExclusions = []string{
	".ssh",
	".tsh",
	".brev",
	".gnupg",
	".aws",
	".azure",
	".kube",
	".docker",
	".config",
	".npm",
	".pki",
	".terraform.d",
}

func SandboxNetworkIdentityFromPermissions(permissions *ResolvedWindowsSandboxPermissions, proxyEnforced bool) SandboxNetworkIdentity {
	if proxyEnforced || permissions == nil || permissions.NetworkPolicy() != "enabled" {
		return SandboxNetworkIdentityOffline
	}
	return SandboxNetworkIdentityOnline
}

func (i SandboxNetworkIdentity) UsesOfflineIdentity() bool {
	return i == SandboxNetworkIdentityOffline
}

func OfflineProxySettingsFromEnv(envMap map[string]string, networkIdentity SandboxNetworkIdentity) OfflineProxySettings {
	if !networkIdentity.UsesOfflineIdentity() {
		return OfflineProxySettings{}
	}
	return OfflineProxySettings{
		ProxyPorts:        ProxyPortsFromEnv(envMap),
		AllowLocalBinding: envMap[allowLocalBindingEnvKey] == "1",
	}
}

func ProxyPortsFromEnv(envMap map[string]string) []uint16 {
	seen := map[uint16]bool{}
	for _, key := range proxyEnvKeys {
		if port, ok := LoopbackProxyPortFromURL(envMap[key]); ok {
			seen[port] = true
		}
	}
	ports := make([]uint16, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports
}

func LoopbackProxyPortFromURL(value string) (uint16, bool) {
	value = strings.TrimSpace(value)
	schemeSplit := strings.SplitN(value, "://", 2)
	if len(schemeSplit) != 2 {
		return 0, false
	}
	authority := strings.SplitN(schemeSplit[1], "/", 2)[0]
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	if strings.HasPrefix(authority, "[") {
		end := strings.Index(authority, "]")
		if end < 0 || authority[1:end] != "::1" {
			return 0, false
		}
		portText := strings.TrimPrefix(authority[end+1:], ":")
		port, ok := parseNonZeroPort(portText)
		return port, ok
	}
	host, portText, ok := strings.Cut(authority, ":")
	if !ok || !(strings.EqualFold(host, "localhost") || host == "127.0.0.1") {
		return 0, false
	}
	return parseNonZeroPort(portText)
}

func GatherReadRoots(commandCWD string, permissions *ResolvedWindowsSandboxPermissions, envMap map[string]string, codexHome string) []string {
	if permissions == nil {
		return nil
	}
	if permissions.HasFullDiskReadAccess() {
		return GatherFullReadRootsForPermissions(commandCWD, permissions, envMap, codexHome)
	}
	roots := GatherHelperReadRoots(codexHome)
	if permissions.IncludePlatformDefaults() {
		roots = append(roots, WindowsPlatformDefaultReadRoots...)
	}
	roots = append(roots, permissions.ReadableRootsForCWD(commandCWD)...)
	return canonicalExisting(roots)
}

func GatherFullReadRootsForPermissions(commandCWD string, permissions *ResolvedWindowsSandboxPermissions, envMap map[string]string, codexHome string) []string {
	roots := GatherHelperReadRoots(codexHome)
	roots = append(roots, WindowsPlatformDefaultReadRoots...)
	if userProfile := strings.TrimSpace(os.Getenv("USERPROFILE")); userProfile != "" {
		roots = append(roots, ProfileReadRoots(userProfile)...)
	}
	roots = append(roots, commandCWD)
	for _, root := range permissions.WritableRootsForCWD(commandCWD, envMap) {
		roots = append(roots, root.Root)
	}
	return canonicalExisting(roots)
}

func GatherHelperReadRoots(codexHome string) []string {
	helperDir := HelperBinDir(codexHome)
	_ = os.MkdirAll(helperDir, 0o700)
	return []string{helperDir}
}

func GatherWriteRootsForPermissions(permissions *ResolvedWindowsSandboxPermissions, commandCWD string, envMap map[string]string) []string {
	if permissions == nil {
		return nil
	}
	var roots []string
	for _, root := range permissions.WritableRootsForCWD(commandCWD, envMap) {
		roots = append(roots, root.Root)
	}
	return canonicalExisting(roots)
}

func EffectiveWriteRootsForSetup(permissions *ResolvedWindowsSandboxPermissions, commandCWD string, envMap map[string]string, codexHome string, writeRootsOverride []string, overrideSet bool) []string {
	var writeRoots []string
	if overrideSet {
		writeRoots = canonicalExisting(writeRootsOverride)
	} else {
		writeRoots = GatherWriteRootsForPermissions(permissions, commandCWD, envMap)
	}
	writeRoots = ExpandUserProfileRoot(writeRoots)
	writeRoots = FilterUserProfileRoot(writeRoots)
	writeRoots = FilterUserProfileRootExclusions(writeRoots)
	writeRoots = FilterSSHConfigDependencyRoots(writeRoots)
	return FilterSensitiveWriteRoots(writeRoots, codexHome)
}

func BuildPayloadRoots(request *SandboxSetupRequest, overrides SetupRootOverrides) ([]string, []string) {
	if request == nil {
		return nil, nil
	}
	writeRoots := EffectiveWriteRootsForSetup(request.Permissions, request.CommandCWD, request.Env, request.CodexHome, overrides.WriteRoots, overrides.WriteRootsSet)
	var readRoots []string
	if overrides.ReadRootsSet {
		readRoots = GatherHelperReadRoots(request.CodexHome)
		if overrides.ReadRootsIncludePlatformDefaults {
			readRoots = append(readRoots, WindowsPlatformDefaultReadRoots...)
		}
		readRoots = append(readRoots, overrides.ReadRoots...)
		readRoots = canonicalExisting(readRoots)
	} else {
		readRoots = GatherReadRoots(request.CommandCWD, request.Permissions, request.Env, request.CodexHome)
	}
	readRoots = ExpandUserProfileRoot(readRoots)
	readRoots = FilterUserProfileRoot(readRoots)
	readRoots = FilterUserProfileRootExclusions(readRoots)
	readRoots = FilterSSHConfigDependencyRoots(readRoots)
	writeSet := map[string]bool{}
	for _, root := range writeRoots {
		writeSet[CanonicalPathKey(root)] = true
	}
	filtered := readRoots[:0]
	for _, root := range readRoots {
		if !writeSet[CanonicalPathKey(root)] {
			filtered = append(filtered, root)
		}
	}
	return filtered, writeRoots
}

func BuildPayloadDenyWritePaths(request *SandboxSetupRequest, explicitDenyWritePaths []string) []string {
	var out []string
	for _, path := range explicitDenyWritePaths {
		if cleaned := cleanWindowsSandboxAbs(path); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	if request == nil || request.Permissions == nil {
		return out
	}
	seen := map[string]bool{}
	for _, path := range out {
		seen[CanonicalPathKey(path)] = true
	}
	for _, root := range request.Permissions.WritableRootsForCWD(request.CommandCWD, request.Env) {
		for _, deny := range root.ReadOnlySubpaths {
			if _, err := os.Stat(deny); err != nil {
				continue
			}
			key := CanonicalPathKey(deny)
			if !seen[key] {
				seen[key] = true
				out = append(out, cleanWindowsSandboxAbs(deny))
			}
		}
	}
	return out
}

func BuildPayloadDenyReadPaths(explicitDenyReadPaths []string) []string {
	return cloneStrings(explicitDenyReadPaths)
}

func ProfileReadRoots(userProfile string) []string {
	entries, err := os.ReadDir(userProfile)
	if err != nil {
		return []string{userProfile}
	}
	var roots []string
	for _, entry := range entries {
		name := entry.Name()
		excluded := false
		for _, exclude := range UserProfileRootExclusions {
			if strings.EqualFold(name, exclude) {
				excluded = true
				break
			}
		}
		if !excluded {
			roots = append(roots, filepath.Join(userProfile, name))
		}
	}
	return roots
}

func ExpandUserProfileRoot(roots []string) []string {
	userProfile := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if userProfile == "" {
		return roots
	}
	return ExpandUserProfileRootFor(roots, userProfile)
}

func ExpandUserProfileRootFor(roots []string, userProfile string) []string {
	userProfileKey := CanonicalPathKey(userProfile)
	var expanded []string
	for _, root := range roots {
		if CanonicalPathKey(root) == userProfileKey {
			expanded = append(expanded, ProfileReadRoots(userProfile)...)
		} else {
			expanded = append(expanded, root)
		}
	}
	return dedupSortedByCanonicalKey(expanded)
}

func FilterUserProfileRoot(roots []string) []string {
	userProfile := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if userProfile == "" {
		return roots
	}
	userProfileKey := CanonicalPathKey(userProfile)
	out := roots[:0]
	for _, root := range roots {
		if CanonicalPathKey(root) != userProfileKey {
			out = append(out, root)
		}
	}
	return out
}

func FilterUserProfileRootExclusions(roots []string) []string {
	userProfile := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if userProfile == "" {
		return roots
	}
	out := roots[:0]
	for _, root := range roots {
		if !IsUserProfileRootExclusion(root, userProfile) {
			out = append(out, root)
		}
	}
	return out
}

func IsUserProfileRootExclusion(root string, userProfile string) bool {
	child := UserProfileChildName(root, userProfile)
	if child == "" {
		return false
	}
	for _, exclude := range UserProfileRootExclusions {
		if strings.EqualFold(child, exclude) {
			return true
		}
	}
	return false
}

func FilterSSHConfigDependencyRoots(roots []string) []string {
	userProfile := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if userProfile == "" {
		return roots
	}
	dependencyPaths := SSHConfigDependencyPaths(userProfile)
	out := roots[:0]
	for _, root := range roots {
		if !IsSSHConfigDependencyRoot(root, userProfile, dependencyPaths) {
			out = append(out, root)
		}
	}
	return out
}

func IsSSHConfigDependencyRoot(root string, userProfile string, dependencyPaths []string) bool {
	child := UserProfileChildName(root, userProfile)
	if child == "" {
		return false
	}
	for _, path := range dependencyPaths {
		if strings.EqualFold(child, UserProfileChildName(path, userProfile)) {
			return true
		}
	}
	return false
}

func UserProfileChildName(path string, userProfile string) string {
	rootKey := CanonicalPathKey(path)
	profileKey := strings.TrimRight(CanonicalPathKey(userProfile), "/")
	prefix := profileKey + "/"
	relative, ok := strings.CutPrefix(rootKey, prefix)
	if !ok {
		return ""
	}
	child, _, _ := strings.Cut(relative, "/")
	return child
}

func FilterSensitiveWriteRoots(roots []string, codexHome string) []string {
	codexHomeKey := CanonicalPathKey(codexHome)
	protected := []string{SandboxDir(codexHome), SandboxBinDir(codexHome), SandboxSecretsDir(codexHome)}
	out := roots[:0]
	for _, root := range roots {
		key := CanonicalPathKey(root)
		if key == codexHomeKey {
			continue
		}
		blocked := false
		for _, protectedRoot := range protected {
			protectedKey := strings.TrimRight(CanonicalPathKey(protectedRoot), "/")
			if key == protectedKey || strings.HasPrefix(key, protectedKey+"/") {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, root)
		}
	}
	return out
}

func canonicalExisting(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		canonical, err := CanonicalizePath(path)
		if err != nil {
			canonical = cleanWindowsSandboxAbs(path)
		}
		key := CanonicalPathKey(canonical)
		if !seen[key] {
			seen[key] = true
			out = append(out, canonical)
		}
	}
	return out
}

func dedupSortedByCanonicalKey(paths []string) []string {
	sort.Slice(paths, func(i, j int) bool {
		return CanonicalPathKey(paths[i]) < CanonicalPathKey(paths[j])
	})
	var out []string
	last := ""
	for _, path := range paths {
		key := CanonicalPathKey(path)
		if key == "" || key == last {
			continue
		}
		out = append(out, path)
		last = key
	}
	return out
}

func parseNonZeroPort(value string) (uint16, bool) {
	port64, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port64 == 0 {
		return 0, false
	}
	return uint16(port64), true
}

func equalUint16s(left []uint16, right []uint16) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func fmtUint16s(values []uint16) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(int(value))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
