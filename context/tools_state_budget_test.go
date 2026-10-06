package context

import (
	"strings"
	"testing"
)

// TestTruncateDeferredNamespaceRowsAtCharacterBoundariesLikeRust mirrors Rust
// tools_budget_tests.rs `truncates_raw_rows_at_character_boundaries_in_group_order`.
func TestTruncateDeferredNamespaceRowsAtCharacterBoundariesLikeRust(t *testing.T) {
	groups := []deferredNamespaceGroup{
		{label: "first", values: map[string]string{"🦀": "&🦀x", "&": "🦀&y"}},
		{label: "empty", values: map[string]string{}},
		{label: "last", values: map[string]string{"b": "x<&"}},
	}
	cases := []struct {
		byteBudget      int
		omissionReserve int
		want            []string // "" with omitted flag for omitted rows
		omitted         []bool
	}{
		{36, 64, []string{"- &: 🦀&y\n", "- 🦀: &🦀x\n", "- b: x<&\n"}, []bool{false, false, false}},
		{32, 64, []string{"- &: ...\n", "- 🦀: &...\n", "- b\n"}, []bool{false, false, false}},
		{15, 64, []string{"- &\n", "- 🦀\n", "- b\n"}, []bool{false, false, false}},
		{14, 6, []string{"- &\n", "", "- b\n"}, []bool{false, true, false}},
	}
	for _, tc := range cases {
		entries := truncateDeferredNamespaceRows(groups, tc.byteBudget, tc.omissionReserve)
		if len(entries) != len(tc.want) {
			t.Fatalf("budget=%d reserve=%d entries=%#v", tc.byteBudget, tc.omissionReserve, entries)
		}
		for i, entry := range entries {
			if entry.omitted != tc.omitted[i] {
				t.Fatalf("budget=%d reserve=%d row %d omitted=%v, want %v", tc.byteBudget, tc.omissionReserve, i, entry.omitted, tc.omitted[i])
			}
			if entry.text != tc.want[i] {
				t.Fatalf("budget=%d reserve=%d row %d text=%q, want %q", tc.byteBudget, tc.omissionReserve, i, entry.text, tc.want[i])
			}
		}
	}
}

// TestMarksTruncatedDescriptionsWithinAllocatedBytesLikeRust mirrors Rust
// tools_budget_tests.rs `marks_truncated_descriptions_within_the_allocated_bytes`.
func TestMarksTruncatedDescriptionsWithinAllocatedBytesLikeRust(t *testing.T) {
	cases := []struct {
		description string
		byteBudget  int
		want        string
	}{
		{"abcdef", 4, "- n\n"},
		{"abcdef", 7, "- n\n"},
		{"abcdef", 8, "- n\n"},
		{"abcdef", 9, "- n: ...\n"},
		{"abcdef", 10, "- n: a...\n"},
		{"abcdef", 12, "- n: abcdef\n"},
		{"ab", 8, "- n: ab\n"},
		{"🦀abcdef", 10, "- n: ...\n"},
		{"🦀abcdef", 13, "- n: 🦀...\n"},
		{"éabcdef", 11, "- n: é...\n"},
	}
	for _, tc := range cases {
		rows := truncateDeferredNamespaceRows(
			[]deferredNamespaceGroup{{label: "namespaces", values: map[string]string{"n": tc.description}}},
			tc.byteBudget,
			64,
		)
		if len(rows) != 1 || rows[0].omitted || rows[0].text != tc.want {
			t.Fatalf("description=%q budget=%d rows=%#v, want %q", tc.description, tc.byteBudget, rows, tc.want)
		}
		renderedBytes := len(rows[0].text)
		if renderedBytes > tc.byteBudget {
			t.Fatalf("description=%q budget=%d rendered %d bytes", tc.description, tc.byteBudget, renderedBytes)
		}
	}
}

// TestAllocatesDescriptionPrefixesRoundRobinLikeRust mirrors Rust
// tools_budget_tests.rs `allocates_complete_description_prefixes_in_stable_round_robin_order`.
func TestAllocatesDescriptionPrefixesRoundRobinLikeRust(t *testing.T) {
	cases := []struct {
		descriptions []string
		descBudget   int
		want         []int
	}{
		{[]string{}, 9, []int{}},
		{[]string{"", ""}, 9, []int{0, 0}},
		{[]string{"", "ab", ""}, 4, []int{0, 2, 0}},
		{[]string{"a", "🦀"}, 0, []int{0, 0}},
		{[]string{"a", "🦀"}, 2, []int{0, 0}},
		{[]string{"é€🦀", "xy"}, 20, []int{9, 2}},
		{[]string{"é€🦀", "xy"}, 12, []int{5, 2}},
		{[]string{"ab", "cd"}, 7, []int{2, 1}},
		{[]string{"a", "bcdef"}, 9, []int{1, 4}},
		{[]string{"a🦀x", "bcdef"}, 9, []int{1, 4}},
		{[]string{"🦀x", "abc"}, 5, []int{0, 3}},
		{[]string{"🦀🦀x", "€🦀y"}, 18, []int{9, 3}},
		{[]string{"🦀🦀x", "€🦀y"}, 19, []int{8, 7}},
	}
	for _, tc := range cases {
		rows := make([]deferredNamespaceRow, 0, len(tc.descriptions))
		for _, description := range tc.descriptions {
			rows = append(rows, deferredNamespaceRow{namespace: "n", description: description})
		}
		got := calculateDeferredRowTruncation(rows, tc.descBudget+4*len(rows), 64)
		if len(got) != len(tc.want) {
			t.Fatalf("descriptions=%q got=%v want=%v", tc.descriptions, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("descriptions=%q budget=%d got=%v want=%v", tc.descriptions, tc.descBudget, got, tc.want)
			}
		}
	}
}

// TestRetainsNamesBeforeDescriptionsLikeRust mirrors Rust
// tools_budget_tests.rs `retains_names_before_descriptions_and_reserves_omissions_only_on_overflow`.
func TestRetainsNamesBeforeDescriptionsLikeRust(t *testing.T) {
	cases := []struct {
		rows            []deferredNamespaceRow
		byteBudget      int
		omissionReserve int
		want            []int
	}{
		{[]deferredNamespaceRow{{namespace: "a", description: "abcd"}, {namespace: "b", description: "x"}}, 11, 64, []int{1, 0}},
		{[]deferredNamespaceRow{{namespace: "a"}, {namespace: "b"}}, 8, 64, []int{0, 0}},
		{[]deferredNamespaceRow{{namespace: "aaaa", description: "x"}, {namespace: "bbbb", description: "x"}, {namespace: "c", description: "x"}}, 15, 3, []int{0, -1, 0}},
		{[]deferredNamespaceRow{{namespace: "oversized_name_aaa", description: "x"}, {namespace: "a", description: "x"}, {namespace: "b", description: "x"}}, 14, 2, []int{-1, 0, 0}},
		{[]deferredNamespaceRow{{namespace: "ab", description: "x"}, {namespace: "c"}}, 0, 2, []int{-1, -1}},
		{[]deferredNamespaceRow{{namespace: "a", description: "x"}, {namespace: "b", description: "x"}}, 6, 64, []int{-1, -1}},
	}
	for _, tc := range cases {
		got := calculateDeferredRowTruncation(tc.rows, tc.byteBudget, tc.omissionReserve)
		if len(got) != len(tc.want) {
			t.Fatalf("rows=%#v got=%v want=%v", tc.rows, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("rows=%#v budget=%d reserve=%d got=%v want=%v", tc.rows, tc.byteBudget, tc.omissionReserve, got, tc.want)
			}
		}
	}
}

// TestDeferredNamespaceDescriptionTruncationAddsEllipsisLikeRust mirrors Rust
// tools.rs description normalization after #48574.
func TestDeferredNamespaceDescriptionTruncationAddsEllipsisLikeRust(t *testing.T) {
	long := strings.Repeat("界", maxDeferredNamespaceDescriptionRunes+10)
	got := NormalizeDeferredToolNamespaces(map[string]string{"app": long})["app"]
	if len([]rune(got)) != maxDeferredNamespaceDescriptionRunes {
		t.Fatalf("rune count = %d, want %d", len([]rune(got)), maxDeferredNamespaceDescriptionRunes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("normalized = %q, want ellipsis suffix", got)
	}
	exact := strings.Repeat("x", maxDeferredNamespaceDescriptionRunes)
	if got := NormalizeDeferredToolNamespaces(map[string]string{"app": exact})["app"]; got != exact {
		t.Fatalf("exact-cap description changed: %q", got)
	}
}
