package appserver

import (
	"fmt"
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// stepSettingsConstraints validates the model/reviewer half of a step-settings
// candidate against the permission profile proposed by the same settings
// write. The Rust #40647 implementation applies step settings after the
// proposed environment selection so full-disk access cannot be validated
// against the old profile.
type stepSettingsConstraints struct {
	requirements              *config.ConfigRequirements
	guardianApprovalEnabled   bool
	hasFullDiskWriteAccess    bool
	model                     string
	approvalsReviewer         string
	approvalsReviewerExplicit bool
	modelChanged              bool
}

// applyStepSettingsConstraints mirrors Rust's StepSettings::apply validation.
// It returns the reviewer that should be committed, including the automatic
// AutoReview selection when a protected model is first selected.
func applyStepSettingsConstraints(candidate stepSettingsConstraints) (string, error) {
	reviewer := normalizeStepApprovalsReviewer(candidate.approvalsReviewer)
	if candidate.approvalsReviewerExplicit &&
		!stepApprovalsReviewerAllowed(candidate.requirements, reviewer) {
		return reviewer, fmt.Errorf(
			"invalid value for `approvals_reviewer`: `%s` is not in the allowed set",
			reviewer,
		)
	}
	if candidate.requirements == nil ||
		!candidate.requirements.AutoReviewRequiredForModel(candidate.model) {
		return reviewer, nil
	}

	if candidate.modelChanged && !candidate.approvalsReviewerExplicit {
		if !stepApprovalsReviewerAllowed(candidate.requirements, string(config.ApprovalsReviewerAutoReview)) {
			return reviewer, autoReviewRequiredModelError(candidate.model)
		}
		reviewer = string(config.ApprovalsReviewerAutoReview)
	}
	if reviewer == string(config.ApprovalsReviewerAutoReview) &&
		!candidate.hasFullDiskWriteAccess &&
		candidate.guardianApprovalEnabled {
		return reviewer, nil
	}
	return reviewer, autoReviewRequiredModelError(candidate.model)
}

func autoReviewRequiredModelError(model string) error {
	return fmt.Errorf("To use model `%s`, you need to use auto review.", strings.TrimSpace(model))
}

func normalizeStepApprovalsReviewer(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "user":
		return string(config.ApprovalsReviewerUser)
	case "auto_review", "autoreview", "guardian_subagent":
		return string(config.ApprovalsReviewerAutoReview)
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func stepApprovalsReviewerAllowed(requirements *config.ConfigRequirements, reviewer string) bool {
	if requirements == nil || requirements.AllowedApprovalsReviewers == nil {
		return true
	}
	reviewer = normalizeStepApprovalsReviewer(reviewer)
	for _, allowed := range requirements.AllowedApprovalsReviewers {
		if normalizeStepApprovalsReviewer(string(allowed)) == reviewer {
			return true
		}
	}
	return false
}

func permissionProfileHasFullDiskWriteAccess(profile *sandbox.PermissionProfile) bool {
	return profile == nil ||
		profile.Disabled ||
		profile.SandboxPolicy == nil ||
		profile.SandboxPolicy.HasFullDiskWriteAccess()
}

// validateTurnStepSettings validates a turn candidate using the permissions
// and primary environment proposed by that turn. A pending standalone update
// is not consulted for the candidate values; it supplies only the current
// baseline for fields the turn does not override.
func (r *RuntimeRouter) validateTurnStepSettings(params *turn.TurnStartParams, reviewerExplicit bool) error {
	if r == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return err
	}
	settings := r.threadSettingsForTurn(params.ThreadID)
	currentModel := firstNonEmpty(
		threadSettingsModel(settings),
		r.persistedThreadModel(params.ThreadID),
		stringConfigValue(cfg, "model"),
	)
	model := firstNonEmpty(strings.TrimSpace(params.Model), currentModel)
	reviewer := ""
	if params.ApprovalsReviewer != nil {
		reviewer = strings.TrimSpace(*params.ApprovalsReviewer)
	}
	if reviewer == "" {
		reviewer = firstNonEmpty(
			threadSettingsApprovalsReviewerValue(settings),
			stringConfigValue(cfg, "approvals_reviewer"),
			stringConfigValue(cfg, "approvalsReviewer"),
			string(config.ApprovalsReviewerUser),
		)
	}
	cwd := firstNonEmpty(strings.TrimSpace(params.CWD), threadSettingsCWD(settings), r.services.DefaultCWD)
	protectedModel := cfg.Requirements != nil && cfg.Requirements.AutoReviewRequiredForModel(model)
	var profile *sandbox.PermissionProfile
	if protectedModel {
		resolution, resolveErr := turnSandboxPermissionProfile(cfg, cwd, params)
		if resolveErr != nil {
			return resolveErr
		}
		if resolution != nil {
			profile = resolution.Profile
		}
	}
	selected, err := applyStepSettingsConstraints(stepSettingsConstraints{
		requirements:              cfg.Requirements,
		guardianApprovalEnabled:   features.Enabled(cfg.FeatureSettings(), "guardian_approval"),
		hasFullDiskWriteAccess:    protectedModel && permissionProfileHasFullDiskWriteAccess(profile),
		model:                     model,
		approvalsReviewer:         reviewer,
		approvalsReviewerExplicit: reviewerExplicit,
		modelChanged:              !strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(currentModel)),
	})
	if err != nil {
		return jsonRPCInvalidRequest(err.Error())
	}
	if selected != reviewer {
		value := selected
		params.ApprovalsReviewer = &value
	}
	return nil
}

// validateThreadSettingsStepSettings validates thread/settings/update before it
// commits. The synthetic turn carries the proposed model and permission
// profile, while the router still supplies the thread's current baseline.
func (r *RuntimeRouter) validateThreadSettingsStepSettings(params *SettingsUpdateParams) error {
	if r == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil
	}
	settings := r.threadSettingsForTurn(params.ThreadID)
	record, _ := r.threadRecord(session.ThreadID(strings.TrimSpace(params.ThreadID)), false, false)
	persistedModel := ""
	if record != nil {
		persistedModel = strings.TrimSpace(record.Metadata.Model)
	}
	model := firstNonEmpty(threadSettingsModel(settings), persistedModel)
	if params.Model != nil {
		model = strings.TrimSpace(*params.Model)
	}
	if collaboration := collaborationModeSettings(params.CollaborationMode); collaboration != nil {
		if collaborationModel := strings.TrimSpace(stringFromAny(firstMapValue(collaboration, "model"))); collaborationModel != "" {
			model = collaborationModel
		}
	}
	proposed := &turn.TurnStartParams{
		ThreadID:              strings.TrimSpace(params.ThreadID),
		CWD:                   firstNonEmpty(threadSettingsCWD(settings), r.threadShellCommandCWDOrEmpty(params.ThreadID)),
		Model:                 model,
		RuntimeWorkspaceRoots: append([]string(nil), settings.RuntimeWorkspaceRoots...),
	}
	if record != nil {
		proposed.Config = threadRecordConfigOverrides(record)
	}
	if params.Permissions != nil {
		proposed.Permissions = cloneString(params.Permissions)
	} else if params.SandboxPolicy != nil {
		proposed.SandboxPolicy = cloneString(params.SandboxPolicy)
	} else if settings != nil {
		if settings.ActivePermissionProfile != nil {
			proposed.Permissions = cloneString(settings.ActivePermissionProfile)
		} else if strings.TrimSpace(settings.SandboxPolicy) != "" {
			proposed.SandboxPolicy = settings.SandboxPolicy
		}
	}
	reviewerExplicit := params.ApprovalsReviewer != nil && strings.TrimSpace(*params.ApprovalsReviewer) != ""
	if params.ApprovalsReviewer != nil {
		proposed.ApprovalsReviewer = cloneString(params.ApprovalsReviewer)
	}
	if err := r.validateTurnStepSettings(proposed, reviewerExplicit); err != nil {
		return err
	}
	if proposed.ApprovalsReviewer != nil &&
		(params.ApprovalsReviewer == nil || *proposed.ApprovalsReviewer != *params.ApprovalsReviewer) {
		params.ApprovalsReviewer = cloneString(proposed.ApprovalsReviewer)
	}
	return nil
}

func (r *RuntimeRouter) validateActiveTurnStepSettings(params *turn.TurnSettingsUpdateParams) error {
	if r == nil || params == nil {
		return nil
	}
	active := r.activeRuntimeTurnStateSnapshot(params.ThreadID, params.TurnID)
	if active == nil || active.Params == nil {
		return nil
	}
	candidate := *active.Params
	candidate.ThreadID = params.ThreadID
	if params.Model != nil {
		candidate.Model = strings.TrimSpace(*params.Model)
	}
	if params.ApprovalsReviewer != nil {
		candidate.ApprovalsReviewer = cloneString(params.ApprovalsReviewer)
	}
	if params.Environments != nil {
		candidate.Environments = cloneMapSlice(params.Environments)
	}
	reviewerExplicit := params.ApprovalsReviewer != nil && strings.TrimSpace(*params.ApprovalsReviewer) != ""
	if err := r.validateTurnStepSettings(&candidate, reviewerExplicit); err != nil {
		return err
	}
	if candidate.ApprovalsReviewer != nil &&
		(params.ApprovalsReviewer == nil || *candidate.ApprovalsReviewer != *params.ApprovalsReviewer) {
		params.ApprovalsReviewer = cloneString(candidate.ApprovalsReviewer)
	}
	return nil
}

func (r *RuntimeRouter) persistedThreadModel(threadID string) string {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return ""
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		return ""
	}
	return strings.TrimSpace(record.Metadata.Model)
}

func (r *RuntimeRouter) threadShellCommandCWDOrEmpty(threadID string) string {
	if r == nil {
		return ""
	}
	cwd, err := r.threadShellCommandCWD(threadID)
	if err != nil {
		return ""
	}
	return cwd
}

func threadSettingsApprovalsReviewerValue(settings *Settings) string {
	if settings == nil {
		return ""
	}
	return strings.TrimSpace(settings.ApprovalsReviewer)
}
