package chatgptapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

const accountProfileResponseMessage = "Invalid account profile response."

// AccountProfile is the read-only profile payload shared with the desktop
// account summary (Rust AccountProfile, #45769). Optional fields keep absence
// distinct from zero, and unknown invocation kinds leave the remaining
// statistics intact.
type AccountProfile struct {
	Profile  *ProfileIdentity `json:"profile"`
	Metadata *ProfileMetadata `json:"metadata"`
	Stats    ProfileStats     `json:"stats"`
}

// ProfileIdentity carries the display name and handle shown by the summary.
type ProfileIdentity struct {
	DisplayName *string `json:"display_name"`
	Username    *string `json:"username"`
}

// ProfileMetadata reports when the statistics were produced and any upstream
// degradation.
type ProfileMetadata struct {
	StatsAsOf  *string `json:"stats_as_of"`
	StatsError *string `json:"stats_error"`
}

// ProfileStats flattens the token usage report so the summary and the token
// view agree on totals.
type ProfileStats struct {
	TokenUsageProfileStats
	FastModeUsagePercentage           *json.Number         `json:"fast_mode_usage_percentage"`
	MostUsedReasoningEffort           *string              `json:"most_used_reasoning_effort"`
	MostUsedReasoningEffortPercentage *json.Number         `json:"most_used_reasoning_effort_percentage"`
	UniqueSkillsUsed                  *int64               `json:"unique_skills_used"`
	TotalSkillsUsed                   *int64               `json:"total_skills_used"`
	TotalThreads                      *int64               `json:"total_threads"`
	TopInvocations                    *[]ProfileInvocation `json:"top_invocations"`
}

// ProfileInvocation is one plugin or skill ranked by the backend.
type ProfileInvocation struct {
	Kind       ProfileInvocationKind `json:"type"`
	PluginName *string               `json:"plugin_name"`
	SkillName  *string               `json:"skill_name"`
	UsageCount *int64                `json:"usage_count"`
}

// ProfileInvocationKind keeps future backend kinds out of the typed set
// without failing the whole profile.
type ProfileInvocationKind string

const (
	ProfileInvocationPlugin  ProfileInvocationKind = "plugin"
	ProfileInvocationSkill   ProfileInvocationKind = "skill"
	ProfileInvocationUnknown ProfileInvocationKind = "unknown"
)

// UnmarshalJSON maps unrecognized invocation kinds onto Unknown.
func (k *ProfileInvocationKind) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch ProfileInvocationKind(value) {
	case ProfileInvocationPlugin, ProfileInvocationSkill:
		*k = ProfileInvocationKind(value)
	default:
		*k = ProfileInvocationUnknown
	}
	return nil
}

// GetAccountProfile reads the full profile from the token usage route
// (Rust get_account_profile).
func (c *CloudClient) GetAccountProfile(ctx context.Context) (*AccountProfile, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	var wire struct {
		Profile  *ProfileIdentity `json:"profile"`
		Metadata *ProfileMetadata `json:"metadata"`
		Stats    *ProfileStats    `json:"stats"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("profiles", "me"), nil, nil, &wire); err != nil {
		var decodeErr *cloudResponseDecodeError
		if errors.As(err, &decodeErr) {
			return nil, errors.New(accountProfileResponseMessage)
		}
		return nil, err
	}
	if wire.Stats == nil {
		return nil, errors.New(accountProfileResponseMessage)
	}
	return &AccountProfile{Profile: wire.Profile, Metadata: wire.Metadata, Stats: *wire.Stats}, nil
}
