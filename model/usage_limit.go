package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// This file ports Rust's `UsageLimitReachedError` (#48174): the evidence a 429
// usage-limit response carries, and the recovery copy its `Display` renders. Rust
// never shows the response body's message for this kind - it rebuilds the copy
// from the plan type, the reset instant, the promo message, the rate-limit
// reached type and the active limit's name.

const (
	// usageLimitActiveLimitHeader names the limit whose headers describe this
	// response (Rust `ACTIVE_LIMIT_HEADER`).
	usageLimitActiveLimitHeader = "x-codex-active-limit"
	// usageLimitPromoMessageHeader carries the server's recovery promotion copy.
	usageLimitPromoMessageHeader = "x-codex-promo-message"
	// usageLimitRateLimitReachedTypeHeader selects the workspace-specific copy.
	usageLimitRateLimitReachedTypeHeader = "x-codex-rate-limit-reached-type"
)

// usageLimitEvidence mirrors the inputs of Rust's `UsageLimitReachedError`.
type usageLimitEvidence struct {
	PlanType             string
	ResetsAt             *time.Time
	LimitWindowMinutes   *uint16
	LimitName            string
	PromoMessage         string
	RateLimitReachedType string
}

// usageLimitRetryNow supplies "now" for the retry timestamp. Rust's
// `now_for_retry` is injectable for the same reason: the same-day branch depends
// on the local date.
var usageLimitRetryNow = time.Now

// usageLimitErrorBody mirrors Rust's `UsageErrorBody`: the 429 body's `error`
// object with Rust's declared field types. A malformed value in any field makes
// Rust's `serde_json::from_str::<UsageErrorResponse>` fail and the response fall
// through to the generic retry-limit classification, so the decode is strict.
type usageLimitErrorBody struct {
	Code     *string `json:"code"`
	Type     *string `json:"type"`
	PlanType *string `json:"plan_type"`
	ResetsAt *int64  `json:"resets_at"`
	// LimitWindowMinutes stays raw: Rust types it as `Option<Value>` and keeps
	// only unsigned integers that fit in u16.
	LimitWindowMinutes json.RawMessage `json:"limit_window_minutes"`
}

// decodeUsageLimitErrorBody decodes a 429 body into Rust's `UsageErrorResponse`
// shape. ok is false when the body is not an object carrying an `error` object
// with Rust's field types.
func decodeUsageLimitErrorBody(body []byte) (usageLimitErrorBody, bool) {
	var payload struct {
		Error *usageLimitErrorBody `json:"error"`
	}
	if len(body) == 0 {
		return usageLimitErrorBody{}, false
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Error == nil {
		return usageLimitErrorBody{}, false
	}
	return *payload.Error, true
}

// usageLimitErrorBodyIsQuotaError mirrors Rust's quota arm inside the single
// strict `UsageErrorResponse` decode (#44492): the `insufficient_quota` error
// type or one of the quota error codes.
func usageLimitErrorBodyIsQuotaError(body usageLimitErrorBody) bool {
	if body.Type != nil && strings.TrimSpace(*body.Type) == "insufficient_quota" {
		return true
	}
	if body.Code == nil {
		return false
	}
	return responsesQuotaErrorCodes[strings.TrimSpace(*body.Code)]
}

// usageLimitEvidenceFromResponse assembles the evidence a usage-limit response
// carries: the body's plan type, reset instant and window, plus the headers'
// promo message, reached type and active limit name.
func usageLimitEvidenceFromResponse(errorBody usageLimitErrorBody, headers http.Header) usageLimitEvidence {
	evidence := usageLimitEvidence{
		PromoMessage:         responseHeaderValue(headers, usageLimitPromoMessageHeader),
		RateLimitReachedType: usageLimitRateLimitReachedType(headers),
	}
	if errorBody.PlanType != nil {
		evidence.PlanType = strings.TrimSpace(*errorBody.PlanType)
	}
	if errorBody.ResetsAt != nil {
		resetsAt := time.Unix(*errorBody.ResetsAt, 0).UTC()
		evidence.ResetsAt = &resetsAt
	}
	var window any
	if len(errorBody.LimitWindowMinutes) > 0 {
		_ = json.Unmarshal(errorBody.LimitWindowMinutes, &window)
	}
	evidence.LimitWindowMinutes = usageLimitWindowMinutesFromValue(window)
	if snapshot := usageLimitActiveSnapshot(headers); snapshot != nil {
		evidence.LimitName = strings.TrimSpace(snapshot.LimitName)
	}
	return evidence
}

// usageLimitRateLimitReachedType reads the header Rust parses with
// `parse_rate_limit_reached_type`: an unknown value is no type at all.
func usageLimitRateLimitReachedType(headers http.Header) string {
	switch value := responseHeaderValue(headers, usageLimitRateLimitReachedTypeHeader); value {
	case "rate_limit_reached",
		"workspace_owner_credits_depleted",
		"workspace_member_credits_depleted",
		"workspace_owner_usage_limit_reached",
		"workspace_member_usage_limit_reached":
		return value
	default:
		return ""
	}
}

// usageLimitActiveSnapshot parses the headers of the limit named by
// `x-codex-active-limit`, defaulting to `codex` (Rust's
// `parse_rate_limit_for_limit`).
func usageLimitActiveSnapshot(headers http.Header) *ResponsesRateLimitSnapshot {
	if headers == nil {
		return nil
	}
	limitID := responseHeaderValue(headers, usageLimitActiveLimitHeader)
	if limitID == "" {
		limitID = "codex"
	}
	return parseResponsesRateLimit(headers, limitID)
}

// usageLimitReachedMessage mirrors Rust's `UsageLimitReachedError` Display: the
// recovery copy the user sees for a usage-limit failure.
func usageLimitReachedMessage(evidence usageLimitEvidence) string {
	// Reserve is a fallback for exhausted ordinary usage, so the standard
	// promo/plan copy below is kept instead of suggesting another model.
	if limitName := strings.TrimSpace(evidence.LimitName); limitName != "" &&
		!strings.EqualFold(limitName, "codex") &&
		!strings.EqualFold(limitName, "gpt-reserve") {
		return "You’ve hit your usage limit for " + limitName + ". Switch to another model now," +
			retrySuffixAfterOr(evidence.ResetsAt)
	}

	switch evidence.RateLimitReachedType {
	case "workspace_owner_credits_depleted":
		return "Your workspace is out of credits. Add credits to continue."
	case "workspace_member_credits_depleted":
		return "Your workspace is out of credits. Ask your workspace owner to refill in order to continue."
	case "workspace_owner_usage_limit_reached":
		return "You hit your spend cap set in your workspace. Increase your spend cap to continue."
	case "workspace_member_usage_limit_reached":
		return "You hit your spend cap set by the owner of your workspace. Ask an owner to increase your spend cap to continue."
	}
	// RateLimitReached and unknown types intentionally use the promo/plan copy.

	if promo := strings.TrimSpace(evidence.PromoMessage); promo != "" {
		return "You’ve hit your usage limit. " + promo + "," + retrySuffixAfterOr(evidence.ResetsAt)
	}

	switch usageLimitPlanCopy(evidence.PlanType) {
	case usageLimitPlanCopyUpgradeToPro:
		return "You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage to purchase more credits" +
			retrySuffixAfterOr(evidence.ResetsAt)
	case usageLimitPlanCopyAdmin:
		return "You’ve hit your usage limit. To get more access now, send a request to your admin" +
			retrySuffixAfterOr(evidence.ResetsAt)
	case usageLimitPlanCopyUpgradeToPlus:
		return "You’ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus)," +
			retrySuffixAfterOr(evidence.ResetsAt)
	case usageLimitPlanCopyPurchaseCredits:
		return "You’ve hit your usage limit. Visit https://chatgpt.com/codex/settings/usage to purchase more credits" +
			retrySuffixAfterOr(evidence.ResetsAt)
	default:
		return "You’ve hit your usage limit." + retrySuffix(evidence.ResetsAt)
	}
}

type usageLimitPlanCopyKind int

const (
	usageLimitPlanCopyGeneric usageLimitPlanCopyKind = iota
	usageLimitPlanCopyUpgradeToPro
	usageLimitPlanCopyAdmin
	usageLimitPlanCopyUpgradeToPlus
	usageLimitPlanCopyPurchaseCredits
)

// usageLimitPlanCopy classifies the body's `plan_type`. Rust deserializes it
// through a serde enum whose known values are case-sensitive raw plan names, so
// an unrecognized spelling takes the generic copy.
func usageLimitPlanCopy(planType string) usageLimitPlanCopyKind {
	switch planType {
	case "plus":
		return usageLimitPlanCopyUpgradeToPro
	case "team", "self_serve_business_prolite", "self_serve_business_usage_based",
		"business", "ent26", "enterprise_cbp_automation", "enterprise_cbp_usage_based":
		return usageLimitPlanCopyAdmin
	case "free", "go":
		return usageLimitPlanCopyUpgradeToPlus
	case "pro", "prolite", "promax":
		return usageLimitPlanCopyPurchaseCredits
	default:
		return usageLimitPlanCopyGeneric
	}
}

func retrySuffix(resetsAt *time.Time) string {
	if resetsAt == nil {
		return " Try again later."
	}
	return " Try again at " + formatUsageLimitRetryTimestamp(*resetsAt) + "."
}

func retrySuffixAfterOr(resetsAt *time.Time) string {
	if resetsAt == nil {
		return " or try again later."
	}
	return " or try again at " + formatUsageLimitRetryTimestamp(*resetsAt) + "."
}

// formatUsageLimitRetryTimestamp mirrors Rust's `format_retry_timestamp`: the
// local clock time when the reset lands today, the local date and time
// otherwise.
func formatUsageLimitRetryTimestamp(resetsAt time.Time) string {
	localReset := resetsAt.Local()
	localNow := usageLimitRetryNow().Local()
	if localReset.Year() == localNow.Year() && localReset.YearDay() == localNow.YearDay() {
		return localReset.Format("3:04 PM")
	}
	return fmt.Sprintf("%s %d%s, %d %s",
		localReset.Format("Jan"),
		localReset.Day(),
		retryDaySuffix(localReset.Day()),
		localReset.Year(),
		localReset.Format("3:04 PM"),
	)
}

// retryDaySuffix mirrors Rust's `day_suffix`.
func retryDaySuffix(day int) string {
	if day >= 11 && day <= 13 {
		return "th"
	}
	switch day % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	default:
		return "th"
	}
}
