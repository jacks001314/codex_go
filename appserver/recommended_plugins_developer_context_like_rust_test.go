package appserver

import (
	"strings"
	"testing"

	contextfrag "codex_go/context"
)

// Rust #47349: recommended plugins are developer context, appended after the
// other developer sections, instead of a user-role message.
func TestRecommendedPluginsEmitDeveloperRoleMessageLikeRust(t *testing.T) {
	rendered := contextfrag.Render(contextfrag.NewRecommendedPluginsInstructions([]contextfrag.RecommendedPlugin{
		{ID: "drive@market", Name: "Drive"},
	}))
	if rendered == nil || rendered.Role != contextfrag.RoleDeveloper {
		t.Fatalf("rendered recommendation = %#v", rendered)
	}

	item, ok := buildRenderedMessage(rendered).(map[string]any)
	if !ok {
		t.Fatalf("recommendation item = %#v", buildRenderedMessage(rendered))
	}
	if item["role"] != contextfrag.RoleDeveloper {
		t.Fatalf("recommendation role = %#v, want developer", item["role"])
	}
	content, ok := item["content"].([]map[string]any)
	if !ok || len(content) != 1 || !strings.Contains(content[0]["text"].(string), "<recommended_plugins>") {
		t.Fatalf("recommendation content = %#v", item["content"])
	}
	if strings.Contains(rendered.Content, "\"role\": \"user\"") {
		t.Fatalf("recommendation still renders as user context: %q", rendered.Content)
	}
}
