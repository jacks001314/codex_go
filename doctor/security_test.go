package doctor

import (
	"strings"
	"testing"
)

func TestEndpointCheckLikeRust(t *testing.T) {
	notInspected := endpointCheck(endpointInspection{})
	if notInspected.Status != CheckStatusOK || notInspected.Summary != "endpoint protection is not inspected on this platform" {
		t.Fatalf("not inspected = %+v", notInspected)
	}

	none := endpointCheck(endpointInspection{inspected: true})
	if none.Status != CheckStatusOK || none.Summary != "no supported endpoint protection detected" {
		t.Fatalf("none detected = %+v", none)
	}

	unavailable := endpointCheck(endpointInspection{inspected: true, visibilityIncomplete: true})
	if unavailable.Status != CheckStatusWarning || unavailable.Summary != "endpoint protection inspection unavailable" {
		t.Fatalf("unavailable = %+v", unavailable)
	}

	partial := endpointCheck(endpointInspection{
		inspected:            true,
		visibilityIncomplete: true,
		products:             []string{"CrowdStrike Falcon"},
	})
	if partial.Status != CheckStatusWarning || len(partial.Issues) != 1 {
		t.Fatalf("partial = %+v", partial)
	}
	if !strings.Contains(partial.Summary, "exclusions are unverified") {
		t.Fatalf("partial summary = %q", partial.Summary)
	}

	multi := endpointCheck(endpointInspection{
		inspected: true,
		products:  []string{"CrowdStrike Falcon", "Microsoft Defender", "SentinelOne", "BeyondTrust Privilege Management", "Jamf Protect"},
	})
	if len(multi.Issues) != 5 {
		t.Fatalf("multi issues = %d, want 5", len(multi.Issues))
	}
	if multi.Issues[1].Remedy == nil || !strings.Contains(*multi.Issues[1].Remedy, "Microsoft Defender: Add a certificate") {
		t.Fatalf("defender remedy = %v", multi.Issues[1].Remedy)
	}
	if multi.Issues[3].Remedy == nil || !strings.Contains(*multi.Issues[3].Remedy, "BeyondTrust: Remove Codex from application blocking rules") {
		t.Fatalf("beyondtrust remedy = %v", multi.Issues[3].Remedy)
	}
	if multi.Issues[4].Remedy == nil || !strings.Contains(*multi.Issues[4].Remedy, "Jamf Protect: Add an Override Threat Prevention exception") {
		t.Fatalf("jamf remedy = %v", multi.Issues[4].Remedy)
	}
}
