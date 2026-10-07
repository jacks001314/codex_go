package tui

import (
	"strings"

	"codex_go/auth"
)

// Subscription labels for TUI surfaces, preserving status-specific plan detail.
// Rust parity: codex-rs/tui/src/subscription.rs (SubscriptionDisplay).
type SubscriptionDisplay int

const (
	// SubscriptionStatus selects the detailed status-view label variant.
	SubscriptionStatus SubscriptionDisplay = iota
	// SubscriptionAnalytics selects the collapsed analytics-view label variant.
	SubscriptionAnalytics
)

// PlanProMax is the raw ChatGPT plan string for Rust's PlanType::ProMax.
// Go's auth package does not declare a named constant for it yet; the shared
// label table still maps the raw value so every Rust row has a counterpart.
const PlanProMax auth.PlanType = "promax"

// SubscriptionLabel returns the shared subscription label for a plan.
// Rust parity: SubscriptionDisplay::label.
func SubscriptionLabel(plan auth.PlanType, display SubscriptionDisplay) string {
	switch plan {
	case auth.PlanFree:
		return "Free"
	case auth.PlanGo:
		return "Go"
	case auth.PlanPlus:
		return "Plus"
	case auth.PlanPro:
		return "Pro 200"
	case auth.PlanProlite:
		return "Pro 100"
	case PlanProMax:
		return "Pro 500"
	case auth.PlanTeam, auth.PlanSelfServeBusinessUsageBased:
		return "Business"
	case auth.PlanBusiness:
		if display == SubscriptionStatus {
			return "Enterprise"
		}
		return "Business"
	case auth.PlanSelfServeBusinessProlite:
		if display == SubscriptionStatus {
			return "Business Premium"
		}
		return "Business"
	case auth.PlanEnterpriseCBPAutomation:
		if display == SubscriptionStatus {
			return "Enterprise (Automation)"
		}
		return "Enterprise"
	case auth.PlanEnterprise, auth.PlanEnt26, auth.PlanEnterpriseCBPUsageBased:
		return "Enterprise"
	case auth.PlanEdu:
		if display == SubscriptionStatus {
			return "Edu"
		}
		return "Education"
	case auth.PlanEduPlus:
		if display == SubscriptionStatus {
			return "Edu Plus"
		}
		return "Education"
	case auth.PlanEduPro:
		if display == SubscriptionStatus {
			return "Edu Pro"
		}
		return "Education"
	case auth.PlanUnknown:
		if display == SubscriptionStatus {
			return "Unknown"
		}
		return "Account"
	default:
		return subscriptionTitleCase(string(plan))
	}
}

func subscriptionTitleCase(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	first := strings.ToUpper(string(runes[0]))
	rest := ""
	if len(runes) > 1 {
		rest = strings.ToLower(string(runes[1:]))
	}
	return first + rest
}
