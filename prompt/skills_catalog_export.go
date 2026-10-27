package prompt

// SkillMetadataCost reports the metadata budget cost of text in the budget's
// unit (Rust #57262 SkillMetadataBudget::cost).
func SkillMetadataCost(budget SkillMetadataBudget, text string) int {
	return budget.cost(text)
}

// SkillMetadataLineCost reports the cost of one catalog row including its
// trailing newline (Rust metadata_line_cost).
func SkillMetadataLineCost(budget SkillMetadataBudget, line string) int {
	return budget.cost(line + "\n")
}

// AliasedSkillMetadataOverheadCost reports the extra body cost a skill root
// table adds over an unaliased catalog (Rust aliased_metadata_overhead_cost).
func AliasedSkillMetadataOverheadCost(budget SkillMetadataBudget, rootLines []string, omitDescriptions bool) int {
	return aliasedMetadataOverheadCostWithPolicy(budget, rootLines, skillCatalogPolicy{omitDescriptions: omitDescriptions})
}

// RenderSkillInstructionsBody renders a complete <skills_instructions> body from
// catalog rows (Rust #57262 catalog_delta::listing_body +
// AvailableSkillsInstructions). It is used when an incremental catalog must
// publish its first listing from accumulated rows.
func RenderSkillInstructionsBody(rootLines []string, skillLines []string, includeUsage bool, omitDescriptions bool) string {
	return renderAvailableSkillsBodyWithPolicy(rootLines, skillLines, includeUsage, skillCatalogPolicy{omitDescriptions: omitDescriptions})
}

// SkillUsageInstructions returns the how-to-use text for the catalog layout
// implied by hasRoots (Rust SkillPrompt::usage_instructions).
func SkillUsageInstructions(hasRoots bool, omitDescriptions bool) string {
	instructions := SkillsHowToUseWithAbsolutePaths
	if hasRoots {
		instructions = SkillsHowToUseWithAliases
	}
	if omitDescriptions {
		instructions = skillCatalogUsageInstructionsWithoutDescriptions(instructions)
	}
	return instructions
}

// SkillRenderWarningWithPolicy mirrors Rust #57262
// SkillRenderReport::warning_message: incremental catalogs report omissions
// without the description-removal prefix.
func SkillRenderWarningWithPolicy(report *SkillRenderReport, budget SkillMetadataBudget, catalogDeltas bool) *string {
	return skillRenderWarningWithPolicy(report, budget, catalogDeltas)
}
