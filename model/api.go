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
}

func (p *ModelListParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Cursor        *string `json:"cursor"`
		Limit         *uint32 `json:"limit"`
		IncludeHidden *bool   `json:"includeHidden"`
	}{
		Cursor:        cloneStringPtr(p.Cursor),
		Limit:         cloneUint32Ptr(p.Limit),
		IncludeHidden: cloneBoolPtr(p.IncludeHidden),
	})
}

type ModelSummary struct {
	ID                        string                  `json:"id"`
	Model                     string                  `json:"model"`
	Name                      string                  `json:"name,omitempty"`
	DisplayName               string                  `json:"displayName"`
	Description               string                  `json:"description"`
	Hidden                    bool                    `json:"hidden"`
	IsDefault                 bool                    `json:"isDefault"`
	DefaultReasoningEffort    string                  `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []ReasoningEffortOption `json:"supportedReasoningEfforts"`
	AdditionalSpeedTiers      []string                `json:"additionalSpeedTiers"`
	ServiceTiers              []ModelServiceTier      `json:"serviceTiers"`
	DefaultServiceTier        *string                 `json:"defaultServiceTier"`
	InputModalities           []string                `json:"inputModalities"`
	SupportsPersonality       bool                    `json:"supportsPersonality"`
	Upgrade                   *string                 `json:"upgrade"`
	UpgradeInfo               *ModelUpgradeInfo       `json:"upgradeInfo,omitempty"`
	AvailabilityNux           *ModelAvailabilityNux   `json:"availabilityNux,omitempty"`
	ContextWindow             int64                   `json:"contextWindow,omitempty"`
	SupportsSearchTool        bool                    `json:"supportsSearchTool,omitempty"`
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
		ID                        string                  `json:"id"`
		Model                     string                  `json:"model"`
		DisplayName               string                  `json:"displayName"`
		Description               string                  `json:"description"`
		Hidden                    bool                    `json:"hidden"`
		IsDefault                 bool                    `json:"isDefault"`
		DefaultReasoningEffort    string                  `json:"defaultReasoningEffort"`
		SupportedReasoningEfforts []ReasoningEffortOption `json:"supportedReasoningEfforts"`
		AdditionalSpeedTiers      []string                `json:"additionalSpeedTiers"`
		ServiceTiers              []ModelServiceTier      `json:"serviceTiers"`
		DefaultServiceTier        *string                 `json:"defaultServiceTier"`
		InputModalities           []string                `json:"inputModalities"`
		SupportsPersonality       bool                    `json:"supportsPersonality"`
		Upgrade                   *string                 `json:"upgrade"`
		UpgradeInfo               *ModelUpgradeInfo       `json:"upgradeInfo"`
		AvailabilityNux           *ModelAvailabilityNux   `json:"availabilityNux"`
	}{
		ID:                        m.ID,
		Model:                     m.Model,
		DisplayName:               m.DisplayName,
		Description:               m.Description,
		Hidden:                    m.Hidden,
		IsDefault:                 m.IsDefault,
		DefaultReasoningEffort:    m.DefaultReasoningEffort,
		SupportedReasoningEfforts: supportedReasoningEfforts,
		AdditionalSpeedTiers:      additionalSpeedTiers,
		ServiceTiers:              serviceTiers,
		DefaultServiceTier:        m.DefaultServiceTier,
		InputModalities:           inputModalities,
		SupportsPersonality:       m.SupportsPersonality,
		Upgrade:                   m.Upgrade,
		UpgradeInfo:               m.UpgradeInfo,
		AvailabilityNux:           m.AvailabilityNux,
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
}

type ModelAvailabilityNux struct {
	Message string `json:"message"`
}

type ProviderCapabilitiesReadParams struct{}

type ProviderCapabilitiesReadResponse struct {
	NamespaceTools  bool `json:"namespaceTools"`
	ImageGeneration bool `json:"imageGeneration"`
	WebSearch       bool `json:"webSearch"`
}

type ModelInfoReadParams struct {
	Model           string
	Config          *ModelsManagerConfig
	RefreshStrategy string
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

func (s *ModelService) Info(params *ModelInfoReadParams) *ModelInfo {
	if params == nil {
		params = &ModelInfoReadParams{}
	}
	manager := ModelsManager(nil)
	if s == nil || s.manager == nil {
		manager = NewStaticModelsManager(BundledModelsResponse())
	} else {
		manager = s.manager
	}
	strategy := parseStrategy(params.RefreshStrategy)
	modelID := strings.TrimSpace(params.Model)
	if modelID == "" {
		modelID = manager.GetDefaultModel("", true, strategy)
	}
	info := manager.GetModelInfo(modelID, params.Config)
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
		NamespaceTools:  info.SupportsParallelToolCalls,
		WebSearch:       info.SupportsSearchTool,
	}
}

func parseStrategy(value string) RefreshStrategy {
	value = strings.TrimSpace(value)
	if value == "" {
		return RefreshOnlineIfUncached
	}
	switch RefreshStrategy(value) {
	case RefreshOnline, RefreshOffline, RefreshOnlineIfUncached:
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
		ServiceTiers:              serviceTiers(info.ServiceTiers),
		ContextWindow:             info.ContextWindow,
		InputModalities:           append([]string(nil), info.InputModalities...),
		SupportsPersonality:       (&info).SupportsPersonality(),
		SupportsSearchTool:        info.SupportsSearchTool,
	}
	if info.DefaultServiceTier != "" {
		value := info.DefaultServiceTier
		summary.DefaultServiceTier = &value
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

func serviceTiers(ids []string) []ModelServiceTier {
	out := make([]ModelServiceTier, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out = append(out, ModelServiceTier{
			ID:          id,
			Name:        id,
			Description: id,
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
