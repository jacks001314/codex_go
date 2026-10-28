package historynotes

import (
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

func collectedCatalogSpecs(t *testing.T, history *model.HistoryToolMessages, notes *model.NotesToolMessages) map[string]tool.Spec {
	t.Helper()
	specs := map[string]tool.Spec{}
	for _, executor := range ToolsWithCatalog(nil, "session", "agent", history, notes) {
		spec := executor.Spec()
		specs[spec.Name.Namespace+"/"+spec.Name.Name] = spec
	}
	return specs
}

func TestHistoryNotesToolsApplyCatalogOverridesLikeRust(t *testing.T) {
	historyNamespace := "catalog history namespace"
	notesNamespace := "catalog notes namespace"
	listWindowsDescription := "catalog list windows"
	readFileDescription := "catalog read file"
	readFileParameters := `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	emptyDescription := ""
	history := &model.HistoryToolMessages{
		NamespaceDescription: &historyNamespace,
		ListWindows:          &model.ToolMessage{Description: &listWindowsDescription},
		ListItems:            &model.ToolMessage{Description: &emptyDescription},
	}
	notes := &model.NotesToolMessages{
		NamespaceDescription: &notesNamespace,
		ReadFile: &model.ToolMessage{
			Description: &readFileDescription,
			Parameters:  &readFileParameters,
		},
	}
	specs := collectedCatalogSpecs(t, history, notes)

	listWindows := specs["history/list_windows"]
	if listWindows.Description != listWindowsDescription {
		t.Fatalf("history/list_windows description = %q", listWindows.Description)
	}
	if listWindows.NamespaceDescription != historyNamespace {
		t.Fatalf("history namespace description = %q", listWindows.NamespaceDescription)
	}
	if specs["history/list_items"].Description != "" {
		t.Fatalf("explicit empty description = %q", specs["history/list_items"].Description)
	}
	if specs["history/read_item"].Description == "" {
		t.Fatal("unoverridden history tool lost its bundled description")
	}
	readFile := specs["notes/read_file"]
	if readFile.Description != readFileDescription {
		t.Fatalf("notes/read_file description = %q", readFile.Description)
	}
	if readFile.NamespaceDescription != notesNamespace {
		t.Fatalf("notes namespace description = %q", readFile.NamespaceDescription)
	}
	properties, _ := readFile.InputSchema["properties"].(map[string]any)
	if _, ok := properties["path"]; !ok {
		t.Fatalf("notes/read_file parameters were not overridden: %#v", readFile.InputSchema)
	}
	if specs["notes/append_to_file"].Description == "" {
		t.Fatal("unoverridden notes tool lost its bundled description")
	}
}

func TestHistoryNotesToolsKeepBundledSchemaOnInvalidCatalogParametersLikeRust(t *testing.T) {
	invalid := `{"type":`
	notes := &model.NotesToolMessages{ReadFile: &model.ToolMessage{Parameters: &invalid}}
	specs := collectedCatalogSpecs(t, nil, notes)
	readFile := specs["notes/read_file"]
	properties, _ := readFile.InputSchema["properties"].(map[string]any)
	path, _ := properties["path"].(map[string]any)
	if path["type"] != "string" {
		t.Fatalf("bundled notes/read_file schema was not kept: %#v", readFile.InputSchema)
	}
}
