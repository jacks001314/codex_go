package tui

import (
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/features"
)

type scriptedTooltipRNG struct {
	values []int
}

func (r *scriptedTooltipRNG) Intn(n int) int {
	if len(r.values) == 0 {
		return 0
	}
	value := r.values[0]
	r.values = r.values[1:]
	if value < 0 {
		value = -value
	}
	if n <= 0 {
		return 0
	}
	return value % n
}

// Rust chains the platform Desktop-app tip onto the shared asset tips: macOS
// always, Linux only for a desktop session, Windows never.
func TestDefaultTooltipsMatchRustPlatformChain(t *testing.T) {
	macos := tooltipsForOS(TooltipTargetOSMacOS)
	if len(macos) == 0 || macos[len(macos)-1] != MacosAppTooltip {
		t.Fatalf("macOS pool tail = %q, want the Desktop-app tip", macos)
	}
	for _, tip := range tooltipsForOS(TooltipTargetOSWindows) {
		if tip == MacosAppTooltip || tip == LinuxAppTooltip {
			t.Fatalf("windows pool should carry no platform app tip: %q", tip)
		}
	}
	for _, tip := range tooltipsForOS(TooltipTargetOSLinux) {
		if tip == MacosAppTooltip {
			t.Fatalf("linux pool should carry no macOS app tip: %q", tip)
		}
	}
}

// Mirrors the Rust snapshot `configured_shortcut_tips_render_at_narrow_width`
// (tui/src/tooltips/snapshots): the default bindings resolve to `tab`,
// `ctrl+o`, `ctrl+t`, `ctrl+g`, `ctrl+r`, and the two alt chords. Go does not
// register `global.find_transcript`, so Rust's contract drops that tip rather
// than showing a raw placeholder.
func TestResolvedTooltipsRenderRustSnapshotLabels(t *testing.T) {
	resolved := ResolvedTooltips(NewKeymapConfig())
	alt := AltKeyLabel()
	want := []string{
		"Press `` tab `` to queue a message when a task is running; otherwise it sends immediately (except `!`).",
		"Use **/copy** or press `` ctrl+o `` to copy the latest agent response as Markdown.",
		"Press `` ctrl+t `` to open the full transcript.",
		"Press `` ctrl+g `` to edit your current draft in an external editor.",
		"Press `` ctrl+r `` to search previously entered prompts.",
		"For models with adjustable reasoning, press `` " + alt + "+. `` to increase reasoning effort or `` " + alt + "+, `` to decrease it.",
	}
	for _, expected := range want {
		found := false
		for _, tip := range resolved {
			if tip == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("resolved pool missing %q:\n%#v", expected, resolved)
		}
	}
	for _, tip := range resolved {
		if strings.Contains(tip, "{key:") {
			t.Fatalf("unresolved placeholder in %q", tip)
		}
	}
}

// Rust's `tooltip_templates` returns `ALL_TOOLTIPS`: the catalog tips followed
// by the experimental feature announcements whose stage carries copy.
func TestTooltipTemplatesAppendExperimentalAnnouncementsLikeRust(t *testing.T) {
	catalog := DefaultTooltips()
	templates := TooltipTemplates()
	if len(templates) < len(catalog) {
		t.Fatalf("templates shorter than the catalog: %#v", templates)
	}
	for index, tip := range catalog {
		if templates[index] != tip {
			t.Fatalf("templates[%d] = %q, want catalog %q", index, templates[index], tip)
		}
	}

	var want []string
	for _, spec := range features.Registry {
		if spec.Stage != features.StageExperimental {
			continue
		}
		if announcement := strings.TrimSpace(spec.ExperimentalAnnouncement); announcement != "" {
			want = append(want, announcement)
		}
	}
	tail := templates[len(catalog):]
	if len(tail) != len(want) {
		t.Fatalf("experimental tail = %#v, want %#v", tail, want)
	}
	for index, tip := range want {
		if tail[index] != tip {
			t.Fatalf("experimental tail[%d] = %q, want %q", index, tail[index], tip)
		}
	}
}

func TestSelectStartupTooltipMatchesRustPlanBranches(t *testing.T) {
	pro := auth.PlanPro
	got, ok := SelectStartupTooltip(&pro, false, nil, &scriptedTooltipRNG{values: []int{0, 1}})
	if !ok || got != FastTooltip {
		t.Fatalf("paid fast promo = %q, %v", got, ok)
	}

	ent26 := auth.PlanEnt26
	got, ok = SelectStartupTooltip(&ent26, false, nil, &scriptedTooltipRNG{values: []int{0, 1}})
	if !ok || got != FastTooltip {
		t.Fatalf("ent26 paid fast promo = %q, %v", got, ok)
	}

	got, ok = SelectStartupTooltip(&pro, true, nil, &scriptedTooltipRNG{values: []int{0}})
	if CurrentTooltipTargetOS() == TooltipTargetOSLinux {
		if ok || got != "" {
			t.Fatalf("linux paid app tooltip = %q, %v, want none", got, ok)
		}
	} else if !ok || got != AppTooltip {
		t.Fatalf("paid app tooltip = %q, %v", got, ok)
	}

	free := auth.PlanFree
	got, ok = SelectStartupTooltip(&free, false, nil, &scriptedTooltipRNG{values: []int{0}})
	if !ok || got != FreeGoTooltip {
		t.Fatalf("free/go tooltip = %q, %v", got, ok)
	}

	got, ok = SelectStartupTooltip(nil, false, nil, &scriptedTooltipRNG{values: []int{0}})
	if !ok || strings.TrimSpace(got) == "" {
		t.Fatalf("unknown plan fallback = %q, %v", got, ok)
	}
}

func TestSelectStartupTooltipUsesAnnouncementBeforeRandom(t *testing.T) {
	announcement := "  announcement wins  "
	got, ok := SelectStartupTooltip(nil, false, &announcement, &scriptedTooltipRNG{values: []int{9}})
	if !ok || got != "announcement wins" {
		t.Fatalf("announcement tooltip = %q, %v", got, ok)
	}
}

func TestSelectStartupTooltipFallsBackToRandomPool(t *testing.T) {
	got, ok := SelectStartupTooltip(nil, false, nil, &scriptedTooltipRNG{values: []int{9, 0}})
	if !ok || got != DefaultTooltips()[0] {
		t.Fatalf("random fallback = %q, %v", got, ok)
	}
}

func TestAnnouncementTipTOMLPicksLastMatching(t *testing.T) {
	toml := `
[[announcements]]
content = "first"
from_date = "2000-01-01"

[[announcements]]
content = "latest match"
version_regex = ".*"
target_app = "cli"

[[announcements]]
content = "should not match"
to_date = "2000-01-01"
`

	got, ok := ParseAnnouncementTipTOML(toml, nil, "0.0.0", dateForTooltipTest("2026-07-08"), TooltipTargetOSWindows)
	if !ok || got != "latest match" {
		t.Fatalf("announcement = %q, %v", got, ok)
	}
}

func TestAnnouncementTipTOMLMatchesTargetPlanAndOS(t *testing.T) {
	toml := `
[[announcements]]
content = "all plans"

[[announcements]]
content = "pro announcement"
target_plan_types = ["pro", "enterprise"]
target_oses = ["windows"]

[[announcements]]
content = "free announcement"
target_plan_types = ["free"]
`

	pro := auth.PlanPro
	got, ok := ParseAnnouncementTipTOML(toml, &pro, "0.0.0", dateForTooltipTest("2026-07-08"), TooltipTargetOSWindows)
	if !ok || got != "pro announcement" {
		t.Fatalf("pro/windows announcement = %q, %v", got, ok)
	}

	got, ok = ParseAnnouncementTipTOML(toml, &pro, "0.0.0", dateForTooltipTest("2026-07-08"), TooltipTargetOSLinux)
	if !ok || got != "all plans" {
		t.Fatalf("pro/linux announcement = %q, %v", got, ok)
	}
}

func TestAnnouncementTipTOMLAcceptsEnt26Plan(t *testing.T) {
	toml := `
[[announcements]]
content = "enterprise announcement"
target_plan_types = ["ent26"]
`

	plan := auth.PlanEnt26
	got, ok := ParseAnnouncementTipTOML(toml, &plan, "0.0.0", dateForTooltipTest("2026-07-08"), TooltipTargetOSWindows)
	if !ok || got != "enterprise announcement" {
		t.Fatalf("ent26 announcement = %q, %v", got, ok)
	}
}

func TestAnnouncementTipTOMLRejectsInvalidEntries(t *testing.T) {
	toml := `
[[announcements]]
content = "all plans"

[[announcements]]
content = "bad plan"
target_plan_types = ["prp"]

[[announcements]]
content = "bad os"
target_oses = ["amiga"]

[[announcements]]
content = "bad date"
from_date = "not-a-date"

[[announcements]]
content = "bad regex"
version_regex = "["
`

	unknown := auth.PlanUnknown
	got, ok := ParseAnnouncementTipTOML(toml, &unknown, "0.0.0", dateForTooltipTest("2026-07-08"), TooltipTargetOSWindows)
	if !ok || got != "all plans" {
		t.Fatalf("invalid entries should be skipped, got %q, %v", got, ok)
	}
}

func TestAnnouncementTipTOMLDateAndVersionGates(t *testing.T) {
	toml := `
[[announcements]]
content = "old"
to_date = "2026-07-08"

[[announcements]]
content = "future"
from_date = "2026-07-09"

[[announcements]]
content = "versioned"
version_regex = "^1\\.2\\."
`

	got, ok := ParseAnnouncementTipTOML(toml, nil, "1.2.3", dateForTooltipTest("2026-07-08"), TooltipTargetOSWindows)
	if !ok || got != "versioned" {
		t.Fatalf("date/version announcement = %q, %v", got, ok)
	}

	got, ok = ParseAnnouncementTipTOML(toml, nil, "2.0.0", dateForTooltipTest("2026-07-08"), TooltipTargetOSWindows)
	if ok || got != "" {
		t.Fatalf("date/version no match = %q, %v", got, ok)
	}
}

func dateForTooltipTest(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return parsed
}

// Mirrors Rust's tooltips/keybinding_tests.rs: placeholders follow the current
// bindings, and a tip is skipped when any referenced shortcut is invalid or
// unbound.
func TestRenderTooltipSubstitutesConfiguredBindingsLikeRust(t *testing.T) {
	defaults := NewKeymapConfig()
	got, ok := RenderTooltip("Press {key:global.open_transcript} to open the full transcript.", defaults)
	if !ok || got != "Press `` ctrl+t `` to open the full transcript." {
		t.Fatalf("default tip = %q, %v", got, ok)
	}

	remapped := NewKeymapConfig()
	if err := remapped.Set("global", "open_transcript", []string{"f12"}); err != nil {
		t.Fatal(err)
	}
	got, ok = RenderTooltip("Press {key:global.open_transcript} to open the full transcript.", remapped)
	if !ok || got != "Press `` f12 `` to open the full transcript." {
		t.Fatalf("remapped tip = %q, %v", got, ok)
	}

	// A key-free template renders without a keymap.
	if got, ok := RenderTooltip("Use /copy to copy a response.", nil); !ok || got != "Use /copy to copy a response." {
		t.Fatalf("key-free tip = %q, %v", got, ok)
	}
}

func TestRenderTooltipSkipsUnboundOrInvalidPlaceholdersLikeRust(t *testing.T) {
	unbound := NewKeymapConfig()
	if err := unbound.Set("global", "copy", []string{}); err != nil {
		t.Fatal(err)
	}
	for _, template := range []string{
		"Press {key:global.copy} to copy.",
		"Press {key:global.open_transcript} or {key:global.copy}.",
		"Press {key:global.missing}.",
		"Press {key:missing.copy}.",
		"Press {key:copy}.",
		"Press {key:global.copy",
	} {
		if got, ok := RenderTooltip(template, unbound); ok {
			t.Fatalf("template %q resolved to %q, want skipped", template, got)
		}
	}
	if got, ok := RenderTooltip("Press {key:global.copy}.", nil); ok {
		t.Fatalf("nil keymap resolved to %q, want skipped", got)
	}
}

// The reasoning tip needs both of its shortcuts bound; unbinding either one
// drops the whole tip.
func TestResolvedTooltipsDropTipsWithAnUnboundPlaceholderLikeRust(t *testing.T) {
	all := NewKeymapConfig()
	resolved := ResolvedTooltips(all)
	var found bool
	for _, tip := range resolved {
		if strings.Contains(tip, "to improve reasoning") || strings.Contains(tip, "reasoning effort") {
			found = true
		}
		if strings.Contains(tip, "{key:") {
			t.Fatalf("resolved tip still carries a placeholder: %q", tip)
		}
	}
	if !found {
		t.Fatalf("reasoning tip missing from the resolved pool: %#v", resolved)
	}

	for _, action := range []string{"increase_reasoning_effort", "decrease_reasoning_effort"} {
		unbound := NewKeymapConfig()
		if err := unbound.Set("chat", action, []string{}); err != nil {
			t.Fatal(err)
		}
		for _, tip := range ResolvedTooltips(unbound) {
			if strings.Contains(tip, "For models with adjustable reasoning") {
				t.Fatalf("unbound %s kept the reasoning tip: %q", action, tip)
			}
		}
	}
}
