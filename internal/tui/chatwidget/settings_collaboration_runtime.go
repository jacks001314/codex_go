package chatwidget

import "strings"

type PlanModeNudgeScope struct {
	NewThread bool
	ThreadID  string
}

type PlanModeNudgeContext struct {
	CollaborationModesEnabled bool
	PlanMaskAvailable         bool
	ActiveMode                CollaborationModeKind
	ComposerText              string
	ComposerInputEnabled      bool
	TaskRunning               bool
	ModalOrPopupActive        bool
	DismissedScopes           map[PlanModeNudgeScope]bool
	Scope                     PlanModeNudgeScope
}

type CollaborationRuntimeState struct {
	CollaborationModesEnabled  bool
	CurrentMode                CollaborationMode
	ActiveMask                 *CollaborationModeMask
	PlanModeReasoningEffort    string
	PlanDefaultReasoningEffort string
	DismissedPlanNudgeScopes   map[PlanModeNudgeScope]bool
	PlanNudgeVisible           bool
	ThreadID                   string
	InfoMessages               []string
}

type CollaborationMaskResult struct {
	Applied                       bool
	RefreshPlanModeNudge          bool
	RefreshModelDependentSurfaces bool
	UpdateCollaborationIndicator  bool
	RequestRedraw                 bool
	SubmitThreadSettingsUpdate    bool
	InfoMessage                   string
	CollaborationModeIndicator    string
	GoalStatusIndicatorMayBeShown bool
}

type ThreadSettingsRuntimeState struct {
	CWD               string
	WorkspaceRoots    []string
	ModelProviderID   string
	ServiceTier       string
	ApprovalPolicy    string
	ApprovalsReviewer string
	Personality       Personality
	Model             string
	ReasoningEffort   string
	Collaboration     CollaborationRuntimeState
}

type ThreadSettingsRuntimeUpdate struct {
	CWD                     string
	WorkspaceRoots          []string
	ModelProviderID         string
	ServiceTier             string
	ApprovalPolicy          string
	ApprovalsReviewer       string
	Personality             Personality
	Model                   string
	ReasoningEffort         string
	CollaborationMode       CollaborationMode
	PermissionProfileSynced bool
}

type ThreadSettingsApplyResult struct {
	CWDChanged                  bool
	RefreshEffectiveServiceTier bool
	RefreshStatusSurfaces       bool
	SyncServiceTierCommands     bool
	SyncPersonalityCommand      bool
	RefreshSkillsForCurrentCWD  bool
	RefreshPluginMentions       bool
	RequestRedraw               bool
	CollaborationResult         CollaborationMaskResult
}

func NewPlanModeNudgeScope(threadID string) PlanModeNudgeScope {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return PlanModeNudgeScope{NewThread: true}
	}
	return PlanModeNudgeScope{ThreadID: threadID}
}

func ContainsPlanKeyword(text string) bool {
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) {
		if strings.EqualFold(word, "plan") {
			return true
		}
	}
	return false
}

func ShouldShowPlanModeNudge(context PlanModeNudgeContext) bool {
	trimmed := strings.TrimLeft(context.ComposerText, " \t\r\n")
	return context.CollaborationModesEnabled &&
		context.PlanMaskAvailable &&
		NormalizeCollaborationModeKind(string(context.ActiveMode)) != CollaborationModeKindPlan &&
		context.ComposerInputEnabled &&
		!context.TaskRunning &&
		!context.ModalOrPopupActive &&
		!strings.HasPrefix(trimmed, "/") &&
		!strings.HasPrefix(trimmed, "!") &&
		ContainsPlanKeyword(context.ComposerText) &&
		!context.DismissedScopes[context.Scope]
}

func (s *CollaborationRuntimeState) ActiveModeKind() CollaborationModeKind {
	if s == nil {
		return CollaborationModeKindDefault
	}
	if s.ActiveMask != nil && s.ActiveMask.Mode != nil {
		return NormalizeCollaborationModeKind(string(*s.ActiveMask.Mode))
	}
	return NormalizeCollaborationModeKind(string(s.CurrentMode.Mode))
}

func (s *CollaborationRuntimeState) CurrentModel() string {
	if s == nil {
		return ""
	}
	if s.CollaborationModesEnabled && s.ActiveMask != nil && s.ActiveMask.Model != nil {
		return strings.TrimSpace(*s.ActiveMask.Model)
	}
	return strings.TrimSpace(s.CurrentMode.Settings.Model)
}

func (s *CollaborationRuntimeState) EffectiveReasoningEffort() string {
	if s == nil {
		return ""
	}
	if s.CollaborationModesEnabled && s.ActiveMask != nil && s.ActiveMask.ReasoningEffort.Present {
		if s.ActiveMask.ReasoningEffort.Value == nil {
			return ""
		}
		return strings.TrimSpace(*s.ActiveMask.ReasoningEffort.Value)
	}
	if s.CurrentMode.Settings.ReasoningEffort == nil {
		return ""
	}
	return strings.TrimSpace(*s.CurrentMode.Settings.ReasoningEffort)
}

func (s *CollaborationRuntimeState) EffectiveCollaborationMode() CollaborationMode {
	if s == nil {
		return NewCollaborationMode(CollaborationModeKindDefault, "", "", "")
	}
	if !s.CollaborationModesEnabled || s.ActiveMask == nil {
		return s.CurrentMode.Clone()
	}
	return s.CurrentMode.ApplyMask(*s.ActiveMask)
}

func (s *CollaborationRuntimeState) RefreshPlanModeNudge(composerText string, composerInputEnabled bool, taskRunning bool, modalOrPopupActive bool) bool {
	if s == nil {
		return false
	}
	_, planAvailable := PlanCollaborationMask(nil)
	visible := ShouldShowPlanModeNudge(PlanModeNudgeContext{
		CollaborationModesEnabled: s.CollaborationModesEnabled,
		PlanMaskAvailable:         planAvailable,
		ActiveMode:                s.ActiveModeKind(),
		ComposerText:              composerText,
		ComposerInputEnabled:      composerInputEnabled,
		TaskRunning:               taskRunning,
		ModalOrPopupActive:        modalOrPopupActive,
		DismissedScopes:           s.DismissedPlanNudgeScopes,
		Scope:                     NewPlanModeNudgeScope(s.ThreadID),
	})
	changed := s.PlanNudgeVisible != visible
	s.PlanNudgeVisible = visible
	return changed
}

func (s *CollaborationRuntimeState) DismissPlanModeNudge() bool {
	if s == nil {
		return false
	}
	if s.DismissedPlanNudgeScopes == nil {
		s.DismissedPlanNudgeScopes = map[PlanModeNudgeScope]bool{}
	}
	scope := NewPlanModeNudgeScope(s.ThreadID)
	already := s.DismissedPlanNudgeScopes[scope]
	s.DismissedPlanNudgeScopes[scope] = true
	wasVisible := s.PlanNudgeVisible
	s.PlanNudgeVisible = false
	return !already || wasVisible
}

func (s *CollaborationRuntimeState) SetCollaborationMask(mask CollaborationModeMask) CollaborationMaskResult {
	if s == nil || !s.CollaborationModesEnabled {
		return CollaborationMaskResult{}
	}
	previousMode := s.ActiveModeKind()
	previousModel := s.CurrentModel()
	previousEffort := s.EffectiveReasoningEffort()
	if mask.Mode != nil && *mask.Mode == CollaborationModeKindPlan && strings.TrimSpace(s.PlanModeReasoningEffort) != "" {
		mask.ReasoningEffort = CollaborationValue(s.PlanModeReasoningEffort)
	}
	if mask.Mode != nil && *mask.Mode == CollaborationModeKindPlan {
		if s.DismissedPlanNudgeScopes == nil {
			s.DismissedPlanNudgeScopes = map[PlanModeNudgeScope]bool{}
		}
		s.DismissedPlanNudgeScopes[NewPlanModeNudgeScope(s.ThreadID)] = true
		s.PlanNudgeVisible = false
	}
	cloned := mask.Clone()
	s.ActiveMask = &cloned
	nextMode := s.ActiveModeKind()
	nextModel := s.CurrentModel()
	nextEffort := s.EffectiveReasoningEffort()
	result := CollaborationMaskResult{
		Applied:                       true,
		RefreshPlanModeNudge:          true,
		RefreshModelDependentSurfaces: true,
		UpdateCollaborationIndicator:  true,
		RequestRedraw:                 true,
		CollaborationModeIndicator:    CollaborationModeIndicator(nextMode, s.CollaborationModesEnabled),
		GoalStatusIndicatorMayBeShown: CollaborationModeIndicator(nextMode, s.CollaborationModesEnabled) == "",
	}
	if previousMode != nextMode && (previousModel != nextModel || previousEffort != nextEffort) {
		result.InfoMessage = collaborationMaskChangeMessage(nextMode, nextModel, nextEffort)
		s.InfoMessages = append(s.InfoMessages, result.InfoMessage)
	}
	return result
}

func (s *CollaborationRuntimeState) SetCollaborationMaskFromUserAction(mask CollaborationModeMask) CollaborationMaskResult {
	result := s.SetCollaborationMask(mask)
	if result.Applied {
		result.SubmitThreadSettingsUpdate = strings.TrimSpace(s.ThreadID) != ""
	}
	return result
}

func (s *CollaborationRuntimeState) CycleCollaborationMode(presets []CollaborationModeMask) CollaborationMaskResult {
	if s == nil || !s.CollaborationModesEnabled {
		return CollaborationMaskResult{}
	}
	next, ok := NextCollaborationMask(presets, s.ActiveMask)
	if !ok {
		return CollaborationMaskResult{}
	}
	return s.SetCollaborationMaskFromUserAction(next)
}

func (s *CollaborationRuntimeState) SetEffectiveCollaborationMode(mode CollaborationMode) CollaborationMaskResult {
	if s == nil {
		return CollaborationMaskResult{}
	}
	modeKind := NormalizeCollaborationModeKind(string(mode.Mode))
	if modeKind == CollaborationModeKindDefault {
		s.CurrentMode = mode.Clone()
	}
	mask := CollaborationModeMask{
		Name:                  modeKind.DisplayName(),
		Mode:                  &modeKind,
		Model:                 stringPtrIfTrimmedNotEmptyChatwidget(mode.Settings.Model),
		DeveloperInstructions: CollaborationUnsetValue(),
	}
	if mode.Settings.ReasoningEffort != nil {
		mask.ReasoningEffort = CollaborationValue(*mode.Settings.ReasoningEffort)
	} else {
		mask.ReasoningEffort = CollaborationClearValue()
	}
	if mode.Settings.DeveloperInstructions != nil {
		mask.DeveloperInstructions = CollaborationValue(*mode.Settings.DeveloperInstructions)
	} else {
		mask.DeveloperInstructions = CollaborationClearValue()
	}
	return s.SetCollaborationMask(mask)
}

func (s *ThreadSettingsRuntimeState) ApplyThreadSettings(update ThreadSettingsRuntimeUpdate) ThreadSettingsApplyResult {
	if s == nil {
		return ThreadSettingsApplyResult{}
	}
	cwdChanged := strings.TrimSpace(s.CWD) != strings.TrimSpace(update.CWD)
	s.applyThreadSettingsCWD(update.CWD)
	s.ModelProviderID = strings.TrimSpace(update.ModelProviderID)
	s.ServiceTier = strings.TrimSpace(update.ServiceTier)
	s.ApprovalPolicy = strings.TrimSpace(update.ApprovalPolicy)
	s.ApprovalsReviewer = strings.TrimSpace(update.ApprovalsReviewer)
	s.Personality = update.Personality
	s.Model = strings.TrimSpace(update.Model)
	s.ReasoningEffort = strings.TrimSpace(update.ReasoningEffort)
	update.CollaborationMode.Settings.Model = s.Model
	if s.ReasoningEffort != "" {
		update.CollaborationMode.Settings.ReasoningEffort = stringPtrIfTrimmedNotEmptyChatwidget(s.ReasoningEffort)
	}
	collaborationResult := s.Collaboration.SetEffectiveCollaborationMode(update.CollaborationMode)
	return ThreadSettingsApplyResult{
		CWDChanged:                  cwdChanged,
		RefreshEffectiveServiceTier: true,
		RefreshStatusSurfaces:       true,
		SyncServiceTierCommands:     true,
		SyncPersonalityCommand:      true,
		RefreshSkillsForCurrentCWD:  cwdChanged,
		RefreshPluginMentions:       true,
		RequestRedraw:               true,
		CollaborationResult:         collaborationResult,
	}
}

func (s *ThreadSettingsRuntimeState) applyThreadSettingsCWD(cwd string) {
	cwd = strings.TrimSpace(cwd)
	previousCWD := strings.TrimSpace(s.CWD)
	s.CWD = cwd
	if previousCWD == "" || !stringSliceContainsFold(s.WorkspaceRoots, previousCWD) {
		return
	}
	previousRoots := append([]string(nil), s.WorkspaceRoots...)
	s.WorkspaceRoots = []string{cwd}
	for _, root := range previousRoots {
		root = strings.TrimSpace(root)
		if root != "" && !strings.EqualFold(root, previousCWD) && !stringSliceContainsFold(s.WorkspaceRoots, root) {
			s.WorkspaceRoots = append(s.WorkspaceRoots, root)
		}
	}
}

func collaborationMaskChangeMessage(mode CollaborationModeKind, model string, effort string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "unknown"
	}
	message := "Model changed to " + model
	if !strings.HasPrefix(model, "codex-auto-") {
		label := strings.TrimSpace(effort)
		if label == "" || strings.EqualFold(label, "none") {
			label = "default"
		}
		message += " " + label
	}
	message += " for " + mode.DisplayName() + " mode."
	return message
}

func stringSliceContainsFold(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}
