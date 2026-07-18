package tui

import (
	"strings"
	"testing"
	"time"

	"codex_go/auth"
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

func TestDefaultTooltipsMatchRustFiltering(t *testing.T) {
	linux := tooltipsForOS(TooltipTargetOSLinux)
	for _, tip := range linux {
		if strings.Contains(tip, "codex app") {
			t.Fatalf("linux tooltip should filter codex app tip: %q", tip)
		}
	}

	windows := tooltipsForOS(TooltipTargetOSWindows)
	var found bool
	for _, tip := range windows {
		if strings.Contains(tip, "codex app") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("windows tooltip pool should keep codex app tip")
	}
}

func TestSelectStartupTooltipMatchesRustPlanBranches(t *testing.T) {
	pro := auth.PlanPro
	got, ok := SelectStartupTooltip(&pro, false, nil, &scriptedTooltipRNG{values: []int{0, 1}})
	if !ok || got != FastTooltip {
		t.Fatalf("paid fast promo = %q, %v", got, ok)
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
