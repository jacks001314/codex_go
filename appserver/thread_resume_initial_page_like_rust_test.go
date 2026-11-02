package appserver

import "testing"

// Rust #58751 (codex-rs/app-server/src/request_processors/thread_lifecycle.rs):
// an ascending initial page only loads the live turn when the prepared page
// already contains it or the page is the final, underfilled one.
func TestResumeActiveTurnBelongsInInitialPageLikeRust(t *testing.T) {
	full := &TurnsPage{
		Data:       []Turn{{ID: "turn-1"}, {ID: "turn-2"}},
		NextCursor: stringPtr("cursor-1"),
	}
	underfilled := &TurnsPage{Data: []Turn{{ID: "turn-1"}}}
	withActive := &TurnsPage{
		Data:       []Turn{{ID: "turn-1"}, {ID: "turn-active"}},
		NextCursor: stringPtr("cursor-1"),
	}

	cases := []struct {
		name         string
		page         *TurnsPage
		sort         SortDirection
		pageSize     int
		activeTurnID string
		want         bool
	}{
		{"descending always leads with the active turn", full, SortDesc, 2, "turn-active", true},
		{"ascending full page without the active turn", full, SortAsc, 2, "turn-active", false},
		{"ascending final underfilled page", underfilled, SortAsc, 2, "turn-active", true},
		{"ascending page already holding the active turn", withActive, SortAsc, 2, "turn-active", true},
		{"ascending empty page before the store answers", nil, SortAsc, 2, "turn-active", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resumeActiveTurnBelongsInInitialPage(tc.page, tc.sort, tc.pageSize, tc.activeTurnID); got != tc.want {
				t.Fatalf("resumeActiveTurnBelongsInInitialPage() = %v, want %v", got, tc.want)
			}
		})
	}
}
