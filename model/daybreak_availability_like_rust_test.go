package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #62499: model discovery carries the server-resolved Daybreak status for
// this caller and model. Missing and unrecognized catalog values decode as
// Default, and the value keeps its snake_case catalog spelling on output.
func TestModelAccessProgramsDecodesDaybreakAvailabilityLikeRust(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want DaybreakAvailability
	}{
		{"available", `{"cyber":["standard"],"daybreak_availability":"available"}`, DaybreakAvailabilityAvailable},
		{"enrollment required", `{"cyber":["standard"],"daybreak_availability":"enrollment_required"}`, DaybreakAvailabilityEnrollmentRequired},
		{"unavailable for model", `{"cyber":["standard"],"daybreak_availability":"unavailable_for_model"}`, DaybreakAvailabilityUnavailableForModel},
		{"explicit default", `{"cyber":["standard"],"daybreak_availability":"default"}`, DaybreakAvailabilityDefault},
		{"future status", `{"cyber":["standard"],"daybreak_availability":"future_status"}`, DaybreakAvailabilityDefault},
		{"missing status", `{"cyber":["standard"]}`, DaybreakAvailabilityDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var programs ModelAccessPrograms
			if err := json.Unmarshal([]byte(tc.raw), &programs); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.raw, err)
			}
			if programs.DaybreakAvailability != tc.want {
				t.Fatalf("DaybreakAvailability = %q, want %q", programs.DaybreakAvailability, tc.want)
			}
			if len(programs.Cyber) != 1 || programs.Cyber[0] != CyberAccessProgramStandard {
				t.Fatalf("Cyber = %#v", programs.Cyber)
			}
			encoded, err := json.Marshal(programs)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			want := `"daybreak_availability":"` + string(tc.want) + `"`
			if !strings.Contains(string(encoded), want) {
				t.Fatalf("catalog output = %s, want %s", encoded, want)
			}
		})
	}
}

// TestModelAccessProgramsSummaryExposesCamelCaseAvailabilityLikeRust mirrors
// the app-server model/list shape: camelCase member and camelCase value, with
// missing or unknown statuses normalized to "default".
func TestModelAccessProgramsSummaryExposesCamelCaseAvailabilityLikeRust(t *testing.T) {
	summary := ModelAccessProgramsSummaryFromInfo(&ModelAccessPrograms{
		Cyber:                []CyberAccessProgram{CyberAccessProgramStandard, CyberAccessProgramDaybreakBlue},
		DaybreakAvailability: DaybreakAvailabilityEnrollmentRequired,
	})
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"cyber":["standard","daybreakBlue"],"daybreakAvailability":"enrollmentRequired"}`
	if string(encoded) != want {
		t.Fatalf("model/list access programs = %s, want %s", encoded, want)
	}

	for _, tc := range []struct {
		value DaybreakAvailability
		want  string
	}{
		{DaybreakAvailabilityAvailable, "available"},
		{DaybreakAvailabilityUnavailableForModel, "unavailableForModel"},
		{DaybreakAvailabilityDefault, "default"},
		{"", "default"},
		{"future_status", "default"},
	} {
		if got := DaybreakAvailabilityWireValue(tc.value); got != tc.want {
			t.Fatalf("DaybreakAvailabilityWireValue(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}

	if ModelAccessProgramsSummaryFromInfo(nil) != nil {
		t.Fatal("nil access programs produced a summary")
	}
}

// Legacy catalogs without access-program metadata or the status stay loadable.
func TestModelAccessProgramsLegacyMetadataStaysLoadableLikeRust(t *testing.T) {
	var info ModelInfo
	if err := json.Unmarshal([]byte(`{"slug":"m","display_name":"M","available_access_programs":{"cyber":["standard","daybreak_blue"]}}`), &info); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if info.AvailableAccessPrograms == nil {
		t.Fatal("access programs missing")
	}
	if info.AvailableAccessPrograms.DaybreakAvailability != DaybreakAvailabilityDefault {
		t.Fatalf("availability = %q, want default", info.AvailableAccessPrograms.DaybreakAvailability)
	}
}
