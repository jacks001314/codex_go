package appserver

import (
	"fmt"
	"strings"

	"codex_go/config"
	"codex_go/sandbox"
)

// guardianReviewerPreparedOptions is the Go equivalent of the StartThreadOptions
// Rust's GuardianExtension::prepare_reviewer_options returns (#40742): an
// isolated reviewer configuration plus environment selections whose resolved
// permissions were narrowed to read-only. Go's synchronous reviewer does not
// spawn an internal session, so the host applies the resolved pieces at the
// review-request boundary and keeps the rest reusable for a future
// extension-owned reviewer session.
type guardianReviewerPreparedOptions struct {
	Config       *config.Config
	Environments []map[string]any
	// PermissionProfile is the reviewer's resolved read-only profile. Rust keeps
	// it on the prepared config (`permissions.permission_profile()`); Go's config
	// is a raw value tree, so the resolved profile travels beside it. The profile
	// preserves the parent's deny-read entries and falls back to a restricted
	// external profile when the parent boundary cannot be intersected.
	PermissionProfile *sandbox.PermissionProfile
	SessionSource     InternalSessionSource
	ThreadSource      ThreadSource
	Model             string
	ReasoningEffort   string
	// RequestMaxRetries and StreamMaxRetries mirror Rust's
	// `model_provider.request_max_retries`/`stream_max_retries = 1`. Those are
	// resolved provider fields in Go, not config tree values, so they travel as
	// prepared options instead of being written into `model_providers`.
	RequestMaxRetries int
	StreamMaxRetries  int
	Ephemeral         bool
	ProviderID        string
	SessionID         string
}

// guardianReviewerPrepareInput carries the parent turn state Rust's
// prepare_reviewer_options receives from its caller. The reviewer model and
// effort are resolved by the caller because Go's catalog-driven
// model.SelectApprovalReviewModel already owns that selection.
type guardianReviewerPrepareInput struct {
	ParentConfig       *config.Config
	ParentEnvironments []map[string]any
	ParentModel        string
	ReviewerModel      string
	ReviewerEffort     string
	// BaseInstructions is the reviewer policy prompt terminated by the output
	// contract (Rust's `template + OUTPUT_CONTRACT`).
	BaseInstructions                string
	ParentPermissionProfile         *sandbox.PermissionProfile
	PreserveClientDeveloperMessages bool
	Ephemeral                       bool
	ProviderID                      string
	SessionID                       string
}

// guardianPrepareReviewerOptions mirrors Rust
// ext/guardian-v2/src/sync_reviewer/reviewer_config.rs::prepare (#40742): it
// isolates the parent configuration, narrows every resolved environment to
// read-only, and fails closed when an environment has no resolved permissions.
func guardianPrepareReviewerOptions(input guardianReviewerPrepareInput) (*guardianReviewerPreparedOptions, error) {
	environments, err := guardianPrepareReviewerEnvironments(input.ParentEnvironments)
	if err != nil {
		return nil, err
	}
	prepared := &guardianReviewerPreparedOptions{
		Config:            guardianReviewerIsolatedConfig(input),
		Environments:      environments,
		PermissionProfile: guardianReviewerReadOnlyProfile(input.ParentPermissionProfile),
		SessionSource:     InternalSessionSourceGuardian,
		ThreadSource:      ThreadSourceGuardianReview,
		Model:             strings.TrimSpace(input.ReviewerModel),
		ReasoningEffort:   strings.TrimSpace(input.ReviewerEffort),
		RequestMaxRetries: 1,
		StreamMaxRetries:  1,
		Ephemeral:         input.Ephemeral,
		ProviderID:        strings.TrimSpace(input.ProviderID),
		SessionID:         strings.TrimSpace(input.SessionID),
	}
	return prepared, nil
}

// guardianReviewerIsolatedConfig clones the parent config and clears every
// surface the reviewer must not inherit. Rust mutates the typed Config clone;
// Go rewrites the `Values` tree and keeps the managed requirements pointer so
// constrained features still win over the reviewer defaults.
func guardianReviewerIsolatedConfig(input guardianReviewerPrepareInput) *config.Config {
	var parentValues map[string]any
	var requirements *config.ConfigRequirements
	if input.ParentConfig != nil {
		parentValues = input.ParentConfig.Values
		requirements = input.ParentConfig.Requirements
	}
	values := guardianCloneConfigValues(parentValues)
	if values == nil {
		values = map[string]any{}
	}
	isolated := &config.Config{Values: values, Requirements: requirements}

	reviewerModel := strings.TrimSpace(input.ReviewerModel)
	values["model"] = reviewerModel
	if effort := strings.TrimSpace(input.ReviewerEffort); effort != "" {
		values["model_reasoning_effort"] = effort
	} else {
		delete(values, "model_reasoning_effort")
	}
	// Always replace the base instructions so a parent's custom prompt can never
	// leak into the reviewer, even when the caller passes an empty prompt.
	values["base_instructions"] = input.BaseInstructions
	values["base_instructions_provenance"] = "custom"
	delete(values, "developer_instructions")
	values["project_doc_max_bytes"] = 0
	delete(values, "personality")

	values["include_apps_instructions"] = false
	values["include_collaboration_mode_instructions"] = false
	guardianReviewerSubTable(values, "skills")["include_instructions"] = false
	values["orchestrator_skills_enabled"] = false
	values["orchestrator_mcp_enabled"] = false
	values["agents_enabled"] = false
	memories := guardianReviewerSubTable(values, "memories")
	memories["use_memories"] = false
	memories["dedicated_tools"] = false

	delete(values, "notify")
	delete(values, "token_budget")
	delete(values, "rollout_budget")
	delete(guardianReviewerSubTable(values, "goals"), "max_goal_token_budget")

	values["approval_policy"] = string(sandbox.ApprovalNever)
	values["mcp_servers"] = map[string]any{}

	for _, feature := range guardianReviewerDisabledFeatures() {
		guardianReviewerDisableFeature(values, feature)
	}
	if input.PreserveClientDeveloperMessages {
		guardianReviewerEnableFeature(values, "retain_client_developer_messages")
	}

	if reviewerModel != strings.TrimSpace(input.ParentModel) {
		delete(values, "model_context_window")
		delete(values, "model_auto_compact_token_limit")
	}
	return isolated
}

// guardianPrepareReviewerEnvironments mirrors Rust's environment loop: Ready
// attachments keep every other field but swap in a read-only permission
// snapshot, FromThread is copied unchanged, and Pending/Failed fail closed
// because the reviewer cannot reason about unresolved permissions.
func guardianPrepareReviewerEnvironments(parent []map[string]any) ([]map[string]any, error) {
	if len(parent) == 0 {
		return nil, nil
	}
	out := make([]map[string]any, 0, len(parent))
	for _, selection := range parent {
		environmentID := selectionEnvironmentID(selection)
		cloned := guardianCloneConfigValues(selection)
		if cloned == nil {
			cloned = map[string]any{}
		}
		state, err := environmentConfigStateFromAnyMap(cloned)
		if err != nil {
			return nil, fmt.Errorf("guardian reviewer environment `%s` has no resolved permissions: %w", environmentID, err)
		}
		switch state.Kind {
		case EnvironmentConfigFromThread:
			// FromThread keeps following the thread's configuration.
		case EnvironmentConfigPending, EnvironmentConfigFailed:
			return nil, fmt.Errorf("guardian reviewer environment `%s` has no resolved permissions", environmentID)
		case EnvironmentConfigReady:
			environmentConfig := state.Config
			if environmentConfig == nil {
				environmentConfig = &EnvironmentConfig{}
			}
			environmentConfig.PermissionProfile = guardianReviewerReadOnlyProfile(environmentConfig.PermissionProfile)
			environmentConfig.PermissionProfileJSON = ""
			// Rust replaces the parent snapshot with PermissionProfileSnapshot::legacy,
			// which carries no named-profile identity.
			environmentConfig.ActivePermissionProfile = ""
			cloned["config"] = environmentConfigStateToAny(state)
		}
		out = append(out, cloned)
	}
	return out, nil
}

// guardianReviewerReadOnlyProfile mirrors Rust's read_only_permission_profile:
// intersect the parent with read-only, keeping its deny-read entries, and fall
// back to an external sandbox with restricted networking when the parent
// boundary cannot be intersected (Go returns nil for external-sandbox profiles).
func guardianReviewerReadOnlyProfile(profile *sandbox.PermissionProfile) *sandbox.PermissionProfile {
	if readOnly := profile.IntersectWithReadOnly(); readOnly != nil {
		return readOnly
	}
	fallback := sandbox.PermissionProfile{
		SandboxPolicy: sandbox.NewExternalSandboxPolicy(sandbox.NetworkRestricted),
	}
	return &fallback
}

// guardianReviewerDisabledFeatures mirrors Rust's DISABLED_FEATURES using the
// Go feature registry's canonical keys. CodexHooks/Collab/MemoryTool map to the
// legacy aliases' canonical keys (`hooks`, `multi_agent`, `memories`).
func guardianReviewerDisabledFeatures() []string {
	return []string{
		"apps",
		"hooks",
		"multi_agent",
		"current_time_reminder",
		"enable_mcp_apps",
		"goals",
		"guardian_approval",
		"guardian_ext",
		"guardianv2",
		"memories",
		"multi_agent_v2",
		"personality",
		"plugins",
		"recommended_plugins",
		"rollout_budget",
		"standalone_web_search",
		"token_budget",
		"tool_suggest",
		"web_search_cached",
		"web_search_request",
	}
}

// guardianReviewerDisableFeature turns a feature off without discarding its
// nested configuration table. Go stores feature flags and feature-owned tables
// under the same `features.<key>` entry, so an existing table keeps its keys and
// gains `enabled = false` (Rust's Features::disable leaves the typed config
// intact).
func guardianReviewerDisableFeature(values map[string]any, key string) {
	table := guardianReviewerSubTable(values, "features")
	if nested, ok := table[key].(map[string]any); ok {
		nested["enabled"] = false
		return
	}
	table[key] = false
}

func guardianReviewerEnableFeature(values map[string]any, key string) {
	table := guardianReviewerSubTable(values, "features")
	if nested, ok := table[key].(map[string]any); ok {
		nested["enabled"] = true
		return
	}
	table[key] = true
}

func guardianReviewerSubTable(values map[string]any, key string) map[string]any {
	if table, ok := values[key].(map[string]any); ok {
		return table
	}
	table := map[string]any{}
	values[key] = table
	return table
}

// guardianCloneConfigValues deep-copies the JSON-shaped config tree so the
// reviewer's nested tables never alias the parent's live configuration.
func guardianCloneConfigValues(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = guardianCloneConfigValue(value)
	}
	return out
}

func guardianCloneConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return guardianCloneConfigValues(typed)
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = guardianCloneConfigValue(item)
		}
		return items
	case map[string]bool:
		cloned := make(map[string]bool, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
