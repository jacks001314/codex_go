package state

import (
	"encoding/json"
	"testing"
)

func TestGuardianElasticSectionMaxTotalRoundTripLikeRust(t *testing.T) {
	limits, err := NewGuardianElasticSectionLimits(300, 100)
	if err != nil {
		t.Fatal(err)
	}
	limits, err = limits.WithMaxTotalSectionTokens(500)
	if err != nil {
		t.Fatal(err)
	}
	config := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionTranscriptTools: limits,
	}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var parsed GuardianElasticBudgetConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal error = %v (%s)", err, raw)
	}
	got := parsed.Sections[GuardianElasticSectionTranscriptTools]
	if got.MaxTotalSectionTokens == nil || *got.MaxTotalSectionTokens != 500 {
		t.Fatalf("max total = %v, want 500", got.MaxTotalSectionTokens)
	}
	var invalid struct {
		Sections map[string]GuardianElasticSectionLimits `json:"sections"`
	}
	body := []byte(`{"sections":{"transcript_tools":{"min_total_section_tokens":300,"max_section_entry_tokens":100,"max_total_section_tokens":200}}}`)
	if err := json.Unmarshal(body, &invalid); err == nil {
		t.Fatal("decoded max_total_section_tokens below the minimum")
	}
}

func TestGuardianElasticAllocationHonorsSectionMaximumLikeRust(t *testing.T) {
	groups := []GuardianElasticSection{
		GuardianElasticSectionTranscriptMessages,
		GuardianElasticSectionTranscriptTools,
	}
	config := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{}}
	for _, group := range groups {
		limits, err := NewGuardianElasticSectionLimits(100, 100)
		if err != nil {
			t.Fatal(err)
		}
		if group == GuardianElasticSectionTranscriptMessages {
			limits, err = limits.WithMaxTotalSectionTokens(200)
			if err != nil {
				t.Fatal(err)
			}
		}
		config.Sections[group] = limits
	}
	demand := guardianElasticDemand{
		groups:        map[GuardianElasticSection]*guardianElasticGroupDemand{},
		contentCounts: make([]int, len(groups)),
		userContent:   make([]bool, len(groups)),
	}
	for sectionIndex, group := range groups {
		entry := &guardianElasticGroupDemand{}
		for itemIndex := 0; itemIndex < 10; itemIndex++ {
			entry.pending = append(entry.pending, guardianElasticCandidate{
				sectionIndex: sectionIndex,
				itemIndex:    itemIndex,
				cost:         100,
			})
			entry.totalCost += 100
		}
		demand.groups[group] = entry
	}
	selected := allocateGuardianElasticItems(&demand, config, 800)
	counts := []int{0, 0}
	for key := range selected {
		counts[key[0]]++
	}
	if counts[0]*100 > 200 {
		t.Fatalf("capped group selected %d tokens, want <= 200", counts[0]*100)
	}
	if counts[1] <= counts[0] {
		t.Fatalf("uncapped group counts = %v, want more than the capped group", counts)
	}
}
