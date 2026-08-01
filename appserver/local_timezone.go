package appserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	localTimezoneOnce sync.Once
	localTimezone     string
)

func localTimezoneName() string {
	localTimezoneOnce.Do(func() {
		localTimezone = resolveLocalTimezoneName()
	})
	return localTimezone
}

func resolveLocalTimezoneName() string {
	if value := strings.TrimSpace(os.Getenv("TZ")); value != "" && !strings.HasPrefix(value, ":") {
		return value
	}
	if runtime.GOOS == "windows" {
		if output, err := exec.Command("tzutil", "/g").Output(); err == nil {
			windowsID := strings.TrimSpace(string(output))
			if iana := windowsTimezoneIANA[windowsID]; iana != "" {
				return iana
			}
			if windowsID != "" {
				return windowsID
			}
		}
	} else {
		if value, err := os.ReadFile("/etc/timezone"); err == nil {
			if name := strings.TrimSpace(string(value)); name != "" {
				return name
			}
		}
		if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
			if marker := strings.Index(filepath.ToSlash(target), "/zoneinfo/"); marker >= 0 {
				return filepath.ToSlash(target)[marker+len("/zoneinfo/"):]
			}
		}
	}
	if name := strings.TrimSpace(time.Local.String()); name != "" && name != "Local" {
		return name
	}
	return "Etc/UTC"
}

var windowsTimezoneIANA = map[string]string{
	"UTC":                            "Etc/UTC",
	"GMT Standard Time":              "Europe/London",
	"Greenwich Standard Time":        "Atlantic/Reykjavik",
	"W. Europe Standard Time":        "Europe/Berlin",
	"Central Europe Standard Time":   "Europe/Budapest",
	"Romance Standard Time":          "Europe/Paris",
	"Central European Standard Time": "Europe/Warsaw",
	"FLE Standard Time":              "Europe/Kyiv",
	"GTB Standard Time":              "Europe/Bucharest",
	"Russian Standard Time":          "Europe/Moscow",
	"Turkey Standard Time":           "Europe/Istanbul",
	"Israel Standard Time":           "Asia/Jerusalem",
	"Arab Standard Time":             "Asia/Riyadh",
	"Arabian Standard Time":          "Asia/Dubai",
	"India Standard Time":            "Asia/Kolkata",
	"China Standard Time":            "Asia/Shanghai",
	"Singapore Standard Time":        "Asia/Singapore",
	"Tokyo Standard Time":            "Asia/Tokyo",
	"Korea Standard Time":            "Asia/Seoul",
	"AUS Eastern Standard Time":      "Australia/Sydney",
	"E. Australia Standard Time":     "Australia/Brisbane",
	"Tasmania Standard Time":         "Australia/Hobart",
	"New Zealand Standard Time":      "Pacific/Auckland",
	"Hawaiian Standard Time":         "Pacific/Honolulu",
	"Alaskan Standard Time":          "America/Anchorage",
	"Pacific Standard Time":          "America/Los_Angeles",
	"Mountain Standard Time":         "America/Denver",
	"US Mountain Standard Time":      "America/Phoenix",
	"Central Standard Time":          "America/Chicago",
	"Eastern Standard Time":          "America/New_York",
	"US Eastern Standard Time":       "America/Indianapolis",
	"Atlantic Standard Time":         "America/Halifax",
	"Newfoundland Standard Time":     "America/St_Johns",
	"E. South America Standard Time": "America/Sao_Paulo",
	"Argentina Standard Time":        "America/Argentina/Buenos_Aires",
	"SA Pacific Standard Time":       "America/Bogota",
	"Pacific SA Standard Time":       "America/Santiago",
	"South Africa Standard Time":     "Africa/Johannesburg",
	"Egypt Standard Time":            "Africa/Cairo",
	"Morocco Standard Time":          "Africa/Casablanca",
}
