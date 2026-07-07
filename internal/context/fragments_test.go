package context

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRenderWrapsMarkers(t *testing.T) {
	rendered := Render(NewSimpleFragment(RoleDeveloper, "<x>", "</x>", "\nhello\n"))
	if rendered == nil {
		t.Fatalf("Render() = nil")
	}
	if rendered.Role != RoleDeveloper {
		t.Fatalf("Role = %q", rendered.Role)
	}
	if rendered.Content != "<x>\nhello\n</x>" {
		t.Fatalf("Content = %q", rendered.Content)
	}
}

func TestRenderManySkipsNilAndEmpty(t *testing.T) {
	rendered := RenderMany([]Fragment{
		nil,
		NewSimpleFragment(RoleUser, "", "", " "),
		NewSimpleFragment(RoleUser, "", "", "hello"),
	})
	if !reflect.DeepEqual(rendered, []RenderedFragment{{Role: RoleUser, Content: "hello"}}) {
		t.Fatalf("RenderMany() = %#v", rendered)
	}
}

func TestAvailablePluginsInstructions(t *testing.T) {
	if NewAvailablePluginsInstructions(nil) != nil {
		t.Fatalf("NewAvailablePluginsInstructions(nil) != nil")
	}
	fragment := NewAvailablePluginsInstructions([]PluginSummary{{DisplayName: "Docs", HasSkills: true}})
	body := fragment.Body()
	for _, want := range []string{"## Plugins", "Skill naming", "Plugins are not invoked directly"} {
		if !strings.Contains(body, want) {
			t.Fatalf("AvailablePluginsInstructions missing %q in:\n%s", want, body)
		}
	}
}

func TestPluginInstructions(t *testing.T) {
	if NewPluginInstructions(" ") != nil {
		t.Fatalf("NewPluginInstructions(empty) != nil")
	}
	fragment := NewPluginInstructions("Use Docs.")
	if fragment.Body() != "Use Docs." {
		t.Fatalf("Body = %q", fragment.Body())
	}
	rendered := Render(fragment)
	if rendered == nil || rendered.Content != "Use Docs." {
		t.Fatalf("Render(plugin instructions) = %#v", rendered)
	}
}

func TestSkillInstructions(t *testing.T) {
	if NewSkillInstructions("", "/tmp/SKILL.md", "body") != nil {
		t.Fatalf("NewSkillInstructions(empty name) != nil")
	}
	rendered := Render(NewSkillInstructions("build", "/tmp/SKILL.md", "Use make."))
	if rendered == nil || rendered.Role != RoleUser {
		t.Fatalf("Render(skill instructions) = %#v", rendered)
	}
	for _, want := range []string{"<skill>", "<name>build</name>", "<path>/tmp/SKILL.md</path>", "Use make.", "</skill>"} {
		if !strings.Contains(rendered.Content, want) {
			t.Fatalf("skill instructions missing %q in:\n%s", want, rendered.Content)
		}
	}
}

func TestRecommendedPluginsInstructions(t *testing.T) {
	fragment := NewRecommendedPluginsInstructions([]RecommendedPlugin{{ID: "docs@market", Name: "Docs"}})
	rendered := Render(fragment)
	if rendered == nil || rendered.Role != RoleUser {
		t.Fatalf("Render(recommended plugins) = %#v", rendered)
	}
	for _, want := range []string{"<recommended_plugins>", "request_plugin_install", "- Docs (docs@market)", "</recommended_plugins>"} {
		if !strings.Contains(rendered.Content, want) {
			t.Fatalf("recommended plugins missing %q in:\n%s", want, rendered.Content)
		}
	}
}

func TestPermissionsInstructions(t *testing.T) {
	fragment := &PermissionsInstructions{
		SandboxMode:    "workspace-write",
		ApprovalPolicy: "on-request",
		WritableRoots:  []string{"/b", "/a"},
		NetworkEnabled: false,
	}
	body := fragment.Body()
	if !strings.Contains(body, "- /a\n- /b") {
		t.Fatalf("roots not sorted in:\n%s", body)
	}
	if !strings.Contains(body, "Network: restricted") {
		t.Fatalf("network missing in:\n%s", body)
	}
}

func TestCurrentTimeReminder(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	body := (&CurrentTimeReminder{Now: now, Location: "UTC"}).Body()
	if body != "It is 2026-06-29 12:00:00 UTC." {
		t.Fatalf("CurrentTimeReminder = %q", body)
	}
}

func TestModelSwitchAndTokenBudget(t *testing.T) {
	if got := (&ModelSwitchInstructions{From: "gpt-4", To: "gpt-5"}).Body(); !strings.Contains(got, "gpt-4") || !strings.Contains(got, "gpt-5") {
		t.Fatalf("ModelSwitchInstructions = %q", got)
	}
	if got := (&TokenBudgetContext{Used: 10, Limit: 100}).Body(); !strings.Contains(got, "90 remaining") {
		t.Fatalf("TokenBudgetContext = %q", got)
	}
}
