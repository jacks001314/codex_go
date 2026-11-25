package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGuardianEnclosingCallCompleteActionAndBudget(t *testing.T) {
	action := Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "submit", Arguments: map[string]any{"amount": 42}}
	call := &GuardianEnclosingCall{Tool: "tool js call", Arguments: `{"code":"await submit(42)"}`, MaxBytes: 4000}
	options := BuildPromptOptions{EnclosingCall: call}
	parts, err := BuildPromptPartsWithOptions(action, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	text := parts[0].Text
	block := strings.Split(strings.Split(text, ">>> APPROVAL REQUEST START\n")[1], ">>> APPROVAL REQUEST END")[0]
	line := strings.Split(strings.TrimPrefix(block, "Enclosing call for this review: "), "\n")[0]
	var got map[string]string
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got["tool"] != call.Tool || got["arguments"] != call.Arguments {
		t.Fatalf("wrong enclosing evidence: %#v", got)
	}
	var proposed map[string]any
	if err := json.Unmarshal([]byte(strings.Split(block, "Planned action JSON:\n")[1]), &proposed); err != nil {
		t.Fatal(err)
	}
	if proposed["tool_name"] != "submit" || proposed["arguments"].(map[string]any)["amount"] != float64(42) {
		t.Fatalf("changed inner action: %#v", proposed)
	}
	for _, presentation := range []ActionPresentation{ActionPresentationSyncFull, ActionPresentationSyncDelta, ActionPresentationAsync} {
		options.Presentation = presentation
		options.EnclosingCall = call
		got, err := BuildPromptWithOptions(action, nil, options)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "Enclosing call for this review:") != (presentation != ActionPresentationAsync) {
			t.Fatal(got)
		}
	}
	options.Presentation = ActionPresentationSyncFull
	for _, arguments := range []string{strings.Repeat("x", 4001), strings.Repeat("\"", 1990)} {
		options.EnclosingCall = &GuardianEnclosingCall{Tool: call.Tool, Arguments: arguments, MaxBytes: 4000}
		got, err := BuildPromptWithOptions(action, nil, options)
		if err != nil || strings.Contains(got, "Enclosing call for this review:") {
			t.Fatalf("partial/oversized evidence: %v %s", err, got)
		}
	}
	options.EnclosingCall = call
	plain, err := BuildPromptWithOptions(Action{Type: "command", Command: "ls", CWD: "/repo"}, nil, options)
	if err != nil || strings.Contains(plain, "Enclosing call for this review:") {
		t.Fatal(plain, err)
	}
}

func TestGuardianEnclosingCallElasticLateAdmission(t *testing.T) {
	action := Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "submit"}
	options := BuildPromptOptions{EnclosingCall: &GuardianEnclosingCall{Tool: "tool js call", Arguments: strings.Repeat("argument", 80)}}
	for _, tc := range []struct {
		name                 string
		budget, entry, total int
		want                 bool
	}{
		{"fits", 4000, 1000, 0, true}, {"aggregate", 100, 1000, 0, false}, {"entry", 4000, 32, 0, false}, {"supporting total", 4000, 1000, 32, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := guardianElasticTestLimits(t, tc.entry)
			if tc.total > 0 {
				limits.MaxTotalSectionTokens = &tc.total
			}
			config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{GuardianElasticSectionPlannedAction: limits}})
			if tc.name == "aggregate" {
				sections, err := composeGuardianElasticPromptSections(action, nil, BuildPromptOptions{}, config)
				if err != nil {
					t.Fatal(err)
				}
				tc.budget = (GuardianElasticContext{Sections: sections}).EstimatedTokens()
			}
			parts, _, err := BuildGuardianElasticPromptPartsWithSectionCosts(action, nil, options, config, GuardianElasticRequestBudget{MaxInputTokens: tc.budget}, "Evidence omitted.")
			if err != nil {
				t.Fatal(err)
			}
			var text string
			for _, p := range parts {
				text += p.Text
			}
			if strings.Contains(text, "Enclosing call for this review:") != tc.want {
				t.Fatalf("wrong admission: %s", text)
			}
			if !strings.Contains(text, `"tool_name": "submit"`) {
				t.Fatalf("lost proposed action: %s", text)
			}
		})
	}
}
