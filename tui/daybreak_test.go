package tui

import (
	"encoding/json"
	"testing"
)

func TestDaybreakRefusalCopyUsesVerifiedAccess(t *testing.T) {
	cases := []struct {
		programs string
		want     DaybreakNotice
	}{
		{`[]`, DaybreakNoticeApply},
		{`[{"program":"cyber","state":"inactive","grants":[]}]`, DaybreakNoticeApply},
		{`[{"program":"cyber","state":"unavailable","grants":[]}]`, DaybreakNoticeLimited},
		{`[{"program":"cyber","state":"active","grants":[{"level":"tac2"}]}]`, DaybreakNoticeLimited},
		{`[{"program":"cyber","state":"active","grants":[]}]`, DaybreakNoticeLimited},
		{`[{"program":"cyber","state":"inactive","grants":[{"level":"tac2"}]}]`, DaybreakNoticeLimited},
		{`[{"program":"cyber","state":"unknown","grants":[]}]`, DaybreakNoticeLimited},
		// Unknown programs are ignored, so an inactive cyber program still means
		// the apply copy.
		{`[{"program":"something_else"},{"program":"cyber","state":"inactive","grants":[]}]`, DaybreakNoticeApply},
	}
	for _, test := range cases {
		var access DaybreakVerifiedAccess
		if err := json.Unmarshal([]byte(`{"programs":`+test.programs+`}`), &access); err != nil {
			t.Fatalf("unmarshal %s: %v", test.programs, err)
		}
		if got := access.Notice(); got != test.want {
			t.Fatalf("Notice(%s) = %v, want %v", test.programs, got, test.want)
		}
	}
}

func TestDaybreakNoticeForModel(t *testing.T) {
	cases := []struct {
		notice DaybreakNotice
		model  string
		want   DaybreakNotice
	}{
		{DaybreakNoticeLimited, "gpt-6-astra", DaybreakNoticeAstra},
		{DaybreakNoticeLimited, "gpt-6-astra-wm", DaybreakNoticeAstra},
		{DaybreakNoticeApply, "gpt-5.6-sol", DaybreakNoticeApply},
		{DaybreakNoticeLimited, "gpt-5.6-sol", DaybreakNoticeLimited},
		{DaybreakNoticeApply, "gpt-5.1-codex", DaybreakNoticeLimited},
		{DaybreakNoticeApply, "", DaybreakNoticeLimited},
	}
	for _, test := range cases {
		if got := DaybreakNoticeForModel(test.notice, test.model); got != test.want {
			t.Fatalf("DaybreakNoticeForModel(%v, %q) = %v, want %v", test.notice, test.model, got, test.want)
		}
	}
}
