package mcp

import (
	"errors"
	"fmt"
	"strings"

	"codex_go/tool"
)

// Per-environment skill requirements, mirroring Rust #51157
// (`codex-rs/ext/skills/src/required.rs` plus
// `core/src/environment_selection.rs::TurnEnvironmentSnapshot::required_skills`).
// A selected environment may require exact catalog names; the turn must fail
// before its first model request when one of them is not supplied by that
// environment.

// EnvironmentSkillRequirements mirrors Rust `EnvironmentSkillRequirements`
// (Rust #51157): the exact skill catalog names one selected environment must
// supply before inference.
type EnvironmentSkillRequirements struct {
	// EnvironmentID is the selected environment that must supply the names.
	EnvironmentID string
	// SkillNames are the exact catalog names required from EnvironmentID,
	// stored verbatim like Rust's `ScopedSkillsConfig::required`.
	SkillNames []string
}

// EnvironmentSkillCatalogEntry is one discovered skill entry the check reads,
// carrying the Rust `SkillCatalogEntry` fields the check uses.
type EnvironmentSkillCatalogEntry struct {
	// Name is the exact catalog name (Rust `SkillCatalogEntry::name`).
	Name string
	// EnvironmentID is the environment that supplies the skill, i.e. the id
	// half of Rust's `main_prompt.environment_path()`. Empty covers skills
	// that are not environment scoped (host or orchestrator authority), which
	// can never satisfy a requirement because Rust matches on `Some((id, _))`.
	EnvironmentID string
	// Enabled mirrors `SkillCatalogEntry::enabled`: a disabled skill does not
	// satisfy a requirement.
	Enabled bool
}

// RequiredSkillsCatalog is the discovered skill state the check reads. Rust
// reads the skills extension's thread state (`SkillsThreadState`) and its
// executor step state; `Available` is the Go stand-in for the extension state
// being found at all, which Rust reports as an error rather than as a missing
// skill.
type RequiredSkillsCatalog struct {
	// Available reports that the skills extension state was found. When false,
	// a non-empty requirement list fails with
	// ErrRequiredSkillsExtensionUnavailable (Rust: "Required skills cannot be
	// validated because the skills extension is unavailable").
	Available bool
	// Entries are the discovered skills (executor plus cloud catalog in Rust).
	Entries []EnvironmentSkillCatalogEntry
}

// ErrRequiredSkillsExtensionUnavailable mirrors Rust `validate_required_skills`
// failing when the skills extension state is missing (Rust #51157).
var ErrRequiredSkillsExtensionUnavailable = errors.New("required skills cannot be validated because the skills extension is unavailable")

// RequiredEnvironmentSkills projects the ordered requirement list from a turn's
// captured executor selections, mirroring Rust
// `TurnEnvironmentSnapshot::required_skills` (Rust #51157): every selection
// contributes its environment's requirements, including a pending or failed
// one (the requirement follows the selection, not the connection state), and a
// selection whose environment requires nothing contributes nothing. A nil
// snapshot (threadless discovery) or nil lookup yields no requirements, and an
// environment that is no longer selected stops applying because it is absent
// from the snapshot.
func RequiredEnvironmentSkills(selected *SelectedEnvironments, required func(environmentID string) []string) []EnvironmentSkillRequirements {
	if selected == nil || required == nil {
		return nil
	}
	var out []EnvironmentSkillRequirements
	seen := map[string]bool{}
	for _, selection := range selected.Selections() {
		environmentID := strings.TrimSpace(selection.EnvironmentID)
		if environmentID == "" || seen[environmentID] {
			continue
		}
		skillNames := required(environmentID)
		if len(skillNames) == 0 {
			continue
		}
		seen[environmentID] = true
		out = append(out, EnvironmentSkillRequirements{
			EnvironmentID: environmentID,
			SkillNames:    append([]string(nil), skillNames...),
		})
	}
	return out
}

// ValidateRequiredEnvironmentSkills mirrors Rust `validate_required_skills`
// (Rust #51157): every required name must have an enabled catalog entry
// supplied by the requiring environment, otherwise the check fails. The
// returned error renders like Rust's `CodexErr::Fatal` ("Fatal error: Required
// skill \"review\" from environment \"required\" is unavailable"), so a caller
// that fails the turn exposes the same client-visible message.
func ValidateRequiredEnvironmentSkills(requirements []EnvironmentSkillRequirements, catalog RequiredSkillsCatalog) error {
	if len(requirements) == 0 {
		return nil
	}
	if !catalog.Available {
		return ErrRequiredSkillsExtensionUnavailable
	}
	for _, requirement := range requirements {
		for _, name := range requirement.SkillNames {
			if !requiredSkillAvailable(requirement.EnvironmentID, name, catalog.Entries) {
				return tool.Fatal(RequiredSkillUnavailableMessage(name, requirement.EnvironmentID))
			}
		}
	}
	return nil
}

// RequiredSkillUnavailableMessage renders the missing-skill failure exactly the
// way Rust's `format!("Required skill {name:?} from environment
// {environment_id:?} is unavailable")` does (`{:?}` on a string is Go's `%q`).
func RequiredSkillUnavailableMessage(name string, environmentID string) string {
	return fmt.Sprintf("Required skill %q from environment %q is unavailable", name, environmentID)
}

func requiredSkillAvailable(environmentID string, name string, entries []EnvironmentSkillCatalogEntry) bool {
	for _, entry := range entries {
		if entry.Enabled && entry.Name == name && entry.EnvironmentID == environmentID {
			return true
		}
	}
	return false
}
