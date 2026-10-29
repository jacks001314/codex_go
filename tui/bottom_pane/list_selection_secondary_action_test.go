package bottompane

import (
	"strings"
	"testing"
)

// Mirrors Rust #45831
// secondary_action_respects_search_disabled_rows_and_dismissal.
func TestSecondaryActionRespectsSearchDisabledRowsAndDismissal(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		searchable bool
		disabled   bool
		key        string
		wantKind   string
	}{
		{name: "secondary", key: "s", wantKind: "secondary"},
		{name: "primary", key: "enter", wantKind: "primary"},
		{name: "disabled row", disabled: true, key: "s", wantKind: ""},
		{name: "searchable view", searchable: true, key: "s", wantKind: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var ran []string
			view := NewListSelectionView("Select model", []ListSelectionItem{{
				ID:              "model",
				Label:           "gpt-test",
				SearchValue:     "gpt-test",
				Disabled:        testCase.disabled,
				DismissOnSelect: true,
				SecondaryAction: &ListSelectionSecondaryAction{
					Key:        "s",
					Run:        func() { ran = append(ran, "secondary") },
					FooterHint: "s to set for this session only",
				},
			}})
			view.Searchable = testCase.searchable
			view.ApplyFilter()

			result, accepted := view.HandleKey(testCase.key)
			switch testCase.wantKind {
			case "secondary":
				if !accepted || len(ran) != 1 || ran[0] != "secondary" {
					t.Fatalf("secondary accept = %v ran=%v", accepted, ran)
				}
			case "primary":
				if !accepted || len(ran) != 0 {
					t.Fatalf("primary accept = %v ran=%v", accepted, ran)
				}
			default:
				if accepted || len(ran) != 0 {
					t.Fatalf("unexpected accept = %v ran=%v", accepted, ran)
				}
			}
			if accepted != (view.Completion != nil) || (accepted && !result.Accepted) {
				t.Fatalf("completion = %#v result = %#v", view.Completion, result)
			}
		})
	}
}

func TestSecondaryActionFooterHintWinsOverViewFooter(t *testing.T) {
	view := NewListSelectionView("Select reasoning", []ListSelectionItem{{
		ID:              "high",
		Label:           "High",
		SecondaryAction: &ListSelectionSecondaryAction{Key: "s", FooterHint: "s to set for this session only"},
	}})
	view.FooterHint = "enter to select"
	view.ApplyFilter()
	if got := view.ActiveFooterHint(); got != "s to set for this session only" {
		t.Fatalf("active footer hint = %q", got)
	}
	if rows := strings.Join(view.Rows(60), "\n"); !strings.Contains(rows, "s to set for this session only") {
		t.Fatalf("rendered rows missing secondary hint:\n%s", rows)
	}

	plain := NewListSelectionView("Select reasoning", []ListSelectionItem{{ID: "high", Label: "High"}})
	plain.FooterHint = "enter to select"
	plain.ApplyFilter()
	if got := plain.ActiveFooterHint(); got != "enter to select" {
		t.Fatalf("fallback footer hint = %q", got)
	}
}
