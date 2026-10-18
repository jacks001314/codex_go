package turn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/skillprovider"
	"codex_go/tool"
	"codex_go/utils"
)

func skillsBudgetTestRouter(t *testing.T, entries []skillprovider.CatalogEntry) *tool.Router {
	t.Helper()
	provider := skillprovider.ProviderFuncs{
		ListFunc: func(context.Context, skillprovider.ListQuery) (skillprovider.Catalog, error) {
			return skillprovider.Catalog{Entries: entries}, nil
		},
	}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.SkillProviders = skillprovider.NewRegistry(skillprovider.Source{
		Kind: skillprovider.SourceExecutor, Label: "executor", Provider: provider,
	})
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	return tool.NewRouter(registry)
}

func skillsBudgetEntry(name string, description string) skillprovider.CatalogEntry {
	pkg := "skill://demo@1/plugin/skills/" + name
	return skillprovider.CatalogEntry{
		PackageID:     pkg,
		Authority:     skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "demo@1"},
		Name:          "demo:" + name,
		Description:   description,
		MainResource:  pkg + "/SKILL.md",
		Enabled:       true,
		PromptVisible: true,
	}
}

// TestSkillsListHonorsResponseBudgetAndPaginatesLikeRust covers Rust #40413:
// pages are bounded by the invocation budget and the cursor remains usable.
func TestSkillsListHonorsResponseBudgetAndPaginatesLikeRust(t *testing.T) {
	entries := []skillprovider.CatalogEntry{
		skillsBudgetEntry("one", strings.Repeat("a", 180)),
		skillsBudgetEntry("two", strings.Repeat("b", 180)),
		skillsBudgetEntry("three", strings.Repeat("c", 180)),
	}
	router := skillsBudgetTestRouter(t, entries)
	budget := utils.BytesPolicy(700)
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		arguments := `{"authority":{"kind":"executor"}`
		if cursor != "" {
			arguments += `,"cursor":"` + cursor + `"`
		}
		arguments += `}`
		output, err := router.Dispatch(context.Background(), &tool.Invocation{
			CallID:     "list",
			ToolName:   tool.NamespacedName("skills", "list"),
			Truncation: &budget,
			Payload:    tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
		})
		if err != nil {
			t.Fatalf("skills.list page %d error = %v", page, err)
		}
		if len(output.Body) > 900 {
			t.Fatalf("page %d body = %d bytes, want bounded", page, len(output.Body))
		}
		var listed skillsListResponse
		if err := json.Unmarshal([]byte(output.Body), &listed); err != nil {
			t.Fatalf("page %d JSON = %v", page, err)
		}
		for _, skill := range listed.Skills {
			seen[skill.Package] = true
		}
		if listed.NextCursor == nil {
			cursor = ""
			break
		}
		cursor = *listed.NextCursor
	}
	if len(seen) != len(entries) {
		t.Fatalf("seen packages = %#v, want all %d entries", seen, len(entries))
	}
}

// TestSkillsListSkipsOversizedEntryWithSingleWarningLikeRust covers the
// oversized-entry omission path.
func TestSkillsListSkipsOversizedEntryWithSingleWarningLikeRust(t *testing.T) {
	entries := []skillprovider.CatalogEntry{
		skillsBudgetEntry("huge", strings.Repeat("x", 1024)),
		skillsBudgetEntry("small", "Short description."),
	}
	router := skillsBudgetTestRouter(t, entries)
	budget := utils.BytesPolicy(500)
	output, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:     "list",
		ToolName:   tool.NamespacedName("skills", "list"),
		Truncation: &budget,
		Payload: tool.Payload{Kind: tool.PayloadFunction,
			Arguments: `{"authority":{"kind":"executor"}}`},
	})
	if err != nil {
		t.Fatalf("skills.list error = %v", err)
	}
	var listed skillsListResponse
	if err := json.Unmarshal([]byte(output.Body), &listed); err != nil {
		t.Fatalf("skills.list JSON = %v", err)
	}
	for _, skill := range listed.Skills {
		if strings.HasSuffix(skill.Name, ":huge") {
			t.Fatalf("oversized entry was returned: %#v", listed)
		}
	}
	warnings := 0
	for _, warning := range listed.Warnings {
		if warning == skillOversizedEntryWarning {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("warnings = %#v, want one omission warning", listed.Warnings)
	}
}

// TestSkillsListAcceptsLegacyCursorAndChangedBudgetLikeRust covers cursor
// compatibility and budget changes between pages.
func TestSkillsListAcceptsLegacyCursorAndChangedBudgetLikeRust(t *testing.T) {
	entries := []skillprovider.CatalogEntry{
		skillsBudgetEntry("one", strings.Repeat("a", 180)),
		skillsBudgetEntry("two", strings.Repeat("b", 180)),
	}
	router := skillsBudgetTestRouter(t, entries)
	smallBudget := utils.BytesPolicy(500)
	firstOutput, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:     "first",
		ToolName:   tool.NamespacedName("skills", "list"),
		Truncation: &smallBudget,
		Payload:    tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"executor"}}`},
	})
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	var firstPage skillsListResponse
	if err := json.Unmarshal([]byte(firstOutput.Body), &firstPage); err != nil || firstPage.NextCursor == nil {
		t.Fatalf("first page = %#v err=%v", firstPage, err)
	}
	parts := strings.Split(*firstPage.NextCursor, ":")
	if len(parts) != 3 {
		t.Fatalf("new cursor = %q", *firstPage.NextCursor)
	}
	legacy := parts[0] + ":" + parts[1]
	budget := utils.BytesPolicy(1000)
	output, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:     "list",
		ToolName:   tool.NamespacedName("skills", "list"),
		Truncation: &budget,
		Payload: tool.Payload{Kind: tool.PayloadFunction,
			Arguments: `{"authority":{"kind":"executor"},"cursor":"` + legacy + `"}`},
	})
	if err != nil {
		t.Fatalf("legacy cursor error = %v", err)
	}
	var listed skillsListResponse
	if err := json.Unmarshal([]byte(output.Body), &listed); err != nil {
		t.Fatalf("legacy cursor JSON = %v", err)
	}
	if len(listed.Skills) != 1 || listed.Skills[0].Package != entries[1].PackageID || listed.NextCursor != nil {
		t.Fatalf("legacy cursor page = %#v", listed)
	}
}
