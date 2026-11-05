package state

import (
	"errors"
	"fmt"
	"testing"
)

// TestGuardianElasticSectionMinimumsReserveInputBudgetLikeRust covers Rust
// #58935: every effective section minimum, including the fallback policy for a
// group omitted from the map, must add up to DEFAULT_MAX_INPUT_TOKENS.
func TestGuardianElasticSectionMinimumsReserveInputBudgetLikeRust(t *testing.T) {
	defaults := GuardianElasticBudgetConfig{}
	if got, want := defaults.TotalMinimumTokens(), 15_900; got != want {
		t.Fatalf("default TotalMinimumTokens() = %d, want %d", got, want)
	}
	if err := defaults.Validate(); err == nil {
		t.Fatal("default Validate() = nil, want the fallback minimums to miss the input budget")
	} else {
		var mismatch GuardianElasticSectionMinimumTotalMismatch
		if !errors.As(err, &mismatch) || mismatch.Total != 15_900 {
			t.Fatalf("default Validate() error = %v, want a mismatch reporting 15900", err)
		}
	}

	complete := guardianElasticCompleteMinimumBudget(t, defaults)
	if got, want := complete.TotalMinimumTokens(), GuardianElasticDefaultMaxInputTokens; got != want {
		t.Fatalf("completed TotalMinimumTokens() = %d, want %d", got, want)
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("completed Validate() error = %v", err)
	}

	limits := complete.SectionLimits(GuardianElasticSectionTrustedSkills)
	for _, adjustment := range []int{-1, 1} {
		t.Run(fmt.Sprintf("offset_%d", adjustment), func(t *testing.T) {
			adjusted, err := NewGuardianElasticSectionLimits(
				limits.MinTotalSectionTokens+adjustment,
				limits.MaxSectionEntryTokens,
			)
			if err != nil {
				t.Fatalf("NewGuardianElasticSectionLimits() error = %v", err)
			}
			adjusted.WithMaxTotalSectionTokens(adjusted.MinTotalSectionTokens)
			config := complete
			config.Sections = map[GuardianElasticSection]GuardianElasticSectionLimits{
				GuardianElasticSectionTrustedSkills: adjusted,
			}
			err = config.Validate()
			mismatch, ok := err.(GuardianElasticSectionMinimumTotalMismatch)
			if !ok {
				t.Fatalf("Validate() error = %v, want GuardianElasticSectionMinimumTotalMismatch", err)
			}
			if want := GuardianElasticDefaultMaxInputTokens + adjustment; mismatch.Total != want {
				t.Fatalf("mismatch total = %d, want %d", mismatch.Total, want)
			}
		})
	}
}

// guardianElasticCompleteMinimumBudget funds the difference on trusted skills,
// the same way Rust's budget_tests::complete_minimum_budget fixture does. The
// other twelve groups keep their fallback reservations.
func guardianElasticCompleteMinimumBudget(t *testing.T, config GuardianElasticBudgetConfig) GuardianElasticBudgetConfig {
	t.Helper()
	limits := config.SectionLimits(GuardianElasticSectionTrustedSkills)
	minimum := limits.MinTotalSectionTokens + GuardianElasticDefaultMaxInputTokens - config.TotalMinimumTokens()
	completed, err := NewGuardianElasticSectionLimits(minimum, limits.MaxSectionEntryTokens)
	if err != nil {
		t.Fatalf("NewGuardianElasticSectionLimits(%d, %d) error = %v", minimum, limits.MaxSectionEntryTokens, err)
	}
	sections := make(map[GuardianElasticSection]GuardianElasticSectionLimits, len(config.Sections)+1)
	for section, limits := range config.Sections {
		sections[section] = limits
	}
	sections[GuardianElasticSectionTrustedSkills] = completed
	config.Sections = sections
	return config
}
