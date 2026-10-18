package prompt

import _ "embed"

// BuiltinPlanModeInstructions is the Plan-mode developer preset clients send
// as collaboration-mode overrides. Rust #41744 matches this entire preset (not
// just a heading) before removing bundled update_plan guidance, so custom
// instructions that mention planning stay unchanged.
//
//go:embed collaboration_mode_plan.md
var builtinPlanModeInstructions string

// BuiltinPlanModeInstructions returns the built-in Plan-mode preset verbatim.
func BuiltinPlanModeInstructions() string {
	return builtinPlanModeInstructions
}
