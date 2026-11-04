package model

import "testing"

// Rust #59192: saved Daybreak intent only selects programs the model
// advertises; unset stays omitted.
func TestSavedDaybreakChoiceSelectsAdvertisedProgramsLikeRust(t *testing.T) {
	tests := []struct {
		name    string
		cyber   []CyberAccessProgram
		wantDay CyberAccessProgram
		wantStd CyberAccessProgram
		hasDay  bool
		hasStd  bool
	}{
		{name: "blue preferred", cyber: []CyberAccessProgram{CyberAccessProgramStandard, CyberAccessProgramDaybreakRed, CyberAccessProgramDaybreakBlue}, wantDay: CyberAccessProgramDaybreakBlue, wantStd: CyberAccessProgramStandard, hasDay: true, hasStd: true},
		{name: "red fallback", cyber: []CyberAccessProgram{CyberAccessProgramDaybreakRed}, wantDay: CyberAccessProgramDaybreakRed, hasDay: true},
		{name: "daybreak without standard", cyber: []CyberAccessProgram{CyberAccessProgramDaybreakBlue}, wantDay: CyberAccessProgramDaybreakBlue, hasDay: true},
		{name: "empty metadata", cyber: []CyberAccessProgram{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			programs := &ModelAccessPrograms{Cyber: test.cyber}
			on := true
			off := false
			if got := programs.SelectSaved(SavedDaybreakChoiceFrom(&on)); (got != nil) != test.hasDay || (got != nil && *got != test.wantDay) {
				t.Fatalf("saved on = %v, want %v (present=%v)", got, test.wantDay, test.hasDay)
			}
			if got := programs.SelectSaved(SavedDaybreakChoiceFrom(&off)); (got != nil) != test.hasStd || (got != nil && *got != test.wantStd) {
				t.Fatalf("saved off = %v, want %v (present=%v)", got, test.wantStd, test.hasStd)
			}
			if got := programs.SelectSaved(SavedDaybreakChoiceFrom(nil)); got != nil {
				t.Fatalf("unset selected %v", *got)
			}
		})
	}
	var missing *ModelAccessPrograms
	if got := missing.SelectSaved(SavedDaybreakChoiceOn); got != nil {
		t.Fatalf("nil programs selected %v", *got)
	}
}
