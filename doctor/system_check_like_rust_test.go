package doctor

import (
	"strings"
	"testing"
)

func rustSystemCheckInputs() systemCheckInputs {
	return systemCheckInputs{
		os:         "Windows 10.0.26200 (Windows 11 CoreCountrySpecific) [64-bit]",
		osType:     "windows",
		osVersion:  "10.0.26200",
		osLanguage: "zh-CN",
		localeEnv:  map[string]string{"LC_ALL": "C.UTF-8", "LC_CTYPE": "C.UTF-8", "LANG": "C.UTF-8"},
		editorEnv:  map[string]string{"VISUAL": "not set", "EDITOR": "not set"},
		pagerEnv:   map[string]string{"PAGER": "cat", "GIT_PAGER": "cat", "GH_PAGER": "cat"},
	}
}

func TestSystemCheckDetailsMatchRustLikeRust(t *testing.T) {
	check := systemCheckFromInputs(rustSystemCheckInputs())
	want := []string{
		"os: Windows 10.0.26200 (Windows 11 CoreCountrySpecific) [64-bit]",
		"os type: windows",
		"os version: 10.0.26200",
		"os language: zh-CN",
		"LC_ALL: C.UTF-8",
		"LC_CTYPE: C.UTF-8",
		"LANG: C.UTF-8",
		"VISUAL: not set",
		"EDITOR: not set",
		"PAGER: cat",
		"GIT_PAGER: cat",
		"GH_PAGER: cat",
	}
	if got := strings.Join(check.Details, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("details = %q", got)
	}
	if check.Summary != "OS language zh-CN" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestSystemCheckOmitsUnsetLocaleAndPagerEntriesLikeRust(t *testing.T) {
	check := systemCheckFromInputs(systemCheckInputs{
		os:         "Linux [64-bit]",
		osType:     "linux",
		osVersion:  "unknown",
		osLanguage: "",
		editorEnv:  map[string]string{"VISUAL": "not set", "EDITOR": "not set"},
	})
	want := []string{
		"os: Linux [64-bit]",
		"os type: linux",
		"os version: unknown",
		"os language: unavailable",
		"VISUAL: not set",
		"EDITOR: not set",
	}
	if got := strings.Join(check.Details, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("details = %q", got)
	}
	if check.Summary != "OS language unavailable" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestRenderHumanSystemRowsMatchRustLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks:        []*DoctorCheck{systemCheckFromInputs(rustSystemCheckInputs())},
	}
	rendered := RenderHuman(report, &Options{ASCII: true})
	for _, want := range []string{
		"      os                       Windows 10.0.26200 (Windows 11 CoreCountrySpecific) [64-bit]\n",
		"      OS language              zh-CN\n",
		"      LC_ALL                   C.UTF-8\n",
		"      GH_PAGER                 cat\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"os type", "os version", "arch", "go1."} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("unexpected %q in\n%s", unwanted, rendered)
		}
	}
}

func TestRenderHumanGroupsFollowRustGroupOrderLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "OS language en-US"),
			NewCheck("system.disk", "disk", CheckStatusOK, "sufficient free disk space"),
			NewCheck("security.endpoint_protection", "security", CheckStatusOK, "security ok"),
			NewCheck("installation", "install", CheckStatusOK, "consistent"),
			NewCheck("runtime.provenance", "runtime", CheckStatusOK, "runtime ok"),
			NewCheck("voice.runtime", "voice", CheckStatusOK, "voice runtime is only bundled in a package install"),
			NewCheck("config.load", "config", CheckStatusOK, "config loaded"),
			NewCheck("desktop.application", "desktop", CheckStatusOK, "no desktop application detected"),
			NewCheck("updates.status", "updates", CheckStatusOK, "updates ok"),
			NewCheck("network.env", "network", CheckStatusOK, "network ok"),
			NewCheck("app_server.status", "app-server", CheckStatusOK, "app-server ok"),
		},
	}
	rendered := RenderHuman(report, &Options{ASCII: true, Summary: true})
	var rows []string
	for _, line := range strings.Split(rendered, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.HasPrefix(fields[0], "[") {
			rows = append(rows, fields[1])
		}
	}
	want := []string{"system", "disk", "security", "runtime", "install", "config", "desktop", "updates", "network", "app-server"}
	if strings.Join(rows, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v (want %v) in\n%s", rows, want, rendered)
	}
	if strings.Contains(rendered, "Other") {
		t.Fatalf("Rust hides categories missing from its group list, got\n%s", rendered)
	}
	if strings.Contains(rendered, "voice") {
		t.Fatalf("Rust hides categories missing from its group list, got\n%s", rendered)
	}
}

func TestRenderHumanUnicodeMarkersMatchRustLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusWarning,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "system ok"),
			NewCheck("terminal.env", "terminal", CheckStatusWarning, "terminal is dumb").
				Detail("TERM: dumb").
				Issue(NewIssue(CheckStatusWarning, "requires a real terminal type").
					WithMeasured("dumb").
					WithExpected("xterm-256color").
					WithField("TERM")),
			NewCheck("app_server.status", "app-server", CheckStatusOK, "app-server ok").
				Detail("status: not running").
				Detail("mode: ephemeral"),
		},
	}
	rendered := RenderHuman(report, &Options{})
	for _, want := range []string{
		"  \u2713 system",
		"  \u26a0 terminal",
		"  \u25cb app-server",
		"    \u25b8 TERM",
		strings.Repeat("\u2500", 61) + "\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in\n%s", want, rendered)
		}
	}
}
