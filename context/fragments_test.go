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
	if !reflect.DeepEqual(rendered, []RenderedFragment{{Role: RoleUser, Content: "hello", ContentKind: "generic"}}) {
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

func TestExecutorSkillInstructionsIncludeResourceAccessLikeRust(t *testing.T) {
	rendered := Render(NewSkillInstructionsWithExecutorResourceAccess("deploy", "skill://demo/root/SKILL.md", "Use it.", &ExecutorSkillResourceAccess{
		AuthorityID:  "demo@1",
		Package:      "skill://demo@1/root",
		MainResource: "skill://demo@1/root/SKILL.md",
	}))
	if rendered == nil {
		t.Fatal("Render() = nil")
	}
	want := `<resource_access>{"authority":{"kind":"executor","id":"demo@1"},"package":"skill://demo@1/root","main_resource":"skill://demo@1/root/SKILL.md"}</resource_access>`
	if !strings.Contains(rendered.Content, want) {
		t.Fatalf("content missing resource access:\n%s", rendered.Content)
	}
}

func TestImagegenSkillInstructionsPreserveContentsLikeRust(t *testing.T) {
	rendered := Render(NewSkillInstructions("imagegen", "/tmp/SKILL.md", "Use the skill."))
	if rendered == nil {
		t.Fatal("Render(imagegen skill instructions) = nil")
	}
	if !strings.Contains(rendered.Content, "Use the skill.") {
		t.Fatalf("imagegen instructions missing body:\n%s", rendered.Content)
	}
	for _, unexpected := range []string{"hosted Responses image_generation tool", "call image_generation directly", "Do not use tool_search"} {
		if strings.Contains(rendered.Content, unexpected) {
			t.Fatalf("imagegen instructions unexpectedly rewrote content with %q:\n%s", unexpected, rendered.Content)
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
	open, close := (&CurrentTimeReminder{Now: now, Location: "UTC"}).Markers()
	if open != "<current_time_reminder>" || close != "</current_time_reminder>" {
		t.Fatalf("CurrentTimeReminder markers = %q/%q", open, close)
	}
	rendered := Render(&CurrentTimeReminder{Now: now, Location: "UTC"})
	if rendered == nil || rendered.Content != "<current_time_reminder>\nIt is 2026-06-29 12:00:00 UTC.\n</current_time_reminder>" {
		t.Fatalf("CurrentTimeReminder rendered = %#v", rendered)
	}
}

func TestModelSwitchAndTokenBudget(t *testing.T) {
	if got := (&ModelSwitchInstructions{From: "gpt-4", To: "gpt-5"}).Body(); !strings.Contains(got, "gpt-4") || !strings.Contains(got, "gpt-5") {
		t.Fatalf("ModelSwitchInstructions = %q", got)
	}
	modelSwitch := RenderStandalone(&ModelSwitchInstructions{Instructions: "new model instructions"})
	if modelSwitch == nil || modelSwitch.Content != "<model_switch>\nThe user was previously using a different model. Please continue the conversation according to the following instructions:\n\nnew model instructions\n</model_switch>" {
		t.Fatalf("model switch fragment = %#v", modelSwitch)
	}
	personality := RenderStandalone(&PersonalitySpecInstructions{Spec: "be pragmatic"})
	if personality == nil || personality.Content != "<personality_spec> The user has requested a new communication style. Future messages should adhere to the following personality: \nbe pragmatic </personality_spec>" {
		t.Fatalf("personality fragment = %#v", personality)
	}
	if got := (&TokenBudgetContext{Used: 10, Limit: 100}).Body(); !strings.Contains(got, "90 remaining") {
		t.Fatalf("TokenBudgetContext = %q", got)
	}
	guidance := RenderStandalone(&ContextWindowGuidance{Message: "keep durable notes"})
	if guidance == nil || guidance.Role != RoleDeveloper || guidance.Content != "<context_window_guidance>keep durable notes</context_window_guidance>" {
		t.Fatalf("context window guidance = %#v", guidance)
	}
}

func TestImageResizeNoticeRendersRustShape(t *testing.T) {
	notice := NewImageResizeNotice(ImageResizeNoticeSourceUserMessage, []ResizedImage{
		{ImageNumber: 1, ImageCount: 2, SourceWidth: 1024, SourceHeight: 768, PreparedWidth: 512, PreparedHeight: 384},
		{ImageNumber: 2, ImageCount: 2, SourceWidth: 800, SourceHeight: 600, PreparedWidth: 400, PreparedHeight: 300},
	})
	rendered := RenderStandalone(notice)
	if rendered == nil || rendered.Role != RoleDeveloper {
		t.Fatalf("rendered = %#v", rendered)
	}
	want := "<image_resize_notice>\n" +
		"Image 1 of 2 in the preceding user message was resized from 1024x768 to 512x384 pixels.\n" +
		"Image 2 of 2 in the preceding user message was resized from 800x600 to 400x300 pixels.\n" +
		"</image_resize_notice>"
	if rendered.Content != want {
		t.Fatalf("content = %q, want %q", rendered.Content, want)
	}
	toolOutput := RenderStandalone(NewImageResizeNotice(ImageResizeNoticeSourceToolOutput, []ResizedImage{
		{ImageNumber: 1, ImageCount: 1, SourceWidth: 100, SourceHeight: 50, PreparedWidth: 50, PreparedHeight: 25},
	}))
	if !strings.Contains(toolOutput.Content, "in the preceding tool output was resized from 100x50 to 50x25 pixels.") {
		t.Fatalf("tool output notice = %q", toolOutput.Content)
	}
}

func TestPersistentModeInstructionsLikeRust(t *testing.T) {
	// Persistent effort + async availability -> developer fragment with the
	// tailored approval channel.
	frag := PersistentModeInstructions("persistent", "", true, false)
	if frag == nil {
		t.Fatal("persistent effort produced nil fragment")
	}
	if frag.Role() != RoleDeveloper {
		t.Fatalf("role = %q, want developer", frag.Role())
	}
	open, close := frag.Markers()
	if open != "<persistent_mode>" || close != "</persistent_mode>" {
		t.Fatalf("markers = %q %q", open, close)
	}
	if frag.ContentKind() != "persistent_mode.instructions" {
		t.Fatalf("content kind = %q", frag.ContentKind())
	}
	if !strings.Contains(frag.Body(), "via functions.send_user_message_async") {
		t.Fatalf("body missing async approval channel:\n%s", frag.Body())
	}

	// No async available -> no channel suffix.
	fragNoAsync := PersistentModeInstructions("persistent", "", false, false)
	if fragNoAsync == nil || strings.Contains(fragNoAsync.Body(), "send_user_message_async") {
		t.Fatalf("no-async body = %#v", fragNoAsync)
	}

	// Non-persistent effort -> nil.
	if frag := PersistentModeInstructions("medium", "", true, false); frag != nil {
		t.Fatalf("non-persistent produced fragment")
	}

	// Guardian session -> nil.
	if frag := PersistentModeInstructions("persistent", "", true, true); frag != nil {
		t.Fatalf("guardian session produced fragment")
	}

	// Catalog instructions override the bundled default.
	catalog := "Custom persistent guidance for {{ approval_request_channel }}."
	fragCustom := PersistentModeInstructions("persistent", catalog, true, false)
	if !strings.Contains(fragCustom.Body(), "Custom persistent guidance for") || !strings.Contains(fragCustom.Body(), "via functions.send_user_message_async") {
		t.Fatalf("catalog body = %#v", fragCustom.Body())
	}
}

func TestParseDelegatedToolOutputLikeRust(t *testing.T) {
	prompt := "<codex_delegation>\n  <source_thread_id>thread-1</source_thread_id>\n  <input>&lt;code&gt;hello&lt;/code&gt;</input>\n</codex_delegation>"
	source, delegated, ok := ParseDelegatedToolOutput("send_message_to_thread", "codex_app", prompt)
	if !ok || source != "thread-1" || delegated != "<code>hello</code>" {
		t.Fatalf("delegated = (%q,%q,%v)", source, delegated, ok)
	}
	// Non-delegation name is rejected.
	if _, _, ok := ParseDelegatedToolOutput("other", "codex_app", prompt); ok {
		t.Fatalf("non-delegation name accepted")
	}
	// Non-trusted namespace is rejected.
	if _, _, ok := ParseDelegatedToolOutput("send_message_to_thread", "untrusted", prompt); ok {
		t.Fatalf("untrusted namespace accepted")
	}
	// Non-delegation prompt is rejected.
	if _, _, ok := ParseDelegatedToolOutput("send_message_to_thread", "codex_app", "plain output"); ok {
		t.Fatalf("non-delegation prompt accepted")
	}
}
