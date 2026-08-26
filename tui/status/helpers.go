package status

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/auth"
	codextui "codex_go/tui"
)

func BoolLabel(value bool, enabled string, disabled string) string {
	if value {
		return enabled
	}
	return disabled
}

type ModelDisplayEntry struct {
	Key   string
	Value string
}

func ComposeModelDisplay(modelName string, entries []ModelDisplayEntry) (string, []string) {
	details := []string{}
	if effort, ok := findModelDisplayEntry(entries, "reasoning effort"); ok {
		details = append(details, "reasoning "+strings.ToLower(effort))
	}
	if summary, ok := findModelDisplayEntry(entries, "reasoning summaries"); ok {
		summary = strings.TrimSpace(summary)
		switch {
		case strings.EqualFold(summary, "none"), strings.EqualFold(summary, "off"):
			details = append(details, "summaries off")
		case summary != "":
			details = append(details, "summaries "+strings.ToLower(summary))
		}
	}
	return modelName, details
}

func findModelDisplayEntry(entries []ModelDisplayEntry, key string) (string, bool) {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Value, true
		}
	}
	return "", false
}

func ComposeAccountDisplay(accountDisplay *AccountStatus) *AccountStatus {
	if accountDisplay == nil {
		return nil
	}
	copy := *accountDisplay
	return &copy
}

func PlanTypeDisplayName(planType auth.PlanType) string {
	switch planType {
	case auth.PlanEnterpriseCBPAutomation:
		return "Enterprise (Automation)"
	case auth.PlanTeam, auth.PlanSelfServeBusinessUsageBased:
		return "Business"
	case auth.PlanSelfServeBusinessProlite:
		return "Business Premium"
	case auth.PlanBusiness, auth.PlanEnt26, auth.PlanEnterpriseCBPUsageBased, auth.PlanEnterprise:
		return "Enterprise"
	case auth.PlanProlite:
		return "Pro Lite"
	case auth.PlanEduPlus:
		return "Edu Plus"
	case auth.PlanEduPro:
		return "Edu Pro"
	default:
		return titleCase(string(planType))
	}
}

func FormatTokensCompact(value int64) string {
	if value < 0 {
		value = 0
	}
	if value == 0 {
		return "0"
	}
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}

	scaled := float64(value)
	suffix := "K"
	switch {
	case value >= 1_000_000_000_000:
		scaled = scaled / 1_000_000_000_000
		suffix = "T"
	case value >= 1_000_000_000:
		scaled = scaled / 1_000_000_000
		suffix = "B"
	case value >= 1_000_000:
		scaled = scaled / 1_000_000
		suffix = "M"
	default:
		scaled = scaled / 1_000
	}

	decimals := 0
	switch {
	case scaled < 10:
		decimals = 2
	case scaled < 100:
		decimals = 1
	}

	formatted := fmt.Sprintf("%.*f", decimals, scaled)
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(formatted, "0")
		formatted = strings.TrimRight(formatted, ".")
	}
	return formatted + suffix
}

func FormatDirectoryDisplay(directory string, maxWidth int) string {
	formatted := directory
	if home, err := os.UserHomeDir(); err == nil {
		if rel, ok := relativeToHome(directory, home); ok {
			if rel == "" {
				formatted = "~"
			} else {
				formatted = "~" + string(filepath.Separator) + rel
			}
		}
	}

	if maxWidth >= 0 {
		if maxWidth == 0 {
			return ""
		}
		if codextui.DisplayWidth(formatted) > maxWidth {
			return codextui.CenterTruncatePath(formatted, maxWidth)
		}
	}
	return formatted
}

func FormatResetTimestamp(dt time.Time, capturedAt time.Time) string {
	local := dt.Local()
	capturedLocal := capturedAt.Local()
	timePart := local.Format("15:04")
	year, month, day := local.Date()
	cYear, cMonth, cDay := capturedLocal.Date()
	if year == cYear && month == cMonth && day == cDay {
		return timePart
	}
	return timePart + " on " + local.Format("2 Jan")
}

func titleCase(value string) string {
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

func relativeToHome(path string, home string) (string, bool) {
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(home)
	if cleanPath == cleanHome {
		return "", true
	}
	rel, err := filepath.Rel(cleanHome, cleanPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	if filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func roundToInt64(value float64) int64 {
	return int64(math.Round(value))
}
