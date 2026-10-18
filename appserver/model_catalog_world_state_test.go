package appserver

import (
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/model"
)

func TestRenderModelCatalogWorldStateLikeRust(t *testing.T) {
	version := string(agent.VersionV2)
	models := []model.ModelSummary{
		{
			Model:                  "z-model",
			Description:            "Later <unsafe> model",
			DefaultReasoningEffort: "high",
			SupportedReasoningEfforts: []model.ReasoningEffortOption{
				{ReasoningEffort: "low"}, {ReasoningEffort: "high"},
			},
			ServiceTiers:      []model.ModelServiceTier{{ID: "priority"}},
			MultiAgentVersion: &version,
		},
		{
			Model:             "a-model",
			Description:       "First model",
			MultiAgentVersion: &version,
		},
		{
			Model:             "hidden-model",
			Hidden:            true,
			MultiAgentVersion: &version,
		},
	}
	rendered := renderModelCatalog(models, agent.VersionV2)
	if strings.Index(rendered, "a-model") > strings.Index(rendered, "z-model") {
		t.Fatalf("models are not sorted: %q", rendered)
	}
	if strings.Contains(rendered, "hidden-model") {
		t.Fatalf("hidden model was listed: %q", rendered)
	}
	if !strings.Contains(rendered, "high (default)") || !strings.Contains(rendered, "Service tiers: priority.") {
		t.Fatalf("capabilities missing: %q", rendered)
	}
	if !strings.Contains(rendered, "&lt;unsafe&gt;") || strings.Contains(rendered, "<unsafe>") {
		t.Fatalf("catalog was not XML-escaped: %q", rendered)
	}
	if len(rendered) > modelCatalogMaxRenderedBytes {
		t.Fatalf("catalog bytes = %d, want at most %d", len(rendered), modelCatalogMaxRenderedBytes)
	}
}

func TestRenderModelCatalogOmitsWholeModelsPastBudgetLikeRust(t *testing.T) {
	version := string(agent.VersionV2)
	models := make([]model.ModelSummary, 0, 20)
	for i := 0; i < 20; i++ {
		models = append(models, model.ModelSummary{
			Model:             "model-" + strings.Repeat("x", i),
			Description:       strings.Repeat("description ", 20),
			MultiAgentVersion: &version,
		})
	}
	rendered := renderModelCatalog(models, agent.VersionV2)
	if len(rendered) > modelCatalogMaxRenderedBytes {
		t.Fatalf("catalog bytes = %d", len(rendered))
	}
	if !strings.Contains(rendered, modelCatalogOmittedNotice) {
		t.Fatalf("missing omission notice: %q", rendered)
	}
}
