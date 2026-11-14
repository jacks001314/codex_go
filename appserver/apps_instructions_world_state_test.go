package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
)

func TestAppsInstructionsRetainedTransitions(t *testing.T) {
	bundled := model.ResolveAppsInstructions(nil, "codex_apps")
	custom := model.ResolvedAppsInstructions{UsageInstructions: "  literal {{name}}\n", CatalogOverride: true}
	snapshot, first := appsInstructionsTransition(nil, nil, true, custom)
	if first != "<catalog_apps_instructions>"+custom.UsageInstructions+"</catalog_apps_instructions>" {
		t.Fatalf("first=%q", first)
	}
	snapshot, repeat := appsInstructionsTransition(snapshot, &first, true, custom)
	if repeat != "" {
		t.Fatalf("repeat=%q", repeat)
	}
	_, replacement := appsInstructionsTransition(snapshot, &first, true, bundled)
	if !strings.Contains(replacement, appsReplacementNotice+bundled.UsageInstructions) {
		t.Fatalf("replacement=%q", replacement)
	}
	snapshot, removed := appsInstructionsTransition(snapshot, &first, false, custom)
	if removed != "<catalog_apps_instructions>"+appsRemovalNotice+"</catalog_apps_instructions>" {
		t.Fatalf("removal=%q", removed)
	}
	_, repeat = appsInstructionsTransition(snapshot, &removed, false, custom)
	if repeat != "" {
		t.Fatalf("repeated removal=%q", repeat)
	}
	for _, literal := range []string{appsRemovalNotice, appsReplacementNotice + "literal"} {
		resolved := model.ResolvedAppsInstructions{UsageInstructions: literal, CatalogOverride: true}
		snap, text := appsInstructionsTransition(nil, nil, true, resolved)
		if !strings.Contains(text, appsReplacementNotice+literal) {
			t.Fatalf("literal not escaped: %q", text)
		}
		_, again := appsInstructionsTransition(snap, &text, true, resolved)
		if again != "" {
			t.Fatalf("literal changed on resume: %q", again)
		}
	}
	_, repeat = appsInstructionsTransition(json.RawMessage("true"), nil, true, bundled)
	if repeat != "" {
		t.Fatal("legacy bundled checkpoint repeated")
	}
	_, text := appsInstructionsTransition(json.RawMessage("false"), nil, true, bundled)
	if text == "" {
		t.Fatal("legacy disabled checkpoint suppressed reenable")
	}
}
