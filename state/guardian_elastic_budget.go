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
	GuardianElasticSectionTranscriptMessages GuardianElasticSection = "transcript_messages"
	GuardianElasticSectionTranscriptTools    GuardianElasticSection = "transcript_tools"
	// Round 1017 (#58388) renamed the remaining groups for the source section
	// that owns their evidence.
	GuardianElasticSectionRootConversationMessages GuardianElasticSection = "root_conversation_messages"
	GuardianElasticSectionRetainedUserInstructions GuardianElasticSection = "retained_user_instructions"
	GuardianElasticSectionTrustedUserAnswers       GuardianElasticSection = "trusted_user_answers"
	GuardianElasticSectionSenderUserMessages       GuardianElasticSection = "sender_user_messages"
	GuardianElasticSectionPlannedAction            GuardianElasticSection = "planned_action"
	GuardianElasticSectionPreviousReviews          GuardianElasticSection = "previous_reviews"
	GuardianElasticSectionTrustedTools             GuardianElasticSection = "trusted_tools"
	GuardianElasticSectionTrustedSkills            GuardianElasticSection = "trusted_skills"
	GuardianElasticSectionTranscriptImages         GuardianElasticSection = "transcript_images"
	GuardianElasticSectionNodeReplEvidence         GuardianElasticSection = "node_repl_evidence"
	GuardianElasticSectionPermissionContext        GuardianElasticSection = "permission_context"

	// Deprecated: pre-#58388 serialized names, retained as aliases so existing
	// configuration keeps decoding into the renamed groups.
	GuardianElasticSectionRootMessages         = GuardianElasticSectionRootConversationMessages
	GuardianElasticSectionRetainedInstructions = GuardianElasticSectionRetainedUserInstructions
	GuardianElasticSectionUserAnswers          = GuardianElasticSectionTrustedUserAnswers
	GuardianElasticSectionSenderMessages       = GuardianElasticSectionSenderUserMessages
	GuardianElasticSectionToolDescriptions     = GuardianElasticSectionPlannedAction
	GuardianElasticSectionImages               = GuardianElasticSectionTranscriptImages
	GuardianElasticSectionReplEvidence         = GuardianElasticSectionNodeReplEvidence
)

var guardianElasticSections = map[GuardianElasticSection]bool{
	GuardianElasticSectionTranscriptMessages:       true,
	GuardianElasticSectionTranscriptTools:          true,
	GuardianElasticSectionRootConversationMessages: true,
	GuardianElasticSectionRetainedUserInstructions: true,
	GuardianElasticSectionTrustedUserAnswers:       true,
	GuardianElasticSectionSenderUserMessages:       true,
	GuardianElasticSectionPlannedAction:            true,
	GuardianElasticSectionPreviousReviews:          true,
	GuardianElasticSectionTrustedTools:             true,
	GuardianElasticSectionTrustedSkills:            true,
	GuardianElasticSectionTranscriptImages:         true,
	GuardianElasticSectionNodeReplEvidence:         true,
	GuardianElasticSectionPermissionContext:        true,
}

var guardianElasticSectionAliases = map[string]GuardianElasticSection{
	"root_messages":                    GuardianElasticSectionRootConversationMessages,
	"retained_instructions":            GuardianElasticSectionRetainedUserInstructions,
	"user_answers":                     GuardianElasticSectionTrustedUserAnswers,
	"sender_messages":                  GuardianElasticSectionSenderUserMessages,
	"tool_descriptions":                GuardianElasticSectionPlannedAction,
	"planned_action_tool_descriptions": GuardianElasticSectionPlannedAction,
	"images":                           GuardianElasticSectionTranscriptImages,
	"repl_evidence":                    GuardianElasticSectionNodeReplEvidence,
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

// GuardianElasticDefaultInstructionTokens mirrors Rust's MAX_INSTRUCTION_TOKENS
// (guardian-context/src/preparation/mod.rs): the synchronous entry cap used as
// the default reservation for every group that is not transcript messages or
// tools.
const GuardianElasticDefaultInstructionTokens = 900

// SectionLimits returns the configured limits for one group, or the
// synchronous entry caps when the group has no override (Rust
// ElasticBudgetConfig::section_limits, #58388). Omitted groups reserve one
// capped entry as their minimum.
func (c GuardianElasticBudgetConfig) SectionLimits(section GuardianElasticSection) GuardianElasticSectionLimits {
	if limits, ok := c.Sections[section]; ok {
		return limits
	}
	entries := SynchronousContextProfile().Transcript.EntryLimits
	tokens := GuardianElasticDefaultInstructionTokens
	switch section {
	case GuardianElasticSectionTranscriptMessages:
		tokens = entries.MessageTokens
	case GuardianElasticSectionTranscriptTools:
		tokens = entries.ToolTokens
	}
	if tokens <= 0 {
		tokens = GuardianElasticDefaultInstructionTokens
	}
	return GuardianElasticSectionLimits{MinTotalSectionTokens: tokens, MaxSectionEntryTokens: tokens}
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
		if canonical, ok := guardianElasticSectionAliases[name]; ok {
			section = canonical
		}
		if !guardianElasticSections[section] {
			return fmt.Errorf("unknown elastic section %q", name)
		}
		sections[section] = limits
	}
	*c = GuardianElasticBudgetConfig{Sections: sections}
	return nil
}
