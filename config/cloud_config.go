package config

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codex_go/apps"
)

var ErrInvalidCloudConfig = errors.New("invalid cloud config")

type CloudConfigFragment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Contents string `json:"contents"`
}

type CloudConfigFragmentSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *CloudConfigFragmentSource) String() string {
	if s == nil {
		return ""
	}
	return s.Name + " (" + s.ID + ")"
}

type CloudConfigLayerSourceType string

const (
	CloudConfigLayerEnterpriseManaged CloudConfigLayerSourceType = "enterpriseManaged"
)

type CloudConfigLayerSource struct {
	Type CloudConfigLayerSourceType `json:"type"`
	ID   string                     `json:"id,omitempty"`
	Name string                     `json:"name,omitempty"`
}

type CloudConfigLayer struct {
	Source  CloudConfigLayerSource `json:"source"`
	Values  map[string]any         `json:"values"`
	RawTOML string                 `json:"rawToml"`
	BaseDir string                 `json:"baseDir"`
}

type CloudConfigRequirementsLayer struct {
	Source  CloudConfigLayerSource `json:"source"`
	Values  map[string]string      `json:"values"`
	RawTOML string                 `json:"rawToml"`
	BaseDir string                 `json:"baseDir"`
}

type CloudConfigTOMLBundle struct {
	EnterpriseManaged []CloudConfigFragment `json:"enterprise_managed"`
}

type CloudConfigRequirementsTOMLBundle struct {
	EnterpriseManaged []CloudConfigFragment `json:"enterprise_managed"`
}

type CloudConfigBundle struct {
	ConfigTOML       CloudConfigTOMLBundle             `json:"config_toml"`
	RequirementsTOML CloudConfigRequirementsTOMLBundle `json:"requirements_toml"`
}

func (b *CloudConfigBundle) IsEmpty() bool {
	return b == nil ||
		(len(b.ConfigTOML.EnterpriseManaged) == 0 && len(b.RequirementsTOML.EnterpriseManaged) == 0)
}

type CloudConfigBundleLayers struct {
	EnterpriseManagedConfig       []CloudConfigLayer             `json:"enterpriseManagedConfig"`
	EnterpriseManagedRequirements []CloudConfigRequirementsLayer `json:"enterpriseManagedRequirements"`
}

func CloudConfigLayersFromFragments(fragments []CloudConfigFragment, baseDir string) ([]CloudConfigLayer, error) {
	layers := make([]CloudConfigLayer, 0, len(fragments))
	for _, fragment := range fragments {
		source := CloudConfigFragmentSource{ID: fragment.ID, Name: fragment.Name}
		values, err := ParseCloudConfigSimpleTOML(fragment.Contents)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to parse cloud config fragment %s: %s", ErrInvalidCloudConfig, source.String(), err)
		}
		ResolveCloudConfigRelativePaths(values, baseDir)
		layers = append(layers, CloudConfigLayer{
			Source:  CloudConfigLayerSource{Type: CloudConfigLayerEnterpriseManaged, ID: fragment.ID, Name: fragment.Name},
			Values:  values,
			RawTOML: fragment.Contents,
			BaseDir: baseDir,
		})
	}
	reverseCloudConfigLayers(layers)
	return layers, nil
}

func CloudConfigLayersFromBundle(bundle CloudConfigBundle, baseDir string) (*CloudConfigBundleLayers, error) {
	configLayers, err := CloudConfigLayersFromFragments(bundle.ConfigTOML.EnterpriseManaged, baseDir)
	if err != nil {
		return nil, err
	}
	requirements := make([]CloudConfigRequirementsLayer, 0, len(bundle.RequirementsTOML.EnterpriseManaged))
	for _, fragment := range bundle.RequirementsTOML.EnterpriseManaged {
		values, err := ParseCloudConfigFlatTOML(fragment.Contents)
		if err != nil {
			source := CloudConfigFragmentSource{ID: fragment.ID, Name: fragment.Name}
			return nil, fmt.Errorf("%w: failed to parse cloud requirements fragment %s: %s", ErrInvalidCloudConfig, source.String(), err)
		}
		requirements = append(requirements, CloudConfigRequirementsLayer{
			Source:  CloudConfigLayerSource{Type: CloudConfigLayerEnterpriseManaged, ID: fragment.ID, Name: fragment.Name},
			Values:  values,
			RawTOML: fragment.Contents,
			BaseDir: baseDir,
		})
	}
	reverseCloudConfigRequirements(requirements)
	return &CloudConfigBundleLayers{
		EnterpriseManagedConfig:       configLayers,
		EnterpriseManagedRequirements: requirements,
	}, nil
}

type CloudConfigLoadErrorCode string

const (
	CloudConfigLoadAuth          CloudConfigLoadErrorCode = "auth"
	CloudConfigLoadTimeout       CloudConfigLoadErrorCode = "timeout"
	CloudConfigLoadRequestFailed CloudConfigLoadErrorCode = "request_failed"
	CloudConfigLoadInvalidBundle CloudConfigLoadErrorCode = "invalid_bundle"
	CloudConfigLoadInternal      CloudConfigLoadErrorCode = "internal"
)

type CloudConfigLoadError struct {
	Code       CloudConfigLoadErrorCode
	Message    string
	StatusCode *int
}

func NewCloudConfigLoadError(code CloudConfigLoadErrorCode, statusCode *int, message string) *CloudConfigLoadError {
	return &CloudConfigLoadError{Code: code, StatusCode: statusCode, Message: message}
}

func (e *CloudConfigLoadError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// CloudConfigBundlePolicyPhase is the admission phase of the delivered
// enterprise policy (Rust #49269 `CloudConfigBundlePolicyPhase`).
type CloudConfigBundlePolicyPhase int

const (
	// CloudConfigBundlePolicyUninitialized is a policy that has neither observed
	// nor published a delivered bundle yet.
	CloudConfigBundlePolicyUninitialized CloudConfigBundlePolicyPhase = iota
	// CloudConfigBundlePolicySuspended holds new enterprise MCP admission while
	// the observed bundle is validated and staged for publication.
	CloudConfigBundlePolicySuspended
	// CloudConfigBundlePolicyActive published the observed bundle and may
	// authorize new enterprise MCP requests.
	CloudConfigBundlePolicyActive
	// CloudConfigBundlePolicyRetired may not authorize anything anymore, e.g.
	// after its loader was replaced or cleared.
	CloudConfigBundlePolicyRetired
)

// CloudConfigBundleBindingStatus reports whether the policy revision a
// configuration was built from is still the delivered one.
type CloudConfigBundleBindingStatus int

const (
	// CloudConfigBundleBindingSuspended means no active policy backs the
	// binding, so enterprise MCP admission must stay off.
	CloudConfigBundleBindingSuspended CloudConfigBundleBindingStatus = iota
	// CloudConfigBundleBindingCurrent means the bound revision is the active one.
	CloudConfigBundleBindingCurrent
	// CloudConfigBundleBindingStale means a newer revision was delivered.
	CloudConfigBundleBindingStale
)

// CloudConfigBundlePolicy tracks which delivered bundle may authorize new
// enterprise MCP requests (Rust #49269 `CloudConfigBundlePolicy`). Observing a
// changed remote bundle suspends the previous revision before that bundle is
// validated or cached; only the snapshot publishing the matching revision
// re-activates admission, and a superseded or retired revision may no longer
// commit staged persistence.
type CloudConfigBundlePolicy struct {
	mu       sync.Mutex
	revision uint64
	bundle   *CloudConfigBundle
	phase    CloudConfigBundlePolicyPhase
	changed  chan struct{}
}

// NewCloudConfigBundlePolicy returns an uninitialized policy.
func NewCloudConfigBundlePolicy() *CloudConfigBundlePolicy {
	return &CloudConfigBundlePolicy{changed: make(chan struct{})}
}

// CloudConfigBundlePolicyRevision is an observed remote policy revision that is
// permitted to commit staged persistence.
type CloudConfigBundlePolicyRevision struct {
	revision uint64
	policy   *CloudConfigBundlePolicy
}

// CommitIfCurrent runs commit only while the observed revision is still current.
// Prepare the expensive work first: the revision check blocks newer
// observations and retirement for the duration of the commit, which is why the
// commit callback must not call back into the policy.
func (r *CloudConfigBundlePolicyRevision) CommitIfCurrent(commit func() error) error {
	if r == nil || r.policy == nil || commit == nil {
		return nil
	}
	r.policy.mu.Lock()
	defer r.policy.mu.Unlock()
	if r.policy.revision == r.revision &&
		(r.policy.phase == CloudConfigBundlePolicySuspended || r.policy.phase == CloudConfigBundlePolicyActive) {
		return commit()
	}
	return nil
}

// CloudConfigBundleBinding is the policy revision that resolved one set of
// enterprise MCP inputs. It stays live: a binding read after a newer revision
// was delivered reports Stale instead of Current.
type CloudConfigBundleBinding struct {
	revision *uint64
	policy   *CloudConfigBundlePolicy
}

// Status reports the current standing of the bound revision.
func (b *CloudConfigBundleBinding) Status() CloudConfigBundleBindingStatus {
	if b == nil || b.policy == nil {
		return CloudConfigBundleBindingSuspended
	}
	b.policy.mu.Lock()
	defer b.policy.mu.Unlock()
	if b.policy.phase != CloudConfigBundlePolicyActive || b.revision == nil {
		return CloudConfigBundleBindingSuspended
	}
	if b.policy.revision == *b.revision {
		return CloudConfigBundleBindingCurrent
	}
	return CloudConfigBundleBindingStale
}

// CloudConfigBundleSnapshot pairs a loaded bundle with the policy revision
// binding it was resolved from.
type CloudConfigBundleSnapshot struct {
	Bundle  *CloudConfigBundle
	Err     error
	Binding *CloudConfigBundleBinding
}

// ObserveRemoteBundle suspends enterprise MCP admission before a changed
// delivered bundle is validated or cached, and returns the revision allowed to
// commit staged persistence. It returns nil when the policy is retired (nothing
// may be admitted or published) or when the revision counter overflowed.
func (p *CloudConfigBundlePolicy) ObserveRemoteBundle(bundle *CloudConfigBundle) *CloudConfigBundlePolicyRevision {
	if p == nil {
		return nil
	}
	observed := bundle
	if bundle.IsEmpty() {
		observed = nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase == CloudConfigBundlePolicyRetired {
		return nil
	}
	if p.phase != CloudConfigBundlePolicyUninitialized && cloudConfigBundlesEqual(p.bundle, observed) {
		return &CloudConfigBundlePolicyRevision{revision: p.revision, policy: p}
	}
	if p.phase != CloudConfigBundlePolicyUninitialized {
		if p.revision == math.MaxUint64 {
			p.phase = CloudConfigBundlePolicyRetired
			p.notifyLocked()
			return nil
		}
		p.revision++
	}
	p.bundle = cloneCloudConfigBundle(observed)
	p.phase = CloudConfigBundlePolicySuspended
	p.notifyLocked()
	return &CloudConfigBundlePolicyRevision{revision: p.revision, policy: p}
}

// PublishSnapshot pairs a load result with its policy revision before the
// snapshot becomes visible. Only the snapshot that matches the observed bundle
// activates the policy; anything else (a superseded result, a load failure)
// leaves admission suspended.
func (p *CloudConfigBundlePolicy) PublishSnapshot(snapshot *CloudConfigBundleSnapshot) {
	if p == nil || snapshot == nil || snapshot.Err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.phase != CloudConfigBundlePolicyRetired &&
		(p.phase == CloudConfigBundlePolicyUninitialized || cloudConfigBundlesEqual(p.bundle, snapshot.Bundle))
	binding := &CloudConfigBundleBinding{policy: p}
	if current {
		revision := p.revision
		binding.revision = &revision
	}
	snapshot.Binding = binding
	if current && p.phase != CloudConfigBundlePolicyActive {
		p.bundle = cloneCloudConfigBundle(snapshot.Bundle)
		p.phase = CloudConfigBundlePolicyActive
		p.notifyLocked()
	}
}

// Retire stops new enterprise MCP admission from this policy owner without
// changing generic loader behavior.
func (p *CloudConfigBundlePolicy) Retire() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = CloudConfigBundlePolicyRetired
	p.notifyLocked()
}

// notifyLocked wakes every observer waiting for a phase or revision change.
// The caller holds p.mu.
func (p *CloudConfigBundlePolicy) notifyLocked() {
	if p.changed == nil {
		p.changed = make(chan struct{})
		return
	}
	close(p.changed)
	p.changed = make(chan struct{})
}

func cloudConfigBundlesEqual(a, b *CloudConfigBundle) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return cloudConfigFragmentsEqual(a.ConfigTOML.EnterpriseManaged, b.ConfigTOML.EnterpriseManaged) &&
		cloudConfigFragmentsEqual(a.RequirementsTOML.EnterpriseManaged, b.RequirementsTOML.EnterpriseManaged)
}

func cloudConfigFragmentsEqual(a, b []CloudConfigFragment) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func cloneCloudConfigBundle(bundle *CloudConfigBundle) *CloudConfigBundle {
	if bundle == nil {
		return nil
	}
	cloned := *bundle
	cloned.ConfigTOML.EnterpriseManaged = append([]CloudConfigFragment(nil), bundle.ConfigTOML.EnterpriseManaged...)
	cloned.RequirementsTOML.EnterpriseManaged = append([]CloudConfigFragment(nil), bundle.RequirementsTOML.EnterpriseManaged...)
	return &cloned
}

type CloudConfigLoader struct {
	mu     sync.Mutex
	load   func() (*CloudConfigBundle, error)
	bundle *CloudConfigBundle
	err    error
	// snapshot, when set, returns the published enterprise policy snapshot
	// instead of fetching through load (Rust #49269
	// CloudConfigBundleLoader::snapshot_getter).
	snapshot func() CloudConfigBundleSnapshot
	// emaPolicy is the enterprise policy owner whose revisions this loader
	// publishes (Rust #49269 CloudConfigBundleLoader::ema_policy).
	emaPolicy *CloudConfigBundlePolicy
}

func NewCloudConfigLoader(load func() (*CloudConfigBundle, error)) *CloudConfigLoader {
	if load == nil {
		load = func() (*CloudConfigBundle, error) { return nil, nil }
	}
	return &CloudConfigLoader{load: load}
}

func (l *CloudConfigLoader) Get() (*CloudConfigBundle, error) {
	if l == nil {
		return nil, nil
	}
	// Rust 070a26a1f0: retrieve the latest shared bundle on each
	// configuration load so later sessions observe refreshed bundles instead
	// of the startup snapshot. The last successful bundle is preserved when a
	// refresh fails.
	bundle, err := l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil {
		l.bundle = bundle
		l.err = nil
		return bundle, nil
	}
	if l.bundle != nil && l.err == nil {
		return l.bundle, nil
	}
	l.err = err
	return nil, err
}

// GetSnapshot returns the bundle together with the policy revision binding it
// was resolved from (Rust #49269 CloudConfigBundleLoader::get_snapshot). A
// loader without an attached policy snapshot getter falls back to Get and
// reports no binding.
func (l *CloudConfigLoader) GetSnapshot() CloudConfigBundleSnapshot {
	if l == nil {
		return CloudConfigBundleSnapshot{}
	}
	if l.snapshot != nil {
		return l.snapshot()
	}
	bundle, err := l.Get()
	return CloudConfigBundleSnapshot{Bundle: bundle, Err: err}
}

// WithEMAPolicySnapshots attaches the enterprise policy owner and the snapshot
// getter that publishes its revisions (Rust #49269
// `CloudConfigBundleLoader::with_ema_policy_snapshots`).
func (l *CloudConfigLoader) WithEMAPolicySnapshots(policy *CloudConfigBundlePolicy, getter func() CloudConfigBundleSnapshot) *CloudConfigLoader {
	if l == nil {
		return l
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.emaPolicy = policy
	l.snapshot = getter
	return l
}

// RetireEMAPolicy stops new enterprise MCP admission from this loader's policy
// owner without changing generic loader behavior.
func (l *CloudConfigLoader) RetireEMAPolicy() {
	if l == nil {
		return
	}
	l.mu.Lock()
	policy := l.emaPolicy
	l.mu.Unlock()
	policy.Retire()
}

func ParseCloudConfigSimpleTOML(input string) (map[string]any, error) {
	root := map[string]any{}
	var section []string
	for _, line := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(cloudConfigStripComment(line))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = cloudConfigSplitDottedPath(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			return nil, fmt.Errorf("expected key = value")
		}
		path := append(append([]string{}, section...), cloudConfigSplitDottedPath(strings.TrimSpace(key))...)
		cloudConfigSetAtPath(root, path, cloudConfigParseValue(strings.TrimSpace(value)))
	}
	return root, nil
}

func ParseCloudConfigFlatTOML(input string) (map[string]string, error) {
	values := map[string]string{}
	parsed, err := ParseCloudConfigSimpleTOML(input)
	if err != nil {
		return nil, err
	}
	cloudConfigFlatten("", parsed, values)
	return values, nil
}

func ResolveCloudConfigRelativePaths(values map[string]any, baseDir string) {
	if strings.TrimSpace(baseDir) == "" {
		return
	}
	for key, value := range values {
		nested, ok := value.(map[string]any)
		if ok {
			ResolveCloudConfigRelativePaths(nested, baseDir)
			continue
		}
		if !cloudConfigLooksLikePathKey(key) {
			continue
		}
		if path, ok := value.(string); ok && path != "" && !filepath.IsAbs(path) {
			values[key] = filepath.Clean(filepath.Join(baseDir, path))
		}
	}
}

func MergeCloudConfigLayers(layers []CloudConfigLayer) map[string]any {
	out := map[string]any{}
	for _, layer := range layers {
		cloudConfigMergeMap(out, layer.Values)
	}
	return out
}

func applyCloudConfigBundle(values map[string]any, requirements *ConfigRequirements, bundle CloudConfigBundle, baseDir string) (*ConfigRequirements, error) {
	layers, err := CloudConfigLayersFromBundle(bundle, baseDir)
	if err != nil {
		return nil, err
	}
	mergeConfigMaps(values, MergeCloudConfigLayers(layers.EnterpriseManagedConfig))

	managedRequirementValues := map[string]any{}
	for _, layer := range layers.EnterpriseManagedRequirements {
		parsed, err := parseRequirementsTOMLValues([]byte(layer.RawTOML))
		if err != nil {
			return nil, fmt.Errorf("%w: failed to parse cloud requirements fragment %s: %s", ErrInvalidCloudConfig, layer.Source.Name, err)
		}
		normalizeFeatureRequirementAliases(parsed)
		if permissions, ok := parsed["permissions"].(map[string]any); ok {
			if err := resolveFilesystemDenyReadPaths(permissions, layer.BaseDir); err != nil {
				return nil, fmt.Errorf("%w: invalid cloud requirements: %s", ErrInvalidCloudConfig, err)
			}
		}
		mergeConfigMaps(managedRequirementValues, parsed)
	}
	managedRequirements, err := configRequirementsFromValidatedMap(managedRequirementValues)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cloud requirements: %s", ErrInvalidCloudConfig, err)
	}
	// Rust 0f21cb3413 (#39043): cli_auth_credentials_store and chatgpt_base_url
	// are local-only authentication requirements and are ignored in
	// cloud-managed requirement layers.
	if managedRequirements != nil {
		managedRequirements.CliAuthCredentialsStore = nil
		managedRequirements.ChatgptBaseURL = nil
	}

	// Permission profiles in requirements are executable policy definitions,
	// while the remaining fields constrain which config values may be selected.
	if rawProfiles, ok := managedRequirementValues["permissions"].(map[string]any); ok {
		mergeConfigMaps(values, map[string]any{"permissions": rawProfiles})
	}
	if defaultProfile, ok := managedRequirementValues["default_permissions"].(string); ok && strings.TrimSpace(defaultProfile) != "" {
		values["default_permissions"] = strings.TrimSpace(defaultProfile)
	}
	return mergeConfigRequirements(requirements, managedRequirements), nil
}

// normalizeFeatureRequirementAliases mirrors Rust #42863: within a single
// requirements layer, the `feature_requirements` (and Go's `featureRequirements`)
// alias is moved onto the canonical `features` key before layers are merged, so
// mixed aliases share one merge path and retain layer precedence. The alias
// wins over a canonical key in the same layer, matching boolMapAnyKey order.
func normalizeFeatureRequirementAliases(values map[string]any) {
	if values == nil {
		return
	}
	for _, key := range []string{"featureRequirements", "feature_requirements"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		nested, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		values["features"] = nested
		delete(values, key)
	}
}

func mergeConfigRequirements(base, overlay *ConfigRequirements) *ConfigRequirements {
	out := cloneRequirements(base)
	if overlay == nil {
		return out
	}
	if out == nil {
		return cloneRequirements(overlay)
	}
	if overlay.AllowedApprovalPolicies != nil {
		out.AllowedApprovalPolicies = cloneSlice(overlay.AllowedApprovalPolicies)
	}
	if overlay.AllowedApprovalsReviewers != nil {
		out.AllowedApprovalsReviewers = cloneSlice(overlay.AllowedApprovalsReviewers)
	}
	if overlay.AllowedSandboxModes != nil {
		out.AllowedSandboxModes = cloneSlice(overlay.AllowedSandboxModes)
	}
	if overlay.AllowedWindowsSandboxImplementations != nil {
		out.AllowedWindowsSandboxImplementations = cloneSlice(overlay.AllowedWindowsSandboxImplementations)
	}
	if overlay.AllowedPermissionProfiles != nil {
		out.AllowedPermissionProfiles = cloneBoolMap(overlay.AllowedPermissionProfiles)
	}
	if overlay.DefaultPermissions != nil {
		out.DefaultPermissions = cloneStringPtr(overlay.DefaultPermissions)
	}
	if overlay.AllowedWebSearchModes != nil {
		out.AllowedWebSearchModes = cloneSlice(overlay.AllowedWebSearchModes)
	}
	if overlay.AllowManagedHooksOnly != nil {
		out.AllowManagedHooksOnly = cloneBoolPtr(overlay.AllowManagedHooksOnly)
	}
	if overlay.AllowBrowserAndComputerUse != nil {
		out.AllowBrowserAndComputerUse = cloneBoolPtr(overlay.AllowBrowserAndComputerUse)
	}
	if overlay.AllowAppshots != nil {
		out.AllowAppshots = cloneBoolPtr(overlay.AllowAppshots)
	}
	if overlay.AllowRemoteControl != nil {
		out.AllowRemoteControl = cloneBoolPtr(overlay.AllowRemoteControl)
	}
	if overlay.ComputerUse != nil {
		out.ComputerUse = cloneComputerUse(overlay.ComputerUse)
	}
	if overlay.BrowserUse != nil {
		out.BrowserUse = cloneBrowserUse(overlay.BrowserUse)
	}
	if overlay.InAppBrowser != nil {
		out.InAppBrowser = cloneInAppBrowser(overlay.InAppBrowser)
	}
	if overlay.AutoReview != nil {
		// Rust 208f05b233: `auto_review.required_on_models` unions model slugs
		// across requirement layers so protected models stay protected even
		// when a lower layer omits the setting. `ignore_rules` follows the
		// first-wins layer semantics for the app-server protocol exposure.
		if out.AutoReview == nil {
			out.AutoReview = cloneAutoReview(overlay.AutoReview)
		} else {
			out.AutoReview.RequiredOnModels = stringUnion(
				out.AutoReview.RequiredOnModels,
				overlay.AutoReview.RequiredOnModels,
			)
			if len(out.AutoReview.IgnoreRules) == 0 {
				out.AutoReview.IgnoreRules = append([]string(nil), overlay.AutoReview.IgnoreRules...)
			}
		}
	}
	if overlay.FeatureRequirements != nil {
		out.FeatureRequirements = cloneBoolMap(overlay.FeatureRequirements)
	}
	if overlay.Hooks != nil {
		out.Hooks = cloneManagedHooks(overlay.Hooks)
	}
	if overlay.EnforceResidency != nil {
		out.EnforceResidency = cloneResidencyRequirementPtr(overlay.EnforceResidency)
	}
	if overlay.Network != nil {
		out.Network = cloneNetwork(overlay.Network)
	}
	if overlay.Models != nil {
		out.Models = cloneModels(overlay.Models)
	}
	if overlay.MCPServers != nil {
		out.MCPServers = cloneMCPServerRequirements(overlay.MCPServers)
	}
	if overlay.Plugins != nil {
		out.Plugins = clonePluginRequirements(overlay.Plugins)
	}
	if len(overlay.Apps) > 0 {
		// Rust merge_app_requirements_descending: either layer can disable an
		// app, and an exact per-tool approval keeps the higher-precedence value.
		out.Apps = apps.MergeAppRequirementsDescending(out.Apps, overlay.Apps)
	}
	return out
}

func stringUnion(values ...[]string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, slice := range values {
		for _, value := range slice {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloudConfigStripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inQuote = ch
			continue
		}
		if ch == '#' {
			return line[:i]
		}
	}
	return line
}

func cloudConfigSplitDottedPath(path string) []string {
	parts := strings.Split(path, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloudConfigParseValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
			return raw[1 : len(raw)-1]
		}
	}
	switch raw {
	case "true":
		return true
	case "false":
		return false
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		content := strings.TrimSpace(raw[1 : len(raw)-1])
		if content == "" {
			return []any{}
		}
		parts := cloudConfigSplitCSV(content)
		values := make([]any, 0, len(parts))
		for _, part := range parts {
			values = append(values, cloudConfigParseValue(part))
		}
		return values
	}
	return raw
}

func cloudConfigSplitCSV(input string) []string {
	var parts []string
	var current strings.Builder
	inQuote := byte(0)
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if inQuote != 0 {
			current.WriteByte(ch)
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inQuote = ch
			current.WriteByte(ch)
			continue
		}
		if ch == ',' {
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	return parts
}

func cloudConfigSetAtPath(root map[string]any, parts []string, value any) {
	if len(parts) == 0 {
		return
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func cloudConfigFlatten(prefix string, values map[string]any, out map[string]string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok := values[key].(map[string]any); ok {
			cloudConfigFlatten(path, nested, out)
			continue
		}
		out[path] = fmt.Sprint(values[key])
	}
}

func cloudConfigLooksLikePathKey(key string) bool {
	key = strings.ToLower(key)
	return strings.HasSuffix(key, "path") || strings.HasSuffix(key, "dir") || strings.HasSuffix(key, "file")
}

func cloudConfigMergeMap(target map[string]any, source map[string]any) {
	for key, value := range source {
		sourceNested, ok := value.(map[string]any)
		if ok {
			targetNested, _ := target[key].(map[string]any)
			if targetNested == nil {
				targetNested = map[string]any{}
				target[key] = targetNested
			}
			cloudConfigMergeMap(targetNested, sourceNested)
			continue
		}
		target[key] = value
	}
}

func reverseCloudConfigLayers(values []CloudConfigLayer) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func reverseCloudConfigRequirements(values []CloudConfigRequirementsLayer) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
