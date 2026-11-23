package config

import "testing"

// Mirrors Rust #63377 `project_diagnostic_uses_the_current_file_and_ignores_its_provider`:
// an unknown project setting ahead of an invalid one must not mask the type
// error, and the project layer's stripped `tui.status_line_provider` must not
// take precedence over the setting the user has to fix.
func TestProjectConfigDiagnosticKeepsTypeErrorAfterUnknownSettingLikeRust(t *testing.T) {
	const text = "ignored_setting = true\n[tui]\nstatus_line_provider = 42\nvim_mode_default = 43\n"
	const want = "invalid type: integer `43`, expected a boolean\nin `tui.vim_mode_default`"
	values, err := ParseConfigDocumentValues([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		t.Fatalf("tui table = %#v", values["tui"])
	}
	if _, present := values["ignored_setting"]; !present {
		t.Fatalf("unknown setting was dropped by the parser: %#v", values)
	}
	// Rust #61211 removes the provider key from a project layer before it
	// diagnoses the layer.
	delete(tui, "status_line_provider")
	if err := ValidateConfigValues(values); err == nil {
		t.Fatal("ValidateConfigValues() accepted the invalid vim setting")
	} else if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}

	// The unknown key is irrelevant to the outcome: the same document without it
	// reports the identical error.
	withoutUnknown, err := ParseConfigDocumentValues([]byte("[tui]\nvim_mode_default = 43\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ValidateConfigValues(withoutUnknown); err == nil || err.Error() != want {
		t.Fatalf("error without the unknown key = %v, want %q", err, want)
	}
}
