package tui

import (
	"strings"
	"testing"
)

func TestOSSSelectionCtrlHLMoveProviderSelection(t *testing.T) {
	widget := NewOSSSelectionWidget(OSSProviderUnknown, OSSProviderUnknown)
	if widget.SelectedIndex != 0 {
		t.Fatalf("initial selected = %d", widget.SelectedIndex)
	}
	widget.HandleKey("ctrl-l")
	if widget.SelectedIndex != 1 {
		t.Fatalf("selected after ctrl-l = %d", widget.SelectedIndex)
	}
	widget.HandleKey("ctrl-h")
	if widget.SelectedIndex != 0 {
		t.Fatalf("selected after ctrl-h = %d", widget.SelectedIndex)
	}
}

func TestOSSSelectionShortcutsAndFallbacksMatchRust(t *testing.T) {
	widget := NewOSSSelectionWidget(OSSProviderUnknown, OSSProviderUnknown)
	selection, done := widget.HandleKey("O")
	if !done || selection != OllamaOSSProviderID {
		t.Fatalf("O selection = %q done=%v", selection, done)
	}

	widget = NewOSSSelectionWidget(OSSProviderUnknown, OSSProviderUnknown)
	selection, done = widget.HandleKey("esc")
	if !done || selection != LMStudioOSSProviderID {
		t.Fatalf("Esc selection = %q done=%v", selection, done)
	}

	widget = NewOSSSelectionWidget(OSSProviderUnknown, OSSProviderUnknown)
	selection, done = widget.HandleKey("ctrl-c")
	if !done || selection != OSSCancelledProvider {
		t.Fatalf("Ctrl-C selection = %q done=%v", selection, done)
	}
}

func TestAutoSelectOSSProviderMatchesRust(t *testing.T) {
	selection, ok := AutoSelectOSSProvider(OSSProviderRunning, OSSProviderNotRunning)
	if !ok || selection.Provider != LMStudioOSSProviderID || selection.ManuallySelected {
		t.Fatalf("lmstudio auto selection = %#v ok=%v", selection, ok)
	}
	selection, ok = AutoSelectOSSProvider(OSSProviderNotRunning, OSSProviderRunning)
	if !ok || selection.Provider != OllamaOSSProviderID || selection.ManuallySelected {
		t.Fatalf("ollama auto selection = %#v ok=%v", selection, ok)
	}
	if _, ok := AutoSelectOSSProvider(OSSProviderRunning, OSSProviderRunning); ok {
		t.Fatal("both running should show UI")
	}
	if _, ok := AutoSelectOSSProvider(OSSProviderNotRunning, OSSProviderNotRunning); ok {
		t.Fatal("both stopped should show UI")
	}
}

func TestOSSSelectionRowsUseSelectedColorBar(t *testing.T) {
	widget := NewOSSSelectionWidget(OSSProviderRunning, OSSProviderNotRunning)
	rows := strings.Join(widget.Rows(120), "\n")
	for _, want := range []string{"Select an open-source provider", SelectionPrefix(true) + "LM Studio", "\x1b["} {
		if !strings.Contains(rows, want) {
			t.Fatalf("rows missing %q:\n%s", want, rows)
		}
	}
	widget.HandleKey("right")
	rows = strings.Join(widget.Rows(120), "\n")
	if !strings.Contains(rows, SelectionPrefix(true)+"Ollama") {
		t.Fatalf("selected row did not move:\n%s", rows)
	}
}
