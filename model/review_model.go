package model

import "strings"

// lowReasoningEffort is Rust `ReasoningEffort::Low`'s wire value. The review
// selection prefers it whenever the selected model can express it.
const lowReasoningEffort = "low"

// ApprovalReviewModel mirrors Rust codex_guardian_reviewer::ReviewModel: the
// catalog-backed reviewer a Guardian review must sample with.
type ApprovalReviewModel struct {
	// Model is the model id the reviewer must use.
	Model string
	// ReasoningEffort is the request-level effort the reviewer must use. An
	// empty value leaves the model's own default in place.
	ReasoningEffort string
	// DefaultReviewModelID is the provider's preferred review model, kept so
	// callers can tell an override from that default.
	DefaultReviewModelID string
	// CatalogContainsAutoReview reports that the catalog lists the preferred
	// review model.
	CatalogContainsAutoReview bool
	// ModelOverridden reports that the parent model declared an override.
	ModelOverridden bool
	// ModelOverride is the declared override, if any.
	ModelOverride string
}

// SelectApprovalReviewModel mirrors Rust
// guardian-reviewer::select_review_model. The parent model's
// `auto_review_model_override` wins, then the provider's preferred review model
// when the catalog lists it, and the parent model is the last-resort fallback.
// The reviewer always prefers the `low` effort when the selected model supports
// it, otherwise it keeps that model's default effort - or, in the fallback
// branch, the parent's selected effort (Rust #46292 keeps that value out of the
// managed reasoning-effort override machinery).
func SelectApprovalReviewModel(parent *ModelInfo, parentReasoningEffort string, defaultReviewModelID string, available []ModelPreset) ApprovalReviewModel {
	preferredEffort := func(supportsLow bool, fallback string) string {
		if supportsLow {
			return lowReasoningEffort
		}
		return strings.TrimSpace(fallback)
	}
	selection := ApprovalReviewModel{DefaultReviewModelID: strings.TrimSpace(defaultReviewModelID)}
	if parent != nil {
		selection.ModelOverride = strings.TrimSpace(parent.AutoReviewModelOverride)
	}
	selection.ModelOverridden = selection.ModelOverride != ""
	for _, preset := range available {
		if strings.TrimSpace(preset.Model) == selection.DefaultReviewModelID && selection.DefaultReviewModelID != "" {
			selection.CatalogContainsAutoReview = true
			break
		}
	}
	reviewModelID := selection.DefaultReviewModelID
	if selection.ModelOverridden {
		reviewModelID = selection.ModelOverride
	}
	for _, preset := range available {
		if strings.TrimSpace(preset.Model) != reviewModelID || reviewModelID == "" {
			continue
		}
		selection.Model = reviewModelID
		selection.ReasoningEffort = preferredEffort(
			supportsLowReasoningEffort(preset.SupportedReasoningLevels),
			preset.DefaultReasoningLevel,
		)
		return selection
	}
	// The catalog does not list the selected review model, so the reviewer
	// inherits the parent's model and effort (Rust's
	// `model_override.unwrap_or(parent_model.slug)`).
	selection.Model = ""
	if selection.ModelOverridden {
		selection.Model = selection.ModelOverride
	}
	if selection.Model == "" && parent != nil {
		selection.Model = strings.TrimSpace(parent.Slug)
	}
	if parent == nil {
		return selection
	}
	selection.ReasoningEffort = preferredEffort(
		supportsLowReasoningEffort(parent.SupportedReasoningLevels),
		firstNonEmptyReasoningEffort(parentReasoningEffort, parent.DefaultReasoningLevel),
	)
	return selection
}

// supportsLowReasoningEffort mirrors Rust's
// `supported_reasoning_efforts.iter().any(|effort| effort.effort == Low)`.
func supportsLowReasoningEffort(levels []string) bool {
	for _, level := range levels {
		if strings.EqualFold(strings.TrimSpace(level), lowReasoningEffort) {
			return true
		}
	}
	return false
}

func firstNonEmptyReasoningEffort(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
