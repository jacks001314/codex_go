package doctor

import (
	"runtime"
	"strings"
	"time"
)

// Rust c7a95f84b3 (#38827): detect supported endpoint protection products on
// macOS and Windows and add the results to the doctor environment report,
// warning when detected products have unverified Codex exclusions.
const (
	endpointProductQueryTimeout = 5 * time.Second
	endpointProductOutputLimit  = 64 * 1024
)

type endpointInspection struct {
	products             []string
	visibilityIncomplete bool
	inspected            bool
}

func endpointProtectionCheck() *DoctorCheck {
	return endpointCheck(endpointProtectionInspection())
}

func endpointCheck(inspection endpointInspection) *DoctorCheck {
	if !inspection.inspected {
		return NewCheck("security.endpoint", "security", CheckStatusOK, "endpoint protection is not inspected on this platform").
			Detail("endpoint products: not inspected on this platform")
	}
	if inspection.visibilityIncomplete && len(inspection.products) == 0 {
		return NewCheck("security.endpoint", "security", CheckStatusWarning, "endpoint protection inspection unavailable").
			Detail("endpoint products: unavailable")
	}
	if len(inspection.products) == 0 {
		return NewCheck("security.endpoint", "security", CheckStatusOK, "no supported endpoint protection detected").
			Detail("endpoint products: none detected")
	}
	targets := "verified Codex app, codex agent, and required helpers"
	if runtime.GOOS == "windows" {
		targets = "signed Codex app; codex.exe; codex-windows-sandbox-setup.exe; codex-command-runner.exe; codex-code-mode-host.exe"
	} else if runtime.GOOS == "darwin" {
		targets = "signing team 2DC432GLL2 plus the installed Codex app identity, signed codex agent, and required helpers"
	}
	check := NewCheck("security.endpoint", "security", CheckStatusWarning, "endpoint protection detected; Codex exclusions are unverified").
		Detail("endpoint products: " + strings.Join(inspection.products, ", ")).
		Detail("exclusion targets: " + targets).
		Detail("Codex exclusions: not verified").
		Remediate("ask your security administrator to verify Codex exclusions and required helper allowances")
	if inspection.visibilityIncomplete {
		check = check.Detail("additional endpoint products: unavailable")
	}
	for _, product := range inspection.products {
		check = check.Issue(NewIssue(CheckStatusWarning, product+" can interfere with Codex. Verify Codex exclusions.").
			WithMeasured("not verified").
			WithExpected("Codex application and helper exclusions").
			WithRemedy(endpointProtectionRemedy(product)).
			WithField("Codex exclusions"))
	}
	return check
}

func endpointProtectionRemedy(product string) string {
	switch product {
	case "CrowdStrike Falcon":
		return "CrowdStrike Falcon: Add a certificate or IOA exclusion for Codex. If sensor overhead continues, exclude the Codex agent from sensor visibility. Keep monitoring descendant processes."
	case "BeyondTrust Privilege Management":
		return "BeyondTrust: Remove Codex from application blocking rules. Add allow rules for Codex helper executables. Do not grant administrator privileges."
	case "Microsoft Defender":
		return "Microsoft Defender: Add a certificate or executable-path exclusion for Codex and its helpers. If Attack Surface Reduction blocks Codex, add a rule exclusion. If Controlled Folder Access blocks Codex, allow the app."
	case "SentinelOne":
		return "SentinelOne: Add a signer, file-hash, or executable-path exclusion for Codex and its helpers."
	case "Jamf Protect":
		return "Jamf Protect: Add an Override Threat Prevention exception for Codex app and helper signing identities. If analytics cause delays, add an Ignore System Events for Analytics exception."
	default:
		return "Add an exclusion for Codex and its helpers. Use the endpoint product instructions."
	}
}
