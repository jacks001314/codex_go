package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/sandbox"
)

var ErrInvalidConfigRequest = errors.New("invalid config request")

const appServerManagedConfigPathEnv = "CODEX_APP_SERVER_MANAGED_CONFIG_PATH"

type ConfigWriteErrorCode string

const (
	ConfigWriteLayerReadonly       ConfigWriteErrorCode = "configLayerReadonly"
	ConfigWriteRequirementReadonly ConfigWriteErrorCode = "configRequirementReadonly"
	ConfigWriteVersionConflict     ConfigWriteErrorCode = "configVersionConflict"
	ConfigWriteValidation          ConfigWriteErrorCode = "configValidationError"
	ConfigWritePathNotFound        ConfigWriteErrorCode = "configPathNotFound"
	ConfigWriteSchemaUnknown       ConfigWriteErrorCode = "configSchemaUnknownKey"
	ConfigWriteUserLayerAbsent     ConfigWriteErrorCode = "userLayerNotFound"
)

type ConfigWriteError struct {
	Code ConfigWriteErrorCode
	Err  error
}

func (e *ConfigWriteError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ConfigWriteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ConfigWriteError) JSONRPCErrorData() map[string]any {
	if e == nil || e.Code == "" {
		return nil
	}
	return map[string]any{"config_write_error_code": string(e.Code)}
}

func configWriteErrorf(code ConfigWriteErrorCode, format string, args ...any) error {
	return &ConfigWriteError{
		Code: code,
		Err:  fmt.Errorf("%w: %s", ErrInvalidConfigRequest, fmt.Sprintf(format, args...)),
	}
}

type LayerSourceType string

const (
	LayerSourceMDM                         LayerSourceType = "mdm"
	LayerSourcePackagedDefaults            LayerSourceType = "packagedDefaults"
	LayerSourceSystem                      LayerSourceType = "system"
	LayerSourceEnterpriseManaged           LayerSourceType = "enterpriseManaged"
	LayerSourceUser                        LayerSourceType = "user"
	LayerSourceProject                     LayerSourceType = "project"
	LayerSourceSessionFlags                LayerSourceType = "sessionFlags"
	LayerSourceLegacyManagedConfigFromFile LayerSourceType = "legacyManagedConfigTomlFromFile"
	LayerSourceLegacyManagedConfigFromMDM  LayerSourceType = "legacyManagedConfigTomlFromMdm"
)

type LayerSource struct {
	Type                LayerSourceType `json:"type"`
	Domain              string          `json:"domain,omitempty"`
	Key                 string          `json:"key,omitempty"`
	File                string          `json:"file,omitempty"`
	ID                  string          `json:"id,omitempty"`
	Name                string          `json:"name,omitempty"`
	Profile             *string         `json:"profile,omitempty"`
	DotCodexFolder      string          `json:"dotCodexFolder,omitempty"`
	HooksDotCodexFolder string          `json:"-"`
}

func (s LayerSource) MarshalJSON() ([]byte, error) {
	switch s.Type {
	case LayerSourcePackagedDefaults:
		return json.Marshal(struct {
			Type LayerSourceType `json:"type"`
			File string          `json:"file"`
		}{Type: s.Type, File: s.File})
	case LayerSourceMDM:
		return json.Marshal(struct {
			Type   LayerSourceType `json:"type"`
			Domain string          `json:"domain"`
			Key    string          `json:"key"`
		}{Type: s.Type, Domain: s.Domain, Key: s.Key})
	case LayerSourceSystem:
		return json.Marshal(struct {
			Type LayerSourceType `json:"type"`
			File string          `json:"file"`
		}{Type: s.Type, File: s.File})
	case LayerSourceEnterpriseManaged:
		return json.Marshal(struct {
			Type LayerSourceType `json:"type"`
			ID   string          `json:"id"`
			Name string          `json:"name"`
		}{Type: s.Type, ID: s.ID, Name: s.Name})
	case LayerSourceUser:
		return json.Marshal(struct {
			Type    LayerSourceType `json:"type"`
			File    string          `json:"file"`
			Profile *string         `json:"profile"`
		}{Type: s.Type, File: s.File, Profile: cloneStringPtr(s.Profile)})
	case LayerSourceProject:
		return json.Marshal(struct {
			Type           LayerSourceType `json:"type"`
			DotCodexFolder string          `json:"dotCodexFolder"`
		}{Type: s.Type, DotCodexFolder: s.DotCodexFolder})
	case LayerSourceSessionFlags, LayerSourceLegacyManagedConfigFromMDM:
		return json.Marshal(struct {
			Type LayerSourceType `json:"type"`
		}{Type: s.Type})
	case LayerSourceLegacyManagedConfigFromFile:
		return json.Marshal(struct {
			Type LayerSourceType `json:"type"`
			File string          `json:"file"`
		}{Type: s.Type, File: s.File})
	default:
		type layerSourceAlias LayerSource
		return json.Marshal(layerSourceAlias(s))
	}
}

func (s *LayerSource) Precedence() int16 {
	if s == nil {
		return -1
	}
	switch s.Type {
	case LayerSourcePackagedDefaults:
		return -10
	case LayerSourceMDM:
		return 0
	case LayerSourceSystem:
		return 10
	case LayerSourceEnterpriseManaged:
		return 15
	case LayerSourceUser:
		if s.Profile != nil {
			return 21
		}
		return 20
	case LayerSourceProject:
		return 25
	case LayerSourceSessionFlags:
		return 30
	case LayerSourceLegacyManagedConfigFromFile:
		return 40
	case LayerSourceLegacyManagedConfigFromMDM:
		return 50
	default:
		return 100
	}
}

type LayerMetadata struct {
	Name    LayerSource `json:"name"`
	Version string      `json:"version"`
}

type Layer struct {
	Name           LayerSource `json:"name"`
	Version        string      `json:"version"`
	Config         any         `json:"config"`
	DisabledReason *string     `json:"disabledReason,omitempty"`
}

type ConfigReadParams struct {
	IncludeLayers bool    `json:"includeLayers,omitempty"`
	CWD           *string `json:"cwd,omitempty"`
}

type ConfigReadResponse struct {
	Config  map[string]any           `json:"config"`
	Origins map[string]LayerMetadata `json:"origins"`
	Layers  []Layer                  `json:"layers,omitempty"`
}

func (r *ConfigReadResponse) MarshalJSON() ([]byte, error) {
	configValues := configValuesForJSON(r.Config)
	origins := make(map[string]LayerMetadata, len(r.Origins))
	for key, value := range r.Origins {
		origins[key] = value
	}
	payload := map[string]any{
		"config":  configValues,
		"origins": origins,
	}
	if r.Layers != nil {
		payload["layers"] = cloneLayers(r.Layers)
	}
	return json.Marshal(payload)
}

var configRequiredNullableKeys = []string{
	"model",
	"review_model",
	"model_context_window",
	"model_auto_compact_token_limit",
	"model_auto_compact_token_limit_scope",
	"model_catalog_json",
	"model_provider",
	"approval_policy",
	"approvals_reviewer",
	"sandbox_mode",
	"sandbox_workspace_write",
	"forced_chatgpt_workspace_id",
	"forced_login_method",
	"web_search",
	"tools",
	"instructions",
	"developer_instructions",
	"compact_prompt",
	"model_reasoning_effort",
	"model_reasoning_summary",
	"model_verbosity",
	"service_tier",
	"analytics",
	"apps",
	"desktop",
}

func configValuesForJSON(values map[string]any) map[string]any {
	out := cloneMap(values)
	for _, key := range configRequiredNullableKeys {
		if _, ok := out[key]; !ok {
			out[key] = nil
		}
	}
	normalizeSandboxWorkspaceWriteConfig(out)
	normalizeToolsConfig(out)
	normalizeAnalyticsConfig(out)
	normalizeAppsConfig(out)
	return out
}

func normalizeSandboxWorkspaceWriteConfig(values map[string]any) {
	object, ok := values["sandbox_workspace_write"].(map[string]any)
	if !ok {
		return
	}
	normalized := cloneMap(object)
	setDefaultJSONField(normalized, "writable_roots", []any{})
	setDefaultJSONField(normalized, "network_access", false)
	setDefaultJSONField(normalized, "exclude_tmpdir_env_var", false)
	setDefaultJSONField(normalized, "exclude_slash_tmp", false)
	values["sandbox_workspace_write"] = normalized
}

func normalizeToolsConfig(values map[string]any) {
	object, ok := values["tools"].(map[string]any)
	if !ok {
		return
	}
	normalized := cloneMap(object)
	if webSearch, ok := normalized["web_search"].(map[string]any); ok {
		webSearch = cloneMap(webSearch)
		setDefaultJSONField(webSearch, "context_size", nil)
		setDefaultJSONField(webSearch, "allowed_domains", nil)
		setDefaultJSONField(webSearch, "location", nil)
		if location, ok := webSearch["location"].(map[string]any); ok {
			location = cloneMap(location)
			setDefaultJSONField(location, "country", nil)
			setDefaultJSONField(location, "region", nil)
			setDefaultJSONField(location, "city", nil)
			setDefaultJSONField(location, "timezone", nil)
			webSearch["location"] = location
		}
		normalized["web_search"] = webSearch
	} else {
		normalized["web_search"] = nil
	}
	values["tools"] = normalized
}

func normalizeAnalyticsConfig(values map[string]any) {
	object, ok := values["analytics"].(map[string]any)
	if !ok {
		return
	}
	normalized := cloneMap(object)
	setDefaultJSONField(normalized, "enabled", nil)
	values["analytics"] = normalized
}

func normalizeAppsConfig(values map[string]any) {
	object, ok := values["apps"].(map[string]any)
	if !ok {
		return
	}
	normalized := cloneMap(object)
	if defaults, ok := normalized["_default"].(map[string]any); ok {
		defaults = cloneMap(defaults)
		setDefaultJSONField(defaults, "enabled", true)
		setDefaultJSONField(defaults, "approvals_reviewer", nil)
		setDefaultJSONField(defaults, "destructive_enabled", true)
		setDefaultJSONField(defaults, "open_world_enabled", true)
		setDefaultJSONField(defaults, "default_tools_approval_mode", nil)
		normalized["_default"] = defaults
	} else {
		setDefaultJSONField(normalized, "_default", nil)
	}
	for key, value := range normalized {
		if key == "_default" {
			continue
		}
		app, ok := value.(map[string]any)
		if !ok {
			continue
		}
		app = cloneMap(app)
		setDefaultJSONField(app, "enabled", true)
		setDefaultJSONField(app, "approvals_reviewer", nil)
		setDefaultJSONField(app, "destructive_enabled", nil)
		setDefaultJSONField(app, "open_world_enabled", nil)
		setDefaultJSONField(app, "default_tools_approval_mode", nil)
		setDefaultJSONField(app, "default_tools_enabled", nil)
		if tools, ok := app["tools"].(map[string]any); ok {
			tools = cloneMap(tools)
			for toolName, toolValue := range tools {
				tool, ok := toolValue.(map[string]any)
				if !ok {
					continue
				}
				tool = cloneMap(tool)
				setDefaultJSONField(tool, "enabled", nil)
				setDefaultJSONField(tool, "approval_mode", nil)
				tools[toolName] = tool
			}
			app["tools"] = tools
		} else {
			setDefaultJSONField(app, "tools", nil)
		}
		normalized[key] = app
	}
	values["apps"] = normalized
}

func setDefaultJSONField(values map[string]any, key string, value any) {
	if _, ok := values[key]; ok {
		return
	}
	values[key] = cloneDefaultJSONValue(value)
}

func cloneDefaultJSONValue(value any) any {
	switch typed := value.(type) {
	case []any:
		return append([]any(nil), typed...)
	case map[string]any:
		return cloneMap(typed)
	default:
		return typed
	}
}

type MergeStrategy string

const (
	MergeReplace MergeStrategy = "replace"
	MergeUpsert  MergeStrategy = "upsert"
)

type WriteStatus string

const (
	WriteOK           WriteStatus = "ok"
	WriteOKOverridden WriteStatus = "okOverridden"
)

type ConfigValueWriteParams struct {
	KeyPath         string        `json:"keyPath"`
	Value           any           `json:"value"`
	MergeStrategy   MergeStrategy `json:"mergeStrategy"`
	FilePath        *string       `json:"filePath,omitempty"`
	ExpectedVersion *string       `json:"expectedVersion,omitempty"`
}

func (p *ConfigValueWriteParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidConfigRequest)
	}
	if strings.TrimSpace(p.KeyPath) == "" {
		return fmt.Errorf("%w: keyPath is required", ErrInvalidConfigRequest)
	}
	switch p.MergeStrategy {
	case "", MergeReplace, MergeUpsert:
		return nil
	default:
		return fmt.Errorf("%w: unsupported mergeStrategy %q", ErrInvalidConfigRequest, p.MergeStrategy)
	}
}

type ConfigEdit struct {
	KeyPath       string        `json:"keyPath"`
	Value         any           `json:"value"`
	MergeStrategy MergeStrategy `json:"mergeStrategy"`
}

func (e *ConfigEdit) Validate() error {
	if e == nil || strings.TrimSpace(e.KeyPath) == "" {
		return fmt.Errorf("%w: keyPath is required", ErrInvalidConfigRequest)
	}
	switch e.MergeStrategy {
	case "", MergeReplace, MergeUpsert:
		return nil
	default:
		return fmt.Errorf("%w: unsupported mergeStrategy %q", ErrInvalidConfigRequest, e.MergeStrategy)
	}
}

type ConfigBatchWriteParams struct {
	Edits            []ConfigEdit `json:"edits"`
	FilePath         *string      `json:"filePath,omitempty"`
	ExpectedVersion  *string      `json:"expectedVersion,omitempty"`
	ReloadUserConfig bool         `json:"reloadUserConfig,omitempty"`
}

func (p *ConfigBatchWriteParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidConfigRequest)
	}
	for i := range p.Edits {
		if err := p.Edits[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

type OverriddenMetadata struct {
	Message         string        `json:"message"`
	OverridingLayer LayerMetadata `json:"overridingLayer"`
	EffectiveValue  any           `json:"effectiveValue"`
}

type SkillConfigWriteParams struct {
	Name    string `json:"name,omitempty"`
	Path    string `json:"path,omitempty"`
	Enabled bool   `json:"enabled"`
}

func (p *SkillConfigWriteParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidConfigRequest)
	}
	hasName := strings.TrimSpace(p.Name) != ""
	hasPath := strings.TrimSpace(p.Path) != ""
	if hasName == hasPath {
		return fmt.Errorf("%w: skills/config/write requires exactly one of path or name", ErrInvalidConfigRequest)
	}
	return nil
}

type ConfigWriteResponse struct {
	Status             WriteStatus         `json:"status"`
	Version            string              `json:"version"`
	FilePath           string              `json:"filePath"`
	OverriddenMetadata *OverriddenMetadata `json:"overriddenMetadata"`
}

type ConfigRequirementsReadResponse struct {
	Requirements *ConfigRequirements `json:"requirements"`
}

// AuthCredentialsStoreMode mirrors Rust codex_config::types::AuthCredentialsStoreMode
// used by the managed cli_auth_credentials_store requirement (#39043).
type AuthCredentialsStoreMode string

const (
	AuthCredentialsStoreFile      AuthCredentialsStoreMode = "file"
	AuthCredentialsStoreKeyring   AuthCredentialsStoreMode = "keyring"
	AuthCredentialsStoreAuto      AuthCredentialsStoreMode = "auto"
	AuthCredentialsStoreEphemeral AuthCredentialsStoreMode = "ephemeral"
)

var supportedExperimentalFeatureEnablement = []string{
	"auth_elicitation",
	"memories",
	"mentions_v2",
	"remote_control",
	"remote_plugin",
	"tool_suggest",
}

type ConfigRequirements struct {
	AllowedApprovalPolicies              []sandbox.AskForApproval  `json:"allowedApprovalPolicies,omitempty"`
	AllowedApprovalsReviewers            []ApprovalsReviewer       `json:"allowedApprovalsReviewers,omitempty"`
	AllowedSandboxModes                  []sandbox.SandboxMode     `json:"allowedSandboxModes,omitempty"`
	AllowedWindowsSandboxImplementations []WindowsSandboxSetupMode `json:"allowedWindowsSandboxImplementations,omitempty"`
	AllowedPermissionProfiles            map[string]bool           `json:"allowedPermissionProfiles,omitempty"`
	DefaultPermissions                   *string                   `json:"defaultPermissions,omitempty"`
	// Permissions carries the managed [permissions] profile catalog from
	// requirements (Rust ConfigRequirementsToml.permissions, #39752). It is
	// internal and not part of the app-server wire ConfigRequirements schema.
	Permissions                     map[string]any                  `json:"-"`
	AllowedWebSearchModes           []WebSearchMode                 `json:"allowedWebSearchModes,omitempty"`
	AllowManagedHooksOnly           *bool                           `json:"allowManagedHooksOnly,omitempty"`
	AllowAppshots                   *bool                           `json:"allowAppshots,omitempty"`
	AllowRemoteControl              *bool                           `json:"allowRemoteControl,omitempty"`
	ComputerUse                     *ComputerUseRequirements        `json:"computerUse,omitempty"`
	BrowserUse                      *BrowserUseRequirements         `json:"browserUse,omitempty"`
	InAppBrowser                    *InAppBrowserRequirements       `json:"inAppBrowser,omitempty"`
	AutoReview                      *AutoReviewRequirements         `json:"autoReview,omitempty"`
	FeatureRequirements             map[string]bool                 `json:"featureRequirements,omitempty"`
	Hooks                           *ManagedHooksRequirements       `json:"hooks,omitempty"`
	EnforceResidency                *ResidencyRequirement           `json:"enforceResidency,omitempty"`
	Network                         *NetworkRequirements            `json:"network,omitempty"`
	Models                          *ModelsRequirements             `json:"models,omitempty"`
	AllowedLoginMethods             []ForcedLoginMethod             `json:"allowedLoginMethods,omitempty"`
	AllowedChatGPTWorkspaces        []string                        `json:"allowedChatGPTWorkspaces,omitempty"`
	CliAuthCredentialsStore         *AuthCredentialsStoreMode       `json:"cliAuthCredentialsStore,omitempty"`
	ChatgptBaseURL                  *string                         `json:"chatgptBaseUrl,omitempty"`
	AdditionalDeveloperInstructions *string                         `json:"additionalDeveloperInstructions,omitempty"`
	MCPServers                      map[string]MCPServerRequirement `json:"-"`
	Plugins                         map[string]PluginRequirements   `json:"-"`
}

func (r *ConfigRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AllowedApprovalPolicies              []sandbox.AskForApproval  `json:"allowedApprovalPolicies"`
		AllowedApprovalsReviewers            []ApprovalsReviewer       `json:"allowedApprovalsReviewers"`
		AllowedSandboxModes                  []sandbox.SandboxMode     `json:"allowedSandboxModes"`
		AllowedWindowsSandboxImplementations []WindowsSandboxSetupMode `json:"allowedWindowsSandboxImplementations"`
		AllowedPermissionProfiles            map[string]bool           `json:"allowedPermissionProfiles"`
		DefaultPermissions                   *string                   `json:"defaultPermissions"`
		AllowedWebSearchModes                []WebSearchMode           `json:"allowedWebSearchModes"`
		AllowManagedHooksOnly                *bool                     `json:"allowManagedHooksOnly"`
		AllowAppshots                        *bool                     `json:"allowAppshots"`
		AllowRemoteControl                   *bool                     `json:"allowRemoteControl"`
		ComputerUse                          *ComputerUseRequirements  `json:"computerUse"`
		BrowserUse                           *BrowserUseRequirements   `json:"browserUse"`
		InAppBrowser                         *InAppBrowserRequirements `json:"inAppBrowser"`
		AutoReview                           *AutoReviewRequirements   `json:"autoReview"`
		FeatureRequirements                  map[string]bool           `json:"featureRequirements"`
		Hooks                                *ManagedHooksRequirements `json:"hooks"`
		EnforceResidency                     *ResidencyRequirement     `json:"enforceResidency"`
		Network                              *NetworkRequirements      `json:"network"`
		Models                               *ModelsRequirements       `json:"models"`
		AllowedLoginMethods                  []ForcedLoginMethod       `json:"allowedLoginMethods"`
		AllowedChatGPTWorkspaces             []string                  `json:"allowedChatGPTWorkspaces"`
		CliAuthCredentialsStore              *AuthCredentialsStoreMode `json:"cliAuthCredentialsStore"`
		ChatgptBaseURL                       *string                   `json:"chatgptBaseUrl"`
		AdditionalDeveloperInstructions      *string                   `json:"additionalDeveloperInstructions"`
	}{
		AllowedApprovalPolicies:              permissionPoliciesOrNil(r.AllowedApprovalPolicies),
		AllowedApprovalsReviewers:            approvalsReviewersOrNil(r.AllowedApprovalsReviewers),
		AllowedSandboxModes:                  sandboxModesOrNil(r.AllowedSandboxModes),
		AllowedWindowsSandboxImplementations: windowsSandboxModesOrNil(r.AllowedWindowsSandboxImplementations),
		AllowedPermissionProfiles:            cloneBoolMap(r.AllowedPermissionProfiles),
		DefaultPermissions:                   cloneStringPtr(r.DefaultPermissions),
		AllowedWebSearchModes:                webSearchModesOrNil(r.AllowedWebSearchModes),
		AllowManagedHooksOnly:                cloneBoolPtr(r.AllowManagedHooksOnly),
		AllowAppshots:                        cloneBoolPtr(r.AllowAppshots),
		AllowRemoteControl:                   cloneBoolPtr(r.AllowRemoteControl),
		ComputerUse:                          cloneComputerUse(r.ComputerUse),
		BrowserUse:                           cloneBrowserUse(r.BrowserUse),
		InAppBrowser:                         cloneInAppBrowser(r.InAppBrowser),
		AutoReview:                           cloneAutoReview(r.AutoReview),
		FeatureRequirements:                  cloneBoolMap(r.FeatureRequirements),
		Hooks:                                cloneManagedHooks(r.Hooks),
		EnforceResidency:                     cloneResidencyRequirementPtr(r.EnforceResidency),
		Network:                              cloneNetwork(r.Network),
		Models:                               cloneModels(r.Models),
		AllowedLoginMethods:                  forcedLoginMethodsOrNil(r.AllowedLoginMethods),
		AllowedChatGPTWorkspaces:             stringSliceOrNil(r.AllowedChatGPTWorkspaces),
		CliAuthCredentialsStore:              cloneAuthCredentialsStoreMode(r.CliAuthCredentialsStore),
		ChatgptBaseURL:                       cloneStringPtr(r.ChatgptBaseURL),
		AdditionalDeveloperInstructions:      cloneStringPtr(r.AdditionalDeveloperInstructions),
	})
}

// AutoReviewRequiredForModel mirrors Rust's auto_review_required_for_model
// (codex-rs/config/src/config_requirements.rs, Rust 208f05b233): a model slug
// (or its exact provider-alias suffix) listed in auto_review.required_on_models
// must run with on-request approvals and the auto_review reviewer.
func (r *ConfigRequirements) AutoReviewRequiredForModel(model string) bool {
	if r == nil || r.AutoReview == nil || len(r.AutoReview.RequiredOnModels) == 0 {
		return false
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if idx := strings.Index(model, "/"); idx > 0 {
		namespace := model[:idx]
		suffix := model[idx+1:]
		if strings.Contains(suffix, "/") || !simpleModelNamespace(namespace) {
			return false
		}
		model = suffix
	}
	for _, slug := range r.AutoReview.RequiredOnModels {
		if slug == model {
			return true
		}
	}
	return false
}

func simpleModelNamespace(namespace string) bool {
	if namespace == "" {
		return false
	}
	for _, character := range namespace {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-') {
			return false
		}
	}
	return true
}

type BrowserUseRequirements struct {
	DisableAutoReview *bool `json:"disableAutoReview,omitempty"`
}

func (r *BrowserUseRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		DisableAutoReview *bool `json:"disableAutoReview"`
	}{
		DisableAutoReview: cloneBoolPtr(r.DisableAutoReview),
	})
}

// InAppBrowserRequirements mirrors Rust's managed in-app browser settings
// policy (Rust #39720): whether external browser settings imports are allowed.
type InAppBrowserRequirements struct {
	AllowExternalBrowserSettingsImport *bool `json:"allowExternalBrowserSettingsImport,omitempty"`
}

func (r *InAppBrowserRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AllowExternalBrowserSettingsImport *bool `json:"allowExternalBrowserSettingsImport"`
	}{
		AllowExternalBrowserSettingsImport: cloneBoolPtr(r.AllowExternalBrowserSettingsImport),
	})
}

// AutoReviewRequirements mirrors Rust's AutoReviewRequirementsToml plus the
// ignore-rule exposure from `auto_review.ignore_rules` (Rust 208f05b233 and
// 2e3a1702c2). requiredOnModels lists model slugs that must run with
// on-request approvals and the auto_review reviewer; ignoreRules lists models
// that ignore saved command-prefix approvals.
type AutoReviewRequirements struct {
	RequiredOnModels []string `json:"requiredOnModels,omitempty"`
	IgnoreRules      []string `json:"ignoreRules,omitempty"`
}

func (r *AutoReviewRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RequiredOnModels []string `json:"requiredOnModels"`
		IgnoreRules      []string `json:"ignoreRules"`
	}{
		RequiredOnModels: stringSliceOrNil(r.RequiredOnModels),
		IgnoreRules:      stringSliceOrNil(r.IgnoreRules),
	})
}

type ApprovalsReviewer string

const (
	ApprovalsReviewerUser             ApprovalsReviewer = "user"
	ApprovalsReviewerAutoReview       ApprovalsReviewer = "auto_review"
	ApprovalsReviewerGuardianSubagent ApprovalsReviewer = "guardian_subagent"
)

type WindowsSandboxSetupMode string

const (
	WindowsSandboxSetupDisabled   WindowsSandboxSetupMode = "disabled"
	WindowsSandboxSetupDefault    WindowsSandboxSetupMode = "default"
	WindowsSandboxSetupElevated   WindowsSandboxSetupMode = "elevated"
	WindowsSandboxSetupUnelevated WindowsSandboxSetupMode = "unelevated"
)

func ResolveAllowedWindowsSandboxSetupMode(requirements *ConfigRequirements, requested WindowsSandboxSetupMode) (WindowsSandboxSetupMode, error) {
	resolved := requested
	if resolved == WindowsSandboxSetupDefault {
		resolved = WindowsSandboxSetupUnelevated
	}
	switch resolved {
	case WindowsSandboxSetupElevated, WindowsSandboxSetupUnelevated:
	default:
		return "", fmt.Errorf("%w: invalid Windows sandbox setup mode: unsupported mode %q", ErrInvalidConfigRequest, requested)
	}
	if requirements == nil || len(requirements.AllowedWindowsSandboxImplementations) == 0 {
		return resolved, nil
	}
	for _, allowed := range requirements.AllowedWindowsSandboxImplementations {
		if allowed == WindowsSandboxSetupDefault {
			allowed = WindowsSandboxSetupUnelevated
		}
		if allowed == resolved {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: invalid Windows sandbox setup mode: %s is not allowed", ErrInvalidConfigRequest, requested)
}

type WebSearchMode string

const (
	WebSearchDisabled WebSearchMode = "disabled"
	WebSearchEnabled  WebSearchMode = "enabled"
)

type ComputerUseRequirements struct {
	AllowLockedComputerUse *bool `json:"allowLockedComputerUse,omitempty"`
}

func (r *ComputerUseRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AllowLockedComputerUse *bool `json:"allowLockedComputerUse"`
	}{
		AllowLockedComputerUse: cloneBoolPtr(r.AllowLockedComputerUse),
	})
}

type ModelsRequirements struct {
	NewThread *NewThreadModelDefaults `json:"newThread,omitempty"`
}

func (r *ModelsRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		NewThread *NewThreadModelDefaults `json:"newThread"`
	}{
		NewThread: cloneNewThreadModelDefaults(r.NewThread),
	})
}

type NewThreadModelDefaults struct {
	Model                *string `json:"model,omitempty"`
	ModelReasoningEffort *string `json:"modelReasoningEffort,omitempty"`
	ServiceTier          *string `json:"serviceTier,omitempty"`
}

func (d *NewThreadModelDefaults) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Model                *string `json:"model"`
		ModelReasoningEffort *string `json:"modelReasoningEffort"`
		ServiceTier          *string `json:"serviceTier"`
	}{
		Model:                cloneStringPtr(d.Model),
		ModelReasoningEffort: cloneStringPtr(d.ModelReasoningEffort),
		ServiceTier:          cloneStringPtr(d.ServiceTier),
	})
}

type ResidencyRequirement string

const ResidencyUS ResidencyRequirement = "us"

type NetworkRequirements struct {
	Enabled                          *bool                        `json:"enabled,omitempty"`
	HTTPPort                         *uint16                      `json:"httpPort,omitempty"`
	SOCKSPort                        *uint16                      `json:"socksPort,omitempty"`
	AllowUpstreamProxy               *bool                        `json:"allowUpstreamProxy,omitempty"`
	DangerouslyAllowNonLoopbackProxy *bool                        `json:"dangerouslyAllowNonLoopbackProxy,omitempty"`
	DangerouslyAllowAllUnixSockets   *bool                        `json:"dangerouslyAllowAllUnixSockets,omitempty"`
	Domains                          map[string]NetworkPermission `json:"domains,omitempty"`
	ManagedAllowedDomainsOnly        *bool                        `json:"managedAllowedDomainsOnly,omitempty"`
	AllowedDomains                   []string                     `json:"allowedDomains,omitempty"`
	DeniedDomains                    []string                     `json:"deniedDomains,omitempty"`
	UnixSockets                      map[string]NetworkPermission `json:"unixSockets,omitempty"`
	AllowUnixSockets                 []string                     `json:"allowUnixSockets,omitempty"`
	AllowLocalBinding                *bool                        `json:"allowLocalBinding,omitempty"`
}

func (r *NetworkRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Enabled                          *bool                        `json:"enabled"`
		HTTPPort                         *uint16                      `json:"httpPort"`
		SOCKSPort                        *uint16                      `json:"socksPort"`
		AllowUpstreamProxy               *bool                        `json:"allowUpstreamProxy"`
		DangerouslyAllowNonLoopbackProxy *bool                        `json:"dangerouslyAllowNonLoopbackProxy"`
		DangerouslyAllowAllUnixSockets   *bool                        `json:"dangerouslyAllowAllUnixSockets"`
		Domains                          map[string]NetworkPermission `json:"domains"`
		ManagedAllowedDomainsOnly        *bool                        `json:"managedAllowedDomainsOnly"`
		AllowedDomains                   []string                     `json:"allowedDomains"`
		DeniedDomains                    []string                     `json:"deniedDomains"`
		UnixSockets                      map[string]NetworkPermission `json:"unixSockets"`
		AllowUnixSockets                 []string                     `json:"allowUnixSockets"`
		AllowLocalBinding                *bool                        `json:"allowLocalBinding"`
	}{
		Enabled:                          cloneBoolPtr(r.Enabled),
		HTTPPort:                         cloneUint16Ptr(r.HTTPPort),
		SOCKSPort:                        cloneUint16Ptr(r.SOCKSPort),
		AllowUpstreamProxy:               cloneBoolPtr(r.AllowUpstreamProxy),
		DangerouslyAllowNonLoopbackProxy: cloneBoolPtr(r.DangerouslyAllowNonLoopbackProxy),
		DangerouslyAllowAllUnixSockets:   cloneBoolPtr(r.DangerouslyAllowAllUnixSockets),
		Domains:                          cloneNetworkMap(r.Domains),
		ManagedAllowedDomainsOnly:        cloneBoolPtr(r.ManagedAllowedDomainsOnly),
		AllowedDomains:                   stringSliceOrNil(r.AllowedDomains),
		DeniedDomains:                    stringSliceOrNil(r.DeniedDomains),
		UnixSockets:                      cloneNetworkMap(r.UnixSockets),
		AllowUnixSockets:                 stringSliceOrNil(r.AllowUnixSockets),
		AllowLocalBinding:                cloneBoolPtr(r.AllowLocalBinding),
	})
}

type NetworkPermission string

const (
	NetworkAllow NetworkPermission = "allow"
	NetworkDeny  NetworkPermission = "deny"
)

type ManagedHooksRequirements struct {
	ManagedDir        *string               `json:"managedDir,omitempty"`
	WindowsManagedDir *string               `json:"windowsManagedDir,omitempty"`
	PreToolUse        []ConfiguredHookGroup `json:"PreToolUse"`
	PermissionRequest []ConfiguredHookGroup `json:"PermissionRequest"`
	PostToolUse       []ConfiguredHookGroup `json:"PostToolUse"`
	PreCompact        []ConfiguredHookGroup `json:"PreCompact"`
	PostCompact       []ConfiguredHookGroup `json:"PostCompact"`
	SessionStart      []ConfiguredHookGroup `json:"SessionStart"`
	SessionEnd        []ConfiguredHookGroup `json:"SessionEnd"`
	UserPromptSubmit  []ConfiguredHookGroup `json:"UserPromptSubmit"`
	SubagentStart     []ConfiguredHookGroup `json:"SubagentStart"`
	SubagentStop      []ConfiguredHookGroup `json:"SubagentStop"`
	Stop              []ConfiguredHookGroup `json:"Stop"`
}

func (r *ManagedHooksRequirements) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ManagedDir        *string               `json:"managedDir"`
		WindowsManagedDir *string               `json:"windowsManagedDir"`
		PreToolUse        []ConfiguredHookGroup `json:"PreToolUse"`
		PermissionRequest []ConfiguredHookGroup `json:"PermissionRequest"`
		PostToolUse       []ConfiguredHookGroup `json:"PostToolUse"`
		PreCompact        []ConfiguredHookGroup `json:"PreCompact"`
		PostCompact       []ConfiguredHookGroup `json:"PostCompact"`
		SessionStart      []ConfiguredHookGroup `json:"SessionStart"`
		SessionEnd        []ConfiguredHookGroup `json:"SessionEnd"`
		UserPromptSubmit  []ConfiguredHookGroup `json:"UserPromptSubmit"`
		SubagentStart     []ConfiguredHookGroup `json:"SubagentStart"`
		SubagentStop      []ConfiguredHookGroup `json:"SubagentStop"`
		Stop              []ConfiguredHookGroup `json:"Stop"`
	}{
		ManagedDir:        cloneStringPtr(r.ManagedDir),
		WindowsManagedDir: cloneStringPtr(r.WindowsManagedDir),
		PreToolUse:        hookGroupsForJSON(r.PreToolUse),
		PermissionRequest: hookGroupsForJSON(r.PermissionRequest),
		PostToolUse:       hookGroupsForJSON(r.PostToolUse),
		PreCompact:        hookGroupsForJSON(r.PreCompact),
		PostCompact:       hookGroupsForJSON(r.PostCompact),
		SessionStart:      hookGroupsForJSON(r.SessionStart),
		SessionEnd:        hookGroupsForJSON(r.SessionEnd),
		UserPromptSubmit:  hookGroupsForJSON(r.UserPromptSubmit),
		SubagentStart:     hookGroupsForJSON(r.SubagentStart),
		SubagentStop:      hookGroupsForJSON(r.SubagentStop),
		Stop:              hookGroupsForJSON(r.Stop),
	})
}

type ConfiguredHookGroup struct {
	Matcher *string                 `json:"matcher"`
	Hooks   []ConfiguredHookHandler `json:"hooks"`
}

type ConfiguredHookHandler struct {
	Type           string         `json:"type"`
	Command        string         `json:"command,omitempty"`
	CommandWindows *string        `json:"commandWindows,omitempty"`
	Server         string         `json:"server,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	Input          map[string]any `json:"input,omitempty"`
	TimeoutSec     *uint64        `json:"timeoutSec,omitempty"`
	Async          bool           `json:"async,omitempty"`
	StatusMessage  *string        `json:"statusMessage,omitempty"`
}

func (h *ConfiguredHookHandler) MarshalJSON() ([]byte, error) {
	switch h.Type {
	case "command":
		return json.Marshal(struct {
			Type           string  `json:"type"`
			Command        string  `json:"command"`
			CommandWindows *string `json:"commandWindows"`
			TimeoutSec     *uint64 `json:"timeoutSec"`
			Async          bool    `json:"async"`
			StatusMessage  *string `json:"statusMessage"`
		}{
			Type:           h.Type,
			Command:        h.Command,
			CommandWindows: cloneStringPtr(h.CommandWindows),
			TimeoutSec:     cloneUint64Ptr(h.TimeoutSec),
			Async:          h.Async,
			StatusMessage:  cloneStringPtr(h.StatusMessage),
		})
	case "prompt", "agent":
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: h.Type})
	case "mcp_tool":
		return json.Marshal(struct {
			Type          string         `json:"type"`
			Server        string         `json:"server"`
			Tool          string         `json:"tool"`
			Input         map[string]any `json:"input"`
			TimeoutSec    *uint64        `json:"timeoutSec"`
			StatusMessage *string        `json:"statusMessage"`
		}{
			Type: h.Type, Server: h.Server, Tool: h.Tool, Input: cloneMap(h.Input),
			TimeoutSec: cloneUint64Ptr(h.TimeoutSec), StatusMessage: cloneStringPtr(h.StatusMessage),
		})
	default:
		type configuredHookHandlerAlias ConfiguredHookHandler
		return json.Marshal((*configuredHookHandlerAlias)(h))
	}
}

type MigrationItemType string

const (
	MigrationAgentsMD        MigrationItemType = "AGENTS_MD"
	MigrationConfig          MigrationItemType = "CONFIG"
	MigrationSkills          MigrationItemType = "SKILLS"
	MigrationPlugins         MigrationItemType = "PLUGINS"
	MigrationMCPServerConfig MigrationItemType = "MCP_SERVER_CONFIG"
	MigrationSubagents       MigrationItemType = "SUBAGENTS"
	MigrationHooks           MigrationItemType = "HOOKS"
	MigrationCommands        MigrationItemType = "COMMANDS"
	MigrationMemory          MigrationItemType = "MEMORY"
	MigrationSessions        MigrationItemType = "SESSIONS"
)

type MemoryFileMigration struct {
	ProjectKey    string  `json:"projectKey"`
	CWD           *string `json:"cwd"`
	SourcePath    string  `json:"sourcePath"`
	SourceFile    string  `json:"sourceFile"`
	ContentSHA256 string  `json:"contentSha256"`
}

type MigrationDetails struct {
	Plugins     []PluginMigration     `json:"plugins"`
	Skills      []NamedMigration      `json:"skills"`
	Sessions    []SessionMigration    `json:"sessions"`
	MCPServers  []NamedMigration      `json:"mcpServers"`
	Hooks       []NamedMigration      `json:"hooks"`
	Subagents   []NamedMigration      `json:"subagents"`
	Commands    []NamedMigration      `json:"commands"`
	MemoryFiles []MemoryFileMigration `json:"memoryFiles"`
}

func (d *MigrationDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Plugins     []PluginMigration     `json:"plugins"`
		Skills      []NamedMigration      `json:"skills"`
		Sessions    []SessionMigration    `json:"sessions"`
		MCPServers  []NamedMigration      `json:"mcpServers"`
		Hooks       []NamedMigration      `json:"hooks"`
		Subagents   []NamedMigration      `json:"subagents"`
		Commands    []NamedMigration      `json:"commands"`
		MemoryFiles []MemoryFileMigration `json:"memoryFiles"`
	}{
		Plugins:     pluginMigrationsForJSON(d.Plugins),
		Skills:      namedMigrationsForJSON(d.Skills),
		Sessions:    sessionMigrationsForJSON(d.Sessions),
		MCPServers:  namedMigrationsForJSON(d.MCPServers),
		Hooks:       namedMigrationsForJSON(d.Hooks),
		Subagents:   namedMigrationsForJSON(d.Subagents),
		Commands:    namedMigrationsForJSON(d.Commands),
		MemoryFiles: memoryFileMigrationsForJSON(d.MemoryFiles),
	})
}

type PluginMigration struct {
	MarketplaceName string   `json:"marketplaceName"`
	PluginNames     []string `json:"pluginNames"`
}

type NamedMigration struct {
	Name string `json:"name"`
}

type SessionMigration struct {
	Path  string  `json:"path"`
	CWD   string  `json:"cwd"`
	Title *string `json:"title"`
}

type ExternalAgentConfigMigrationItem struct {
	ItemType    MigrationItemType `json:"itemType"`
	Description string            `json:"description"`
	CWD         *string           `json:"cwd"`
	Details     *MigrationDetails `json:"details"`
}

type ExternalAgentConfigDetectParams struct {
	IncludeHome       bool     `json:"includeHome,omitempty"`
	CWDs              []string `json:"cwds,omitempty"`
	MaxSessionAgeDays *uint32  `json:"maxSessionAgeDays,omitempty"`
	MaxSessions       *uint32  `json:"maxSessions,omitempty"`
	Source            *string  `json:"source,omitempty"`
	MigrationSource   *string  `json:"migrationSource,omitempty"`
}

type ExternalAgentConfigDetectResponse struct {
	Items      []ExternalAgentConfigMigrationItem        `json:"items"`
	Connectors []ExternalAgentDetectedConnectorCandidate `json:"connectors"`
}

type ExternalAgentDetectedConnectorSource string

const (
	ExternalAgentConnectorRemoteMCPServersConfig ExternalAgentDetectedConnectorSource = "remoteMcpServersConfig"
	ExternalAgentConnectorSessionToolUse         ExternalAgentDetectedConnectorSource = "sessionToolUse"
)

type ExternalAgentDetectedConnectorCandidate struct {
	Name         string                               `json:"name"`
	SessionCount uint32                               `json:"sessionCount"`
	Source       ExternalAgentDetectedConnectorSource `json:"source"`
}

func (r *ExternalAgentConfigDetectResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Items      []ExternalAgentConfigMigrationItem        `json:"items"`
		Connectors []ExternalAgentDetectedConnectorCandidate `json:"connectors"`
	}{
		Items:      migrationItemsForJSON(r.Items),
		Connectors: detectedConnectorCandidatesForJSON(r.Connectors),
	})
}

func (r *ExternalAgentConfigDetectResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		Items      []ExternalAgentConfigMigrationItem        `json:"items"`
		Connectors []ExternalAgentDetectedConnectorCandidate `json:"connectors"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.Items = migrationItemsForJSON(wire.Items)
	r.Connectors = detectedConnectorCandidatesForJSON(wire.Connectors)
	return nil
}

type ExternalAgentConfigImportParams struct {
	MigrationItems  []ExternalAgentConfigMigrationItem `json:"migrationItems"`
	Source          *string                            `json:"source"`
	ProviderID      *string                            `json:"providerId"`
	MigrationSource *string                            `json:"migrationSource,omitempty"`
}

type ExternalAgentConfigImportResponse struct {
	ImportID string `json:"importId"`
}

type ExternalAgentConfigImportItemTypeSuccess struct {
	ItemType MigrationItemType `json:"itemType"`
	CWD      *string           `json:"cwd"`
	Source   *string           `json:"source"`
	Target   *string           `json:"target"`
	Title    *string           `json:"title"`
}

type ExternalAgentConfigImportItemTypeFailure struct {
	ItemType     MigrationItemType `json:"itemType"`
	ErrorType    *string           `json:"errorType"`
	SubErrorType *string           `json:"subErrorType"`
	FailureStage string            `json:"failureStage"`
	Message      string            `json:"message"`
	CWD          *string           `json:"cwd"`
	Source       *string           `json:"source"`
}

type ExternalAgentConfigImportTypeResult struct {
	ItemType  MigrationItemType                          `json:"itemType"`
	Successes []ExternalAgentConfigImportItemTypeSuccess `json:"successes"`
	Failures  []ExternalAgentConfigImportItemTypeFailure `json:"failures"`
}

func (r *ExternalAgentConfigImportTypeResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ItemType  MigrationItemType                          `json:"itemType"`
		Successes []ExternalAgentConfigImportItemTypeSuccess `json:"successes"`
		Failures  []ExternalAgentConfigImportItemTypeFailure `json:"failures"`
	}{
		ItemType:  r.ItemType,
		Successes: importSuccessesForJSON(r.Successes),
		Failures:  importFailuresForJSON(r.Failures),
	})
}

type ExternalAgentConfigImportHistoryRecordParams struct {
	ProviderID      string                                                   `json:"providerId"`
	ItemTypeResults []ExternalAgentConfigImportHistoryRecordTypeResultParams `json:"itemTypeResults"`
}

type ExternalAgentConfigImportHistoryRecordSuccessParams struct {
	ItemType MigrationItemType `json:"itemType"`
	CWD      *string           `json:"cwd"`
	Source   *string           `json:"source"`
	Target   *string           `json:"target"`
	Title    *string           `json:"title"`
}

type ExternalAgentConfigImportHistoryRecordTypeResultParams struct {
	ItemType  MigrationItemType                                     `json:"itemType"`
	Successes []ExternalAgentConfigImportHistoryRecordSuccessParams `json:"successes"`
	Failures  []ExternalAgentConfigImportItemTypeFailure            `json:"failures"`
}

type ExternalAgentConfigImportHistoryRecordResponse struct {
	ImportID string `json:"importId"`
}

type ExternalAgentConfigImportHistory struct {
	ImportID      string                                     `json:"importId"`
	ProviderID    *string                                    `json:"providerId"`
	CompletedAtMS int64                                      `json:"completedAtMs"`
	Successes     []ExternalAgentConfigImportItemTypeSuccess `json:"successes"`
	Failures      []ExternalAgentConfigImportItemTypeFailure `json:"failures"`
}

func (h *ExternalAgentConfigImportHistory) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ImportID      string                                     `json:"importId"`
		ProviderID    *string                                    `json:"providerId"`
		CompletedAtMS int64                                      `json:"completedAtMs"`
		Successes     []ExternalAgentConfigImportItemTypeSuccess `json:"successes"`
		Failures      []ExternalAgentConfigImportItemTypeFailure `json:"failures"`
	}{
		ImportID:      h.ImportID,
		ProviderID:    cloneStringPtr(h.ProviderID),
		CompletedAtMS: h.CompletedAtMS,
		Successes:     importSuccessesForJSON(h.Successes),
		Failures:      importFailuresForJSON(h.Failures),
	})
}

type ExternalAgentConfigImportHistoriesReadResponse struct {
	Data []ExternalAgentConfigImportHistory `json:"data"`
}

func (r *ExternalAgentConfigImportHistoriesReadResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Data []ExternalAgentConfigImportHistory `json:"data"`
	}{
		Data: importHistoriesForJSON(r.Data),
	})
}

type ExternalAgentConfigImportProgressNotification struct {
	ImportID        string                                `json:"importId"`
	ItemTypeResults []ExternalAgentConfigImportTypeResult `json:"itemTypeResults"`
}

func (n *ExternalAgentConfigImportProgressNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ImportID        string                                `json:"importId"`
		ItemTypeResults []ExternalAgentConfigImportTypeResult `json:"itemTypeResults"`
	}{
		ImportID:        n.ImportID,
		ItemTypeResults: importTypeResultsForJSON(n.ItemTypeResults),
	})
}

type ExternalAgentConfigImportCompletedNotification struct {
	ImportID        string                                `json:"importId"`
	ItemTypeResults []ExternalAgentConfigImportTypeResult `json:"itemTypeResults"`
}

func (n *ExternalAgentConfigImportCompletedNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ImportID        string                                `json:"importId"`
		ItemTypeResults []ExternalAgentConfigImportTypeResult `json:"itemTypeResults"`
	}{
		ImportID:        n.ImportID,
		ItemTypeResults: importTypeResultsForJSON(n.ItemTypeResults),
	})
}

type ExternalAgentConfigImportOutcome struct {
	ItemResults          []ExternalAgentConfigImportTypeResult
	PendingSessionItems  []ExternalAgentConfigMigrationItem
	PendingPluginImports []ExternalAgentConfigMigrationItem
}

type TextPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type TextRange struct {
	Start TextPosition `json:"start"`
	End   TextPosition `json:"end"`
}

type ConfigWarningNotification struct {
	Summary string     `json:"summary"`
	Details *string    `json:"details"`
	Path    *string    `json:"path,omitempty"`
	Range   *TextRange `json:"range,omitempty"`
}

func (n *ConfigWarningNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Summary string     `json:"summary"`
		Details *string    `json:"details"`
		Path    *string    `json:"path,omitempty"`
		Range   *TextRange `json:"range,omitempty"`
	}{
		Summary: n.Summary,
		Details: cloneStringPtr(n.Details),
		Path:    cloneStringPtr(n.Path),
		Range:   cloneTextRangePtr(n.Range),
	})
}

func NewMigrationDetails() *MigrationDetails {
	return &MigrationDetails{
		Plugins:     []PluginMigration{},
		Skills:      []NamedMigration{},
		Sessions:    []SessionMigration{},
		MCPServers:  []NamedMigration{},
		Hooks:       []NamedMigration{},
		Subagents:   []NamedMigration{},
		Commands:    []NamedMigration{},
		MemoryFiles: []MemoryFileMigration{},
	}
}

func MigrationItemsNeedRuntimeRefresh(items []ExternalAgentConfigMigrationItem) bool {
	for i := range items {
		switch items[i].ItemType {
		case MigrationConfig, MigrationSkills, MigrationMCPServerConfig, MigrationHooks, MigrationCommands, MigrationPlugins:
			return true
		}
	}
	return false
}

func ExternalAgentMigrationItemOrder(itemType MigrationItemType) int {
	switch itemType {
	case MigrationConfig:
		return 0
	case MigrationSkills:
		return 1
	case MigrationAgentsMD:
		return 2
	case MigrationPlugins:
		return 3
	case MigrationMCPServerConfig:
		return 4
	case MigrationSubagents:
		return 5
	case MigrationHooks:
		return 6
	case MigrationCommands:
		return 7
	case MigrationMemory:
		return 8
	case MigrationSessions:
		return 9
	default:
		return 100
	}
}

func CloneMigrationDetails(details *MigrationDetails) *MigrationDetails {
	if details == nil {
		return nil
	}
	out := NewMigrationDetails()
	out.Plugins = make([]PluginMigration, len(details.Plugins))
	for i := range details.Plugins {
		out.Plugins[i] = PluginMigration{
			MarketplaceName: details.Plugins[i].MarketplaceName,
			PluginNames:     append([]string(nil), details.Plugins[i].PluginNames...),
		}
	}
	out.Skills = cloneNamedMigrations(details.Skills)
	out.Sessions = make([]SessionMigration, len(details.Sessions))
	for i := range details.Sessions {
		out.Sessions[i] = SessionMigration{
			Path:  details.Sessions[i].Path,
			CWD:   details.Sessions[i].CWD,
			Title: cloneStringPtr(details.Sessions[i].Title),
		}
	}
	out.MCPServers = cloneNamedMigrations(details.MCPServers)
	out.Hooks = cloneNamedMigrations(details.Hooks)
	out.Subagents = cloneNamedMigrations(details.Subagents)
	out.Commands = cloneNamedMigrations(details.Commands)
	out.MemoryFiles = cloneMemoryFileMigrations(details.MemoryFiles)
	return out
}

func CloneExternalAgentMigrationItem(item *ExternalAgentConfigMigrationItem) ExternalAgentConfigMigrationItem {
	if item == nil {
		return ExternalAgentConfigMigrationItem{}
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    item.ItemType,
		Description: item.Description,
		CWD:         cloneStringPtr(item.CWD),
		Details:     CloneMigrationDetails(item.Details),
	}
}

func BuildExternalAgentImportResult(importID string, results []ExternalAgentConfigImportTypeResult) *ExternalAgentConfigImportCompletedNotification {
	merged := map[MigrationItemType]*ExternalAgentConfigImportTypeResult{}
	for i := range results {
		result := results[i]
		slot := merged[result.ItemType]
		if slot == nil {
			slot = &ExternalAgentConfigImportTypeResult{ItemType: result.ItemType}
			merged[result.ItemType] = slot
		}
		slot.Successes = append(slot.Successes, cloneImportSuccesses(result.Successes)...)
		slot.Failures = append(slot.Failures, cloneImportFailures(result.Failures)...)
	}
	out := make([]ExternalAgentConfigImportTypeResult, 0, len(merged))
	for _, result := range merged {
		out = append(out, *result)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		left := ExternalAgentMigrationItemOrder(out[i].ItemType)
		right := ExternalAgentMigrationItemOrder(out[j].ItemType)
		if left != right {
			return left < right
		}
		return out[i].ItemType < out[j].ItemType
	})
	return &ExternalAgentConfigImportCompletedNotification{
		ImportID:        importID,
		ItemTypeResults: out,
	}
}

func ValidatePendingSessionImports(items []ExternalAgentConfigMigrationItem) (selected []SessionMigration, result *ExternalAgentConfigImportTypeResult) {
	result = &ExternalAgentConfigImportTypeResult{ItemType: MigrationSessions}
	seen := map[string]bool{}
	for i := range items {
		if items[i].ItemType != MigrationSessions || items[i].Details == nil {
			continue
		}
		for _, session := range items[i].Details.Sessions {
			path := strings.TrimSpace(session.Path)
			if path == "" {
				result.Failures = append(result.Failures, ExternalAgentConfigImportItemTypeFailure{
					ItemType:     MigrationSessions,
					SubErrorType: stringPtrIfNotEmpty("session_not_detected"),
					FailureStage: "session_missing",
					Message:      "external agent session path is required",
					CWD:          cloneStringPtr(items[i].CWD),
				})
				continue
			}
			if seen[path] {
				continue
			}
			seen[path] = true
			selected = append(selected, SessionMigration{Path: path, CWD: session.CWD, Title: cloneStringPtr(session.Title)})
		}
	}
	if len(selected) == 0 && len(result.Failures) == 0 {
		return nil, nil
	}
	return selected, result
}

func ExternalAgentImportSuccessesForItem(item *ExternalAgentConfigMigrationItem, source *string) []ExternalAgentConfigImportItemTypeSuccess {
	if item == nil {
		return nil
	}
	makeSuccess := func(itemSource *string, target *string) ExternalAgentConfigImportItemTypeSuccess {
		if itemSource == nil {
			itemSource = source
		}
		return ExternalAgentConfigImportItemTypeSuccess{
			ItemType: item.ItemType,
			CWD:      cloneStringPtr(item.CWD),
			Source:   cloneStringPtr(itemSource),
			Target:   cloneStringPtr(target),
		}
	}
	if item.Details == nil {
		return []ExternalAgentConfigImportItemTypeSuccess{
			makeSuccess(source, stringPtrIfNotEmpty(strings.ToLower(string(item.ItemType)))),
		}
	}
	var successes []ExternalAgentConfigImportItemTypeSuccess
	switch item.ItemType {
	case MigrationPlugins:
		for _, pluginGroup := range item.Details.Plugins {
			for _, pluginName := range pluginGroup.PluginNames {
				pluginName = strings.TrimSpace(pluginName)
				if pluginName == "" {
					continue
				}
				sourceValue := strings.TrimSpace(pluginGroup.MarketplaceName)
				if sourceValue != "" {
					sourceValue += "/" + pluginName
				}
				successes = append(successes, makeSuccess(stringPtrIfNotEmpty(sourceValue), stringPtrIfNotEmpty(pluginName)))
			}
		}
	case MigrationSkills:
		for _, skill := range item.Details.Skills {
			successes = append(successes, makeSuccess(stringPtrIfNotEmpty(skill.Name), stringPtrIfNotEmpty(skill.Name)))
		}
	case MigrationSessions:
		for _, session := range item.Details.Sessions {
			if strings.TrimSpace(session.Path) == "" {
				continue
			}
			target := strings.TrimSpace(session.Path)
			if session.Title != nil && strings.TrimSpace(*session.Title) != "" {
				target = strings.TrimSpace(*session.Title)
			}
			successes = append(successes, makeSuccess(stringPtrIfNotEmpty(session.Path), stringPtrIfNotEmpty(target)))
		}
	case MigrationMCPServerConfig:
		for _, server := range item.Details.MCPServers {
			successes = append(successes, makeSuccess(stringPtrIfNotEmpty(server.Name), stringPtrIfNotEmpty(server.Name)))
		}
	case MigrationHooks:
		for _, hook := range item.Details.Hooks {
			successes = append(successes, makeSuccess(stringPtrIfNotEmpty(hook.Name), stringPtrIfNotEmpty(hook.Name)))
		}
	case MigrationSubagents:
		for _, subagent := range item.Details.Subagents {
			successes = append(successes, makeSuccess(stringPtrIfNotEmpty(subagent.Name), stringPtrIfNotEmpty(subagent.Name)))
		}
	case MigrationCommands:
		for _, command := range item.Details.Commands {
			successes = append(successes, makeSuccess(stringPtrIfNotEmpty(command.Name), stringPtrIfNotEmpty(command.Name)))
		}
	}
	if len(successes) == 0 && !migrationDetailsHasEntries(item) {
		successes = append(successes, makeSuccess(source, stringPtrIfNotEmpty(strings.ToLower(string(item.ItemType)))))
	}
	return successes
}

func migrationDetailsHasEntries(item *ExternalAgentConfigMigrationItem) bool {
	if item == nil || item.Details == nil {
		return false
	}
	switch item.ItemType {
	case MigrationPlugins:
		return len(item.Details.Plugins) > 0
	case MigrationSkills:
		return len(item.Details.Skills) > 0
	case MigrationSessions:
		return len(item.Details.Sessions) > 0
	case MigrationMCPServerConfig:
		return len(item.Details.MCPServers) > 0
	case MigrationHooks:
		return len(item.Details.Hooks) > 0
	case MigrationSubagents:
		return len(item.Details.Subagents) > 0
	case MigrationCommands:
		return len(item.Details.Commands) > 0
	case MigrationMemory:
		return len(item.Details.MemoryFiles) > 0
	default:
		return false
	}
}

type ConfigService struct {
	mu                    sync.Mutex
	codexHome             string
	profile               string
	userConfig            string
	packagedDefaultsLayer *Layer
	requirements          *ConfigRequirements
	requirementsOverride  bool
	warnings              []ConfigWarningNotification
	managedLayers         []Layer
	featureDefaults       map[string]bool
	importHistory         []ExternalAgentConfigImportHistory
	nextImportID          int
	externalAgentHome     string
	now                   func() time.Time
}

func NewConfigService(codexHome string) *ConfigService {
	service := &ConfigService{codexHome: codexHome, externalAgentHome: defaultExternalAgentHome(), now: time.Now}
	service.loadRequirementsFromHome()
	service.loadManagedConfigLayerFromEnv()
	return service
}

func (s *ConfigService) SetExternalAgentHome(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.externalAgentHome = strings.TrimSpace(path)
}

func NewProfileConfigService(codexHome string, profile string) *ConfigService {
	service := NewConfigService(codexHome)
	service.SetProfile(profile)
	return service
}

func (s *ConfigService) CodexHome() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codexHome
}

func (s *ConfigService) SetProfile(profile string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profile = strings.TrimSpace(profile)
	s.userConfig = ""
}

func (s *ConfigService) SetUserConfigPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userConfig = strings.TrimSpace(path)
}

func (s *ConfigService) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		s.now = time.Now
		return
	}
	s.now = clock
}

func (s *ConfigService) SetRequirements(requirements *ConfigRequirements) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requirements = cloneRequirements(requirements)
	s.requirementsOverride = true
}

// SetRequirementsIfDifferentFromLoaded preserves file-backed requirements
// when the caller passes the same startup snapshot, while still treating a
// genuinely different value as an explicit override.
func (s *ConfigService) SetRequirementsIfDifferentFromLoaded(requirements *ConfigRequirements) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reflect.DeepEqual(s.requirements, requirements) {
		return
	}
	s.requirements = cloneRequirements(requirements)
	s.requirementsOverride = true
}

func (s *ConfigService) ReloadRequirementsFromHome() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.requirementsOverride {
		s.mu.Unlock()
		return nil
	}
	home := s.codexHome
	s.mu.Unlock()
	requirements, err := LoadRequirementsFile(filepath.Join(home, "requirements.toml"))
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.requirements = cloneRequirements(requirements)
	s.mu.Unlock()
	return nil
}

func (s *ConfigService) loadRequirementsFromHome() {
	if s == nil || strings.TrimSpace(s.codexHome) == "" {
		return
	}
	path := filepath.Join(s.codexHome, "requirements.toml")
	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		details := err.Error()
		s.warnings = append(s.warnings, ConfigWarningNotification{
			Summary: "Invalid managed requirements; ignoring.",
			Details: &details,
			Path:    &path,
		})
		return
	}
	s.requirements = requirements
}

func (s *ConfigService) loadManagedConfigLayerFromEnv() {
	if s == nil || strings.TrimSpace(s.codexHome) == "" {
		return
	}
	override, ok := os.LookupEnv(appServerManagedConfigPathEnv)
	if !ok || strings.TrimSpace(override) == "" {
		// Rust #38947: on Windows the default CODEX_HOME/managed_config.toml is
		// ignored. When the deprecated file still exists, surface the startup
		// warning directing users to the supported locations.
		if shouldIgnoreDefaultManagedConfig("") {
			deprecated := managedConfigPath(s.codexHome, "")
			if pathExists(deprecated) {
				details := fmt.Sprintf(
					"Ignoring deprecated managed config file at %s; CODEX_HOME/managed_config.toml is no longer supported on Windows. Use %%ProgramData%%\\OpenAI\\Codex\\requirements.toml for enforced settings or config.toml for defaults.",
					deprecated,
				)
				s.warnings = append(s.warnings, ConfigWarningNotification{
					Summary: "Ignoring deprecated managed config file.",
					Details: &details,
					Path:    &deprecated,
				})
			}
		}
		return
	}
	path := managedConfigPath(s.codexHome, override)
	values, exists, err := loadConfigFileIfExists(path)
	if err != nil {
		details := err.Error()
		s.warnings = append(s.warnings, ConfigWarningNotification{
			Summary: "Invalid managed configuration; ignoring.",
			Details: &details,
			Path:    &path,
		})
		return
	}
	if !exists {
		return
	}
	s.managedLayers = append(s.managedLayers, Layer{
		Name:    LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: path},
		Version: configVersion(values),
		Config:  cloneMap(values),
	})
}

func (s *ConfigService) SetWarnings(warnings []ConfigWarningNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings = cloneConfigWarnings(warnings)
}

func (s *ConfigService) Warnings() []ConfigWarningNotification {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneConfigWarnings(s.warnings)
}

func (s *ConfigService) SetManagedLayers(layers []Layer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.managedLayers = cloneLayers(layers)
}

// SetPackagedDefaultsLayer installs an optional package-supplied configuration
// file as the lowest-precedence configuration layer. A configured path that is
// missing on disk is an error (mirrors the Rust loader behavior).
func (s *ConfigService) SetPackagedDefaultsLayer(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	values, exists, err := loadConfigFileIfExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("packaged defaults config file %s not found", path)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packagedDefaultsLayer = &Layer{
		Name:    LayerSource{Type: LayerSourcePackagedDefaults, File: path},
		Version: configVersion(values),
		Config:  cloneMap(values),
	}
	return nil
}

func (s *ConfigService) SetFeatureEnablementDefaults(enablement map[string]bool) {
	if s == nil || len(enablement) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.featureDefaults == nil {
		s.featureDefaults = map[string]bool{}
	}
	for key, enabled := range enablement {
		key = strings.TrimSpace(key)
		if key != "" {
			s.featureDefaults[key] = enabled
		}
	}
}

func (s *ConfigService) Read(params *ConfigReadParams) (*ConfigReadResponse, error) {
	if params == nil {
		params = &ConfigReadParams{}
	}
	profile := s.currentProfile()
	if cwd := configReadCWD(params); cwd != "" {
		layers, err := s.readLayersForCWD(cwd, profile)
		if err != nil {
			return nil, err
		}
		values, origins := mergeConfigLayers(layers)
		s.applyFeatureEnablementDefaults(values, origins)
		applySupportedFeatureEnablement(values)
		response := &ConfigReadResponse{Config: values, Origins: origins}
		if params.IncludeLayers {
			response.Layers = cloneLayers(rpcLayers(layers))
		}
		return response, nil
	}
	userConfigPath, err := s.currentUserConfigPath()
	if err != nil {
		return nil, err
	}
	userValues, err := loadConfigFile(ConfigPath(s.codexHome))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(profile) != "" {
		if err := applyProfileLayer(s.codexHome, userValues, profile); err != nil {
			return nil, err
		}
	}
	userLayer := Layer{
		Name:    LayerSource{Type: LayerSourceUser, File: userConfigPath, Profile: stringPtrIfNotEmpty(profile)},
		Version: configVersion(userValues),
		Config:  cloneMap(userValues),
	}
	layers := s.readLayers(userLayer)
	values, origins := mergeConfigLayers(layers)
	s.applyFeatureEnablementDefaults(values, origins)
	applySupportedFeatureEnablement(values)
	response := &ConfigReadResponse{Config: values, Origins: origins}
	if params.IncludeLayers {
		response.Layers = cloneLayers(rpcLayers(layers))
	}
	return response, nil
}

func (s *ConfigService) applyFeatureEnablementDefaults(values map[string]any, origins map[string]LayerMetadata) {
	if s == nil || values == nil {
		return
	}
	s.mu.Lock()
	defaults := cloneBoolMap(s.featureDefaults)
	s.mu.Unlock()
	if len(defaults) == 0 {
		return
	}
	featuresValue, ok := values["features"].(map[string]any)
	if !ok {
		featuresValue = map[string]any{}
		values["features"] = featuresValue
	}
	origin := LayerMetadata{Name: LayerSource{Type: LayerSourceSessionFlags}}
	for key, enabled := range defaults {
		if _, exists := featuresValue[key]; exists {
			continue
		}
		featuresValue[key] = enabled
		if origins != nil {
			origins["features."+key] = origin
		}
	}
}

func configReadCWD(params *ConfigReadParams) string {
	if params == nil {
		return ""
	}
	if params.CWD == nil {
		return ""
	}
	return strings.TrimSpace(*params.CWD)
}

func (s *ConfigService) WriteValue(params *ConfigValueWriteParams) (*ConfigWriteResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	return s.BatchWrite(&ConfigBatchWriteParams{
		Edits: []ConfigEdit{{
			KeyPath:       params.KeyPath,
			Value:         params.Value,
			MergeStrategy: params.MergeStrategy,
		}},
		FilePath:        params.FilePath,
		ExpectedVersion: params.ExpectedVersion,
	})
}

func (s *ConfigService) BatchWrite(params *ConfigBatchWriteParams) (*ConfigWriteResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	allowedPath, err := s.currentUserConfigPath()
	if err != nil {
		return nil, err
	}
	path := allowedPath
	if params.FilePath != nil && strings.TrimSpace(*params.FilePath) != "" {
		path = *params.FilePath
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: filePath must be absolute", ErrInvalidConfigRequest)
	}
	if !pathsMatchAfterNormalization(allowedPath, path) {
		return nil, configWriteErrorf(ConfigWriteLayerReadonly, "Only writes to the user config are allowed")
	}
	values, err := loadConfigFile(path)
	if err != nil {
		return nil, err
	}
	values = cloneMap(values)
	currentVersion := configVersion(values)
	if params.ExpectedVersion != nil && *params.ExpectedVersion != currentVersion {
		return nil, configWriteErrorf(ConfigWriteVersionConflict, "Configuration was modified since last read. Fetch latest version and retry.")
	}
	for i := range params.Edits {
		if err := s.rejectManagedAuthWrite(params.Edits[i].KeyPath); err != nil {
			return nil, err
		}
		if err := validateWritableKeyPath(params.Edits[i].KeyPath, params.Edits[i].Value); err != nil {
			return nil, err
		}
		applyEdit(values, &params.Edits[i])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(renderTOML(values)), 0o600); err != nil {
		return nil, err
	}
	response := &ConfigWriteResponse{
		Status:   WriteOK,
		Version:  configVersion(values),
		FilePath: path,
	}
	if overridden := s.overriddenMetadataAfterWrite(params.Edits); overridden != nil {
		response.Status = WriteOKOverridden
		response.OverriddenMetadata = overridden
	}
	return response, nil
}

// rejectManagedAuthWrite mirrors Rust config_processor write rejection for
// exact managed requirements (#39043): cli_auth_credentials_store and
// chatgpt_base_url become read-only through config write APIs when a managed
// requirement pins them.
func (s *ConfigService) rejectManagedAuthWrite(keyPath string) error {
	if s == nil || s.requirements == nil {
		return nil
	}
	parts := splitKeyPath(keyPath)
	if len(parts) != 1 {
		return nil
	}
	switch parts[0] {
	case "cli_auth_credentials_store":
		if s.requirements.CliAuthCredentialsStore != nil {
			return configWriteErrorf(ConfigWriteValidation, "cli_auth_credentials_store is managed by requirements and cannot be written")
		}
	case "chatgpt_base_url":
		if s.requirements.ChatgptBaseURL != nil {
			return configWriteErrorf(ConfigWriteValidation, "chatgpt_base_url is managed by requirements and cannot be written")
		}
	}
	return nil
}

func pathsMatchAfterNormalization(expected string, provided string) bool {
	expected = normalizeConfigWritePath(expected)
	provided = normalizeConfigWritePath(provided)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(expected, provided)
	}
	return expected == provided
}

func normalizeConfigWritePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func (s *ConfigService) WriteSkillConfig(params *SkillConfigWriteParams) (*ConfigWriteResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	path, err := s.currentUserConfigPath()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: filePath must be absolute", ErrInvalidConfigRequest)
	}
	values, err := loadConfigFile(path)
	if err != nil {
		return nil, err
	}
	values = cloneMap(values)
	applySkillConfigEdit(values, params)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(renderTOML(values)), 0o600); err != nil {
		return nil, err
	}
	return &ConfigWriteResponse{
		Status:   WriteOK,
		Version:  configVersion(values),
		FilePath: path,
	}, nil
}

func (s *ConfigService) overriddenMetadataAfterWrite(edits []ConfigEdit) *OverriddenMetadata {
	if len(edits) == 0 {
		return nil
	}
	read, err := s.Read(&ConfigReadParams{})
	if err != nil || read == nil {
		return nil
	}
	userPrecedence := int16(20)
	if profile := s.currentProfile(); profile != "" {
		userPrecedence = 21
	}
	for i := range edits {
		keyPath := strings.TrimSpace(edits[i].KeyPath)
		effective, ok := valueAtPath(read.Config, keyPath)
		if !ok {
			continue
		}
		if valuesEqual(effective, edits[i].Value) {
			continue
		}
		origin := read.Origins[keyPath]
		// Only report an override when the effective layer has strictly higher
		// precedence than the user layer (Rust #38179): clearing a user value
		// falls back to the packaged default without a false override, and a
		// value the user set themselves is not "overridden".
		if origin.Name.Precedence() <= userPrecedence {
			continue
		}
		return &OverriddenMetadata{
			Message:         fmt.Sprintf("%s was written but is overridden by a higher-priority config layer", keyPath),
			OverridingLayer: origin,
			EffectiveValue:  effective,
		}
	}
	return nil
}

func (s *ConfigService) readLayers(userLayer Layer) []Layer {
	managed := cloneLayers(s.managedLayers)
	packagedDefaults := s.packagedDefaultsLayerForRead()
	layers := make([]Layer, 0, 1+len(managed))
	if packagedDefaults != nil {
		layers = append(layers, *packagedDefaults)
	}
	layers = append(layers, userLayer)
	layers = append(layers, managed...)
	sort.SliceStable(layers, func(i int, j int) bool {
		return layers[i].Name.Precedence() < layers[j].Name.Precedence()
	})
	return layers
}

func (s *ConfigService) readLayersForCWD(cwd string, profile string) ([]Layer, error) {
	userPath := ConfigPath(s.codexHome)
	userValues, err := loadConfigFile(userPath)
	if err != nil {
		return nil, err
	}
	packagedDefaults := s.packagedDefaultsLayerForRead()
	layers := []Layer{}
	if packagedDefaults != nil {
		layers = append(layers, *packagedDefaults)
	}
	layers = append(layers, Layer{
		Name:    LayerSource{Type: LayerSourceUser, File: userPath},
		Version: configVersion(userValues),
		Config:  cloneMap(userValues),
	})
	if ProjectConfigEnabled(userValues, cwd) {
		for _, dotCodexFolder := range ProjectDotCodexFolders(cwd) {
			path := filepath.Join(dotCodexFolder, "config.toml")
			projectValues, exists, err := loadConfigFileIfExists(path)
			if err != nil {
				return nil, err
			}
			if exists {
				resolveProjectRelativeConfigValues(projectValues, dotCodexFolder)
				sanitizeProjectConfigValues(projectValues)
			}
			layers = append(layers, Layer{
				Name: LayerSource{
					Type:                LayerSourceProject,
					File:                path,
					DotCodexFolder:      dotCodexFolder,
					HooksDotCodexFolder: ProjectHooksDotCodexFolder(cwd, dotCodexFolder),
				},
				Version: configVersion(projectValues),
				Config:  cloneMap(projectValues),
			})
		}
	}
	if strings.TrimSpace(profile) != "" {
		profileLayer, err := s.profileLayer(profile)
		if err != nil {
			return nil, err
		}
		if profileLayer != nil {
			layers = append(layers, *profileLayer)
		}
	}
	layers = append(layers, s.managedLayersForRead()...)
	return layers, nil
}

// packagedDefaultsLayerForRead returns the explicit packaged-defaults layer
// when one was installed via SetPackagedDefaultsLayer; otherwise it returns
// the embedded packaged defaults, which are always installed as the
// lowest-precedence layer (Rust #38179).
func (s *ConfigService) packagedDefaultsLayerForRead() *Layer {
	s.mu.Lock()
	explicit := cloneLayerPtr(s.packagedDefaultsLayer)
	s.mu.Unlock()
	if explicit != nil {
		return explicit
	}
	values, err := embeddedDefaultsValues()
	if err != nil {
		return nil
	}
	file := ""
	if exe, err := os.Executable(); err == nil {
		file = exe
	}
	return &Layer{
		Name:    LayerSource{Type: LayerSourcePackagedDefaults, File: file},
		Version: configVersion(values),
		Config:  cloneMap(values),
	}
}

// rpcLayers filters the packaged-defaults layer out of the config RPC layer
// list (Rust #38179): packaged defaults contribute to the effective config but
// are not surfaced as a layer or origin.
func rpcLayers(layers []Layer) []Layer {
	out := make([]Layer, 0, len(layers))
	for i := range layers {
		if layers[i].Name.Type == LayerSourcePackagedDefaults {
			continue
		}
		out = append(out, layers[i])
	}
	return out
}

func (s *ConfigService) profileLayer(profile string) (*Layer, error) {
	profilePath, err := ResolveProfileConfigPath(s.codexHome, profile)
	if err != nil {
		return nil, err
	}
	profileValues, exists, err := loadConfigFileIfExists(profilePath)
	if err != nil {
		return nil, err
	}
	if !exists {
		userValues, err := loadConfigFile(ConfigPath(s.codexHome))
		if err != nil {
			return nil, err
		}
		legacyValues, ok, err := legacyProfileValues(userValues, profile)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("profile %q not found", profile)
		}
		profileValues = cloneMap(legacyValues)
	}
	return &Layer{
		Name:    LayerSource{Type: LayerSourceUser, File: profilePath, Profile: stringPtrIfNotEmpty(profile)},
		Version: configVersion(profileValues),
		Config:  cloneMap(profileValues),
	}, nil
}

func (s *ConfigService) managedLayersForRead() []Layer {
	s.mu.Lock()
	managed := cloneLayers(s.managedLayers)
	s.mu.Unlock()
	sort.SliceStable(managed, func(i int, j int) bool {
		return managed[i].Name.Precedence() < managed[j].Name.Precedence()
	})
	return managed
}

func mergeConfigLayers(layers []Layer) (map[string]any, map[string]LayerMetadata) {
	values := map[string]any{}
	origins := map[string]LayerMetadata{}
	for i := range layers {
		layerValues, ok := layers[i].Config.(map[string]any)
		if !ok {
			continue
		}
		layerValues = cloneMap(layerValues)
		cloudConfigMergeMap(values, layerValues)
		// Packaged defaults contribute to the effective config but stay out of
		// origin metadata (Rust #38179).
		if layers[i].Name.Type == LayerSourcePackagedDefaults {
			continue
		}
		fillOrigins("", layerValues, LayerMetadata{Name: layers[i].Name, Version: layers[i].Version}, origins)
	}
	return values, origins
}

func cloneLayers(layers []Layer) []Layer {
	if layers == nil {
		return nil
	}
	out := make([]Layer, len(layers))
	for i := range layers {
		out[i] = cloneLayer(&layers[i])
	}
	return out
}

func cloneLayerPtr(layer *Layer) *Layer {
	if layer == nil {
		return nil
	}
	cloned := cloneLayer(layer)
	return &cloned
}

func cloneLayer(layer *Layer) Layer {
	cloned := *layer
	if configMap, ok := layer.Config.(map[string]any); ok {
		cloned.Config = cloneMap(configMap)
	}
	if layer.DisabledReason != nil {
		value := *layer.DisabledReason
		cloned.DisabledReason = &value
	}
	return cloned
}

func (s *ConfigService) currentProfile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.profile
}

func (s *ConfigService) currentUserConfigPath() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userConfig != "" {
		return s.userConfig, nil
	}
	if s.profile != "" {
		path, err := ResolveProfileConfigPath(s.codexHome, s.profile)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	return ConfigPath(s.codexHome), nil
}

func (s *ConfigService) Requirements() *ConfigRequirementsReadResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &ConfigRequirementsReadResponse{Requirements: cloneRequirements(s.requirements)}
}

func (s *ConfigService) DetectExternalAgentConfig(params *ExternalAgentConfigDetectParams) *ExternalAgentConfigDetectResponse {
	if params == nil {
		params = &ExternalAgentConfigDetectParams{}
	}
	migrationSource := normalizeExternalMigrationSource(params.MigrationSource)
	items := make([]ExternalAgentConfigMigrationItem, 0, len(params.CWDs)+1)
	if params.IncludeHome {
		if migrationSource == externalMigrationSourceCursor {
			items = append(items, s.detectExternalCursorMigrations(externalMigrationScope{})...)
		} else {
			items = append(items, s.detectExternalCoreMigrations(externalMigrationScope{})...)
		}
		if item, ok := s.detectExternalSessionMigrationForSource(migrationSource, params); ok {
			items = append(items, item)
		}
	}
	for _, cwd := range params.CWDs {
		root := externalRepoRoot(cwd)
		if root == "" {
			continue
		}
		if migrationSource == externalMigrationSourceCursor {
			items = append(items, s.detectExternalCursorMigrations(externalMigrationScope{repoRoot: root})...)
		} else {
			items = append(items, s.detectExternalCoreMigrations(externalMigrationScope{repoRoot: root})...)
		}
	}
	if params.IncludeHome {
		if migrationSource == externalMigrationSourceClaude {
			if item, ok := s.detectExternalMemoryMigration(params.CWDs); ok {
				items = append(items, item)
			}
		}
	}
	return &ExternalAgentConfigDetectResponse{Items: items}
}

func (s *ConfigService) ImportExternalAgentConfig(params *ExternalAgentConfigImportParams) (*ExternalAgentConfigImportResponse, *ExternalAgentConfigImportCompletedNotification) {
	return s.ImportExternalAgentConfigWithResults(params, nil)
}

func (s *ConfigService) ImportExternalAgentConfigWithResults(params *ExternalAgentConfigImportParams, additional []ExternalAgentConfigImportTypeResult) (*ExternalAgentConfigImportResponse, *ExternalAgentConfigImportCompletedNotification) {
	response, typeResults := s.StartExternalAgentConfigImport(params, false)
	typeResults = append(typeResults, additional...)
	return response, s.CompleteExternalAgentConfigImport(response.ImportID, params, typeResults)
}

// StartExternalAgentConfigImport reserves a stable import ID and applies the
// migration items that must finish before the app-server response is sent.
func (s *ConfigService) StartExternalAgentConfigImport(params *ExternalAgentConfigImportParams, deferPlugins bool) (*ExternalAgentConfigImportResponse, []ExternalAgentConfigImportTypeResult) {
	if params == nil {
		params = &ExternalAgentConfigImportParams{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	importID := s.nextExternalAgentImportIDLocked()
	var typeResults []ExternalAgentConfigImportTypeResult
	if _, validation := ValidatePendingSessionImports(params.MigrationItems); validation != nil && len(validation.Failures) > 0 {
		typeResults = append(typeResults, *validation)
	}
	typeResults = append(typeResults, s.importExternalAgentConfigItemsLocked(params, deferPlugins)...)
	return &ExternalAgentConfigImportResponse{ImportID: importID}, typeResults
}

// ImportExternalAgentConfigItems processes a deferred subset without reserving
// another import ID or recording a second history entry.
func (s *ConfigService) ImportExternalAgentConfigItems(params *ExternalAgentConfigImportParams) []ExternalAgentConfigImportTypeResult {
	if params == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.importExternalAgentConfigItemsLocked(params, false)
}

func (s *ConfigService) importExternalAgentConfigItemsLocked(params *ExternalAgentConfigImportParams, deferPlugins bool) []ExternalAgentConfigImportTypeResult {
	migrationSource := normalizeExternalMigrationSource(params.MigrationSource)
	var typeResults []ExternalAgentConfigImportTypeResult
	for i := range params.MigrationItems {
		item := params.MigrationItems[i]
		if item.ItemType == MigrationSessions || deferPlugins && item.ItemType == MigrationPlugins {
			continue
		}
		if item.ItemType == MigrationMemory {
			result := s.importExternalMemory(&item, params.Source)
			typeResults = append(typeResults, result)
			continue
		}
		if migrationSource == externalMigrationSourceCursor {
			result := s.importExternalCursorMigration(item)
			typeResults = append(typeResults, result)
			continue
		}
		if item.ItemType == MigrationConfig || item.ItemType == MigrationSkills || item.ItemType == MigrationAgentsMD {
			result := s.importExternalCoreMigration(item)
			typeResults = append(typeResults, result)
			continue
		}
		if item.ItemType == MigrationMCPServerConfig || item.ItemType == MigrationCommands || item.ItemType == MigrationSubagents {
			result := s.importExternalToolsMigration(item)
			typeResults = append(typeResults, result)
			continue
		}
		if item.ItemType == MigrationHooks {
			result := s.importExternalHooksMigration(item)
			typeResults = append(typeResults, result)
			continue
		}
		if item.ItemType == MigrationPlugins {
			result := s.importExternalPluginsMigration(item)
			typeResults = append(typeResults, result)
			continue
		}
		itemSuccesses := ExternalAgentImportSuccessesForItem(&item, params.Source)
		typeResults = append(typeResults, ExternalAgentConfigImportTypeResult{
			ItemType:  item.ItemType,
			Successes: itemSuccesses,
			Failures:  []ExternalAgentConfigImportItemTypeFailure{},
		})
	}
	return typeResults
}

func (s *ConfigService) CompleteExternalAgentConfigImport(importID string, params *ExternalAgentConfigImportParams, typeResults []ExternalAgentConfigImportTypeResult) *ExternalAgentConfigImportCompletedNotification {
	if params == nil {
		params = &ExternalAgentConfigImportParams{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	notification := BuildExternalAgentImportResult(importID, typeResults)
	history := ExternalAgentConfigImportHistory{
		ImportID:      importID,
		ProviderID:    cloneStringPtr(params.ProviderID),
		CompletedAtMS: s.now().UTC().UnixMilli(),
		Successes:     collectImportSuccesses(notification.ItemTypeResults),
		Failures:      collectImportFailures(notification.ItemTypeResults),
	}
	s.importHistory = append(s.importHistory, history)
	return notification
}

func (s *ConfigService) nextExternalAgentImportIDLocked() string {
	if s.nextImportID <= len(s.importHistory) {
		s.nextImportID = len(s.importHistory) + 1
	}
	importID := fmt.Sprintf("import-%d", s.nextImportID)
	s.nextImportID++
	return importID
}

func (s *ConfigService) ImportHistories() *ExternalAgentConfigImportHistoriesReadResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ExternalAgentConfigImportHistory, len(s.importHistory))
	for i := range s.importHistory {
		out[i] = cloneImportHistory(&s.importHistory[i])
	}
	return &ExternalAgentConfigImportHistoriesReadResponse{Data: out}
}

func (s *ConfigService) RecordExternalAgentImportHistory(params *ExternalAgentConfigImportHistoryRecordParams) *ExternalAgentConfigImportHistoryRecordResponse {
	if params == nil {
		params = &ExternalAgentConfigImportHistoryRecordParams{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	importID := s.nextExternalAgentImportIDLocked()
	providerID := strings.TrimSpace(params.ProviderID)
	history := ExternalAgentConfigImportHistory{
		ImportID:      importID,
		ProviderID:    stringPtrIfNotEmpty(providerID),
		CompletedAtMS: s.now().UTC().UnixMilli(),
		Successes:     collectImportHistoryRecordSuccesses(params.ItemTypeResults),
		Failures:      collectImportHistoryRecordFailures(params.ItemTypeResults),
	}
	s.importHistory = append(s.importHistory, history)
	return &ExternalAgentConfigImportHistoryRecordResponse{ImportID: importID}
}

func applyEdit(root map[string]any, edit *ConfigEdit) {
	strategy := edit.MergeStrategy
	if strategy == "" {
		strategy = MergeReplace
	}
	parts := splitKeyPath(edit.KeyPath)
	if edit.Value == nil {
		deleteAtPath(root, parts)
		return
	}
	if strategy == MergeUpsert {
		existing, ok := getAtPath(root, parts).(map[string]any)
		incoming, incomingOK := edit.Value.(map[string]any)
		if ok && incomingOK {
			merged := cloneMap(existing)
			cloudConfigMergeMap(merged, cloneMap(incoming))
			setAtPath(root, parts, merged)
			return
		}
	}
	setAtPath(root, parts, edit.Value)
}

func deleteAtPath(root map[string]any, parts []string) bool {
	if root == nil || len(parts) == 0 {
		return len(root) == 0
	}
	if len(parts) == 1 {
		delete(root, parts[0])
		return len(root) == 0
	}
	next, ok := root[parts[0]].(map[string]any)
	if !ok {
		return len(root) == 0
	}
	if deleteAtPath(next, parts[1:]) {
		delete(root, parts[0])
	}
	return len(root) == 0
}

func applySkillConfigEdit(root map[string]any, params *SkillConfigWriteParams) bool {
	if root == nil || params == nil {
		return false
	}
	selector := skillConfigEditSelector(params)
	if selector == nil {
		return false
	}
	skills, ok := root["skills"].(map[string]any)
	if !ok {
		if params.Enabled {
			return false
		}
		skills = map[string]any{}
		root["skills"] = skills
	}
	entries := skillConfigTables(skills["config"])
	index := -1
	for i := range entries {
		if skillConfigTableMatchesSelector(entries[i], selector) {
			index = i
			break
		}
	}

	mutated := false
	if params.Enabled {
		if index >= 0 {
			entries = append(entries[:index], entries[index+1:]...)
			mutated = true
		}
	} else {
		entry := map[string]any{"enabled": false}
		if selector.Name != "" {
			entry["name"] = selector.Name
		} else {
			entry["path"] = selector.Path
		}
		if index >= 0 {
			if !valuesEqual(entries[index], entry) {
				entries[index] = entry
				mutated = true
			}
		} else {
			entries = append(entries, entry)
			mutated = true
		}
	}

	if len(entries) == 0 {
		delete(skills, "config")
		if len(skills) == 0 {
			delete(root, "skills")
		}
		return mutated
	}
	skills["config"] = entries
	return mutated
}

type skillConfigSelector struct {
	Name string
	Path string
}

func skillConfigEditSelector(params *SkillConfigWriteParams) *skillConfigSelector {
	if params == nil {
		return nil
	}
	name := strings.TrimSpace(params.Name)
	path := strings.TrimSpace(params.Path)
	switch {
	case name != "" && path == "":
		return &skillConfigSelector{Name: name}
	case path != "" && name == "":
		return &skillConfigSelector{Path: normalizeSkillConfigPath(path)}
	default:
		return nil
	}
}

func skillConfigTableMatchesSelector(table map[string]any, selector *skillConfigSelector) bool {
	if table == nil || selector == nil {
		return false
	}
	name, hasName := table["name"].(string)
	path, hasPath := table["path"].(string)
	name = strings.TrimSpace(name)
	path = strings.TrimSpace(path)
	if selector.Name != "" {
		return hasName && !hasPath && name == selector.Name
	}
	if selector.Path != "" {
		return hasPath && !hasName && normalizeSkillConfigPath(path) == selector.Path
	}
	return false
}

func skillConfigTables(value any) []map[string]any {
	switch v := value.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			out = append(out, cloneMap(item))
		}
		return out
	default:
		return nil
	}
}

func normalizeSkillConfigPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if absolute, absErr := filepath.Abs(path); absErr == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

func validateWritableKeyPath(keyPath string, value any) error {
	if value == nil {
		return nil
	}
	parts := splitKeyPath(keyPath)
	if len(parts) == 0 {
		return fmt.Errorf("%w: keyPath is required", ErrInvalidConfigRequest)
	}
	switch parts[0] {
	case "profile":
		if len(parts) == 1 {
			return configWriteErrorf(ConfigWriteValidation, "`profile` is a legacy config selector and can no longer be written; use `--profile <name>` with `<name>.config.toml` instead")
		}
	case "profiles":
		return configWriteErrorf(ConfigWriteValidation, "`profiles` contains legacy config profile tables and can no longer be written; use `--profile <name>` with `<name>.config.toml` instead")
	case "approval_policy":
		if text, ok := value.(string); ok && strings.EqualFold(strings.TrimSpace(text), "untrusted") {
			return configWriteErrorf(ConfigWriteValidation, `approval_policy = "untrusted" is no longer supported; remove this setting`)
		}
	}
	return nil
}

func splitKeyPath(path string) []string {
	parts := splitDottedPathRespectingQuotes(path)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getAtPath(root map[string]any, parts []string) any {
	var current any = root
	for _, part := range parts {
		next, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = next[part]
	}
	return current
}

func valueAtPath(root map[string]any, keyPath string) (any, bool) {
	parts := splitKeyPath(keyPath)
	if len(parts) == 0 {
		return nil, false
	}
	value := getAtPath(root, parts)
	return value, value != nil
}

func valuesEqual(left any, right any) bool {
	return reflect.DeepEqual(normalizeJSONValue(left), normalizeJSONValue(right))
}

func setAtPath(root map[string]any, parts []string, value any) {
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
	current[parts[len(parts)-1]] = normalizeJSONValue(value)
}

func normalizeJSONValue(value any) any {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint:
		return uint64(v)
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	case float32:
		floatValue := float64(v)
		if floatValue == float64(int64(floatValue)) {
			return int64(floatValue)
		}
		return floatValue
	case float64:
		if v == float64(int64(v)) {
			return int64(v)
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = normalizeJSONValue(value)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = normalizeJSONValue(v[i])
		}
		return out
	}
	return value
}

func applySupportedFeatureEnablement(values map[string]any) {
	if values == nil {
		return
	}
	featuresValue, ok := values["features"].(map[string]any)
	if !ok {
		featuresValue = map[string]any{}
		values["features"] = featuresValue
	}
	defaults := defaultSupportedFeatureEnablement()
	for _, key := range supportedExperimentalFeatureEnablement {
		if _, exists := featuresValue[key]; exists {
			continue
		}
		featuresValue[key] = defaults[key]
	}
}

func defaultSupportedFeatureEnablement() map[string]bool {
	return map[string]bool{
		"auth_elicitation": false,
		"memories":         false,
		"mentions_v2":      true,
		"remote_control":   false,
		"remote_plugin":    true,
		"tool_suggest":     true,
	}
}

func fillOrigins(prefix string, values map[string]any, origin LayerMetadata, out map[string]LayerMetadata) {
	for key, value := range values {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		out[path] = origin
		fillOriginChildren(path, value, origin, out)
	}
}

func fillOriginChildren(prefix string, value any, origin LayerMetadata, out map[string]LayerMetadata) {
	switch typed := value.(type) {
	case map[string]any:
		fillOrigins(prefix, typed, origin, out)
	case []any:
		for index, item := range typed {
			path := fmt.Sprintf("%s.%d", prefix, index)
			out[path] = origin
			fillOriginChildren(path, item, origin, out)
		}
	}
}

func renderTOML(values map[string]any) string {
	var builder strings.Builder
	renderScalarSection(&builder, "", values)
	sections := collectSections(values, nil)
	sort.Slice(sections, func(i int, j int) bool {
		return formatTOMLPath(sections[i]) < formatTOMLPath(sections[j])
	})
	for _, section := range sections {
		nested := getAtPath(values, section)
		nestedMap, ok := nested.(map[string]any)
		if !ok {
			continue
		}
		if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n\n") {
			builder.WriteByte('\n')
		}
		builder.WriteString("[")
		builder.WriteString(formatTOMLPath(section))
		builder.WriteString("]\n")
		renderScalarSection(&builder, formatTOMLPath(section), nestedMap)
	}
	arraySections := collectArrayTableSections(values, nil)
	sort.Slice(arraySections, func(i int, j int) bool {
		return formatTOMLPath(arraySections[i]) < formatTOMLPath(arraySections[j])
	})
	for _, section := range arraySections {
		tables, ok := tomlArrayTables(getAtPath(values, section))
		if !ok {
			continue
		}
		for _, table := range tables {
			if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n\n") {
				builder.WriteByte('\n')
			}
			builder.WriteString("[[")
			builder.WriteString(formatTOMLPath(section))
			builder.WriteString("]]\n")
			renderScalarSection(&builder, formatTOMLPath(section), table)
		}
	}
	return builder.String()
}

func renderScalarSection(builder *strings.Builder, prefix string, values map[string]any) {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if _, nested := value.(map[string]any); nested {
			continue
		}
		if _, nested := tomlArrayTables(value); nested {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString(" = ")
		builder.WriteString(formatTOMLValue(values[key]))
		builder.WriteByte('\n')
	}
}

func collectSections(values map[string]any, prefix []string) [][]string {
	var sections [][]string
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		nested, ok := values[key].(map[string]any)
		if !ok {
			continue
		}
		path := append(append([]string(nil), prefix...), key)
		sections = append(sections, path)
		sections = append(sections, collectSections(nested, path)...)
	}
	return sections
}

func collectArrayTableSections(values map[string]any, prefix []string) [][]string {
	var sections [][]string
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		path := append(append([]string(nil), prefix...), key)
		if _, ok := tomlArrayTables(value); ok {
			sections = append(sections, path)
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			sections = append(sections, collectArrayTableSections(nested, path)...)
		}
	}
	return sections
}

func tomlArrayTables(value any) ([]map[string]any, bool) {
	tables := skillConfigTables(value)
	return tables, len(tables) > 0
}

func formatTOMLPath(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if isBareTOMLKey(part) {
			out = append(out, part)
			continue
		}
		out = append(out, strconv.Quote(part))
	}
	return strings.Join(out, ".")
}

func isBareTOMLKey(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func formatTOMLValue(value any) string {
	switch v := value.(type) {
	case string:
		return strconv.Quote(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case []string:
		parts := make([]string, len(v))
		for i := range v {
			parts[i] = formatTOMLValue(v[i])
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []any:
		parts := make([]string, len(v))
		for i := range v {
			parts[i] = formatTOMLValue(v[i])
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		return formatTOMLInlineTable(v)
	case map[string]string:
		converted := make(map[string]any, len(v))
		for key, value := range v {
			converted[key] = value
		}
		return formatTOMLInlineTable(converted)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return strconv.Quote(fmt.Sprint(v))
		}
		return strconv.Quote(string(data))
	}
}

func formatTOMLInlineTable(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, formatTOMLInlineKey(key)+" = "+formatTOMLValue(values[key]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func formatTOMLInlineKey(value string) string {
	if isBareTOMLKey(value) {
		return value
	}
	return strconv.Quote(value)
}

func configVersion(values map[string]any) string {
	data, err := json.Marshal(values)
	if err != nil {
		return "0"
	}
	var hash uint64 = 1469598103934665603
	for _, b := range data {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return fmt.Sprintf("%x", hash)
}

func cloneRequirements(requirements *ConfigRequirements) *ConfigRequirements {
	if requirements == nil {
		return nil
	}
	clone := *requirements
	clone.AllowedApprovalPolicies = cloneSlice(requirements.AllowedApprovalPolicies)
	clone.AllowedApprovalsReviewers = cloneSlice(requirements.AllowedApprovalsReviewers)
	clone.AllowedSandboxModes = cloneSlice(requirements.AllowedSandboxModes)
	clone.AllowedWindowsSandboxImplementations = cloneSlice(requirements.AllowedWindowsSandboxImplementations)
	clone.AllowedPermissionProfiles = cloneBoolMap(requirements.AllowedPermissionProfiles)
	clone.Permissions = cloneMap(requirements.Permissions)
	clone.AllowedWebSearchModes = cloneSlice(requirements.AllowedWebSearchModes)
	clone.FeatureRequirements = cloneBoolMap(requirements.FeatureRequirements)
	clone.DefaultPermissions = cloneStringPtr(requirements.DefaultPermissions)
	clone.AdditionalDeveloperInstructions = cloneStringPtr(requirements.AdditionalDeveloperInstructions)
	clone.AllowManagedHooksOnly = cloneBoolPtr(requirements.AllowManagedHooksOnly)
	clone.AllowAppshots = cloneBoolPtr(requirements.AllowAppshots)
	clone.AllowRemoteControl = cloneBoolPtr(requirements.AllowRemoteControl)
	clone.ComputerUse = cloneComputerUse(requirements.ComputerUse)
	clone.BrowserUse = cloneBrowserUse(requirements.BrowserUse)
	clone.AutoReview = cloneAutoReview(requirements.AutoReview)
	clone.Hooks = cloneManagedHooks(requirements.Hooks)
	clone.EnforceResidency = cloneResidencyRequirementPtr(requirements.EnforceResidency)
	clone.Network = cloneNetwork(requirements.Network)
	clone.Models = cloneModels(requirements.Models)
	clone.MCPServers = cloneMCPServerRequirements(requirements.MCPServers)
	clone.Plugins = clonePluginRequirements(requirements.Plugins)
	clone.CliAuthCredentialsStore = cloneAuthCredentialsStoreMode(requirements.CliAuthCredentialsStore)
	clone.ChatgptBaseURL = cloneStringPtr(requirements.ChatgptBaseURL)
	return &clone
}

func cloneAutoReview(value *AutoReviewRequirements) *AutoReviewRequirements {
	if value == nil {
		return nil
	}
	return &AutoReviewRequirements{
		RequiredOnModels: stringSliceOrNil(value.RequiredOnModels),
		IgnoreRules:      stringSliceOrNil(value.IgnoreRules),
	}
}

func cloneMCPServerRequirements(values map[string]MCPServerRequirement) map[string]MCPServerRequirement {
	if values == nil {
		return nil
	}
	out := make(map[string]MCPServerRequirement, len(values))
	for name, requirement := range values {
		cloned := requirement
		if requirement.Identity != nil {
			identity := *requirement.Identity
			identity.Command = cloneStringPtr(requirement.Identity.Command)
			identity.URL = cloneStringPtr(requirement.Identity.URL)
			cloned.Identity = &identity
		}
		if requirement.Command != nil {
			command := *requirement.Command
			command.Args = append([]MCPServerValueMatcher(nil), requirement.Command.Args...)
			cloned.Command = &command
		}
		if requirement.URL != nil {
			urlMatcher := *requirement.URL
			cloned.URL = &urlMatcher
		}
		out[name] = cloned
	}
	return out
}

func clonePluginRequirements(values map[string]PluginRequirements) map[string]PluginRequirements {
	if values == nil {
		return nil
	}
	out := make(map[string]PluginRequirements, len(values))
	for name, requirement := range values {
		cloned := requirement
		if requirement.MCPServers != nil {
			servers := cloneMCPServerRequirements(*requirement.MCPServers)
			cloned.MCPServers = &servers
		}
		out[name] = cloned
	}
	return out
}

func cloneSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	return append([]T{}, values...)
}

func cloneConfigWarnings(warnings []ConfigWarningNotification) []ConfigWarningNotification {
	if warnings == nil {
		return nil
	}
	out := make([]ConfigWarningNotification, len(warnings))
	for i := range warnings {
		out[i] = ConfigWarningNotification{
			Summary: warnings[i].Summary,
			Details: cloneStringPtr(warnings[i].Details),
			Path:    cloneStringPtr(warnings[i].Path),
			Range:   cloneTextRangePtr(warnings[i].Range),
		}
	}
	return out
}

func cloneComputerUse(value *ComputerUseRequirements) *ComputerUseRequirements {
	if value == nil {
		return nil
	}
	return &ComputerUseRequirements{AllowLockedComputerUse: cloneBoolPtr(value.AllowLockedComputerUse)}
}

func cloneBrowserUse(value *BrowserUseRequirements) *BrowserUseRequirements {
	if value == nil {
		return nil
	}
	return &BrowserUseRequirements{DisableAutoReview: cloneBoolPtr(value.DisableAutoReview)}
}

func cloneInAppBrowser(value *InAppBrowserRequirements) *InAppBrowserRequirements {
	if value == nil {
		return nil
	}
	return &InAppBrowserRequirements{AllowExternalBrowserSettingsImport: cloneBoolPtr(value.AllowExternalBrowserSettingsImport)}
}

func cloneManagedHooks(value *ManagedHooksRequirements) *ManagedHooksRequirements {
	if value == nil {
		return nil
	}
	return &ManagedHooksRequirements{
		ManagedDir:        cloneStringPtr(value.ManagedDir),
		WindowsManagedDir: cloneStringPtr(value.WindowsManagedDir),
		PreToolUse:        hookGroupsForJSON(value.PreToolUse),
		PermissionRequest: hookGroupsForJSON(value.PermissionRequest),
		PostToolUse:       hookGroupsForJSON(value.PostToolUse),
		PreCompact:        hookGroupsForJSON(value.PreCompact),
		PostCompact:       hookGroupsForJSON(value.PostCompact),
		SessionStart:      hookGroupsForJSON(value.SessionStart),
		UserPromptSubmit:  hookGroupsForJSON(value.UserPromptSubmit),
		SubagentStart:     hookGroupsForJSON(value.SubagentStart),
		SubagentStop:      hookGroupsForJSON(value.SubagentStop),
		Stop:              hookGroupsForJSON(value.Stop),
	}
}

func cloneResidencyRequirementPtr(value *ResidencyRequirement) *ResidencyRequirement {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneNetwork(value *NetworkRequirements) *NetworkRequirements {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Enabled = cloneBoolPtr(value.Enabled)
	clone.HTTPPort = cloneUint16Ptr(value.HTTPPort)
	clone.SOCKSPort = cloneUint16Ptr(value.SOCKSPort)
	clone.AllowUpstreamProxy = cloneBoolPtr(value.AllowUpstreamProxy)
	clone.DangerouslyAllowNonLoopbackProxy = cloneBoolPtr(value.DangerouslyAllowNonLoopbackProxy)
	clone.DangerouslyAllowAllUnixSockets = cloneBoolPtr(value.DangerouslyAllowAllUnixSockets)
	clone.Domains = cloneNetworkMap(value.Domains)
	clone.ManagedAllowedDomainsOnly = cloneBoolPtr(value.ManagedAllowedDomainsOnly)
	clone.AllowedDomains = stringSliceOrNil(value.AllowedDomains)
	clone.DeniedDomains = stringSliceOrNil(value.DeniedDomains)
	clone.UnixSockets = cloneNetworkMap(value.UnixSockets)
	clone.AllowUnixSockets = stringSliceOrNil(value.AllowUnixSockets)
	clone.AllowLocalBinding = cloneBoolPtr(value.AllowLocalBinding)
	return &clone
}

func cloneModels(value *ModelsRequirements) *ModelsRequirements {
	if value == nil {
		return nil
	}
	clone := *value
	clone.NewThread = cloneNewThreadModelDefaults(value.NewThread)
	return &clone
}

func cloneNewThreadModelDefaults(value *NewThreadModelDefaults) *NewThreadModelDefaults {
	if value == nil {
		return nil
	}
	return &NewThreadModelDefaults{
		Model:                cloneStringPtr(value.Model),
		ModelReasoningEffort: cloneStringPtr(value.ModelReasoningEffort),
		ServiceTier:          cloneStringPtr(value.ServiceTier),
	}
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case map[string]any:
			cloned[key] = cloneMap(typed)
		case []any:
			array := make([]any, len(typed))
			for i := range typed {
				array[i] = normalizeJSONValue(typed[i])
			}
			cloned[key] = array
		default:
			cloned[key] = typed
		}
	}
	return cloned
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	cloned := make(map[string]bool, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func permissionPoliciesOrNil(values []sandbox.AskForApproval) []sandbox.AskForApproval {
	if values == nil {
		return nil
	}
	out := make([]sandbox.AskForApproval, len(values))
	copy(out, values)
	return out
}

func approvalsReviewersOrNil(values []ApprovalsReviewer) []ApprovalsReviewer {
	if values == nil {
		return nil
	}
	out := make([]ApprovalsReviewer, len(values))
	copy(out, values)
	return out
}

func sandboxModesOrNil(values []sandbox.SandboxMode) []sandbox.SandboxMode {
	if values == nil {
		return nil
	}
	out := make([]sandbox.SandboxMode, len(values))
	copy(out, values)
	return out
}

func windowsSandboxModesOrNil(values []WindowsSandboxSetupMode) []WindowsSandboxSetupMode {
	if values == nil {
		return nil
	}
	out := make([]WindowsSandboxSetupMode, len(values))
	copy(out, values)
	return out
}

func webSearchModesOrNil(values []WebSearchMode) []WebSearchMode {
	if values == nil {
		return nil
	}
	out := make([]WebSearchMode, len(values))
	copy(out, values)
	return out
}

func stringSliceOrNil(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func forcedLoginMethodsOrNil(values []ForcedLoginMethod) []ForcedLoginMethod {
	if values == nil {
		return nil
	}
	out := make([]ForcedLoginMethod, len(values))
	copy(out, values)
	return out
}

func cloneNetworkMap(values map[string]NetworkPermission) map[string]NetworkPermission {
	if values == nil {
		return nil
	}
	cloned := make(map[string]NetworkPermission, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneUint16Ptr(value *uint16) *uint16 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneUint64Ptr(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneAuthCredentialsStoreMode(value *AuthCredentialsStoreMode) *AuthCredentialsStoreMode {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTextRangePtr(value *TextRange) *TextRange {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneNamedMigrations(values []NamedMigration) []NamedMigration {
	out := make([]NamedMigration, len(values))
	copy(out, values)
	return out
}

func cloneMemoryFileMigrations(values []MemoryFileMigration) []MemoryFileMigration {
	out := make([]MemoryFileMigration, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].CWD = cloneStringPtr(values[i].CWD)
	}
	return out
}

func memoryFileMigrationsForJSON(values []MemoryFileMigration) []MemoryFileMigration {
	out := cloneMemoryFileMigrations(values)
	if out == nil {
		return []MemoryFileMigration{}
	}
	return out
}

func pluginMigrationsForJSON(values []PluginMigration) []PluginMigration {
	if values == nil {
		return []PluginMigration{}
	}
	out := make([]PluginMigration, len(values))
	for i := range values {
		out[i] = PluginMigration{
			MarketplaceName: values[i].MarketplaceName,
			PluginNames:     stringSliceForJSON(values[i].PluginNames),
		}
	}
	return out
}

func namedMigrationsForJSON(values []NamedMigration) []NamedMigration {
	out := cloneNamedMigrations(values)
	if out == nil {
		return []NamedMigration{}
	}
	return out
}

func sessionMigrationsForJSON(values []SessionMigration) []SessionMigration {
	if values == nil {
		return []SessionMigration{}
	}
	out := make([]SessionMigration, len(values))
	for i := range values {
		out[i] = SessionMigration{
			Path:  values[i].Path,
			CWD:   values[i].CWD,
			Title: cloneStringPtr(values[i].Title),
		}
	}
	return out
}

func migrationItemsForJSON(values []ExternalAgentConfigMigrationItem) []ExternalAgentConfigMigrationItem {
	if values == nil {
		return []ExternalAgentConfigMigrationItem{}
	}
	out := make([]ExternalAgentConfigMigrationItem, len(values))
	for i := range values {
		out[i] = CloneExternalAgentMigrationItem(&values[i])
	}
	return out
}

func detectedConnectorCandidatesForJSON(values []ExternalAgentDetectedConnectorCandidate) []ExternalAgentDetectedConnectorCandidate {
	if values == nil {
		return []ExternalAgentDetectedConnectorCandidate{}
	}
	return append([]ExternalAgentDetectedConnectorCandidate(nil), values...)
}

func importTypeResultsForJSON(values []ExternalAgentConfigImportTypeResult) []ExternalAgentConfigImportTypeResult {
	if values == nil {
		return []ExternalAgentConfigImportTypeResult{}
	}
	out := make([]ExternalAgentConfigImportTypeResult, len(values))
	for i := range values {
		out[i] = ExternalAgentConfigImportTypeResult{
			ItemType:  values[i].ItemType,
			Successes: importSuccessesForJSON(values[i].Successes),
			Failures:  importFailuresForJSON(values[i].Failures),
		}
	}
	return out
}

func importSuccessesForJSON(values []ExternalAgentConfigImportItemTypeSuccess) []ExternalAgentConfigImportItemTypeSuccess {
	out := cloneImportSuccesses(values)
	if out == nil {
		return []ExternalAgentConfigImportItemTypeSuccess{}
	}
	return out
}

func importFailuresForJSON(values []ExternalAgentConfigImportItemTypeFailure) []ExternalAgentConfigImportItemTypeFailure {
	out := cloneImportFailures(values)
	if out == nil {
		return []ExternalAgentConfigImportItemTypeFailure{}
	}
	return out
}

func importHistoriesForJSON(values []ExternalAgentConfigImportHistory) []ExternalAgentConfigImportHistory {
	if values == nil {
		return []ExternalAgentConfigImportHistory{}
	}
	out := make([]ExternalAgentConfigImportHistory, len(values))
	for i := range values {
		out[i] = cloneImportHistory(&values[i])
	}
	return out
}

func stringSliceForJSON(values []string) []string {
	out := append([]string(nil), values...)
	if out == nil {
		return []string{}
	}
	return out
}

func hookGroupsForJSON(values []ConfiguredHookGroup) []ConfiguredHookGroup {
	if values == nil {
		return []ConfiguredHookGroup{}
	}
	out := make([]ConfiguredHookGroup, len(values))
	for i := range values {
		out[i] = ConfiguredHookGroup{
			Matcher: cloneStringPtr(values[i].Matcher),
			Hooks:   append([]ConfiguredHookHandler(nil), values[i].Hooks...),
		}
		for j := range out[i].Hooks {
			out[i].Hooks[j].CommandWindows = cloneStringPtr(out[i].Hooks[j].CommandWindows)
			if out[i].Hooks[j].TimeoutSec != nil {
				value := *out[i].Hooks[j].TimeoutSec
				out[i].Hooks[j].TimeoutSec = &value
			}
			out[i].Hooks[j].StatusMessage = cloneStringPtr(out[i].Hooks[j].StatusMessage)
			out[i].Hooks[j].Input = cloneMap(out[i].Hooks[j].Input)
		}
		if out[i].Hooks == nil {
			out[i].Hooks = []ConfiguredHookHandler{}
		}
	}
	return out
}

func cloneImportSuccesses(values []ExternalAgentConfigImportItemTypeSuccess) []ExternalAgentConfigImportItemTypeSuccess {
	out := make([]ExternalAgentConfigImportItemTypeSuccess, len(values))
	for i := range values {
		out[i] = ExternalAgentConfigImportItemTypeSuccess{
			ItemType: values[i].ItemType,
			CWD:      cloneStringPtr(values[i].CWD),
			Source:   cloneStringPtr(values[i].Source),
			Target:   cloneStringPtr(values[i].Target),
			Title:    cloneStringPtr(values[i].Title),
		}
	}
	return out
}

func cloneImportFailures(values []ExternalAgentConfigImportItemTypeFailure) []ExternalAgentConfigImportItemTypeFailure {
	out := make([]ExternalAgentConfigImportItemTypeFailure, len(values))
	for i := range values {
		out[i] = ExternalAgentConfigImportItemTypeFailure{
			ItemType:     values[i].ItemType,
			ErrorType:    cloneStringPtr(values[i].ErrorType),
			SubErrorType: cloneStringPtr(values[i].SubErrorType),
			FailureStage: values[i].FailureStage,
			Message:      values[i].Message,
			CWD:          cloneStringPtr(values[i].CWD),
			Source:       cloneStringPtr(values[i].Source),
		}
	}
	return out
}

func collectImportFailures(values []ExternalAgentConfigImportTypeResult) []ExternalAgentConfigImportItemTypeFailure {
	var out []ExternalAgentConfigImportItemTypeFailure
	for i := range values {
		out = append(out, cloneImportFailures(values[i].Failures)...)
	}
	return out
}

func collectImportSuccesses(values []ExternalAgentConfigImportTypeResult) []ExternalAgentConfigImportItemTypeSuccess {
	var out []ExternalAgentConfigImportItemTypeSuccess
	for i := range values {
		out = append(out, cloneImportSuccesses(values[i].Successes)...)
	}
	return out
}

func collectImportHistoryRecordSuccesses(values []ExternalAgentConfigImportHistoryRecordTypeResultParams) []ExternalAgentConfigImportItemTypeSuccess {
	var out []ExternalAgentConfigImportItemTypeSuccess
	for i := range values {
		for j := range values[i].Successes {
			success := values[i].Successes[j]
			out = append(out, ExternalAgentConfigImportItemTypeSuccess{
				ItemType: success.ItemType,
				CWD:      cloneStringPtr(success.CWD), Source: cloneStringPtr(success.Source),
				Target: cloneStringPtr(success.Target), Title: cloneStringPtr(success.Title),
			})
		}
	}
	return out
}

func collectImportHistoryRecordFailures(values []ExternalAgentConfigImportHistoryRecordTypeResultParams) []ExternalAgentConfigImportItemTypeFailure {
	var out []ExternalAgentConfigImportItemTypeFailure
	for i := range values {
		out = append(out, cloneImportFailures(values[i].Failures)...)
	}
	return out
}

func cloneImportHistory(value *ExternalAgentConfigImportHistory) ExternalAgentConfigImportHistory {
	if value == nil {
		return ExternalAgentConfigImportHistory{}
	}
	return ExternalAgentConfigImportHistory{
		ImportID:      value.ImportID,
		ProviderID:    cloneStringPtr(value.ProviderID),
		CompletedAtMS: value.CompletedAtMS,
		Successes:     cloneImportSuccesses(value.Successes),
		Failures:      cloneImportFailures(value.Failures),
	}
}

func stringPtrIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
