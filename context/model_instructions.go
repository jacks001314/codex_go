package context

import "strings"

const (
	// ModelInstructionsContentKind classifies replacement model instructions in
	// Responses metadata (Rust core/context/model_instructions.rs, #53872).
	ModelInstructionsContentKind = "model.instructions"
	modelInstructionsReplacement = "The following instructions replace the previous base instructions:"
	modelInstructionsRemoval     = "The previous base instructions no longer apply. No replacement base instructions are active."
)

// ModelInstructionsFragment reports an independently changed model identity,
// base-instruction replacement, or base-instruction removal.
type ModelInstructionsFragment struct {
	// Model is empty when the model did not change.
	Model string
	// Instructions is nil when instructions did not change. A non-nil empty
	// value explicitly removes the previous base instructions.
	Instructions *string
}

// Role returns the fragment's author role.
func (f ModelInstructionsFragment) Role() string {
	return RoleDeveloper
}

// Markers returns the model-instruction wrapper tags.
func (f ModelInstructionsFragment) Markers() (string, string) {
	return "<model_instructions>", "</model_instructions>"
}

// Body renders the model and instruction transitions.
func (f ModelInstructionsFragment) Body() string {
	var body strings.Builder
	body.WriteString("\n")
	if modelID := strings.TrimSpace(f.Model); modelID != "" {
		body.WriteString("The current model is ")
		body.WriteString(modelID)
		body.WriteString(".\n")
	}
	if f.Instructions == nil {
		return body.String()
	}
	if strings.TrimSpace(*f.Instructions) == "" {
		body.WriteString(modelInstructionsRemoval)
		body.WriteString("\n")
		return body.String()
	}
	body.WriteString(modelInstructionsReplacement)
	body.WriteString("\n\n")
	body.WriteString(*f.Instructions)
	body.WriteString("\n")
	return body.String()
}

// ContentKind returns the model-instructions classification.
func (f ModelInstructionsFragment) ContentKind() string {
	return ModelInstructionsContentKind
}
