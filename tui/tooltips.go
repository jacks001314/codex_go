package tui

import (
	_ "embed"
	"math/rand"
	"os"
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

	// The copy mirrors Rust's `tooltips.rs` constants verbatim (including the
	// en dash and right single quote in FreeGoTooltip).
	AppTooltip         = "Try the **Desktop app**. Run 'codex app' or visit https://chatgpt.com/codex?app-landing-page=true"
	MacosAppTooltip    = "Run `codex app` to open the Desktop app (it installs on macOS if needed)."
	LinuxAppTooltip    = "Try the **Desktop app** on Linux: install it from https://learn.chatgpt.com/docs/linux/linux-app and run 'chatgpt'."
	FastTooltip        = "*New* Use **/fast** to enable our fastest inference with increased plan usage."
	OtherTooltip       = "*New* Build faster with the **Desktop app**. Run 'codex app' or visit https://chatgpt.com/codex?app-landing-page=true"
	OtherTooltipNonMac = "*New* Build faster with Codex."
	FreeGoTooltip      = "*New* For a limited time, Codex is included in your plan for free \u2013 let\u2019s build together."
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
	return GetTooltipWithKeymap(plan, fastModeEnabled, nil)
}

// GetTooltipWithKeymap mirrors Rust's `get_tooltip(plan, fast_mode_enabled,
// keymap)`: the announcement/promo policy first, then a random tip from the
// keymap-resolved pool.
func GetTooltipWithKeymap(plan *auth.PlanType, fastModeEnabled bool, keymap *KeymapConfig) (string, bool) {
	return selectStartupTooltip(plan, fastModeEnabled, nil, globalTooltipRNG{}, keymap)
}

func SelectStartupTooltip(plan *auth.PlanType, fastModeEnabled bool, announcement *string, rng TooltipRNG) (string, bool) {
	return selectStartupTooltip(plan, fastModeEnabled, announcement, rng, nil)
}

func selectStartupTooltip(plan *auth.PlanType, fastModeEnabled bool, announcement *string, rng TooltipRNG, keymap *KeymapConfig) (string, bool) {
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

	return PickTooltipWithKeymap(rng, keymap)
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
	return PickTooltipWithKeymap(rng, nil)
}

// PickTooltipWithKeymap draws a random tip from the keymap-resolved pool (Rust's
// `pick_tooltip`, which resolves `{key:...}` against the current bindings).
func PickTooltipWithKeymap(rng TooltipRNG, keymap *KeymapConfig) (string, bool) {
	if rng == nil {
		rng = globalTooltipRNG{}
	}
	// Rust's `pick_tooltip` draws from `resolved_tooltips(keymap)`; without a
	// keymap only the key-free tips remain, so a `{key:...}` template can never
	// be shown as a raw placeholder.
	tips := ResolvedTooltips(keymap)
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
		tips = append(tips, line)
	}
	// Rust chains the platform app tip onto the asset tips: macOS always shows
	// the Desktop-app tip, Linux only when a desktop session is present.
	switch targetOS {
	case TooltipTargetOSMacOS:
		tips = append(tips, MacosAppTooltip)
	case TooltipTargetOSLinux:
		if tip, ok := linuxAppTooltip(); ok {
			tips = append(tips, tip)
		}
	}
	return tips
}

func paidAppTooltip() (string, bool) {
	switch CurrentTooltipTargetOS() {
	case TooltipTargetOSMacOS, TooltipTargetOSWindows:
		return AppTooltip, true
	default:
		return linuxAppTooltip()
	}
}

// linuxDesktopSession mirrors Rust's `LinuxDesktopSession::current`: a desktop
// session needs a display server and must not be WSL.
type linuxDesktopSession struct {
	hasDisplay bool
	isWSL      bool
}

func currentLinuxDesktopSession() linuxDesktopSession {
	if runtime.GOOS != "linux" {
		return linuxDesktopSession{}
	}
	hasDisplay := strings.TrimSpace(os.Getenv("DISPLAY")) != "" ||
		strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
	return linuxDesktopSession{hasDisplay: hasDisplay, isWSL: isProbablyWSL()}
}

func linuxAppTooltip() (string, bool) {
	session := currentLinuxDesktopSession()
	if session.hasDisplay && !session.isWSL {
		return LinuxAppTooltip, true
	}
	return "", false
}

// isProbablyWSL mirrors Rust's `is_probably_wsl`: the kernel version is the
// primary signal, with the WSL environment variables as a fallback.
func isProbablyWSL() bool {
	if version, err := os.ReadFile("/proc/version"); err == nil {
		lower := strings.ToLower(string(version))
		if strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl") {
			return true
		}
	}
	return strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != "" ||
		strings.TrimSpace(os.Getenv("WSL_INTEROP")) != ""
}

func paidTooltipPlan(plan auth.PlanType) bool {
	switch plan {
	case auth.PlanPlus, auth.PlanEnterprise, auth.PlanPro, auth.PlanProlite,
		auth.PlanTeam, auth.PlanSelfServeBusinessProlite, auth.PlanSelfServeBusinessUsageBased,
		auth.PlanBusiness, auth.PlanEnt26, auth.PlanEnterpriseCBPAutomation, auth.PlanEnterpriseCBPUsageBased:
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
		auth.PlanTeam, auth.PlanSelfServeBusinessProlite, auth.PlanSelfServeBusinessUsageBased, auth.PlanBusiness,
		auth.PlanEnt26, auth.PlanEnterpriseCBPAutomation, auth.PlanEnterpriseCBPUsageBased, auth.PlanEnterprise, auth.PlanEdu:
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
