//go:build windows

package doctor

import (
	"strconv"
	"strings"
	"testing"
)

func TestDesktopSecuritySourcesDistinguishAuditsFromBlocks(t *testing.T) {
	cases := []struct {
		channel int
		audit   int
		block   int
	}{
		{0, 1122, 1121},
		{0, 1125, 1126},
		{1, 8003, 8004},
		{2, 8021, 8022},
		{3, 8006, 8007},
		{4, 3076, 3077},
	}
	for _, tc := range cases {
		for _, want := range []struct {
			id     int
			status CheckStatus
		}{{tc.audit, CheckStatusWarning}, {tc.block, CheckStatusFail}} {
			events := parseDesktopSecurityEvents(desktopSecurityFixture(want.id, "codex.exe", nil), desktopSecurityChannels[tc.channel])
			if len(events) != 1 || events[0].status != want.status {
				t.Fatalf("event %d = %#v, want status %q", want.id, events, want.status)
			}
		}
	}
}

func TestDesktopSecurityDefenderDistinguishesDetectionAndRemediation(t *testing.T) {
	cases := []struct {
		id     int
		action string
		want   CheckStatus
	}{
		{1116, "", CheckStatusWarning},
		{1117, "Allow", CheckStatusOK},
		{1117, "Quarantine", CheckStatusFail},
		{1117, "Remove", CheckStatusFail},
	}
	for _, tc := range cases {
		var fields [][2]string
		if tc.action != "" {
			fields = append(fields, [2]string{"Action Name", tc.action})
		}
		events := parseDesktopSecurityEvents(desktopSecurityFixture(tc.id, "codex.exe", fields), desktopSecurityChannels[0])
		if got := classifyDesktopSecurity([]*[]desktopSecurityEvidence{&events}).Status; got != tc.want {
			t.Fatalf("event %d action %q status = %q, want %q", tc.id, tc.action, got, tc.want)
		}
	}
}

func TestDesktopSecurityOnlyTrustedCodexExecutablesAreReported(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"codex.exe", true},
		{`OpenAI.Codex_2p2nqsd0c76g0\ChatGPT.exe`, true},
		{"evil-codex.exe", false},
		{`C:\Other\ChatGPT.exe`, false},
		{`OpenAI.CodexEvil_1\ChatGPT.exe`, false},
	}
	for _, tc := range cases {
		events := parseDesktopSecurityEvents(desktopSecurityFixture(1121, tc.path, nil), desktopSecurityChannels[0])
		if got := len(events) > 0; got != tc.want {
			t.Fatalf("path %q reported = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDesktopSecurityEvidenceIsBoundedClassifiedAndRedacted(t *testing.T) {
	if got := classifyDesktopSecurity(nil).Status; got != CheckStatusWarning {
		t.Fatalf("classify(nil) = %q, want warning", got)
	}
	empty := []desktopSecurityEvidence{}
	if got := classifyDesktopSecurity([]*[]desktopSecurityEvidence{&empty, nil}).Status; got != CheckStatusOK {
		t.Fatalf("classify(empty, missing) = %q, want ok", got)
	}
	var audits strings.Builder
	for index := 0; index < desktopMaxRenderedEvents; index++ {
		audits.WriteString(desktopSecurityFixture(1122, "codex.exe", nil))
	}
	secret := "private-customer-secret"
	audits.WriteString(desktopSecurityFixture(1121, "codex.exe", [][2]string{{"User", secret}}))
	events := parseDesktopSecurityEvents(audits.String(), desktopSecurityChannels[0])
	check := classifyDesktopSecurity([]*[]desktopSecurityEvidence{&events})
	if check.ID != "desktop.security.enforcement" || check.Status != CheckStatusFail {
		t.Fatalf("check = %+v", check)
	}
	if len(check.Details) != desktopMaxRenderedEvents {
		t.Fatalf("details = %d, want %d", len(check.Details), desktopMaxRenderedEvents)
	}
	joined := strings.Join(check.Details, "\n")
	if strings.Contains(joined, secret) {
		t.Fatalf("details leak an event payload:\n%s", joined)
	}
	if check.Remediation == nil || *check.Remediation == "" {
		t.Fatalf("remediation missing: %+v", check)
	}
}

func TestDesktopSecuritySummariesMatchRust(t *testing.T) {
	if got := classifyDesktopSecurity(nil).Summary; got != "security event channels could not be inspected" {
		t.Fatalf("unavailable summary = %q", got)
	}
	empty := []desktopSecurityEvidence{}
	if got := classifyDesktopSecurity([]*[]desktopSecurityEvidence{&empty}).Summary; got != "no locally visible recent Codex security enforcement was found" {
		t.Fatalf("clean summary = %q", got)
	}
	if got := classifyDesktopSecurity([]*[]desktopSecurityEvidence{&empty, nil}).Summary; got != "security event coverage is incomplete" {
		t.Fatalf("partial summary = %q", got)
	}
	audit := parseDesktopSecurityEvents(desktopSecurityFixture(1122, "codex.exe", nil), desktopSecurityChannels[0])
	if got := classifyDesktopSecurity([]*[]desktopSecurityEvidence{&audit}).Summary; got != "recent Codex security audit or detection requires review" {
		t.Fatalf("audit summary = %q", got)
	}
	block := parseDesktopSecurityEvents(desktopSecurityFixture(1121, "codex.exe", nil), desktopSecurityChannels[0])
	if got := classifyDesktopSecurity([]*[]desktopSecurityEvidence{&block}).Summary; got != "endpoint security blocked or quarantined a Codex executable" {
		t.Fatalf("block summary = %q", got)
	}
}

func desktopSecurityFixture(id int, path string, fields [][2]string) string {
	var data strings.Builder
	for _, field := range fields {
		data.WriteString(`<Data Name="` + field[0] + `">` + field[1] + `</Data>`)
	}
	return "<Event><System><EventID>" + strconv.Itoa(id) + `</EventID><TimeCreated SystemTime="2026-01-01T00:00:00Z"/></System>` +
		`<EventData><Data Name="Path">` + path + "</Data>" + data.String() + "</EventData></Event>"
}
