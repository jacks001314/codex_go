package model

import (
	"encoding/json"
	"testing"
)

func TestModelAccessProgramsParsingLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantNil bool
		want    []CyberAccessProgram
	}{
		{name: "absent", raw: `{"slug":"m"}`, wantNil: true},
		{name: "null", raw: `{"slug":"m","available_access_programs":null}`, wantNil: true},
		{name: "empty list", raw: `{"slug":"m","available_access_programs":{"cyber":[]}}`, want: []CyberAccessProgram{}},
		{
			name: "populated with unknown program",
			raw:  `{"slug":"m","available_access_programs":{"cyber":["standard","daybreak_blue","future_program","daybreak_red"]}}`,
			want: []CyberAccessProgram{CyberAccessProgramStandard, CyberAccessProgramDaybreakBlue, CyberAccessProgramDaybreakRed},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var info ModelInfo
			if err := json.Unmarshal([]byte(tc.raw), &info); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if tc.wantNil {
				if info.AvailableAccessPrograms != nil {
					t.Fatalf("available access programs = %#v, want nil", info.AvailableAccessPrograms)
				}
				return
			}
			if info.AvailableAccessPrograms == nil {
				t.Fatal("available access programs = nil, want non-nil")
			}
			if got := info.AvailableAccessPrograms.Cyber; len(got) != len(tc.want) {
				t.Fatalf("cyber = %#v, want %#v", got, tc.want)
			} else {
				for i := range tc.want {
					if got[i] != tc.want[i] {
						t.Fatalf("cyber[%d] = %q, want %q", i, got[i], tc.want[i])
					}
				}
			}
		})
	}
}

func TestModelInfoAccessProgramsRoundTripThroughCache(t *testing.T) {
	info := ModelInfo{
		Slug:                     "m",
		SupportedReasoningLevels: []string{"medium"},
		AvailableAccessPrograms:  &ModelAccessPrograms{Cyber: []CyberAccessProgram{CyberAccessProgramStandard}},
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTripped ModelInfo
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTripped.AvailableAccessPrograms == nil || len(roundTripped.AvailableAccessPrograms.Cyber) != 1 ||
		roundTripped.AvailableAccessPrograms.Cyber[0] != CyberAccessProgramStandard {
		t.Fatalf("round trip access programs = %#v", roundTripped.AvailableAccessPrograms)
	}
}

func TestModelSummarySerializesAvailableAccessProgramsLikeRust(t *testing.T) {
	marshalPrograms := func(info ModelInfo) map[string]any {
		t.Helper()
		data, err := json.Marshal(summaryFromModel(info, false))
		if err != nil {
			t.Fatalf("marshal summary: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("unmarshal summary: %v", err)
		}
		return payload
	}

	populated := marshalPrograms(ModelInfo{
		Slug:                     "m",
		SupportedReasoningLevels: []string{"medium"},
		AvailableAccessPrograms: &ModelAccessPrograms{Cyber: []CyberAccessProgram{
			CyberAccessProgramStandard,
			CyberAccessProgramDaybreakBlue,
			CyberAccessProgramDaybreakRed,
		}},
	})
	programs, ok := populated["availableAccessPrograms"].(map[string]any)
	if !ok {
		t.Fatalf("availableAccessPrograms = %#v", populated["availableAccessPrograms"])
	}
	cyber, ok := programs["cyber"].([]any)
	if !ok || len(cyber) != 3 || cyber[0] != "standard" || cyber[1] != "daybreakBlue" || cyber[2] != "daybreakRed" {
		t.Fatalf("cyber = %#v", programs["cyber"])
	}

	missing := marshalPrograms(ModelInfo{Slug: "n", SupportedReasoningLevels: []string{"medium"}})
	if value, ok := missing["availableAccessPrograms"]; !ok || value != nil {
		t.Fatalf("missing metadata = %#v, want explicit null", missing["availableAccessPrograms"])
	}

	empty := marshalPrograms(ModelInfo{
		Slug:                     "e",
		SupportedReasoningLevels: []string{"medium"},
		AvailableAccessPrograms:  &ModelAccessPrograms{Cyber: []CyberAccessProgram{}},
	})
	emptyPrograms, ok := empty["availableAccessPrograms"].(map[string]any)
	if !ok {
		t.Fatalf("empty metadata = %#v, want object", empty["availableAccessPrograms"])
	}
	emptyCyber, ok := emptyPrograms["cyber"].([]any)
	if !ok || len(emptyCyber) != 0 {
		t.Fatalf("empty cyber = %#v, want empty array", emptyPrograms["cyber"])
	}
}
