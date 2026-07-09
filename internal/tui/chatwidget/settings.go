package chatwidget

import (
	"strings"

	"codex_go/internal/features"
)

type Personality string

const (
	PersonalityNone      Personality = "none"
	PersonalityFriendly  Personality = "friendly"
	PersonalityPragmatic Personality = "pragmatic"
)

type SettingsPopupKind string

const (
	SettingsPopupOK    SettingsPopupKind = "ok"
	SettingsPopupInfo  SettingsPopupKind = "info"
	SettingsPopupError SettingsPopupKind = "error"
)

type PersonalityPopupResult struct {
	Kind    SettingsPopupKind
	Message string
	View    PersonalityPopupView
}

type PersonalityPopupView struct {
	Title      string
	Subtitle   string
	FooterHint string
	Items      []PersonalityOption
}

type PersonalityOption struct {
	Personality Personality
	Name        string
	Description string
	Current     bool
	Disabled    bool
}

type ExperimentalFeatureOption struct {
	Key         string
	Name        string
	Description string
	Enabled     bool
}

type ExperimentalFeaturesViewModel struct {
	Title      string
	FooterHint string
	Items      []ExperimentalFeatureOption
}

type SettingsFeature string

const (
	SettingsFeatureFastMode               SettingsFeature = "fast_mode"
	SettingsFeaturePersonality            SettingsFeature = "personality"
	SettingsFeaturePlugins                SettingsFeature = "plugins"
	SettingsFeatureGoals                  SettingsFeature = "goals"
	SettingsFeatureMentionsV2             SettingsFeature = "mentions_v2"
	SettingsFeaturePreventIdleSleep       SettingsFeature = "prevent_idle_sleep"
	SettingsFeatureWindowsSandbox         SettingsFeature = "windows_sandbox"
	SettingsFeatureWindowsSandboxElevated SettingsFeature = "windows_sandbox_elevated"
)

type SettingsRuntimeState struct {
	ApprovalPolicy                      string
	ApprovalsReviewer                   string
	PermissionProfile                   string
	ActivePermissionProfile             string
	NetworkProxy                        string
	WindowsSandboxMode                  string
	Features                            map[SettingsFeature]bool
	Personality                         Personality
	TUITheme                            *string
	Model                               string
	ReasoningEffort                     string
	PlanModeReasoningEffort             string
	CollaborationModesEnabled           bool
	ActiveModePlan                      bool
	ActiveMaskModel                     string
	ActiveMaskReasoningEffort           string
	PlanDefaultReasoningEffort          string
	GoalStatusActive                    bool
	PreventIdleSleep                    bool
	HasChatGPTAccount                   bool
	HasCodexBackendAuth                 bool
	PendingTokenActivity                bool
	PendingRateLimitReset               bool
	RefreshingStatusOutputCount         int
	RateLimitSwitchPromptVisible        bool
	CodexRateLimitReachedType           string
	StatusLineWorkspaceHeadline         string
	StatusLineWorkspaceMessagesDisabled bool
}

type SettingsRuntimeUpdateResult struct {
	RefreshStatusSurfaces         bool
	RefreshEffectiveServiceTier   bool
	SyncServiceTierCommands       bool
	SyncPersonalityCommand        bool
	SyncPluginsCommand            bool
	RefreshPluginMentions         bool
	SyncGoalCommand               bool
	ClearGoalStatus               bool
	UpdateCollaborationIndicator  bool
	SyncMentionsV2                bool
	UpdatePreventIdleSleep        bool
	RefreshModelDependentSurfaces bool
	WindowsSandboxLevelRefresh    bool
	ClearPendingTokenActivity     bool
	ClearPendingRateLimitReset    bool
	ResetRateLimitWarnings        bool
	DismissRateLimitSwitchPrompt  bool
	FinishRefreshingStatusOutputs bool
	RequestRedraw                 bool
	ConnectorsEnabled             bool
	TokenActivityCommandEnabled   bool
}

func NewPersonalityPopup(current Personality, sessionConfigured bool, modelSupportsPersonality bool, currentModel string) PersonalityPopupResult {
	if !sessionConfigured {
		return PersonalityPopupResult{
			Kind:    SettingsPopupInfo,
			Message: "Personality selection is disabled until startup completes.",
		}
	}
	if !modelSupportsPersonality {
		model := strings.TrimSpace(currentModel)
		if model == "" {
			model = "current model"
		}
		return PersonalityPopupResult{
			Kind:    SettingsPopupError,
			Message: "Current model (" + model + ") doesn't support personalities. Try /model to pick a different model.",
		}
	}
	return PersonalityPopupResult{
		Kind: SettingsPopupOK,
		View: PersonalityPopupView{
			Title:      "Select Personality",
			Subtitle:   "Choose a communication style for Codex.",
			FooterHint: standardPopupHintLine,
			Items: []PersonalityOption{
				personalityOption(PersonalityFriendly, current, false),
				personalityOption(PersonalityPragmatic, current, false),
			},
		},
	}
}

func (s *SettingsRuntimeState) SetFeatureEnabled(feature SettingsFeature, enabled bool) SettingsRuntimeUpdateResult {
	if s == nil {
		return SettingsRuntimeUpdateResult{}
	}
	if s.Features == nil {
		s.Features = map[SettingsFeature]bool{}
	}
	s.Features[feature] = enabled
	result := SettingsRuntimeUpdateResult{}
	switch feature {
	case SettingsFeatureFastMode:
		result.RefreshEffectiveServiceTier = true
		result.SyncServiceTierCommands = true
	case SettingsFeaturePersonality:
		result.SyncPersonalityCommand = true
	case SettingsFeaturePlugins:
		result.SyncPluginsCommand = true
		result.RefreshPluginMentions = true
	case SettingsFeatureGoals:
		result.SyncGoalCommand = true
		if !enabled {
			s.GoalStatusActive = false
			result.ClearGoalStatus = true
			result.UpdateCollaborationIndicator = true
		}
	case SettingsFeatureMentionsV2:
		result.SyncMentionsV2 = true
	case SettingsFeaturePreventIdleSleep:
		s.PreventIdleSleep = enabled
		result.UpdatePreventIdleSleep = true
	case SettingsFeatureWindowsSandbox, SettingsFeatureWindowsSandboxElevated:
		result.WindowsSandboxLevelRefresh = true
	}
	return result
}

func (s *SettingsRuntimeState) SetPlanModeReasoningEffort(effort string) SettingsRuntimeUpdateResult {
	if s == nil {
		return SettingsRuntimeUpdateResult{}
	}
	s.PlanModeReasoningEffort = strings.TrimSpace(effort)
	if s.CollaborationModesEnabled && s.ActiveModePlan {
		if s.PlanModeReasoningEffort != "" {
			s.ActiveMaskReasoningEffort = s.PlanModeReasoningEffort
		} else {
			s.ActiveMaskReasoningEffort = s.PlanDefaultReasoningEffort
		}
	}
	return SettingsRuntimeUpdateResult{RefreshModelDependentSurfaces: true}
}

func (s *SettingsRuntimeState) SetReasoningEffort(effort string) SettingsRuntimeUpdateResult {
	if s == nil {
		return SettingsRuntimeUpdateResult{}
	}
	effort = strings.TrimSpace(effort)
	s.ReasoningEffort = effort
	if s.CollaborationModesEnabled && !s.ActiveModePlan {
		s.ActiveMaskReasoningEffort = effort
	}
	return SettingsRuntimeUpdateResult{RefreshModelDependentSurfaces: true}
}

func (s *SettingsRuntimeState) SetModel(model string) SettingsRuntimeUpdateResult {
	if s == nil {
		return SettingsRuntimeUpdateResult{}
	}
	model = strings.TrimSpace(model)
	s.Model = model
	if s.CollaborationModesEnabled {
		s.ActiveMaskModel = model
	}
	return SettingsRuntimeUpdateResult{
		RefreshEffectiveServiceTier:   true,
		RefreshModelDependentSurfaces: true,
	}
}

func (s *SettingsRuntimeState) UpdateAccountState(hasChatGPTAccount bool, hasCodexBackendAuth bool, connectorsEnabled bool) SettingsRuntimeUpdateResult {
	if s == nil {
		return SettingsRuntimeUpdateResult{}
	}
	result := SettingsRuntimeUpdateResult{
		ClearPendingTokenActivity:     s.PendingTokenActivity,
		ClearPendingRateLimitReset:    s.PendingRateLimitReset,
		ResetRateLimitWarnings:        true,
		DismissRateLimitSwitchPrompt:  s.RateLimitSwitchPromptVisible,
		FinishRefreshingStatusOutputs: s.RefreshingStatusOutputCount > 0,
		RequestRedraw:                 s.RefreshingStatusOutputCount > 0,
		ConnectorsEnabled:             connectorsEnabled,
		TokenActivityCommandEnabled:   hasCodexBackendAuth,
		RefreshStatusSurfaces:         true,
	}
	s.PendingTokenActivity = false
	s.PendingRateLimitReset = false
	s.RefreshingStatusOutputCount = 0
	s.RateLimitSwitchPromptVisible = false
	s.CodexRateLimitReachedType = ""
	s.StatusLineWorkspaceHeadline = ""
	s.StatusLineWorkspaceMessagesDisabled = false
	s.HasChatGPTAccount = hasChatGPTAccount
	s.HasCodexBackendAuth = hasCodexBackendAuth
	return result
}

func personalityOption(personality Personality, current Personality, disabled bool) PersonalityOption {
	return PersonalityOption{
		Personality: personality,
		Name:        PersonalityLabel(personality),
		Description: PersonalityDescription(personality),
		Current:     normalizedPersonality(current) == normalizedPersonality(personality),
		Disabled:    disabled,
	}
}

func PersonalityLabel(personality Personality) string {
	switch normalizedPersonality(personality) {
	case PersonalityNone:
		return "None"
	case PersonalityFriendly:
		return "Friendly"
	case PersonalityPragmatic:
		return "Pragmatic"
	default:
		return string(personality)
	}
}

func PersonalityDescription(personality Personality) string {
	switch normalizedPersonality(personality) {
	case PersonalityNone:
		return "No personality instructions."
	case PersonalityFriendly:
		return "Warm, collaborative, and helpful."
	case PersonalityPragmatic:
		return "Concise, task-focused, and direct."
	default:
		return ""
	}
}

func NewExperimentalFeaturesView(settings map[string]bool) ExperimentalFeaturesViewModel {
	items := []ExperimentalFeatureOption{}
	for _, spec := range features.Registry {
		if !experimentalMenuVisible(spec) {
			continue
		}
		items = append(items, ExperimentalFeatureOption{
			Key:         spec.Key,
			Name:        spec.ExperimentalName,
			Description: spec.ExperimentalMenuDescription,
			Enabled:     features.Enabled(settings, spec.Key),
		})
	}
	return ExperimentalFeaturesViewModel{
		Title:      "Experimental Features",
		FooterHint: standardPopupHintLine,
		Items:      items,
	}
}

func experimentalMenuVisible(spec features.Spec) bool {
	return spec.Stage == features.StageExperimental &&
		strings.TrimSpace(spec.ExperimentalName) != "" &&
		strings.TrimSpace(spec.ExperimentalMenuDescription) != ""
}

func normalizedPersonality(personality Personality) Personality {
	switch Personality(strings.ToLower(strings.TrimSpace(string(personality)))) {
	case PersonalityNone:
		return PersonalityNone
	case PersonalityPragmatic:
		return PersonalityPragmatic
	default:
		return PersonalityFriendly
	}
}
