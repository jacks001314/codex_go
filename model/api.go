package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var ErrInvalidModelRequest = errors.New("invalid model request")

type invalidModelRequestError struct {
	message string
}

func (e *invalidModelRequestError) Error() string {
	return e.message
}

func (e *invalidModelRequestError) Unwrap() error {
	return ErrInvalidModelRequest
}

func invalidModelRequest(message string) error {
	return &invalidModelRequestError{message: message}
}

type ModelListParams struct {
	IncludeHidden   *bool   `json:"includeHidden,omitempty"`
	RefreshStrategy string  `json:"refreshStrategy,omitempty"`
	Limit           *uint32 `json:"limit,omitempty"`
	Cursor          *string `json:"cursor,omitempty"`
	// ModelProvider selects a configured provider for this listing (Rust #59776).
	// Empty keeps the app-server's active provider and its cached catalog.
	ModelProvider string `json:"modelProvider,omitempty"`
}

func (p *ModelListParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	var modelProvider *string
	if trimmed := strings.TrimSpace(p.ModelProvider); trimmed != "" {
		modelProvider = &trimmed
	}
	return json.Marshal(struct {
		Cursor        *string `json:"cursor"`
		Limit         *uint32 `json:"limit"`
		IncludeHidden *bool   `json:"includeHidden"`
		ModelProvider *string `json:"modelProvider"`
	}{
		Cursor:        cloneStringPtr(p.Cursor),
		Limit:         cloneUint32Ptr(p.Limit),
		IncludeHidden: cloneBoolPtr(p.IncludeHidden),
		ModelProvider: modelProvider,
	})
}

// ModelAccessProgramsSummary is the app-server `model/list` wire shape for
// discovery access metadata (Rust #44893). Program names are camelCase.
type ModelAccessProgramsSummary struct {
	Cyber []string `json:"cyber"`
}

// ModelAccessProgramsSummaryFromInfo converts catalog metadata (snake_case) to
// the camelCase app-server wire shape; nil input stays nil.
func ModelAccessProgramsSummaryFromInfo(programs *ModelAccessPrograms) *ModelAccessProgramsSummary {
	if programs == nil {
		return nil
	}
	out := &ModelAccessProgramsSummary{Cyber: make([]string, 0, len(programs.Cyber))}
	for _, program := range programs.Cyber {
		switch program {
		case CyberAccessProgramStandard:
			out.Cyber = append(out.Cyber, "standard")
		case CyberAccessProgramDaybreakBlue:
			out.Cyber = append(out.Cyber, "daybreakBlue")
		case CyberAccessProgramDaybreakRed:
			out.Cyber = append(out.Cyber, "daybreakRed")
		}
	}
	return out
}

type ModelSummary struct {
	// AvailableAccessPrograms is the app-server wire shape (camelCase program
	// names); nil when the catalog provided no access-program metadata.
	AvailableAccessPrograms   *ModelAccessProgramsSummary `json:"availableAccessPrograms"`
	ID                        string                      `json:"id"`
	Model                     string                      `json:"model"`
	Name                      string                      `json:"name,omitempty"`
	DisplayName               string                      `json:"displayName"`
	Description               string                      `json:"description"`
	ModelSpecialty            *string                     `json:"modelSpecialty,omitempty"`
	Hidden                    bool                        `json:"hidden"`
	IsDefault                 bool                        `json:"isDefault"`
	DefaultReasoningEffort    string                      `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []ReasoningEffortOption     `json:"supportedReasoningEfforts"`
	AdditionalSpeedTiers      []string                    `json:"additionalSpeedTiers"`
	ServiceTiers              []ModelServiceTier          `json:"serviceTiers"`
	DefaultServiceTier        *string                     `json:"defaultServiceTier"`
	InputModalities           []string                    `json:"inputModalities"`
	SupportsPersonality       bool                        `json:"supportsPersonality"`
	MultiAgentVersion         *string                     `json:"multiAgentVersion,omitempty"`
	Upgrade                   *string                     `json:"upgrade"`
	UpgradeInfo               *ModelUpgradeInfo           `json:"upgradeInfo,omitempty"`
	AvailabilityNux           *ModelAvailabilityNux       `json:"availabilityNux,omitempty"`
	ContextWindow             int64                       `json:"contextWindow,omitempty"`
	SupportsSearchTool        bool                        `json:"supportsSearchTool,omitempty"`
}

func (m *ModelSummary) MarshalJSON() ([]byte, error) {
	supportedReasoningEfforts := append([]ReasoningEffortOption(nil), m.SupportedReasoningEfforts...)
	if supportedReasoningEfforts == nil {
		supportedReasoningEfforts = []ReasoningEffortOption{}
	}
	additionalSpeedTiers := append([]string(nil), m.AdditionalSpeedTiers...)
	if additionalSpeedTiers == nil {
		additionalSpeedTiers = []string{}
	}
	serviceTiers := append([]ModelServiceTier(nil), m.ServiceTiers...)
	if serviceTiers == nil {
		serviceTiers = []ModelServiceTier{}
	}
	inputModalities := append([]string(nil), m.InputModalities...)
	if inputModalities == nil {
		inputModalities = []string{}
	}
	return json.Marshal(struct {
		ID                        string                      `json:"id"`
		Model                     string                      `json:"model"`
		DisplayName               string                      `json:"displayName"`
		Description               string                      `json:"description"`
		ModelSpecialty            *string                     `json:"modelSpecialty"`
		Hidden                    bool                        `json:"hidden"`
		IsDefault                 bool                        `json:"isDefault"`
		DefaultReasoningEffort    string                      `json:"defaultReasoningEffort"`
		SupportedReasoningEfforts []ReasoningEffortOption     `json:"supportedReasoningEfforts"`
		AdditionalSpeedTiers      []string                    `json:"additionalSpeedTiers"`
		ServiceTiers              []ModelServiceTier          `json:"serviceTiers"`
		DefaultServiceTier        *string                     `json:"defaultServiceTier"`
		InputModalities           []string                    `json:"inputModalities"`
		SupportsPersonality       bool                        `json:"supportsPersonality"`
		MultiAgentVersion         *string                     `json:"multiAgentVersion"`
		Upgrade                   *string                     `json:"upgrade"`
		UpgradeInfo               *ModelUpgradeInfo           `json:"upgradeInfo"`
		AvailabilityNux           *ModelAvailabilityNux       `json:"availabilityNux"`
		AvailableAccessPrograms   *ModelAccessProgramsSummary `json:"availableAccessPrograms"`
	}{
		ID:                        m.ID,
		Model:                     m.Model,
		DisplayName:               m.DisplayName,
		Description:               m.Description,
		ModelSpecialty:            m.ModelSpecialty,
		Hidden:                    m.Hidden,
		IsDefault:                 m.IsDefault,
		DefaultReasoningEffort:    m.DefaultReasoningEffort,
		SupportedReasoningEfforts: supportedReasoningEfforts,
		AdditionalSpeedTiers:      additionalSpeedTiers,
		ServiceTiers:              serviceTiers,
		DefaultServiceTier:        m.DefaultServiceTier,
		InputModalities:           inputModalities,
		SupportsPersonality:       m.SupportsPersonality,
		MultiAgentVersion:         cloneStringPtr(m.MultiAgentVersion),
		Upgrade:                   m.Upgrade,
		UpgradeInfo:               m.UpgradeInfo,
		AvailabilityNux:           m.AvailabilityNux,
		AvailableAccessPrograms:   m.AvailableAccessPrograms,
	})
}

type ModelListResponse struct {
	Data       []ModelSummary `json:"data"`
	NextCursor *string        `json:"nextCursor"`
	Models     []ModelSummary `json:"models,omitempty"`
}

func (r *ModelListResponse) MarshalJSON() ([]byte, error) {
	data := append([]ModelSummary(nil), r.Data...)
	if data == nil {
		data = []ModelSummary{}
	}
	return json.Marshal(struct {
		Data       []ModelSummary `json:"data"`
		NextCursor *string        `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: cloneStringPtr(r.NextCursor),
	})
}

type ReasoningEffortOption struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

type ModelServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ModelUpgradeInfo struct {
	Model             string  `json:"model"`
	ModelLink         *string `json:"modelLink"`
	UpgradeCopy       *string `json:"upgradeCopy"`
	MigrationMarkdown *string `json:"migrationMarkdown"`
	RetirementAt      *int64  `json:"retirementAt"`
}

type ModelAvailabilityNux struct {
	Message string `json:"message"`
}

type ProviderCapabilitiesReadParams struct{}

type ProviderCapabilitiesReadResponse struct {
	ImageGeneration bool `json:"imageGeneration"`
	WebSearch       bool `json:"webSearch"`
}

// ProviderListParams is the empty request body for modelProvider/list.
type ProviderListParams struct{}

// ProviderListResponse mirrors Rust's app-server ModelProviderListResponse.
type ProviderListResponse struct {
	Data              []ProviderSummary `json:"data"`
	DefaultProviderID string            `json:"defaultProviderId"`
	CanChangeDefault  bool              `json:"canChangeDefault"`
}

// ProviderSummary describes one configured or built-in model provider without
// exposing credentials.
type ProviderSummary struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	CanEdit  bool             `json:"canEdit"`
	BaseURL  *string          `json:"baseUrl"`
	Settings ProviderSettings `json:"settings"`
}

// ProviderSettings is the provider-specific configuration visible to clients.
// Type selects which fields are serialized, matching Rust's tagged enum.
type ProviderSettings struct {
	Type           string
	Auth           *GatewayAuth
	Profile        *string
	Region         *string
	ProjectID      *string
	Location       *string
	CredentialFile *string
}

// GatewayAuth describes how a custom gateway obtains credentials. EnvKey is
// omitted for `none` and explicitly null for externallyConfigured without a
// named variable.
type GatewayAuth struct {
	Mode   string
	EnvKey *string
}

func (s ProviderSettings) MarshalJSON() ([]byte, error) {
	switch s.Type {
	case "openai":
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: s.Type})
	case "customGateway":
		return json.Marshal(struct {
			Type string       `json:"type"`
			Auth *GatewayAuth `json:"auth"`
		}{Type: s.Type, Auth: s.Auth})
	case "bedrock":
		return json.Marshal(struct {
			Type    string  `json:"type"`
			Profile *string `json:"profile"`
			Region  *string `json:"region"`
		}{Type: s.Type, Profile: s.Profile, Region: s.Region})
	case "gcp":
		return json.Marshal(struct {
			Type           string  `json:"type"`
			ProjectID      *string `json:"projectId"`
			Location       *string `json:"location"`
			CredentialFile *string `json:"credentialFile"`
		}{Type: s.Type, ProjectID: s.ProjectID, Location: s.Location, CredentialFile: s.CredentialFile})
	default:
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: s.Type})
	}
}

func (a GatewayAuth) MarshalJSON() ([]byte, error) {
	switch a.Mode {
	case "environmentVariable":
		envKey := ""
		if a.EnvKey != nil {
			envKey = *a.EnvKey
		}
		return json.Marshal(struct {
			Mode   string `json:"mode"`
			EnvKey string `json:"envKey"`
		}{Mode: a.Mode, EnvKey: envKey})
	case "externallyConfigured":
		return json.Marshal(struct {
			Mode   string  `json:"mode"`
			EnvKey *string `json:"envKey"`
		}{Mode: a.Mode, EnvKey: a.EnvKey})
	default:
		return json.Marshal(struct {
			Mode string `json:"mode"`
		}{Mode: a.Mode})
	}
}

type ModelInfoReadParams struct {
	Model           string
	Config          *ModelsManagerConfig
	RefreshStrategy string
	// Originator selects an isolated model catalog and cache for supported
	// thread callers (Rust #56284).
	Originator string
}

type ModelService struct {
	manager ModelsManager
}

func NewModelService(manager ModelsManager) *ModelService {
	if manager == nil {
		manager = NewStaticModelsManager(BundledModelsResponse())
	}
	return &ModelService{manager: manager}
}

// RefreshAfterAuthChange forwards Rust #46508's best-effort catalog refresh
// (Rust ModelsManager::refresh_after_auth_change) to the underlying manager.
func (s *ModelService) RefreshAfterAuthChange() {
	if s == nil || s.manager == nil {
		return
	}
	s.manager.RefreshAfterAuthChange()
}

// CatalogIdentity reports the provider/auth identity the running catalog
// belongs to (Rust #46508).
func (s *ModelService) CatalogIdentity() string {
	if s == nil || s.manager == nil {
		return ""
	}
	return s.manager.CatalogIdentity()
}

// Presets exposes the available-model presets of the underlying manager
// (Rust ModelsManager::list_models) for consumers that need the preset metadata
// rather than the client-facing summary, such as the Guardian review-model
// selection.
func (s *ModelService) Presets(strategy RefreshStrategy) []ModelPreset {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.ListModels(strategy)
}

// ModelCatalogWithOrigins exposes the effective catalog with each entry's
// source (Rust model_catalog_with_origins, #57770).
func (s *ModelService) ModelCatalogWithOrigins(strategy RefreshStrategy) []CatalogModel {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.ModelCatalogWithOrigins(strategy)
}

// SetAPIKeyModelDiscoveryEnabled applies the API-key model discovery policy to
// the underlying model manager when it supports it (Rust #44392).
func (s *ModelService) SetAPIKeyModelDiscoveryEnabled(enabled bool) {
	if s == nil || s.manager == nil {
		return
	}
	SetAPIKeyModelDiscoveryEnabled(s.manager, enabled)
}

// ManagerForOriginator returns the isolated catalog manager for a supported
// thread originator, or the default manager for legacy callers (Rust #56284).
func (s *ModelService) ManagerForOriginator(originator string) ModelsManager {
	if s == nil || s.manager == nil {
		return NewStaticModelsManager(BundledModelsResponse())
	}
	return ModelsManagerForOriginator(s.manager, originator)
}

// PresetsForOriginator exposes model presets from an originator-specific
// catalog, retaining the default catalog for unsupported callers.
func (s *ModelService) PresetsForOriginator(strategy RefreshStrategy, originator string) []ModelPreset {
	manager := s.ManagerForOriginator(originator)
	if manager == nil {
		return nil
	}
	return manager.ListModels(strategy)
}

func (s *ModelService) Info(params *ModelInfoReadParams) *ModelInfo {
	catalog := s.CatalogInfo(params)
	if catalog == nil {
		return nil
	}
	return &catalog.Model
}

// CatalogInfo resolves model metadata together with the origin of the catalog
// entry it came from (Rust #58361 ModelsManager::get_model_info returning
// CatalogModel). The origin survives config overrides because it is tracked
// outside the serialized metadata.
func (s *ModelService) CatalogInfo(params *ModelInfoReadParams) *CatalogModel {
	if params == nil {
		params = &ModelInfoReadParams{}
	}
	manager := ModelsManager(nil)
	if s == nil || s.manager == nil {
		manager = NewStaticModelsManager(BundledModelsResponse())
	} else {
		manager = ModelsManagerForOriginator(s.manager, params.Originator)
	}
	strategy := parseStrategy(params.RefreshStrategy)
	modelID := strings.TrimSpace(params.Model)
	if modelID == "" {
		modelID = manager.GetDefaultModel("", true, strategy)
	}
	info := manager.GetCatalogModel(modelID, params.Config)
	return &info
}

func (s *ModelService) List(params *ModelListParams) (*ModelListResponse, error) {
	if params == nil {
		params = &ModelListParams{}
	}
	strategy := parseStrategy(params.RefreshStrategy)
	catalog := s.manager.RawModelCatalog(strategy)
	models := make([]ModelSummary, 0, len(catalog.Models))
	includeHidden := params.IncludeHidden != nil && *params.IncludeHidden
	for _, info := range catalog.Models {
		hidden := modelHiddenFromPicker(info.Visibility)
		if hidden && !includeHidden {
			continue
		}
		if !info.SupportedInAPI {
			continue
		}
		models = append(models, summaryFromModel(info, hidden))
	}
	sort.SliceStable(models, func(i int, j int) bool {
		if models[i].IsDefault != models[j].IsDefault {
			return models[i].IsDefault
		}
		return models[i].ID < models[j].ID
	})
	if len(models) > 0 {
		defaultID := s.manager.GetDefaultModel("", true, strategy)
		for i := range models {
			models[i].IsDefault = models[i].ID == defaultID
		}
	}
	data, nextCursor, err := paginateModels(models, params.Cursor, params.Limit)
	if err != nil {
		return nil, err
	}
	return &ModelListResponse{Data: data, NextCursor: nextCursor, Models: data}, nil
}

func (s *ModelService) ProviderCapabilities(params *ProviderCapabilitiesReadParams) *ProviderCapabilitiesReadResponse {
	info := ModelInfo{}
	catalog := s.manager.RawModelCatalog(RefreshOffline)
	for _, candidate := range catalog.Models {
		if info.Slug == "" || candidate.Priority < info.Priority {
			info = candidate
		}
	}
	return &ProviderCapabilitiesReadResponse{
		ImageGeneration: containsString(info.InputModalities, "image"),
		WebSearch:       info.SupportsSearchTool,
	}
}

func parseStrategy(value string) RefreshStrategy {
	value = strings.TrimSpace(value)
	if value == "" {
		return RefreshOnlineIfUncached
	}
	switch RefreshStrategy(value) {
	case RefreshInMemory, RefreshOnline, RefreshOffline, RefreshOnlineIfUncached:
		return RefreshStrategy(value)
	default:
		return RefreshOnlineIfUncached
	}
}

func summaryFromModel(info ModelInfo, hidden bool) ModelSummary {
	summary := ModelSummary{
		ID:                        info.Slug,
		Model:                     info.Slug,
		Name:                      firstNonEmpty(info.DisplayName, info.Slug),
		DisplayName:               firstNonEmpty(info.DisplayName, info.Slug),
		Description:               info.Description,
		Hidden:                    hidden,
		DefaultReasoningEffort:    firstNonEmpty(info.DefaultReasoningLevel, "medium"),
		SupportedReasoningEfforts: reasoningOptions(info.SupportedReasoningLevels),
		AdditionalSpeedTiers:      append([]string(nil), info.AdditionalSpeedTiers...),
		ServiceTiers:              ServiceTierDefinitionsFor(&info),
		ContextWindow:             info.ContextWindow,
		InputModalities:           append([]string(nil), info.InputModalities...),
		SupportsPersonality:       (&info).SupportsPersonality(),
		SupportsSearchTool:        info.SupportsSearchTool,
	}
	summary.AvailableAccessPrograms = ModelAccessProgramsSummaryFromInfo(info.AvailableAccessPrograms)
	if version := strings.TrimSpace(info.MultiAgentVersion); version != "" {
		summary.MultiAgentVersion = &version
	}
	if info.DefaultServiceTier != "" {
		value := info.DefaultServiceTier
		summary.DefaultServiceTier = &value
	}
	if strings.TrimSpace(info.ModelSpecialty) != "" {
		value := info.ModelSpecialty
		summary.ModelSpecialty = &value
	}
	if info.Upgrade != nil && strings.TrimSpace(info.Upgrade.Model) != "" {
		model := strings.TrimSpace(info.Upgrade.Model)
		summary.Upgrade = &model
		upgradeInfo := &ModelUpgradeInfo{Model: model, RetirementAt: info.Upgrade.RetirementAt}
		if migration := strings.TrimSpace(info.Upgrade.MigrationMarkdown); migration != "" {
			upgradeInfo.MigrationMarkdown = &migration
		}
		summary.UpgradeInfo = upgradeInfo
	}
	return summary
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func paginateModels(models []ModelSummary, cursor *string, limit *uint32) ([]ModelSummary, *string, error) {
	total := len(models)
	if total == 0 {
		return []ModelSummary{}, nil, nil
	}
	start := 0
	if cursor != nil {
		value := strings.TrimSpace(*cursor)
		if value != "" {
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return nil, nil, invalidModelRequest(fmt.Sprintf("invalid cursor: %s", value))
			}
			start = int(parsed)
		}
	}
	if start > total {
		return nil, nil, invalidModelRequest(fmt.Sprintf("cursor %d exceeds total models %d", start, total))
	}
	effectiveLimit := total
	if limit != nil {
		effectiveLimit = int(*limit)
		if effectiveLimit < 1 {
			effectiveLimit = 1
		}
		if effectiveLimit > total {
			effectiveLimit = total
		}
	}
	end := start + effectiveLimit
	if end > total {
		end = total
	}
	var next *string
	if end < total {
		value := strconv.Itoa(end)
		next = &value
	}
	return append([]ModelSummary(nil), models[start:end]...), next, nil
}

func reasoningOptions(levels []string) []ReasoningEffortOption {
	if len(levels) == 0 {
		levels = []string{"medium"}
	}
	out := make([]ReasoningEffortOption, 0, len(levels))
	for _, level := range levels {
		level = strings.TrimSpace(level)
		if level == "" {
			continue
		}
		out = append(out, ReasoningEffortOption{
			ReasoningEffort: level,
			Description:     level,
		})
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneUint32Ptr(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// ProviderConfigWriteParams is the request body for modelProvider/config/write.
type ProviderConfigWriteParams struct {
	Settings ProviderSettingsPatch `json:"settings"`
}

// ProviderConfigWriteResponse returns the saved provider using the same safe
// summary shape as modelProvider/list.
type ProviderConfigWriteResponse struct {
	Provider ProviderSummary `json:"provider"`
}

// ProviderSettingsPatch mirrors Rust's tagged ProviderSettingsPatch. Omitted
// fields keep their values; explicit null clears a nullable field.
type ProviderSettingsPatch struct {
	Type          string
	CustomGateway *CustomGatewaySettingsParams
	Bedrock       *BedrockSettingsParams
	Gcp           *GcpSettingsParams
}

// OptionalString distinguishes an omitted patch field from an explicit null.
type OptionalString struct {
	Set   bool
	Value *string
}

// CustomGatewaySettingsParams patches a custom gateway. ProviderID is nil when
// the request allocates a new provider id.
type CustomGatewaySettingsParams struct {
	ProviderID *string
	Name       *string
	BaseURL    OptionalString
	EnvKey     OptionalString
}

// BedrockSettingsParams patches one of the built-in Bedrock providers.
type BedrockSettingsParams struct {
	ProviderID string
	Profile    OptionalString
	Region     OptionalString
}

// GcpSettingsParams patches the built-in GCP provider.
type GcpSettingsParams struct {
	ProviderID     string
	ProjectID      OptionalString
	Location       OptionalString
	CredentialFile OptionalString
}

func (p *ProviderSettingsPatch) UnmarshalJSON(data []byte) error {
	fields, err := decodeStrictProviderPatchObject(data)
	if err != nil {
		return err
	}
	var patchType string
	if raw, ok := fields["type"]; ok {
		if err := json.Unmarshal(raw, &patchType); err != nil {
			return fmt.Errorf("settings.type must be a string")
		}
	}
	switch patchType {
	case "customGateway":
		custom, err := decodeCustomGatewayPatch(fields)
		if err != nil {
			return err
		}
		p.Type = patchType
		p.CustomGateway = custom
		return nil
	case "bedrock":
		bedrock, err := decodeBedrockPatch(fields)
		if err != nil {
			return err
		}
		p.Type = patchType
		p.Bedrock = bedrock
		return nil
	case "gcp":
		gcp, err := decodeGcpPatch(fields)
		if err != nil {
			return err
		}
		p.Type = patchType
		p.Gcp = gcp
		return nil
	case "":
		return fmt.Errorf("settings.type is required")
	default:
		return fmt.Errorf("unknown settings type %q", patchType)
	}
}

func (p ProviderSettingsPatch) MarshalJSON() ([]byte, error) {
	switch p.Type {
	case "customGateway":
		if p.CustomGateway == nil {
			return nil, fmt.Errorf("customGateway settings are missing")
		}
		payload := map[string]any{"type": p.Type}
		if p.CustomGateway.ProviderID != nil {
			payload["providerId"] = *p.CustomGateway.ProviderID
		}
		if p.CustomGateway.Name != nil {
			payload["name"] = *p.CustomGateway.Name
		}
		if p.CustomGateway.BaseURL.Set {
			payload["baseUrl"] = optionalStringWire(p.CustomGateway.BaseURL)
		}
		if p.CustomGateway.EnvKey.Set {
			payload["envKey"] = optionalStringWire(p.CustomGateway.EnvKey)
		}
		return json.Marshal(payload)
	case "bedrock":
		if p.Bedrock == nil {
			return nil, fmt.Errorf("bedrock settings are missing")
		}
		payload := map[string]any{"type": p.Type, "providerId": p.Bedrock.ProviderID}
		if p.Bedrock.Profile.Set {
			payload["profile"] = optionalStringWire(p.Bedrock.Profile)
		}
		if p.Bedrock.Region.Set {
			payload["region"] = optionalStringWire(p.Bedrock.Region)
		}
		return json.Marshal(payload)
	case "gcp":
		if p.Gcp == nil {
			return nil, fmt.Errorf("gcp settings are missing")
		}
		payload := map[string]any{"type": p.Type, "providerId": p.Gcp.ProviderID}
		if p.Gcp.ProjectID.Set {
			payload["projectId"] = optionalStringWire(p.Gcp.ProjectID)
		}
		if p.Gcp.Location.Set {
			payload["location"] = optionalStringWire(p.Gcp.Location)
		}
		if p.Gcp.CredentialFile.Set {
			payload["credentialFile"] = optionalStringWire(p.Gcp.CredentialFile)
		}
		return json.Marshal(payload)
	default:
		return nil, fmt.Errorf("unknown settings type %q", p.Type)
	}
}

func optionalStringWire(value OptionalString) any {
	if value.Value == nil {
		return nil
	}
	return *value.Value
}

func decodeStrictProviderPatchObject(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("settings must be an object: %w", err)
	}
	return fields, nil
}

func rejectUnknownProviderPatchFields(fields map[string]json.RawMessage, allowed ...string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = true
	}
	for key := range fields {
		if !allowedSet[key] {
			return fmt.Errorf("unknown field `%s` in provider settings", key)
		}
	}
	return nil
}

func decodeCustomGatewayPatch(fields map[string]json.RawMessage) (*CustomGatewaySettingsParams, error) {
	if err := rejectUnknownProviderPatchFields(fields, "type", "providerId", "name", "baseUrl", "envKey"); err != nil {
		return nil, err
	}
	patch := &CustomGatewaySettingsParams{}
	if raw, ok := fields["providerId"]; ok && !isJSONNull(raw) {
		var providerID string
		if err := json.Unmarshal(raw, &providerID); err != nil {
			return nil, fmt.Errorf("settings.providerId must be a string or null")
		}
		patch.ProviderID = &providerID
	}
	if raw, ok := fields["name"]; ok {
		if isJSONNull(raw) {
			return nil, fmt.Errorf("settings.name must be a string")
		}
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return nil, fmt.Errorf("settings.name must be a string")
		}
		patch.Name = &name
	}
	baseURL, err := optionalStringField(fields, "baseUrl")
	if err != nil {
		return nil, err
	}
	envKey, err := optionalStringField(fields, "envKey")
	if err != nil {
		return nil, err
	}
	patch.BaseURL = baseURL
	patch.EnvKey = envKey
	return patch, nil
}

func decodeBedrockPatch(fields map[string]json.RawMessage) (*BedrockSettingsParams, error) {
	if err := rejectUnknownProviderPatchFields(fields, "type", "providerId", "profile", "region"); err != nil {
		return nil, err
	}
	raw, ok := fields["providerId"]
	if !ok {
		return nil, fmt.Errorf("settings.providerId is required")
	}
	var providerID string
	if err := json.Unmarshal(raw, &providerID); err != nil {
		return nil, fmt.Errorf("settings.providerId must be a string")
	}
	switch providerID {
	case AmazonBedrockProviderID, AmazonBedrockRuntimeProviderID:
	default:
		return nil, fmt.Errorf("unknown bedrock providerId %q", providerID)
	}
	profile, err := optionalStringField(fields, "profile")
	if err != nil {
		return nil, err
	}
	region, err := optionalStringField(fields, "region")
	if err != nil {
		return nil, err
	}
	return &BedrockSettingsParams{ProviderID: providerID, Profile: profile, Region: region}, nil
}

func decodeGcpPatch(fields map[string]json.RawMessage) (*GcpSettingsParams, error) {
	if err := rejectUnknownProviderPatchFields(fields, "type", "providerId", "projectId", "location", "credentialFile"); err != nil {
		return nil, err
	}
	raw, ok := fields["providerId"]
	if !ok {
		return nil, fmt.Errorf("settings.providerId is required")
	}
	var providerID string
	if err := json.Unmarshal(raw, &providerID); err != nil {
		return nil, fmt.Errorf("settings.providerId must be a string")
	}
	if providerID != GCPProviderID {
		return nil, fmt.Errorf("unknown gcp providerId %q", providerID)
	}
	projectID, err := optionalStringField(fields, "projectId")
	if err != nil {
		return nil, err
	}
	location, err := optionalStringField(fields, "location")
	if err != nil {
		return nil, err
	}
	credentialFile, err := optionalStringField(fields, "credentialFile")
	if err != nil {
		return nil, err
	}
	return &GcpSettingsParams{ProviderID: providerID, ProjectID: projectID, Location: location, CredentialFile: credentialFile}, nil
}

func optionalStringField(fields map[string]json.RawMessage, key string) (OptionalString, error) {
	raw, ok := fields[key]
	if !ok {
		return OptionalString{}, nil
	}
	if isJSONNull(raw) {
		return OptionalString{Set: true}, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return OptionalString{}, fmt.Errorf("settings.%s must be a string or null", key)
	}
	return OptionalString{Set: true, Value: &value}, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}
