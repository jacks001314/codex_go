package chatwidget

import (
	"strings"

	"codex_go/features"
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

type ExperimentalFeatureOption struct {
	Key         string
	Name        string
	Description string
	Enabled     bool
	// DefaultEnabled mirrors the catalog's defaultEnabled, which decides whether
	// disabling the feature clears the config override (Rust
	// experimental_features::write).
	DefaultEnabled bool
}

type ExperimentalFeaturesViewModel struct {
	Title      string
	FooterHint string
	Items      []ExperimentalFeatureOption
}

type SettingsFeature string

const (
	SettingsFeatureFastMode               SettingsFeature = "fast_mode"
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

func NewExperimentalFeaturesView(settings map[string]bool) ExperimentalFeaturesViewModel {
	items := []ExperimentalFeatureOption{}
	for _, spec := range features.Registry {
		if !experimentalMenuVisible(spec) {
			continue
		}
		items = append(items, ExperimentalFeatureOption{
			Key:            spec.Key,
			Name:           spec.ExperimentalName,
			Description:    spec.ExperimentalMenuDescription,
			Enabled:        features.Enabled(settings, spec.Key),
			DefaultEnabled: spec.DefaultEnabled,
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
