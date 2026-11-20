package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Rust parity: codex-rs/config/src/guardian_policy.rs and
// codex-rs/config/src/loader/managed_requirements.rs
// load_guardian_policy_snapshot (#62274).
//
// One managed review policy observation carries the reviewer restrictions, the
// required models and the policy text together, together with the managed
// `auto_review` fallbacks it was resolved from. A refresh can therefore publish
// a policy without rebuilding the rest of the configuration, and a later full
// rebuild can retain the published policy instead of reviving an older managed
// value.

// guardianRequirementKeys are the managed requirement keys a Guardian policy
// snapshot owns. Every other managed key is dropped before composition, so an
// unrelated managed setting cannot block a policy update.
var guardianRequirementKeys = []string{
	"allowed_approvals_reviewers",
	"auto_review",
	"guardian_policy_config",
	"guardian_extra_policy",
	"config_server",
}

// GuardianFallbackLayer is one layer's projected `auto_review` value with the
// precedence that decides where it merges.
type GuardianFallbackLayer struct {
	Source LayerSource
	Value  any
}

// NewGuardianFallbackLayer projects an `auto_review` value published by a layer.
// Only `policy` survives from a table; a scalar is published unchanged so
// merging can reject it, and an empty table still replaces an earlier scalar.
func NewGuardianFallbackLayer(source LayerSource, value any) GuardianFallbackLayer {
	if table, ok := value.(map[string]any); ok {
		projected := make(map[string]any, 1)
		if policy, ok := table["policy"]; ok {
			projected["policy"] = policy
		}
		return GuardianFallbackLayer{Source: source, Value: projected}
	}
	return GuardianFallbackLayer{Source: source, Value: value}
}

// ProjectGuardianFallbackLayer projects a config layer's `auto_review` section.
// An omitted section contributes nothing (Rust GuardianFallbackLayer::project).
func ProjectGuardianFallbackLayer(layer Layer) (GuardianFallbackLayer, bool) {
	values := layerConfigMap(layer)
	if values == nil {
		return GuardianFallbackLayer{}, false
	}
	value, ok := values["auto_review"]
	if !ok {
		return GuardianFallbackLayer{}, false
	}
	return NewGuardianFallbackLayer(layer.Name, value), true
}

// GuardianPolicySnapshot is one managed review policy observation. The revision
// is allocated before loading, so publication can reject older in-flight work
// even when the observation itself did not change.
type GuardianPolicySnapshot struct {
	Revision      int64
	Binding       *CloudConfigBundleBinding
	Requirements  *ConfigRequirements
	ManagedConfig []GuardianFallbackLayer
}

// GuardianPolicyRevision reports the revision of the policy this configuration
// carries, or zero before the first publication.
func (c *Config) GuardianPolicyRevision() int64 {
	if c == nil {
		return 0
	}
	return c.guardianPolicyRevision
}

// GuardianPolicyBinding reports the policy revision that backs this
// configuration's Guardian requirements. Before the first publication it is the
// bundle the full load resolved from (Rust
// ConfigLayerStack::guardian_policy_binding).
func (c *Config) GuardianPolicyBinding() *CloudConfigBundleBinding {
	if c == nil {
		return nil
	}
	if c.guardianConfig != nil {
		return c.guardianBinding
	}
	return c.cloudConfigBinding
}

// ApplyGuardianPolicy publishes a snapshot: only the Guardian requirements, the
// managed fallbacks and the source validity change, so the rest of the
// configuration stays as loaded. The merged fallbacks are validated afterwards,
// which is why an invalid managed `auto_review` is retained (Rust #62274): a
// higher-precedence session layer may still make the merged value valid.
func (c *Config) ApplyGuardianPolicy(snapshot *GuardianPolicySnapshot) error {
	if c == nil {
		return fmt.Errorf("guardian policy: configuration is nil")
	}
	if snapshot == nil {
		return fmt.Errorf("guardian policy: snapshot is nil")
	}
	c.copyGuardianRequirements(snapshot.Requirements)
	c.guardianConfig = append(c.retainedGuardianFallbacks(), snapshot.ManagedConfig...)
	c.guardianBinding = snapshot.Binding
	c.guardianPolicyRevision = snapshot.Revision
	_, err := c.guardianPolicyFallback()
	return err
}

// RetainGuardianPolicyFrom keeps a published policy when a separate runtime
// refresh rebuilds the configuration stack (Rust
// ConfigLayerStack::retain_guardian_policy_from).
func (c *Config) RetainGuardianPolicyFrom(current *Config) {
	if c == nil || current == nil {
		return
	}
	c.copyGuardianRequirements(current.Requirements)
	if current.guardianConfig != nil {
		c.guardianConfig = cloneGuardianFallbacks(current.guardianConfig)
	} else {
		c.guardianConfig = current.startupGuardianFallbacks()
	}
	c.guardianBinding = current.GuardianPolicyBinding()
	c.guardianPolicyRevision = current.guardianPolicyRevision
}

// copyGuardianRequirements copies only the Guardian-owned requirements onto the
// retained ones, so publishing a policy never rewrites unrelated constraints.
func (c *Config) copyGuardianRequirements(source *ConfigRequirements) {
	if source == nil {
		if c.Requirements != nil {
			c.Requirements.AllowedApprovalsReviewers = nil
			c.Requirements.AutoReview = nil
			c.Requirements.GuardianPolicyConfig = nil
			c.Requirements.GuardianExtraPolicy = nil
		}
		return
	}
	if c.Requirements == nil {
		c.Requirements = &ConfigRequirements{}
	}
	c.Requirements.AllowedApprovalsReviewers = cloneSlice(source.AllowedApprovalsReviewers)
	c.Requirements.AutoReview = cloneAutoReview(source.AutoReview)
	c.Requirements.GuardianPolicyConfig = cloneStringPtr(source.GuardianPolicyConfig)
	c.Requirements.GuardianExtraPolicy = cloneStringPtr(source.GuardianExtraPolicy)
}

// retainedGuardianFallbacks projects the fallbacks a publication keeps around a
// new snapshot: the non-managed entries of the policy it replaces, or, before
// the first publication, the layers no snapshot owns yet.
func (c *Config) retainedGuardianFallbacks() []GuardianFallbackLayer {
	if c.guardianConfig != nil {
		fallbacks := make([]GuardianFallbackLayer, 0, len(c.guardianConfig))
		for _, layer := range c.guardianConfig {
			if !isManagedGuardianLayer(layer.Source) {
				fallbacks = append(fallbacks, layer)
			}
		}
		return fallbacks
	}
	fallbacks := make([]GuardianFallbackLayer, 0, len(c.featureLayers))
	for _, layer := range c.featureLayers {
		if isManagedGuardianLayer(layer.Name) || isGuardianLocalLayer(layer.Name) {
			continue
		}
		if projected, ok := ProjectGuardianFallbackLayer(layer); ok {
			fallbacks = append(fallbacks, projected)
		}
	}
	return fallbacks
}

// startupGuardianFallbacks projects every non-local layer, which is what a
// rebuilt configuration carries forward before it has published a policy.
func (c *Config) startupGuardianFallbacks() []GuardianFallbackLayer {
	fallbacks := make([]GuardianFallbackLayer, 0, len(c.featureLayers))
	for _, layer := range c.featureLayers {
		if isGuardianLocalLayer(layer.Name) {
			continue
		}
		if projected, ok := ProjectGuardianFallbackLayer(layer); ok {
			fallbacks = append(fallbacks, projected)
		}
	}
	return fallbacks
}

// localGuardianFallbacks projects the layers that stay on this configuration
// rather than coming from a published policy: every layer before the first
// publication, and the user and session sources afterwards, because a rebuild
// may carry a session override the managed snapshot does not own.
func (c *Config) localGuardianFallbacks() []GuardianFallbackLayer {
	fallbacks := make([]GuardianFallbackLayer, 0, len(c.featureLayers))
	for _, layer := range c.featureLayers {
		if c.guardianConfig != nil && !isGuardianLocalLayer(layer.Name) {
			continue
		}
		if projected, ok := ProjectGuardianFallbackLayer(layer); ok {
			fallbacks = append(fallbacks, projected)
		}
	}
	return fallbacks
}

// guardianPolicyFallback resolves the policy text after merging the refreshed
// managed fallbacks with the retained local layers in precedence order, then
// validating the result (Rust ConfigLayerStack::guardian_config).
func (c *Config) guardianPolicyFallback() (string, error) {
	fallbacks := cloneGuardianFallbacks(c.guardianConfig)
	fallbacks = append(fallbacks, c.localGuardianFallbacks()...)
	sort.SliceStable(fallbacks, func(i, j int) bool {
		return fallbacks[i].Source.Precedence() < fallbacks[j].Source.Precedence()
	})
	return mergeGuardianFallbacks(fallbacks)
}

// mergeGuardianFallbacks merges published fallbacks the way Rust merges TOML
// values: tables merge by key, and any other value replaces the accumulated
// result.
func mergeGuardianFallbacks(fallbacks []GuardianFallbackLayer) (string, error) {
	var merged any = map[string]any{}
	for _, fallback := range fallbacks {
		next, err := mergeGuardianValue(merged, fallback.Value)
		if err != nil {
			return "", err
		}
		merged = next
	}
	table, ok := merged.(map[string]any)
	if !ok {
		// Rust's AutoReviewToml is a struct, so a surviving scalar is invalid no
		// matter which layer published it.
		return "", fmt.Errorf("guardian policy: auto_review must be a table, found %T", merged)
	}
	value, ok := table["policy"]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("guardian policy: invalid auto_review.policy value %T", value)
	}
	return text, nil
}

func mergeGuardianValue(target, source any) (any, error) {
	sourceTable, sourceIsTable := source.(map[string]any)
	targetTable, targetIsTable := target.(map[string]any)
	if !sourceIsTable || !targetIsTable {
		return source, nil
	}
	merged := make(map[string]any, len(targetTable)+len(sourceTable))
	for key, value := range targetTable {
		merged[key] = value
	}
	for key, value := range sourceTable {
		existing, ok := merged[key]
		if !ok {
			merged[key] = value
			continue
		}
		next, err := mergeGuardianValue(existing, value)
		if err != nil {
			return nil, err
		}
		merged[key] = next
	}
	return merged, nil
}

func cloneGuardianFallbacks(fallbacks []GuardianFallbackLayer) []GuardianFallbackLayer {
	if fallbacks == nil {
		return nil
	}
	out := make([]GuardianFallbackLayer, len(fallbacks))
	copy(out, fallbacks)
	return out
}

// isGuardianLocalLayer reports whether a layer stays local to a rebuildable
// configuration rather than coming from managed policy.
func isGuardianLocalLayer(source LayerSource) bool {
	return source.Type == LayerSourceUser || source.Type == LayerSourceSessionFlags
}

// isManagedGuardianLayer reports whether a layer's `auto_review` value is part
// of the managed policy a snapshot replaces.
func isManagedGuardianLayer(source LayerSource) bool {
	switch source.Type {
	case LayerSourceEnterpriseManaged, LayerSourceLegacyManagedConfigFromFile, LayerSourceLegacyManagedConfigFromMDM:
		return true
	default:
		return false
	}
}

// LoadGuardianPolicySnapshot loads only the managed review policy (Rust #62274
// load_guardian_policy_snapshot): the legacy managed config publishes its
// managed `auto_review` fallback and its approvals reviewer, fetched policy
// publishes the fragments the bundle delivered, and every other managed setting
// is ignored. The bundle's own config server must be the one the machine
// requirements select, so policy is never published from an untrusted bundle.
func LoadGuardianPolicySnapshot(codexHome string, loader *CloudConfigLoader, revision int64) (*GuardianPolicySnapshot, error) {
	snapshot := loader.GetSnapshot()
	if snapshot.Err != nil {
		return nil, snapshot.Err
	}
	fallbacks := []GuardianFallbackLayer{}
	requirementLayers := []map[string]any{}
	if cloudBundle := snapshot.Bundle; cloudBundle != nil && !cloudBundle.IsEmpty() {
		layers, err := CloudConfigLayersFromBundle(*cloudBundle, codexHome)
		if err != nil {
			return nil, err
		}
		for _, layer := range layers.EnterpriseManagedConfig {
			value, ok := layer.Values["auto_review"]
			if !ok {
				continue
			}
			source := LayerSource{Type: LayerSourceEnterpriseManaged, ID: layer.Source.ID, Name: layer.Source.Name}
			fallbacks = append(fallbacks, NewGuardianFallbackLayer(source, value))
		}
		for _, layer := range layers.EnterpriseManagedRequirements {
			values, err := parseRequirementsTOMLValues([]byte(layer.RawTOML))
			if err != nil {
				return nil, fmt.Errorf("%w: failed to parse cloud requirements fragment %s: %s", ErrInvalidCloudConfig, layer.Source.Name, err)
			}
			requirementLayers = append(requirementLayers, narrowGuardianRequirements(values))
		}
	}
	if local, err := loadGuardianLocalRequirements(codexHome); err != nil {
		return nil, err
	} else {
		requirementLayers = append(requirementLayers, local...)
	}
	merged := map[string]any{}
	for _, values := range requirementLayers {
		mergeConfigMaps(merged, values)
	}
	var requirements *ConfigRequirements
	if len(merged) > 0 {
		parsed, err := configRequirementsFromValidatedMap(merged)
		if err != nil {
			return nil, err
		}
		requirements = parsed
	}
	var required *ConfigServer
	if requirements != nil {
		required = requirements.ConfigServer
	}
	if err := ValidateAppliedConfigServer(snapshot.ConfigServer, required); err != nil {
		return nil, err
	}
	if requirements != nil {
		// Rust takes the config server out of the published requirements: the
		// snapshot carries policy, not the bundle's own identity.
		requirements.ConfigServer = nil
	}
	return &GuardianPolicySnapshot{
		Revision:      revision,
		Binding:       snapshot.Binding,
		Requirements:  requirements,
		ManagedConfig: fallbacks,
	}, nil
}

// loadGuardianLocalRequirements reads the machine's own Guardian requirements:
// requirements.toml above fetched policy, and the legacy managed config above
// both, which is the order the full configuration stack composes them in.
func loadGuardianLocalRequirements(codexHome string) ([]map[string]any, error) {
	layers := []map[string]any{}
	values, err := parseRequirementsFileValues(requirementsPathForHome(codexHome))
	if err != nil {
		return nil, err
	}
	if values != nil {
		layers = append(layers, narrowGuardianRequirements(values))
	}
	if path, ok := managedConfigPathForRuntime(codexHome); ok {
		managed, exists, err := loadConfigFileIfExists(path)
		if err != nil {
			return nil, err
		}
		if exists {
			legacy := map[string]any{}
			if value, ok := managed["approvals_reviewer"]; ok {
				legacy["approvals_reviewer"] = value
			}
			layers = append(layers, narrowGuardianRequirements(LegacyManagedRequirementValues(legacy)))
		}
	}
	return layers, nil
}

// parseRequirementsFileValues parses one requirements document and reports a
// missing file as no layer at all.
func parseRequirementsFileValues(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseRequirementsTOMLValues(data)
}

// narrowGuardianRequirements keeps only the managed keys a Guardian policy
// snapshot owns, so unrelated managed settings drop out before composition.
func narrowGuardianRequirements(values map[string]any) map[string]any {
	narrowed := map[string]any{}
	for _, key := range guardianRequirementKeys {
		if value, ok := values[key]; ok {
			narrowed[key] = value
		}
	}
	return narrowed
}

// guardianPolicyFallbackText mirrors Rust resolve_guardian_policy_text: the
// managed policy requirement wins over the resolved `auto_review.policy`.
func guardianPolicyFallbackText(requirements *ConfigRequirements, fallback string) (string, bool) {
	if requirements != nil && requirements.GuardianPolicyConfig != nil {
		if value := strings.TrimSpace(*requirements.GuardianPolicyConfig); value != "" {
			return value, true
		}
	}
	value := strings.TrimSpace(fallback)
	return value, value != ""
}
