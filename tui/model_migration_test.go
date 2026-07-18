package tui

import (
	"strings"
	"testing"
)

func TestMigrationCopyForModelsMatchesRustFallbackCopy(t *testing.T) {
	link := "https://www.codex.com/models/gpt-new"
	description := "Latest recommended model for better performance."
	copy := MigrationCopyForModels("gpt-old", "gpt-new", &link, nil, nil, "gpt-new", &description, true)
	if len(copy.Heading) != 1 || copy.Heading[0] != "Codex just got an upgrade. Introducing gpt-new." {
		t.Fatalf("heading = %#v", copy.Heading)
	}
	joined := strings.Join(copy.Content, "\n")
	for _, want := range []string{
		"We recommend switching from gpt-old to gpt-new.",
		"Latest recommended model for better performance. Learn more about gpt-new at https://www.codex.com/models/gpt-new",
		"You can continue using gpt-old if you prefer.",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("content missing %q:\n%s", want, joined)
		}
	}
	if !copy.CanOptOut || copy.Markdown != nil {
		t.Fatalf("copy flags = %#v", copy)
	}
}

func TestMigrationCopyForModelsMatchesRustCustomCopyAndForcedContinue(t *testing.T) {
	custom := "Upgrade to gpt-new for the latest model."
	copy := MigrationCopyForModels("gpt-old", "gpt-new", nil, &custom, nil, "gpt-new", nil, false)
	joined := strings.Join(copy.Content, "\n")
	if strings.Contains(joined, "We recommend switching") {
		t.Fatalf("custom copy should suppress generic recommendation:\n%s", joined)
	}
	if !strings.Contains(joined, custom) || !strings.Contains(joined, "Press enter to continue") {
		t.Fatalf("custom copy content = %q", joined)
	}
}

func TestMigrationMarkdownFillsPlaceholders(t *testing.T) {
	template := "Move from {model_from} to {model_to}.\nKeep {model_from} only if needed."
	copy := MigrationCopyForModels("gpt-old", "gpt-new", nil, nil, &template, "gpt-new", nil, false)
	if copy.Markdown == nil || *copy.Markdown != "Move from gpt-old to gpt-new.\nKeep gpt-old only if needed." {
		t.Fatalf("markdown = %#v", copy.Markdown)
	}
}

func TestModelMigrationScreenOptOutKeysMatchRust(t *testing.T) {
	screen := NewModelMigrationScreen(MigrationCopyForModels("gpt-old", "gpt-new", nil, nil, nil, "gpt-new", nil, true))
	screen.HandleKey("down")
	if screen.HighlightedOption() != MigrationMenuUseExistingModel {
		t.Fatalf("highlight = %v", screen.HighlightedOption())
	}
	screen.HandleKey("enter")
	if !screen.IsDone() || screen.Outcome() != ModelMigrationRejected {
		t.Fatalf("enter existing outcome = done %v outcome %v", screen.IsDone(), screen.Outcome())
	}

	screen = NewModelMigrationScreen(MigrationCopyForModels("gpt-old", "gpt-new", nil, nil, nil, "gpt-new", nil, true))
	screen.HandleKey("2")
	if !screen.IsDone() || screen.Outcome() != ModelMigrationRejected {
		t.Fatalf("2 outcome = done %v outcome %v", screen.IsDone(), screen.Outcome())
	}

	screen = NewModelMigrationScreen(MigrationCopyForModels("gpt-old", "gpt-new", nil, nil, nil, "gpt-new", nil, true))
	screen.HandleKey("esc")
	if !screen.IsDone() || screen.Outcome() != ModelMigrationAccepted {
		t.Fatalf("esc default outcome = done %v outcome %v", screen.IsDone(), screen.Outcome())
	}
}

func TestModelMigrationScreenForcedKeysMatchRust(t *testing.T) {
	screen := NewModelMigrationScreen(MigrationCopyForModels("gpt-old", "gpt-new", nil, nil, nil, "gpt-new", nil, false))
	screen.HandleKey("down")
	if screen.IsDone() {
		t.Fatal("forced prompt should ignore menu movement")
	}
	screen.HandleKey("esc")
	if !screen.IsDone() || screen.Outcome() != ModelMigrationAccepted {
		t.Fatalf("forced esc outcome = done %v outcome %v", screen.IsDone(), screen.Outcome())
	}

	screen = NewModelMigrationScreen(MigrationCopyForModels("gpt-old", "gpt-new", nil, nil, nil, "gpt-new", nil, false))
	screen.HandleKey("ctrl-c")
	if !screen.IsDone() || screen.Outcome() != ModelMigrationExit {
		t.Fatalf("ctrl-c outcome = done %v outcome %v", screen.IsDone(), screen.Outcome())
	}
}

func TestModelMigrationRowsUseSelectionColorBar(t *testing.T) {
	screen := NewModelMigrationScreen(MigrationCopyForModels("gpt-old", "gpt-new", nil, nil, nil, "gpt-new", nil, true))
	rows := screen.Rows()
	want := RenderSelectedRow(NumberedSelectionPrefix(0, true) + "Try new model")
	var selected bool
	for _, row := range rows {
		if row == want {
			selected = true
			break
		}
	}
	if !selected {
		t.Fatalf("rows missing selected color bar: %#v", rows)
	}
}
