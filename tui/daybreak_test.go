package tui

import "testing"

func TestDaybreakNoticeFromCyberProgramsLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		programs []string
		want     DaybreakNotice
	}{
		{
			name:     "astra standard access without Daybreak",
			model:    "gpt-6-astra",
			programs: []string{"standard"},
			want:     DaybreakNoticeAstra,
		},
		{
			name:     "astra standard access with Daybreak",
			model:    "gpt-6-astra-wm",
			programs: []string{"standard", "daybreak_blue"},
			want:     DaybreakNoticeLimited,
		},
		{
			name:     "sol with Daybreak",
			model:    "gpt-5.6-sol",
			programs: []string{"daybreak_red"},
			want:     DaybreakNoticeLimited,
		},
		{
			name:     "standard access uses application guidance",
			model:    "gpt-5.6-sol",
			programs: []string{"standard"},
			want:     DaybreakNoticeApply,
		},
		{
			name:  "missing model metadata",
			model: "gpt-6-astra",
			want:  DaybreakNoticeApply,
		},
		{
			name:     "unknown programs are ignored",
			model:    "gpt-6-astra",
			programs: []string{"future_program"},
			want:     DaybreakNoticeApply,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DaybreakNoticeFromCyberPrograms(test.model, test.programs); got != test.want {
				t.Fatalf("DaybreakNoticeFromCyberPrograms(%q, %#v) = %v, want %v", test.model, test.programs, got, test.want)
			}
		})
	}
}
