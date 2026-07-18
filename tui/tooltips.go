package tui

import (
	_ "embed"
	"math/rand"
	"regexp"
	"runtime"
	"strings"
	"time"

	"codex_go/auth"

	"github.com/pelletier/go-toml/v2"
)

// Rust parity: codex-rs/tui/src/tooltips.rs.
const (
	AnnouncementTipURL = "https://raw.githubusercontent.com/openai/codex/main/announcement_tip.toml"

	AppTooltip         = "Try the **Codex App**. Run 'codex app' or visit https://chatgpt.com/codex?app-landing-page=true"
	FastTooltip        = "*New* Use **/fast** to enable our fastest inference with increased plan usage."
	OtherTooltip       = "*New* Build faster with the **Codex App**. Run 'codex app' or visit https://chatgpt.com/codex?app-landing-page=true"
	OtherTooltipNonMac = "*New* Build faster with Codex."
	FreeGoTooltip      = "*New* For a limited time, Codex is included in your plan for free - let's build together."
)

//go:embed tooltips.txt
var rawTooltips string

type Tooltip struct {
	ID   string
	Text string
}

type TooltipTargetOS string

const (
	TooltipTargetOSLinux   TooltipTargetOS = "linux"
	TooltipTargetOSMacOS   TooltipTargetOS = "macos"
	TooltipTargetOSWindows TooltipTargetOS = "windows"
	TooltipTargetOSUnknown TooltipTargetOS = "unknown"
)

type TooltipRNG interface {
	Intn(n int) int
}

type globalTooltipRNG struct{}

func (globalTooltipRNG) Intn(n int) int {
	return rand.Intn(n)
}

type announcementTipRaw struct {
	Content         string          `toml:"content"`
	FromDate        *string         `toml:"from_date"`
	ToDate          *string         `toml:"to_date"`
	VersionRegex    *string         `toml:"version_regex"`
	TargetApp       *string         `toml:"target_app"`
	TargetPlanTypes []auth.PlanType `toml:"target_plan_types"`
	TargetOSes      []string        `toml:"target_oses"`
}

type announcementTipDocument struct {
	Announcements []announcementTipRaw `toml:"announcements"`
}

type announcementTip struct {
	Content         string
	FromDate        *time.Time
	ToDate          *time.Time
	VersionRegex    *regexp.Regexp
	TargetApp       string
	TargetPlanTypes []auth.PlanType
	TargetOSes      []TooltipTargetOS
}

// DefaultTooltips returns the static startup tips after applying the same
// platform filter as Rust: Codex App tips are only shown on macOS and Windows.
func DefaultTooltips() []string {
	return tooltipsForOS(CurrentTooltipTargetOS())
}

func CurrentTooltipTargetOS() TooltipTargetOS {
	switch runtime.GOOS {
	case "darwin":
		return TooltipTargetOSMacOS
	case "windows":
		return TooltipTargetOSWindows
	case "linux":
		return TooltipTargetOSLinux
	default:
		return TooltipTargetOSUnknown
	}
}

func GetTooltip(plan *auth.PlanType, fastModeEnabled bool) (string, bool) {
	return SelectStartupTooltip(plan, fastModeEnabled, nil, globalTooltipRNG{})
}

func SelectStartupTooltip(plan *auth.PlanType, fastModeEnabled bool, announcement *string, rng TooltipRNG) (string, bool) {
	if announcement != nil {
		if content := strings.TrimSpace(*announcement); content != "" {
			return content, true
		}
	}
	if rng == nil {
		rng = globalTooltipRNG{}
	}

	if rng.Intn(10) < 8 {
		switch {
		case plan != nil && paidTooltipPlan(*plan):
			return PickPaidTooltip(rng, fastModeEnabled)
		case plan != nil && (*plan == auth.PlanGo || *plan == auth.PlanFree):
			return FreeGoTooltip, true
		default:
			if CurrentTooltipTargetOS() == TooltipTargetOSMacOS {
				return OtherTooltip, true
			}
			return OtherTooltipNonMac, true
		}
	}

	return PickTooltip(rng)
}

func PickPaidTooltip(rng TooltipRNG, fastModeEnabled bool) (string, bool) {
	if rng == nil {
		rng = globalTooltipRNG{}
	}
	if fastModeEnabled || rng.Intn(2) == 0 {
		return paidAppTooltip()
	}
	return FastTooltip, true
}

func PickTooltip(rng TooltipRNG) (string, bool) {
	if rng == nil {
		rng = globalTooltipRNG{}
	}
	tips := DefaultTooltips()
	if len(tips) == 0 {
		return "", false
	}
	return tips[rng.Intn(len(tips))], true
}

func ParseAnnouncementTipTOML(text string, plan *auth.PlanType, version string, today time.Time, targetOS TooltipTargetOS) (string, bool) {
	raws, ok := parseAnnouncementRaw(text)
	if !ok {
		return "", false
	}

	var latest string
	var matched bool
	for _, raw := range raws {
		tip, ok := announcementTipFromRaw(raw)
		if !ok {
			continue
		}
		if tip.matches(plan, version, today, targetOS) {
			latest = tip.Content
			matched = true
		}
	}
	return latest, matched
}

func tooltipsForOS(targetOS TooltipTargetOS) []string {
	lines := strings.Split(rawTooltips, "\n")
	tips := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if targetOS != TooltipTargetOSMacOS && targetOS != TooltipTargetOSWindows && strings.Contains(line, "codex app") {
			continue
		}
		tips = append(tips, line)
	}
	return tips
}

func paidAppTooltip() (string, bool) {
	switch CurrentTooltipTargetOS() {
	case TooltipTargetOSMacOS, TooltipTargetOSWindows:
		return AppTooltip, true
	default:
		return "", false
	}
}

func paidTooltipPlan(plan auth.PlanType) bool {
	switch plan {
	case auth.PlanPlus, auth.PlanEnterprise, auth.PlanPro, auth.PlanProlite,
		auth.PlanTeam, auth.PlanSelfServeBusinessUsageBased,
		auth.PlanBusiness, auth.PlanEnterpriseCBPUsageBased:
		return true
	default:
		return false
	}
}

func parseAnnouncementRaw(text string) ([]announcementTipRaw, bool) {
	var doc announcementTipDocument
	if err := toml.Unmarshal([]byte(text), &doc); err == nil && len(doc.Announcements) > 0 {
		return doc.Announcements, true
	}

	var raws []announcementTipRaw
	if err := toml.Unmarshal([]byte(text), &raws); err != nil {
		return nil, false
	}
	return raws, true
}

func announcementTipFromRaw(raw announcementTipRaw) (announcementTip, bool) {
	content := strings.TrimSpace(raw.Content)
	if content == "" {
		return announcementTip{}, false
	}

	fromDate, ok := parseAnnouncementDate(raw.FromDate)
	if !ok {
		return announcementTip{}, false
	}
	toDate, ok := parseAnnouncementDate(raw.ToDate)
	if !ok {
		return announcementTip{}, false
	}

	var versionRegex *regexp.Regexp
	if raw.VersionRegex != nil {
		compiled, err := regexp.Compile(*raw.VersionRegex)
		if err != nil {
			return announcementTip{}, false
		}
		versionRegex = compiled
	}

	plans := make([]auth.PlanType, 0, len(raw.TargetPlanTypes))
	for _, plan := range raw.TargetPlanTypes {
		normalized, ok := normalizeAnnouncementPlan(plan)
		if !ok {
			return announcementTip{}, false
		}
		plans = append(plans, normalized)
	}

	oses := make([]TooltipTargetOS, 0, len(raw.TargetOSes))
	for _, value := range raw.TargetOSes {
		targetOS, ok := parseAnnouncementTargetOS(value)
		if !ok {
			return announcementTip{}, false
		}
		oses = append(oses, targetOS)
	}

	targetApp := "cli"
	if raw.TargetApp != nil {
		targetApp = strings.ToLower(strings.TrimSpace(*raw.TargetApp))
	}

	return announcementTip{
		Content:         content,
		FromDate:        fromDate,
		ToDate:          toDate,
		VersionRegex:    versionRegex,
		TargetApp:       targetApp,
		TargetPlanTypes: plans,
		TargetOSes:      oses,
	}, true
}

func parseAnnouncementDate(raw *string) (*time.Time, bool) {
	if raw == nil {
		return nil, true
	}
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(*raw))
	if err != nil {
		return nil, false
	}
	return &parsed, true
}

func (t announcementTip) matches(plan *auth.PlanType, version string, today time.Time, targetOS TooltipTargetOS) bool {
	if t.VersionRegex != nil && !t.VersionRegex.MatchString(version) {
		return false
	}
	if !t.dateMatches(today) || t.TargetApp != "cli" {
		return false
	}
	if len(t.TargetPlanTypes) > 0 {
		if plan == nil || !containsPlan(t.TargetPlanTypes, *plan) {
			return false
		}
	}
	if len(t.TargetOSes) > 0 && !containsTooltipOS(t.TargetOSes, targetOS) {
		return false
	}
	return true
}

func (t announcementTip) dateMatches(today time.Time) bool {
	day := dateOnly(today)
	if t.FromDate != nil && day.Before(dateOnly(*t.FromDate)) {
		return false
	}
	if t.ToDate != nil && !day.Before(dateOnly(*t.ToDate)) {
		return false
	}
	return true
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func normalizeAnnouncementPlan(plan auth.PlanType) (auth.PlanType, bool) {
	switch plan {
	case auth.PlanFree, auth.PlanGo, auth.PlanPlus, auth.PlanPro, auth.PlanProlite,
		auth.PlanTeam, auth.PlanSelfServeBusinessUsageBased, auth.PlanBusiness,
		auth.PlanEnterpriseCBPUsageBased, auth.PlanEnterprise, auth.PlanEdu:
		return plan, true
	default:
		return "", false
	}
}

func parseAnnouncementTargetOS(value string) (TooltipTargetOS, bool) {
	switch TooltipTargetOS(strings.ToLower(strings.TrimSpace(value))) {
	case TooltipTargetOSLinux:
		return TooltipTargetOSLinux, true
	case TooltipTargetOSMacOS:
		return TooltipTargetOSMacOS, true
	case TooltipTargetOSWindows:
		return TooltipTargetOSWindows, true
	default:
		return "", false
	}
}

func containsPlan(plans []auth.PlanType, plan auth.PlanType) bool {
	for _, candidate := range plans {
		if candidate == plan {
			return true
		}
	}
	return false
}

func containsTooltipOS(oses []TooltipTargetOS, targetOS TooltipTargetOS) bool {
	for _, candidate := range oses {
		if candidate == targetOS {
			return true
		}
	}
	return false
}
