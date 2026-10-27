package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// skillCatalogIntro adapts the catalog introduction to the description policy
// (Rust #57500 SkillPrompt::intro): with descriptions omitted the text names
// only the fields the model can still see.
func skillCatalogIntro(intro string, policy skillCatalogPolicy) string {
	if !policy.omitDescriptions {
		return intro
	}
	return strings.ReplaceAll(intro, "name, description, and", "name and")
}

// skillCatalogUsageInstructionsWithoutDescriptions mirrors Rust's
// SkillPrompt::usage_instructions replacements for the omitted policy.
func skillCatalogUsageInstructionsWithoutDescriptions(instructions string) string {
	instructions = strings.ReplaceAll(instructions, "name + description +", "name +")
	return strings.ReplaceAll(instructions, "description shown above", "name shown above")
}

// skillCatalogSelection is one rendered catalog together with the per-entry
// comparison metadata the incremental catalog needs.
type skillCatalogSelection struct {
	skillLines []string
	report     *SkillRenderReport
	rootLines  []string
	entries    []SkillCatalogEntryRender
}

// renderBestSkillCatalogForScope mirrors Rust #41011/#57262: the aliased
// catalog is evaluated regardless of budget pressure and selected whenever it
// preserves skill inclusion and description content while reducing prompt
// size. The selected layout also determines the comparison metadata.
func renderBestSkillCatalogForScope(lines []skillRenderLine, budget SkillMetadataBudget, scope string, policy skillCatalogPolicy) skillCatalogSelection {
	absolute := renderSkillCatalogLines(lines, budget, nil, policy)

	plan, ok := buildSkillAliasPlanForScope(lines, budget, scope)
	if !ok || plan.tableCost >= budget.Limit {
		return absolute
	}
	adjustedBudget := budget
	adjustedBudget.Limit -= plan.tableCost
	aliased := renderSkillCatalogLines(lines, adjustedBudget, plan, policy)
	if aliasedRenderIsBetterWithPolicy(aliased.skillLines, aliased.report, aliased.rootLines, absolute.skillLines, absolute.report, nil, budget, policy) {
		return aliased
	}
	return absolute
}

// renderSkillCatalogLines renders one catalog, applying plan (when non-nil) to
// the rows and recording which rows were shown.
func renderSkillCatalogLines(lines []skillRenderLine, budget SkillMetadataBudget, plan *skillAliasPlan, policy skillCatalogPolicy) skillCatalogSelection {
	applied := applySkillAliases(lines, plan)
	allocations := allocateSkillLines(applied, budget)
	rendered, report := renderAllocatedSkillLines(applied, allocations)
	var rootLines []string
	if plan != nil {
		rootLines = plan.rootLines
	}
	return skillCatalogSelection{
		skillLines: rendered,
		report:     report,
		rootLines:  rootLines,
		entries:    skillCatalogEntriesForRender(lines, applied, allocations, plan, policy),
	}
}

// skillCatalogEntriesForRender maps every catalog entry to its comparison
// metadata (Rust #57262 PreparedSkillCatalog::entries_for_render). original and
// applied are index-aligned; applied is original with alias rewriting applied.
func skillCatalogEntriesForRender(original []skillRenderLine, applied []skillRenderLine, allocations []skillLineAllocation, plan *skillAliasPlan, policy skillCatalogPolicy) []SkillCatalogEntryRender {
	if len(original) == 0 {
		return nil
	}
	entries := make([]SkillCatalogEntryRender, 0, len(original))
	for index, line := range original {
		row := line
		allocation := skillLineAllocation{}
		if index < len(applied) {
			row = applied[index]
		}
		if index < len(allocations) {
			allocation = allocations[index]
		}
		rendered := row.renderWithDescriptionChars(allocation.descriptionChars)
		entry := SkillCatalogEntryRender{
			ID:          line.id,
			Fingerprint: skillCatalogEntryFingerprint(line.name, policy.description(line.description), line.mainPrompt, line.path),
			Line:        rendered,
			Shown:       !allocation.omitted,
		}
		if plan != nil {
			if rootValue := plan.aliasRootByPath[line.path]; rootValue != "" && row.path != line.path {
				entry.RootValue = rootValue
				entry.RootName = plan.rootAliases[rootValue]
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// skillCatalogEntryID builds the cross-turn identity Rust hashes into
// CatalogSkill.catalog_id: authority kind, authority id, and entry id.
func skillCatalogEntryID(skill InstructionsSkillMetadata) string {
	kind := strings.TrimSpace(skill.AuthorityKind)
	authorityID := strings.TrimSpace(skill.AuthorityID)
	entryID := firstNonEmptyString(skill.ResourceID, skill.PackageID, skillRenderLocator(skill), skill.Path, skill.Name)
	data, err := json.Marshal([]string{kind, authorityID, entryID})
	if err != nil {
		return kind + "\x00" + authorityID + "\x00" + entryID
	}
	return string(data)
}

// skillCatalogEntryFingerprint hashes the model-visible fields of one entry.
// Rust uses blake3; Go only compares these values against its own persisted
// snapshots, so a stable SHA-256 keeps the same semantics.
func skillCatalogEntryFingerprint(name string, description string, mainPrompt string, locator string) string {
	data, err := json.Marshal([]string{name, description, mainPrompt, locator})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
