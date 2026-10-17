package context

// BaseInstructionsContentKind classifies model base instructions in Responses
// metadata (Rust core/context/base_instructions.rs, #40296).
const BaseInstructionsContentKind = "model.base_instructions"

// BaseInstructionsFragment carries the model base instructions as a developer
// fragment with no visible markers. Responses Lite sends it as a separate
// message so its content classification survives request preparation.
type BaseInstructionsFragment struct {
	Text string
}

// Role returns the fragment's author role.
func (f BaseInstructionsFragment) Role() string {
	return RoleDeveloper
}

// Markers returns the empty marker pair used for unmarked base instructions.
func (f BaseInstructionsFragment) Markers() (string, string) {
	return "", ""
}

// Body returns the base instruction text.
func (f BaseInstructionsFragment) Body() string {
	return f.Text
}

// ContentKind returns the model base-instruction classification.
func (f BaseInstructionsFragment) ContentKind() string {
	return BaseInstructionsContentKind
}
