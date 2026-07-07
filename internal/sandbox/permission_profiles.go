package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var ErrInvalidPermissionProfileRequest = errors.New("invalid permission profile request")

type PermissionProfileListParams struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  *int    `json:"limit,omitempty"`
	CWD    *string `json:"cwd,omitempty"`
}

type PermissionProfileSummary struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Allowed     bool   `json:"allowed"`
	SandboxMode string `json:"sandboxMode,omitempty"`
	Network     string `json:"network,omitempty"`
}

func (s *PermissionProfileSummary) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID          string  `json:"id"`
		Description *string `json:"description"`
		Allowed     bool    `json:"allowed"`
	}{
		ID:          s.ID,
		Description: stringPtrIfNotEmpty(s.Description),
		Allowed:     s.Allowed,
	})
}

type PermissionProfileListResponse struct {
	Data       []PermissionProfileSummary `json:"data"`
	NextCursor *string                    `json:"nextCursor"`
}

func (r *PermissionProfileListResponse) MarshalJSON() ([]byte, error) {
	data := append([]PermissionProfileSummary(nil), r.Data...)
	if data == nil {
		data = []PermissionProfileSummary{}
	}
	return json.Marshal(struct {
		Data       []PermissionProfileSummary `json:"data"`
		NextCursor *string                    `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: cloneLocalStringPtr(r.NextCursor),
	})
}

type PermissionProfileService struct {
	profiles []PermissionProfileSummary
}

func NewPermissionProfileService(profiles []PermissionProfileSummary) *PermissionProfileService {
	if len(profiles) == 0 {
		profiles = defaultProfiles()
	}
	return &PermissionProfileService{profiles: cloneProfiles(profiles)}
}

func (s *PermissionProfileService) List(params *PermissionProfileListParams) (*PermissionProfileListResponse, error) {
	if params == nil {
		params = &PermissionProfileListParams{}
	}
	start, err := parseCursor(params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := 50
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
	}
	profiles := cloneProfiles(s.profiles)
	sort.SliceStable(profiles, func(i int, j int) bool {
		return profiles[i].ID < profiles[j].ID
	})
	if start >= len(profiles) {
		return &PermissionProfileListResponse{Data: []PermissionProfileSummary{}}, nil
	}
	end := start + limit
	if end > len(profiles) {
		end = len(profiles)
	}
	var next *string
	if end < len(profiles) {
		value := strconv.Itoa(end)
		next = &value
	}
	return &PermissionProfileListResponse{Data: profiles[start:end], NextCursor: next}, nil
}

func ProfileFromSandbox(id string, description string, policy *SandboxPolicy) PermissionProfileSummary {
	summary := PermissionProfileSummary{ID: id, Description: description, Allowed: true}
	if policy == nil {
		return summary
	}
	summary.SandboxMode = string(policy.Kind)
	if policy.HasFullNetworkAccess() {
		summary.Network = string(NetworkEnabled)
	} else {
		summary.Network = string(NetworkRestricted)
	}
	return summary
}

func defaultProfiles() []PermissionProfileSummary {
	return []PermissionProfileSummary{
		ProfileFromSandbox(":danger-full-access", "Full filesystem and network access.", NewDangerFullAccessPolicy()),
		ProfileFromSandbox(":read-only", "Read-only filesystem access.", NewReadOnlyPolicy()),
		ProfileFromSandbox(":workspace", "Write access inside the workspace.", NewWorkspaceWritePolicy()),
	}
}

func parseCursor(cursor *string) (int, error) {
	if cursor == nil || strings.TrimSpace(*cursor) == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(*cursor)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%w: invalid cursor: %s", ErrInvalidPermissionProfileRequest, *cursor)
	}
	return value, nil
}

func cloneProfiles(profiles []PermissionProfileSummary) []PermissionProfileSummary {
	out := make([]PermissionProfileSummary, len(profiles))
	copy(out, profiles)
	return out
}

func cloneLocalStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func stringPtrIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
