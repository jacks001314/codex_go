package state

import (
	"encoding/json"
	"testing"
)

// TestGuardianElasticSectionConfigValidatesEntryLimitsLikeRust covers Rust
// #58123: elastic section policies round-trip and reject empty quotas,
// oversized entries and minimums that cannot hold one capped entry.
func TestGuardianElasticSectionConfigValidatesEntryLimitsLikeRust(t *testing.T) {
	cap := GuardianMaxTextPartEstimatedTokens
	valid := map[string]any{
		"sections": map[string]any{
			"transcript_tools": map[string]any{
				"min_total_section_tokens": cap,
				"max_section_entry_tokens": cap,
			},
		},
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	var parsed GuardianElasticBudgetConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	roundTrip, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("Marshal parsed error = %v", err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("Unmarshal want error = %v", err)
	}
	if err := json.Unmarshal(roundTrip, &got); err != nil {
		t.Fatalf("Unmarshal got error = %v", err)
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("round trip = %s, want %s", gotJSON, wantJSON)
	}
	limits := parsed.Sections[GuardianElasticSectionTranscriptTools]
	if limits.MinTotalSectionTokens != cap || limits.MaxSectionEntryTokens != cap {
		t.Fatalf("limits = %#v", limits)
	}

	for _, tc := range []struct {
		minimum  int
		entryCap int
	}{
		{0, cap},
		{cap, 0},
		{cap - 1, cap},
		{cap + 1, cap + 1},
	} {
		invalid := map[string]any{
			"sections": map[string]any{
				"transcript_tools": map[string]any{
					"min_total_section_tokens": tc.minimum,
					"max_section_entry_tokens": tc.entryCap,
				},
			},
		}
		body, err := json.Marshal(invalid)
		if err != nil {
			t.Fatalf("Marshal invalid error = %v", err)
		}
		var decoded GuardianElasticBudgetConfig
		if err := json.Unmarshal(body, &decoded); err == nil {
			t.Fatalf("decoded invalid limits min=%d entry=%d", tc.minimum, tc.entryCap)
		}
	}

	unknownSection := []byte(`{"sections":{"unknown_group":{"min_total_section_tokens":1,"max_section_entry_tokens":1}}}`)
	var decoded GuardianElasticBudgetConfig
	if err := json.Unmarshal(unknownSection, &decoded); err == nil {
		t.Fatal("decoded an unknown elastic section")
	}
	unknownField := []byte(`{"sections":{},"extra":true}`)
	if err := json.Unmarshal(unknownField, &decoded); err == nil {
		t.Fatal("decoded an unknown config field")
	}
	if _, err := NewGuardianElasticSectionLimits(cap, cap); err != nil {
		t.Fatalf("NewGuardianElasticSectionLimits() error = %v", err)
	}
	if _, err := NewGuardianElasticSectionLimits(cap+5, cap+1); err != GuardianElasticLimitsEntryExceedsDelivery {
		t.Fatalf("oversized entry error = %v", err)
	}
}
