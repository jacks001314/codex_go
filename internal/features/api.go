package features

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var ErrInvalidFeatureRequest = errors.New("invalid feature request")

type FeatureAPIStage string

const (
	FeatureStageStable           FeatureAPIStage = "stable"
	FeatureStageBeta             FeatureAPIStage = "beta"
	FeatureStageUnderDevelopment FeatureAPIStage = "underDevelopment"
	FeatureStageDeprecated       FeatureAPIStage = "deprecated"
	FeatureStageRemoved          FeatureAPIStage = "removed"
)

type FeatureEntry struct {
	Key            string          `json:"key"`
	DisplayName    *string         `json:"displayName"`
	Description    *string         `json:"description"`
	Announcement   *string         `json:"announcement"`
	Stage          FeatureAPIStage `json:"stage"`
	Enabled        bool            `json:"enabled"`
	DefaultEnabled bool            `json:"defaultEnabled"`
}

func (e *FeatureEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name           string          `json:"name"`
		Stage          FeatureAPIStage `json:"stage"`
		Enabled        bool            `json:"enabled"`
		DefaultEnabled bool            `json:"defaultEnabled"`
		DisplayName    *string         `json:"displayName"`
		Description    *string         `json:"description"`
		Announcement   *string         `json:"announcement"`
	}{
		Name:           e.Key,
		Stage:          e.Stage,
		Enabled:        e.Enabled,
		DefaultEnabled: e.DefaultEnabled,
		DisplayName:    cloneStringPtr(e.DisplayName),
		Description:    cloneStringPtr(e.Description),
		Announcement:   cloneStringPtr(e.Announcement),
	})
}

type FeatureListParams struct {
	Cursor   *string `json:"cursor,omitempty"`
	Limit    *int    `json:"limit,omitempty"`
	ThreadID *string `json:"threadId,omitempty"`
}

func (p *FeatureListParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Cursor   *string `json:"cursor"`
		Limit    *int    `json:"limit"`
		ThreadID *string `json:"threadId"`
	}{
		Cursor:   cloneStringPtr(p.Cursor),
		Limit:    cloneIntPtr(p.Limit),
		ThreadID: cloneStringPtr(p.ThreadID),
	})
}

type FeatureListResponse struct {
	Data       []FeatureEntry `json:"data"`
	NextCursor *string        `json:"nextCursor"`
}

func (r *FeatureListResponse) MarshalJSON() ([]byte, error) {
	data := append([]FeatureEntry(nil), r.Data...)
	if data == nil {
		data = []FeatureEntry{}
	}
	return json.Marshal(struct {
		Data       []FeatureEntry `json:"data"`
		NextCursor *string        `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: cloneStringPtr(r.NextCursor),
	})
}

type FeatureEnablementSetParams struct {
	Enablement map[string]bool `json:"enablement,omitempty"`
	Enabled    []string        `json:"enabled,omitempty"`
	Disabled   []string        `json:"disabled,omitempty"`
}

func (p *FeatureEnablementSetParams) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	enablement := cloneBoolMap(p.Enablement)
	if enablement == nil {
		enablement = map[string]bool{}
	}
	return json.Marshal(struct {
		Enablement map[string]bool `json:"enablement"`
	}{Enablement: enablement})
}

type FeatureEnablementSetResponse struct{}

type FeatureService struct {
	mu        sync.Mutex
	catalog   []FeatureEntry
	overrides map[string]bool
}

func NewFeatureService(catalog []FeatureEntry) *FeatureService {
	if catalog == nil {
		catalog = DefaultFeatureCatalog()
	}
	normalized := append([]FeatureEntry(nil), catalog...)
	sort.SliceStable(normalized, func(i int, j int) bool {
		return normalized[i].Key < normalized[j].Key
	})
	return &FeatureService{catalog: normalized, overrides: map[string]bool{}}
}

func DefaultFeatureCatalog() []FeatureEntry {
	result := []FeatureEntry{}
	defaults := Defaults()
	for _, spec := range Sorted() {
		stage := stageFromFeature(spec.Stage)
		displayName := toTitle(spec.Key)
		description := "Enable " + spec.Key
		result = append(result, FeatureEntry{
			Key:            spec.Key,
			DisplayName:    &displayName,
			Description:    &description,
			Stage:          stage,
			Enabled:        defaults[spec.Key],
			DefaultEnabled: defaults[spec.Key],
		})
	}
	if len(result) == 0 {
		name := "Experimental UI"
		description := "Enable experimental app-server UI flows"
		result = append(result, FeatureEntry{Key: "experimental_ui", DisplayName: &name, Description: &description, Stage: FeatureStageBeta})
	}
	return result
}

func stageFromFeature(stage Stage) FeatureAPIStage {
	switch stage {
	case StageStable:
		return FeatureStageStable
	case StageUnderDevelopment:
		return FeatureStageUnderDevelopment
	default:
		return FeatureStageBeta
	}
}

func (s *FeatureService) List(params *FeatureListParams) (*FeatureListResponse, error) {
	if params == nil {
		params = &FeatureListParams{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	start := 0
	if params.Cursor != nil && *params.Cursor != "" {
		if _, err := fmt.Sscanf(*params.Cursor, "%d", &start); err != nil || start < 0 {
			return nil, fmt.Errorf("%w: invalid cursor: %s", ErrInvalidFeatureRequest, *params.Cursor)
		}
	}
	limit := len(s.catalog)
	if params.Limit != nil && *params.Limit > 0 && *params.Limit < limit {
		limit = *params.Limit
	}
	data := []FeatureEntry{}
	for _, entry := range s.catalog {
		cloned := entry
		if enabled, ok := s.overrides[entry.Key]; ok {
			cloned.Enabled = enabled
		}
		data = append(data, cloned)
	}
	if start >= len(data) {
		return &FeatureListResponse{Data: []FeatureEntry{}}, nil
	}
	end := start + limit
	if end > len(data) {
		end = len(data)
	}
	var next *string
	if end < len(data) {
		value := fmt.Sprintf("%d", end)
		next = &value
	}
	return &FeatureListResponse{Data: append([]FeatureEntry(nil), data[start:end]...), NextCursor: next}, nil
}

func (s *FeatureService) SetEnablement(params *FeatureEnablementSetParams) (*FeatureEnablementSetResponse, error) {
	if params == nil {
		params = &FeatureEnablementSetParams{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := map[string]bool{}
	for _, entry := range s.catalog {
		known[entry.Key] = true
	}
	for key, enabled := range params.Enablement {
		key = strings.TrimSpace(key)
		if known[key] {
			s.overrides[key] = enabled
		}
	}
	for _, key := range params.Enabled {
		key = strings.TrimSpace(key)
		if known[key] {
			s.overrides[key] = true
		}
	}
	for _, key := range params.Disabled {
		key = strings.TrimSpace(key)
		if known[key] {
			s.overrides[key] = false
		}
	}
	return &FeatureEnablementSetResponse{}, nil
}

func toTitle(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneBoolMap(value map[string]bool) map[string]bool {
	if value == nil {
		return nil
	}
	clone := make(map[string]bool, len(value))
	for key, enabled := range value {
		clone[key] = enabled
	}
	return clone
}
