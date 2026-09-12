package app

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode"

	"codex_go/auth"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/tui/status"
)

// Rust parity: codex-rs/tui/src/backend_banners(.rs, /actions.rs). The backend
// (through the existing account usage read) owns banner eligibility, copy,
// actions, and ordered model fallback instructions; the TUI parses supported,
// bounded content before rendering or acting on it.
const (
	BackendBannerLunaReserve    = "luna_reserve"
	BackendBannerRecoveryViewID = "luna-reserve-recovery"

	backendBannerMaxCTAs            = 8
	backendBannerMaxTitleBytes      = 1024
	backendBannerMaxDescriptionByte = 4096
	backendBannerMaxTitleLines      = 3
	backendBannerMaxDescriptionLine = 12
	backendBannerMaxSlugBytes       = 256
	backendBannerMaxFallbackSlugs   = 16

	backendBannerUsageURL          = "https://chatgpt.com/codex/settings/usage"
	backendBannerWorkspaceUsageURL = "https://chatgpt.com/admin/usage-limits/workspace"
)

// BannerPresentation mirrors Rust BannerPresentation.
type BannerPresentation string

const (
	BannerPresentationInline      BannerPresentation = "inline"
	BannerPresentationDismissible BannerPresentation = "dismissible"
)

// BackendBannerCTA is one backend-supplied call to action.
type BackendBannerCTA struct {
	Action string `json:"action"`
	Label  string `json:"label"`
}

// BackendBanner is the backend banner payload (Rust BackendBanner). The account
// id and plan type are supplied by the usage read, not the payload.
type BackendBanner struct {
	BannerType         string             `json:"banner_type"`
	Title              string             `json:"title"`
	Description        string             `json:"description"`
	CTAs               []BackendBannerCTA `json:"ctas"`
	ResetAt            *int64             `json:"reset_at,omitempty"`
	ModelSlug          *string            `json:"model_slug,omitempty"`
	BlockedModelSlug   *string            `json:"blocked_model_slug,omitempty"`
	FallbackModelSlugs []string           `json:"fallback_model_slugs,omitempty"`
	Presentation       BannerPresentation `json:"presentation,omitempty"`
	RequestURL         *string            `json:"request_url,omitempty"`
	AccountID          string             `json:"-"`
	PlanType           auth.PlanType      `json:"-"`
}

// ParseBackendBanner parses supported, bounded banner content (Rust
// BackendBanner::parse). Unsupported or unrenderable payloads are rejected
// rather than partially rendered.
func ParseBackendBanner(raw map[string]any) (BackendBanner, bool) {
	if raw == nil {
		return BackendBanner{}, false
	}
	for _, required := range []string{"banner_type", "title", "description", "ctas"} {
		if _, present := raw[required]; !present {
			return BackendBanner{}, false
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return BackendBanner{}, false
	}
	var banner BackendBanner
	if err := json.Unmarshal(data, &banner); err != nil {
		return BackendBanner{}, false
	}
	banner.Presentation = BannerPresentationInline
	if rawPresentation, present := raw["presentation"]; present {
		text, ok := rawPresentation.(string)
		if !ok {
			return BackendBanner{}, false
		}
		switch BannerPresentation(text) {
		case BannerPresentationInline, BannerPresentationDismissible:
			banner.Presentation = BannerPresentation(text)
		default:
			return BackendBanner{}, false
		}
	}
	if !validBackendBannerContent(banner) {
		return BackendBanner{}, false
	}
	return banner, true
}

func validBackendBannerContent(banner BackendBanner) bool {
	validSlug := func(slug string) bool {
		return strings.TrimSpace(slug) != "" && len(slug) <= backendBannerMaxSlugBytes &&
			!strings.ContainsFunc(slug, unicode.IsControl)
	}
	if strings.TrimSpace(banner.Title) == "" ||
		len(banner.Title) > backendBannerMaxTitleBytes ||
		backendBannerLineCount(banner.Title) > backendBannerMaxTitleLines ||
		len(banner.Description) > backendBannerMaxDescriptionByte ||
		backendBannerLineCount(banner.Description) > backendBannerMaxDescriptionLine ||
		len(banner.CTAs) > backendBannerMaxCTAs ||
		len(banner.FallbackModelSlugs) > backendBannerMaxFallbackSlugs {
		return false
	}
	if banner.BlockedModelSlug != nil && !validSlug(*banner.BlockedModelSlug) {
		return false
	}
	for _, slug := range banner.FallbackModelSlugs {
		if !validSlug(slug) {
			return false
		}
	}
	return true
}

// backendBannerLineCount mirrors Rust str::lines(): a trailing newline does not
// add an empty final line.
func backendBannerLineCount(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

// BannerActionKind identifies what a resolved CTA does.
type BannerActionKind string

const (
	BannerActionOpenURL     BannerActionKind = "open_url"
	BannerActionNotifyOwner BannerActionKind = "notify_owner"
	BannerActionResetUsage  BannerActionKind = "reset_usage"
)

// BannerAction is a resolved CTA action (Rust BannerAction).
type BannerAction struct {
	Kind       BannerActionKind
	URL        string
	CreditType chatwidget.AddCreditsNudgeCreditType
}

// ResolveBannerAction maps a backend CTA name to an existing CLI action or a
// validated browser destination (Rust BackendBanner::resolve_action).
func (b BackendBanner) ResolveBannerAction(action string) (BannerAction, bool) {
	switch action {
	case "notify_owner", "contact_owner":
		return BannerAction{Kind: BannerActionNotifyOwner, CreditType: chatwidget.AddCreditsNudgeCredits}, true
	case "request_increase":
		if b.RequestURL == nil {
			return BannerAction{Kind: BannerActionNotifyOwner, CreditType: chatwidget.AddCreditsNudgeUsageLimit}, true
		}
		destination, ok := sanitizeBannerDestination(*b.RequestURL)
		if !ok {
			return BannerAction{}, false
		}
		return BannerAction{Kind: BannerActionOpenURL, URL: destination}, true
	case "reset_usage":
		return BannerAction{Kind: BannerActionResetUsage}, true
	}

	workspaceAccount := b.PlanType.IsWorkspaceAccount()
	var destination string
	switch action {
	case "add_credits", "buy_credits":
		if workspaceAccount {
			destination = "https://chatgpt.com/admin/billing?codex_credit_action=add_credits"
		} else {
			destination = backendBannerUsageURL + "?credits_modal=true"
		}
	case "buy_reset":
		destination = "https://chatgpt.com/codex/purchase/reset"
	case "view_usage", "request_increase_usage_settings":
		destination = backendBannerUsageURL
	case "view_workspace_usage", "increase_spend_cap":
		destination = backendBannerWorkspaceUsageURL
	case "open_plus_pricing_web":
		destination = "https://chatgpt.com/explore/plus"
	case "open_pro_pricing_web":
		destination = "https://chatgpt.com/explore/pro"
	case "open_pricing_dialog":
		parsed, err := url.Parse("https://chatgpt.com/")
		if err != nil {
			return BannerAction{}, false
		}
		target := "plus"
		switch b.PlanType {
		case auth.PlanPlus, auth.PlanProlite:
			target = "pro"
		}
		query := parsed.Query()
		query.Set("cta_tab", "personal")
		query.Set("highlight_plan", target)
		if b.PlanType == auth.PlanProlite {
			query.Set("pro_variant", "2x")
		}
		parsed.RawQuery = query.Encode()
		parsed.Fragment = "pricing"
		destination = parsed.String()
	default:
		// Desktop-only referral and Premium dialogs need their own CLI flow.
		return BannerAction{}, false
	}
	parsed, err := url.Parse(destination)
	if err != nil {
		return BannerAction{}, false
	}
	if strings.HasPrefix(parsed.Path, "/admin/") {
		if strings.TrimSpace(b.AccountID) == "" {
			return BannerAction{}, false
		}
		// The existing admin route selects the requested workspace before opening
		// the modal.
		query := parsed.Query()
		query.Set("account_id", strings.TrimSpace(b.AccountID))
		parsed.RawQuery = query.Encode()
	}
	return BannerAction{Kind: BannerActionOpenURL, URL: parsed.String()}, true
}

// sanitizeBannerDestination validates a backend-supplied request URL.
func sanitizeBannerDestination(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
		return "", false
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); parsed.User.Username() != "" || hasPassword {
			return "", false
		}
	}
	return parsed.String(), true
}

// BackendBannerActionable is one rendered CTA: its label plus the resolved
// action (Rust ActionableBanner's SelectionItem).
type BackendBannerActionable struct {
	Label  string
	Action BannerAction
}

// ActionableActions mirrors Rust actionable_banner's action list: CTAs are
// dropped when their label is unusable or their action does not resolve.
func (b BackendBanner) ActionableActions() []BackendBannerActionable {
	actions := make([]BackendBannerActionable, 0, len(b.CTAs))
	for _, cta := range b.CTAs {
		label := cta.Label
		if strings.TrimSpace(label) == "" || len(label) > backendBannerMaxSlugBytes ||
			strings.ContainsFunc(label, unicode.IsControl) {
			continue
		}
		action, ok := b.ResolveBannerAction(cta.Action)
		if !ok {
			continue
		}
		actions = append(actions, BackendBannerActionable{Label: label, Action: action})
	}
	return actions
}

// Dismissible reports whether the banner can be dismissed.
func (b BackendBanner) Dismissible() bool {
	return b.Presentation == BannerPresentationDismissible
}

// ResetTime renders the banner's reset timestamp for "{time}" substitution
// (Rust actionable_banner's reset_time).
func (b BackendBanner) ResetTime(now time.Time) string {
	if b.ResetAt == nil {
		return ""
	}
	return status.FormatResetTimestamp(time.Unix(*b.ResetAt, 0), now)
}

// BannerCopy strips control characters (keeping newlines) and substitutes the
// reset time (Rust actionable_banner's copy).
func (b BackendBanner) BannerCopy(text string, now time.Time) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range text {
		if r == '\n' || !unicode.IsControl(r) {
			builder.WriteRune(r)
		}
	}
	copied := builder.String()
	if reset := b.ResetTime(now); reset != "" {
		copied = strings.ReplaceAll(copied, "{time}", reset)
	}
	return copied
}
