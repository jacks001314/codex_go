package config

import (
	"testing"

	"codex_go/apps"
)

// TestParseRequirementsTOMLAppsLikeRust covers the managed app requirement
// surface (Rust ConfigRequirementsToml.apps).
func TestParseRequirementsTOMLAppsLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[apps.calendar]
enabled = false

[apps.calendar.tools."events/create"]
approval_mode = "approve"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || len(requirements.Apps) != 1 {
		t.Fatalf("requirements = %#v", requirements)
	}
	calendar, ok := requirements.Apps["calendar"]
	if !ok || calendar.Enabled == nil || *calendar.Enabled {
		t.Fatalf("calendar requirement = %#v", calendar)
	}
	create, ok := calendar.Tools["events/create"]
	if !ok || create.ApprovalMode == nil || *create.ApprovalMode != apps.AppToolApprovalApprove {
		t.Fatalf("events/create requirement = %#v", calendar.Tools)
	}
}

// TestMergeConfigRequirementsAppsDescendingLikeRust covers Rust
// merge_app_requirements_descending wiring into the requirements merge: either
// layer can disable an app, and an exact tool approval keeps the
// higher-precedence value.
func TestMergeConfigRequirementsAppsDescendingLikeRust(t *testing.T) {
	approve := apps.AppToolApprovalApprove
	prompt := apps.AppToolApprovalPrompt
	enabled := true
	disabled := false
	base := &ConfigRequirements{Apps: apps.AppsRequirements{
		"calendar": {Enabled: &enabled, Tools: map[string]apps.AppToolRequirement{
			"events/create": {ApprovalMode: &approve},
		}},
	}}
	overlay := &ConfigRequirements{Apps: apps.AppsRequirements{
		"calendar": {Enabled: &disabled, Tools: map[string]apps.AppToolRequirement{
			"events/create": {ApprovalMode: &prompt},
			"events/list":   {ApprovalMode: &prompt},
		}},
		"drive": {Enabled: &enabled},
	}}
	merged := mergeConfigRequirements(base, overlay)
	if merged.Apps["calendar"].Enabled == nil || *merged.Apps["calendar"].Enabled {
		t.Fatalf("a lower layer must be able to disable an app: %#v", merged.Apps["calendar"])
	}
	if mode := merged.Apps["calendar"].Tools["events/create"].ApprovalMode; mode == nil || *mode != apps.AppToolApprovalApprove {
		t.Fatalf("higher-precedence tool approval lost: %#v", merged.Apps["calendar"].Tools)
	}
	if mode := merged.Apps["calendar"].Tools["events/list"].ApprovalMode; mode == nil || *mode != apps.AppToolApprovalPrompt {
		t.Fatalf("lower-precedence tool approval not adopted: %#v", merged.Apps["calendar"].Tools)
	}
	if merged.Apps["drive"].Enabled == nil || !*merged.Apps["drive"].Enabled {
		t.Fatalf("distinct app requirement not unioned: %#v", merged.Apps)
	}
	// The merge must not mutate the higher-precedence input.
	if base.Apps["calendar"].Enabled == nil || !*base.Apps["calendar"].Enabled {
		t.Fatalf("merge mutated the base requirements: %#v", base.Apps["calendar"])
	}
}

// TestCloneRequirementsAppsDeepCopiesLikeRust pins the clone used by the
// requirements merge.
func TestCloneRequirementsAppsDeepCopiesLikeRust(t *testing.T) {
	approve := apps.AppToolApprovalApprove
	original := &ConfigRequirements{Apps: apps.AppsRequirements{
		"calendar": {Tools: map[string]apps.AppToolRequirement{
			"events/create": {ApprovalMode: &approve},
		}},
	}}
	cloned := cloneRequirements(original)
	if cloned.Apps["calendar"].Tools["events/create"].ApprovalMode == nil {
		t.Fatalf("clone lost the tool approval: %#v", cloned.Apps)
	}
	prompt := apps.AppToolApprovalPrompt
	tool := cloned.Apps["calendar"].Tools["events/create"]
	tool.ApprovalMode = &prompt
	cloned.Apps["calendar"].Tools["events/create"] = tool
	if mode := original.Apps["calendar"].Tools["events/create"].ApprovalMode; mode == nil || *mode != apps.AppToolApprovalApprove {
		t.Fatalf("clone aliased the original: %#v", original.Apps)
	}
}
