package chatwidget

import (
	_ "embed"
	"strings"
)

type CollaborationModeKind string

const (
	CollaborationModeKindDefault         CollaborationModeKind = "default"
	CollaborationModeKindPlan            CollaborationModeKind = "plan"
	CollaborationModeKindPairProgramming CollaborationModeKind = "pair_programming"
	CollaborationModeKindExecute         CollaborationModeKind = "execute"
)

const (
	CollaborationPlanDefaultReasoningEffort = "medium"
	CollaborationModeIndicatorPlan          = "plan"
)

const collaborationModeDefaultInstructions = "# Collaboration Mode: Default\n\n" +
	"You are now in Default mode. Any previous instructions for other modes (e.g. Plan mode) are no longer active.\n\n" +
	"Your active mode changes only when new developer instructions with a different `<collaboration_mode>...</collaboration_mode>` change it; user requests or tool descriptions do not change mode by themselves. Known mode names are Plan and Default.\n\n" +
	"## request_user_input availability\n\n" +
	"Use the `request_user_input` tool only when it is listed in the available tools for this turn.\n\n" +
	"In Default mode, strongly prefer making reasonable assumptions and executing the user's request rather than stopping to ask questions. If you absolutely must ask a question because the answer cannot be discovered from local context and a reasonable assumption would be risky, ask the user directly with a concise plain-text question. Never write a multiple choice question as a textual assistant message.\n"

//go:embed prompt_for_plan_mode.md
var collaborationModePlanInstructions string

func CollaborationModeInstructions(kind CollaborationModeKind) string {
	if NormalizeCollaborationModeKind(string(kind)) == CollaborationModeKindPlan {
		return strings.TrimSpace(collaborationModePlanInstructions)
	}
	return strings.TrimSpace(collaborationModeDefaultInstructions)
}

type CollaborationOptionalString struct {
	Present bool
	Value   *string
}

type CollaborationModeSettings struct {
	Model                 string
	ReasoningEffort       *string
	DeveloperInstructions *string
}

type CollaborationMode struct {
	Mode     CollaborationModeKind
	Settings CollaborationModeSettings
}

type CollaborationModeMask struct {
	Name                  string
	Mode                  *CollaborationModeKind
	Model                 *string
	ReasoningEffort       CollaborationOptionalString
	DeveloperInstructions CollaborationOptionalString
}

func NormalizeCollaborationModeKind(value string) CollaborationModeKind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(CollaborationModeKindPlan):
		return CollaborationModeKindPlan
	case string(CollaborationModeKindPairProgramming), "pair-programming", "code", string(CollaborationModeKindExecute), "custom":
		return CollaborationModeKindDefault
	default:
		return CollaborationModeKindDefault
	}
}

func (k CollaborationModeKind) DisplayName() string {
	switch k {
	case CollaborationModeKindPlan:
		return "Plan"
	case CollaborationModeKindPairProgramming:
		return "Pair Programming"
	case CollaborationModeKindExecute:
		return "Execute"
	default:
		return "Default"
	}
}

func (k CollaborationModeKind) IsTUIVisible() bool {
	return k == CollaborationModeKindDefault || k == CollaborationModeKindPlan
}

func (k CollaborationModeKind) AllowsRequestUserInput() bool {
	return k == CollaborationModeKindPlan
}

func CollaborationValue(value string) CollaborationOptionalString {
	trimmed := strings.TrimSpace(value)
	return CollaborationOptionalString{Present: true, Value: &trimmed}
}

func CollaborationClearValue() CollaborationOptionalString {
	return CollaborationOptionalString{Present: true}
}

func CollaborationUnsetValue() CollaborationOptionalString {
	return CollaborationOptionalString{}
}

func NewCollaborationMode(mode CollaborationModeKind, model string, reasoningEffort string, developerInstructions string) CollaborationMode {
	return CollaborationMode{
		Mode: NormalizeCollaborationModeKind(string(mode)),
		Settings: CollaborationModeSettings{
			Model:                 strings.TrimSpace(model),
			ReasoningEffort:       stringPtrIfTrimmedNotEmptyChatwidget(reasoningEffort),
			DeveloperInstructions: stringPtrIfTrimmedNotEmptyChatwidget(developerInstructions),
		},
	}
}

func (m CollaborationMode) WithUpdates(model *string, effort CollaborationOptionalString, developerInstructions CollaborationOptionalString) CollaborationMode {
	out := m.Clone()
	if model != nil {
		out.Settings.Model = strings.TrimSpace(*model)
	}
	if effort.Present {
		out.Settings.ReasoningEffort = cloneCollaborationStringPtr(effort.Value)
	}
	if developerInstructions.Present {
		out.Settings.DeveloperInstructions = cloneCollaborationStringPtr(developerInstructions.Value)
	}
	return out
}

func (m CollaborationMode) ApplyMask(mask CollaborationModeMask) CollaborationMode {
	out := m.Clone()
	if mask.Mode != nil {
		out.Mode = NormalizeCollaborationModeKind(string(*mask.Mode))
	}
	if mask.Model != nil {
		out.Settings.Model = strings.TrimSpace(*mask.Model)
	}
	if mask.ReasoningEffort.Present {
		out.Settings.ReasoningEffort = cloneCollaborationStringPtr(mask.ReasoningEffort.Value)
	}
	if mask.DeveloperInstructions.Present {
		out.Settings.DeveloperInstructions = cloneCollaborationStringPtr(mask.DeveloperInstructions.Value)
	}
	return out
}

func (m CollaborationMode) Clone() CollaborationMode {
	return CollaborationMode{
		Mode: m.Mode,
		Settings: CollaborationModeSettings{
			Model:                 m.Settings.Model,
			ReasoningEffort:       cloneCollaborationStringPtr(m.Settings.ReasoningEffort),
			DeveloperInstructions: cloneCollaborationStringPtr(m.Settings.DeveloperInstructions),
		},
	}
}

func (m CollaborationModeMask) Clone() CollaborationModeMask {
	return CollaborationModeMask{
		Name:                  m.Name,
		Mode:                  cloneCollaborationModeKindPtr(m.Mode),
		Model:                 cloneCollaborationStringPtr(m.Model),
		ReasoningEffort:       cloneCollaborationOptionalString(m.ReasoningEffort),
		DeveloperInstructions: cloneCollaborationOptionalString(m.DeveloperInstructions),
	}
}

func BuiltinCollaborationModePresets() []CollaborationModeMask {
	plan := CollaborationModeKindPlan
	defaultMode := CollaborationModeKindDefault
	return []CollaborationModeMask{
		{
			Name:                  plan.DisplayName(),
			Mode:                  &plan,
			ReasoningEffort:       CollaborationValue(CollaborationPlanDefaultReasoningEffort),
			DeveloperInstructions: CollaborationValue(collaborationModePlanInstructions),
		},
		{
			Name:                  defaultMode.DisplayName(),
			Mode:                  &defaultMode,
			DeveloperInstructions: CollaborationValue(collaborationModeDefaultInstructions),
		},
	}
}

func FilteredCollaborationModePresets(presets []CollaborationModeMask) []CollaborationModeMask {
	if len(presets) == 0 {
		presets = BuiltinCollaborationModePresets()
	}
	out := make([]CollaborationModeMask, 0, len(presets))
	for _, preset := range presets {
		if preset.Mode == nil || !preset.Mode.IsTUIVisible() {
			continue
		}
		out = append(out, preset.Clone())
	}
	return out
}

func DefaultCollaborationMask(presets []CollaborationModeMask) (CollaborationModeMask, bool) {
	filtered := FilteredCollaborationModePresets(presets)
	for _, preset := range filtered {
		if preset.Mode != nil && *preset.Mode == CollaborationModeKindDefault {
			return preset.Clone(), true
		}
	}
	if len(filtered) > 0 {
		return filtered[0].Clone(), true
	}
	return CollaborationModeMask{}, false
}

func CollaborationMaskForKind(presets []CollaborationModeMask, kind CollaborationModeKind) (CollaborationModeMask, bool) {
	kind = NormalizeCollaborationModeKind(string(kind))
	if !kind.IsTUIVisible() {
		return CollaborationModeMask{}, false
	}
	for _, preset := range FilteredCollaborationModePresets(presets) {
		if preset.Mode != nil && *preset.Mode == kind {
			return preset.Clone(), true
		}
	}
	return CollaborationModeMask{}, false
}

func DefaultModeCollaborationMask(presets []CollaborationModeMask) (CollaborationModeMask, bool) {
	return CollaborationMaskForKind(presets, CollaborationModeKindDefault)
}

func PlanCollaborationMask(presets []CollaborationModeMask) (CollaborationModeMask, bool) {
	return CollaborationMaskForKind(presets, CollaborationModeKindPlan)
}

func NextCollaborationMask(presets []CollaborationModeMask, current *CollaborationModeMask) (CollaborationModeMask, bool) {
	filtered := FilteredCollaborationModePresets(presets)
	if len(filtered) == 0 {
		return CollaborationModeMask{}, false
	}
	var currentKind *CollaborationModeKind
	if current != nil {
		currentKind = current.Mode
	}
	nextIndex := 0
	if currentKind != nil {
		for index, preset := range filtered {
			if preset.Mode != nil && *preset.Mode == *currentKind {
				nextIndex = (index + 1) % len(filtered)
				break
			}
		}
	}
	return filtered[nextIndex].Clone(), true
}

func InitialCollaborationMask(presets []CollaborationModeMask, modelOverride string) (CollaborationModeMask, bool) {
	mask, ok := DefaultCollaborationMask(presets)
	if !ok {
		return CollaborationModeMask{}, false
	}
	if model := strings.TrimSpace(modelOverride); model != "" {
		mask.Model = &model
	}
	return mask, true
}

func CollaborationModeLabel(kind CollaborationModeKind, enabled bool) string {
	if !enabled {
		return ""
	}
	kind = NormalizeCollaborationModeKind(string(kind))
	if !kind.IsTUIVisible() {
		return ""
	}
	return kind.DisplayName()
}

func CollaborationModeIndicator(kind CollaborationModeKind, enabled bool) string {
	if !enabled {
		return ""
	}
	if NormalizeCollaborationModeKind(string(kind)) == CollaborationModeKindPlan {
		return CollaborationModeIndicatorPlan
	}
	return ""
}

func cloneCollaborationOptionalString(value CollaborationOptionalString) CollaborationOptionalString {
	return CollaborationOptionalString{
		Present: value.Present,
		Value:   cloneCollaborationStringPtr(value.Value),
	}
}

func cloneCollaborationStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneCollaborationModeKindPtr(value *CollaborationModeKind) *CollaborationModeKind {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func stringPtrIfTrimmedNotEmptyChatwidget(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
