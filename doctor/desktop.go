package doctor

import (
	"fmt"
	"strings"
)

// Desktop diagnostics mirror Rust cli/src/doctor/desktop.rs (#39060/#39067/
// #39074): installation/version (desktop.app.version), platform security
// enforcement (desktop.security), and update state (desktop.updates). codex_go
// has no ChatGPT desktop product surface, so the probes report the local
// desktop application installation when present and otherwise an explicit
// no-installation/not-applicable result instead of drifting silently.

type desktopAppInstallation struct {
	Name     string
	Path     string
	Version  string
	Platform string
}

var (
	desktopInstallationProbe = func() []desktopAppInstallation { return desktopAppInstallations() }
	desktopSecurityProbe     = func(apps []desktopAppInstallation) string { return desktopSecurityEnforcement(apps) }
	desktopUpdatesProbe      = func(apps []desktopAppInstallation) string { return desktopUpdateState(apps) }
)

func desktopChecks() []*DoctorCheck {
	apps := desktopInstallationProbe()
	checks := []*DoctorCheck{desktopAppVersionCheck(apps)}
	checks = append(checks, desktopSecurityCheck(apps))
	checks = append(checks, desktopUpdatesCheck(apps))
	return checks
}

func desktopAppVersionCheck(apps []desktopAppInstallation) *DoctorCheck {
	check := NewCheck("desktop.app.version", "desktop", CheckStatusOK, "no desktop application installation detected")
	if len(apps) == 0 {
		return check.Detail("the ChatGPT desktop application is not installed on this machine")
	}
	check.Summary = "the desktop application is installed"
	for i := range apps {
		check.Detail(desktopInstallationDetail(&apps[i]))
	}
	return check
}

func desktopSecurityCheck(apps []desktopAppInstallation) *DoctorCheck {
	check := NewCheck("desktop.security", "desktop", CheckStatusOK, "no desktop application installation to secure")
	detail := desktopSecurityProbe(apps)
	if strings.TrimSpace(detail) != "" {
		check.Detail(detail)
	}
	return check
}

func desktopUpdatesCheck(apps []desktopAppInstallation) *DoctorCheck {
	check := NewCheck("desktop.updates", "desktop", CheckStatusOK, "no managed desktop application update channel")
	detail := desktopUpdatesProbe(apps)
	if strings.TrimSpace(detail) != "" {
		check.Detail(detail)
	}
	return check
}

func desktopInstallationDetail(app *desktopAppInstallation) string {
	if app == nil {
		return ""
	}
	parts := []string{"name: " + strings.TrimSpace(app.Name)}
	if path := strings.TrimSpace(app.Path); path != "" {
		parts = append(parts, "path: "+path)
	}
	if version := strings.TrimSpace(app.Version); version != "" {
		parts = append(parts, "version: "+version)
	}
	if platform := strings.TrimSpace(app.Platform); platform != "" {
		parts = append(parts, "platform: "+platform)
	}
	return strings.Join(parts, "; ")
}

func desktopSecurityEnforcement(apps []desktopAppInstallation) string {
	if len(apps) == 0 {
		return "desktop security enforcement is N/A: no desktop application installation detected (codex_go has no ChatGPT desktop product surface; platform security is provided by the OS)"
	}
	return fmt.Sprintf("desktop security enforcement is N/A for the OSS surface: %d desktop application installation(s) present", len(apps))
}

func desktopUpdateState(apps []desktopAppInstallation) string {
	if len(apps) == 0 {
		return "desktop updates are N/A: no desktop application installation detected (no managed update channel for the OSS desktop surface)"
	}
	return fmt.Sprintf("desktop updates are N/A for the OSS surface: %d desktop application installation(s) present", len(apps))
}
