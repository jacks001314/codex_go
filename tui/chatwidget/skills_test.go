package chatwidget

import (
	"strings"
	"testing"

	"codex_go/apps"
	"codex_go/appserver"
)

func TestSkillsMenuViewUsesRustCopyAndShortcut(t *testing.T) {
	view := NewSkillsMenuView(false)
	if view.Title != "Skills" || view.Subtitle != "Choose an action" || len(view.Items) != 2 {
		t.Fatalf("view = %+v", view)
	}
	if view.Items[0].Name != "List skills" || view.Items[0].Description != "Tip: press $ to open this list directly." {
		t.Fatalf("list item = %+v", view.Items[0])
	}
	if got := NewSkillsMenuView(true).Items[0].Description; got != "Tip: press @ to open this list directly." {
		t.Fatalf("mentions v2 shortcut = %q", got)
	}
}

func TestSkillsBrowserViewUsesRuntimeInventory(t *testing.T) {
	response := appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
		CWD: `D:\repo`,
		Skills: []appserver.SkillsListEntry{{
			Name:             "Docs:review",
			Path:             `D:\repo\.codex\skills\review\SKILL.md`,
			Scope:            "plugin",
			Description:      "Long review guidance",
			ShortDescription: "Review code",
			Enabled:          true,
			PluginID:         "docs@team",
		}},
		Errors: []appserver.SkillErrorInfo{{Path: `D:\bad`, Message: "bad skill"}},
	}}}

	view := NewSkillsBrowserView(response, `D:\repo`)
	if view.ViewID != "skills-browser" || view.Title != "Skills" || !view.Searchable {
		t.Fatalf("view = %+v", view)
	}
	if len(view.HeaderLines) != 1 || !strings.Contains(view.HeaderLines[0], "1 enabled of 1 skills.") {
		t.Fatalf("header = %+v", view.HeaderLines)
	}
	if len(view.Items) != 2 {
		t.Fatalf("items = %+v", view.Items)
	}
	first := view.Items[0]
	if first.Name != "review (Docs)" || first.ID == "" {
		t.Fatalf("first item = %+v", first)
	}
	for _, want := range []string{"enabled", "Review code", "scope: plugin", "plugin: docs@team", `D:\repo\.codex\skills\review\SKILL.md`} {
		if !strings.Contains(first.Description, want) {
			t.Fatalf("first description missing %q: %q", want, first.Description)
		}
	}
	second := view.Items[1]
	if second.Name != "Skill error" || !second.Disabled || !strings.Contains(second.Description, "bad skill") {
		t.Fatalf("error item = %+v", second)
	}
}

func TestManageSkillsViewAndChangeSummary(t *testing.T) {
	view := NewManageSkillsView([]appserver.SkillsListEntry{
		{Name: "Docs:Writer", Path: "skills/docs/SKILL.md", Description: "Long docs.", ShortDescription: "Short docs.", Enabled: true},
		{Name: "hidden"},
	})
	if len(view.Items) != 1 {
		t.Fatalf("items = %+v", view.Items)
	}
	if view.Items[0].Name != "Writer (Docs)" || view.Items[0].Description != "Short docs." || !view.Items[0].Enabled {
		t.Fatalf("item = %+v", view.Items[0])
	}
	enabled, disabled, message, changed := ManageSkillsChangeSummary(
		map[string]bool{"a": false, "b": true, "c": true},
		map[string]bool{"a": true, "b": false},
	)
	if !changed || enabled != 1 || disabled != 1 || message != "1 skills enabled, 1 skills disabled" {
		t.Fatalf("summary = %d/%d/%q/%v", enabled, disabled, message, changed)
	}
}

func TestSkillDisplayNameAndDescriptionUseInterfaceFallbacks(t *testing.T) {
	skill := appserver.SkillsListEntry{
		Name:             "Plugin:Reviewer",
		Description:      "Long description.",
		ShortDescription: "Short description.",
		Interface:        &appserver.SkillInterface{DisplayName: "Review Bot", ShortDescription: "Interface short."},
	}
	if got := SkillDisplayName(skill); got != "Review Bot" {
		t.Fatalf("SkillDisplayName(interface) = %q", got)
	}
	if got := SkillDescription(skill); got != "Interface short." {
		t.Fatalf("SkillDescription(interface) = %q", got)
	}
	skill.Interface = nil
	if got := SkillDisplayName(skill); got != "Reviewer (Plugin)" {
		t.Fatalf("SkillDisplayName(plugin prefix) = %q", got)
	}
	if got := SkillDescription(skill); got != "Short description." {
		t.Fatalf("SkillDescription(short) = %q", got)
	}
}

func TestCollectToolMentionsIgnoresEnvVarsAndParsesLinkedPaths(t *testing.T) {
	mentions := CollectToolMentions(
		"Use $writer and $PATH plus [$reader](skill://skills/reader/SKILL.md) and [$drive](app://google_drive).",
		map[string]string{"writer": "skills/writer/SKILL.md"},
	)
	if mentions.Names["PATH"] {
		t.Fatalf("env var PATH should be ignored: %+v", mentions.Names)
	}
	if !mentions.Names["writer"] || !mentions.Names["reader"] {
		t.Fatalf("names = %+v", mentions.Names)
	}
	if mentions.LinkedPaths["writer"] != "skills/writer/SKILL.md" {
		t.Fatalf("writer path = %q", mentions.LinkedPaths["writer"])
	}
	if mentions.LinkedPaths["reader"] != "skill://skills/reader/SKILL.md" {
		t.Fatalf("reader path = %q", mentions.LinkedPaths["reader"])
	}
	if mentions.Names["drive"] {
		t.Fatalf("linked app path should not become a skill/plain name")
	}
	if got := AppIDFromMentionPath(mentions.LinkedPaths["drive"]); got != "google_drive" {
		t.Fatalf("drive app id = %q", got)
	}
}

func TestFindSkillMentionsPrefersLinkedPathsThenNames(t *testing.T) {
	skills := []appserver.SkillsListEntry{
		{Name: "writer", Path: "skills/writer/SKILL.md", Enabled: true},
		{Name: "reader", Path: "skills/reader/SKILL.md", Enabled: true},
		{Name: "reader", Path: "skills/duplicate-reader/SKILL.md", Enabled: true},
	}
	mentions := CollectToolMentions("$writer [$reader](skill://skills/reader/SKILL.md)", nil)
	got := FindSkillMentions(mentions, skills)
	names := []string{}
	for _, skill := range got {
		names = append(names, skill.Name+":"+NormalizeSkillMentionPath(skill.Path))
	}
	if strings.Join(names, ",") != "reader:skills/reader/SKILL.md,writer:skills/writer/SKILL.md" {
		t.Fatalf("skill mentions = %v", names)
	}
}

func TestFindAppMentionsRequiresAccessibleEnabledUniqueAndNoSkillCollision(t *testing.T) {
	appList := []apps.AppEntry{
		{ID: "google_drive", Name: "Google Drive", IsAccessible: true, IsEnabled: true},
		{ID: "arabica_uae", Name: "% Arabica UAE", IsAccessible: false, IsEnabled: true},
		{ID: "linear", Name: "Linear", IsAccessible: true, IsEnabled: false},
		{ID: "docs_a", Name: "Docs", IsAccessible: true, IsEnabled: true},
		{ID: "docs_b", Name: "Docs", IsAccessible: true, IsEnabled: true},
	}
	mentions := CollectToolMentions("$google-drive $arabica-uae $linear $docs", nil)
	got := FindAppMentions(mentions, appList, map[string]bool{"docs": true})
	if len(got) != 1 || got[0].ID != "google_drive" {
		t.Fatalf("app mentions = %+v", got)
	}
}

func TestFindAppMentionsUsesLinkedAppPathsButStillRequiresMentionable(t *testing.T) {
	appList := []apps.AppEntry{
		{ID: "google_drive", Name: "Google Drive", IsAccessible: true, IsEnabled: true},
		{ID: "linear", Name: "Linear", IsAccessible: true, IsEnabled: false},
	}
	mentions := CollectToolMentions("$google-drive $linear", map[string]string{
		"google-drive": "app://google_drive",
		"linear":       "app://linear",
	})
	got := FindAppMentions(mentions, appList, nil)
	if len(got) != 1 || got[0].ID != "google_drive" {
		t.Fatalf("linked app mentions = %+v", got)
	}
}

func TestSkillsForCWDAndEnabledSkills(t *testing.T) {
	response := &appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{
		{CWD: `C:\repo`, Skills: []appserver.SkillsListEntry{
			{Name: "enabled", Path: "a", Enabled: true},
			{Name: "disabled", Path: "b", Enabled: false},
		}},
	}}
	skills := SkillsForCWD(`C:\repo`, response)
	if len(skills) != 2 {
		t.Fatalf("SkillsForCWD() = %+v", skills)
	}
	enabled := EnabledSkillsForMentions(skills)
	if len(enabled) != 1 || enabled[0].Name != "enabled" {
		t.Fatalf("EnabledSkillsForMentions() = %+v", enabled)
	}
}
