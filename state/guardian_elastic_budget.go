package state

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// GuardianElasticSection names one optional allocation group (Rust #58123).
// Groups are independent of prompt placement: the transcript has separate
// message and tool groups even though they share one section. Required
// evidence, including the reviewed action and manual approvals, bypasses these
// limits.
type GuardianElasticSection string

const (
	GuardianElasticSectionTranscriptMessages   GuardianElasticSection = "transcript_messages"
	GuardianElasticSectionTranscriptTools      GuardianElasticSection = "transcript_tools"
	GuardianElasticSectionRootMessages         GuardianElasticSection = "root_messages"
	GuardianElasticSectionRetainedInstructions GuardianElasticSection = "retained_instructions"
	GuardianElasticSectionUserAnswers          GuardianElasticSection = "user_answers"
	GuardianElasticSectionSenderMessages       GuardianElasticSection = "sender_messages"
	GuardianElasticSectionToolDescriptions     GuardianElasticSection = "tool_descriptions"
	GuardianElasticSectionPreviousReviews      GuardianElasticSection = "previous_reviews"
	GuardianElasticSectionTrustedTools         GuardianElasticSection = "trusted_tools"
	GuardianElasticSectionTrustedSkills        GuardianElasticSection = "trusted_skills"
	GuardianElasticSectionImages               GuardianElasticSection = "images"
	GuardianElasticSectionReplEvidence         GuardianElasticSection = "repl_evidence"
	GuardianElasticSectionPermissionContext    GuardianElasticSection = "permission_context"
)

var guardianElasticSections = map[GuardianElasticSection]bool{
	GuardianElasticSectionTranscriptMessages:   true,
	GuardianElasticSectionTranscriptTools:      true,
	GuardianElasticSectionRootMessages:         true,
	GuardianElasticSectionRetainedInstructions: true,
	GuardianElasticSectionUserAnswers:          true,
	GuardianElasticSectionSenderMessages:       true,
	GuardianElasticSectionToolDescriptions:     true,
	GuardianElasticSectionPreviousReviews:      true,
	GuardianElasticSectionTrustedTools:         true,
	GuardianElasticSectionTrustedSkills:        true,
	GuardianElasticSectionImages:               true,
	GuardianElasticSectionReplEvidence:         true,
	GuardianElasticSectionPermissionContext:    true,
}

// GuardianElasticLimitsError reports an invalid elastic section policy.
type GuardianElasticLimitsError string

func (e GuardianElasticLimitsError) Error() string { return string(e) }

const (
	GuardianElasticLimitsZeroTokens           GuardianElasticLimitsError = "elastic section token limits must be greater than zero"
	GuardianElasticLimitsEntryExceedsMinimum  GuardianElasticLimitsError = "min_total_section_tokens must be at least max_section_entry_tokens"
	GuardianElasticLimitsEntryExceedsDelivery GuardianElasticLimitsError = "max_section_entry_tokens must not exceed the delivered text-part limit"
)

// GuardianElasticSectionLimits is the demand-capped minimum allocation and
// per-entry text allowance for one optional evidence group. Constructing or
// decoding an invalid policy is rejected, so invalid limits cannot enter
// preparation.
type GuardianElasticSectionLimits struct {
	MinTotalSectionTokens int
	MaxSectionEntryTokens int
}

// NewGuardianElasticSectionLimits validates one limits pair.
func NewGuardianElasticSectionLimits(minTotalSectionTokens int, maxSectionEntryTokens int) (GuardianElasticSectionLimits, error) {
	if minTotalSectionTokens <= 0 || maxSectionEntryTokens <= 0 {
		return GuardianElasticSectionLimits{}, GuardianElasticLimitsZeroTokens
	}
	if maxSectionEntryTokens > minTotalSectionTokens {
		return GuardianElasticSectionLimits{}, GuardianElasticLimitsEntryExceedsMinimum
	}
	if maxSectionEntryTokens > GuardianMaxTextPartEstimatedTokens {
		return GuardianElasticSectionLimits{}, GuardianElasticLimitsEntryExceedsDelivery
	}
	return GuardianElasticSectionLimits{
		MinTotalSectionTokens: minTotalSectionTokens,
		MaxSectionEntryTokens: maxSectionEntryTokens,
	}, nil
}

// MarshalJSON emits the wire field names shared with Rust's config.
func (l GuardianElasticSectionLimits) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		MinTotalSectionTokens int `json:"min_total_section_tokens"`
		MaxSectionEntryTokens int `json:"max_section_entry_tokens"`
	}{l.MinTotalSectionTokens, l.MaxSectionEntryTokens})
}

// UnmarshalJSON rejects unknown fields and out-of-range limits.
func (l *GuardianElasticSectionLimits) UnmarshalJSON(data []byte) error {
	var fields struct {
		MinTotalSectionTokens int `json:"min_total_section_tokens"`
		MaxSectionEntryTokens int `json:"max_section_entry_tokens"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	limits, err := NewGuardianElasticSectionLimits(fields.MinTotalSectionTokens, fields.MaxSectionEntryTokens)
	if err != nil {
		return err
	}
	*l = limits
	return nil
}

// GuardianElasticBudgetConfig is the per-group elastic limit configuration
// (Rust ElasticBudgetConfig). It is validated when decoded; it is not an
// activation switch and does not change fixed request preparation.
type GuardianElasticBudgetConfig struct {
	Sections map[GuardianElasticSection]GuardianElasticSectionLimits
}

// MarshalJSON emits `{"sections": {...}}` with stable group names.
func (c GuardianElasticBudgetConfig) MarshalJSON() ([]byte, error) {
	sections := make(map[string]GuardianElasticSectionLimits, len(c.Sections))
	for section, limits := range c.Sections {
		sections[string(section)] = limits
	}
	return json.Marshal(struct {
		Sections map[string]GuardianElasticSectionLimits `json:"sections"`
	}{sections})
}

// UnmarshalJSON rejects unknown fields, unknown groups and invalid limits.
func (c *GuardianElasticBudgetConfig) UnmarshalJSON(data []byte) error {
	var fields struct {
		Sections map[string]GuardianElasticSectionLimits `json:"sections"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	sections := make(map[GuardianElasticSection]GuardianElasticSectionLimits, len(fields.Sections))
	for name, limits := range fields.Sections {
		section := GuardianElasticSection(name)
		if !guardianElasticSections[section] {
			return fmt.Errorf("unknown elastic section %q", name)
		}
		sections[section] = limits
	}
	*c = GuardianElasticBudgetConfig{Sections: sections}
	return nil
}
