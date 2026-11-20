package tui

// Rust parity: codex-rs/tui/src/ui_tree_tests.rs (upstream #62569). A root row
// with justify "between" reaches both footer edges at every width, wraps per
// line, and rounds each item's share to the nearest cell; an explicit "start"
// groups adjacent bare strings into one item.

import (
	"encoding/json"
	"strings"
	"testing"
)

func uiTreeJustifyRow(gap int, wrap string, justify string, children ...any) map[string]any {
	props := map[string]any{"gap": gap}
	if wrap != "" {
		props["wrap"] = wrap
	}
	if justify != "" {
		props["justify"] = justify
	}
	return map[string]any{"type": "row", "props": props, "children": children}
}

func uiTreeJustifyTree(t *testing.T, value map[string]any) *UINode {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal tree: %v", err)
	}
	tree, err := ParseUITree(encoded)
	if err != nil {
		t.Fatalf("parse tree: %v", err)
	}
	return tree
}

func uiTreeJustifyRows(t *testing.T, value map[string]any, width int, height int) []string {
	t.Helper()
	return uiTreePreparedRows(uiTreeJustifyTree(t, value), width, height)
}

func TestUITreeReadsRowJustifyLikeRust(t *testing.T) {
	row := func(props any) map[string]any {
		return map[string]any{"type": "row", "props": props, "children": []any{"ok"}}
	}
	parse := func(props any) (*UINode, error) {
		encoded, err := json.Marshal(row(props))
		if err != nil {
			t.Fatalf("marshal row: %v", err)
		}
		return ParseUITree(encoded)
	}
	assertEqual := func(actual any, expected string, label string) {
		t.Helper()
		encoded, err := json.Marshal(actual)
		if err != nil {
			t.Fatalf("marshal %s: %v", label, err)
		}
		if string(encoded) != expected {
			t.Fatalf("%s = %s, want %s", label, encoded, expected)
		}
	}

	explicit := []any{1, "red", "wrap", "start"}
	positional, err := parse(explicit)
	if err != nil {
		t.Fatalf("positional justify row: %v", err)
	}
	named, err := parse(map[string]any{"gap": 1, "background": "red", "wrap": "wrap", "justify": "start"})
	if err != nil {
		t.Fatalf("named justify row: %v", err)
	}
	if named.Justify == nil || *named.Justify != UIRowJustifyStart {
		t.Fatalf("named row justify = %v, want %q", named.Justify, UIRowJustifyStart)
	}
	// The positional form round-trips to the named form, so the encoder only
	// ever emits named props.
	assertEqual(positional, string(mustJSON(t, named)), "positional row")
	if positional.Gap != 1 || positional.Background == nil || *positional.Background != "red" || positional.Wrap != UIRowWrapWrap {
		t.Fatalf("positional row props = %d/%v/%q", positional.Gap, positional.Background, positional.Wrap)
	}

	for _, invalid := range []any{"right", "space-between", nil} {
		if _, err := parse(map[string]any{"gap": 0, "justify": invalid}); err == nil {
			t.Fatalf("justify %#v decoded without an error", invalid)
		}
	}
}

func TestUITreeBetweenKeepsGroupsAtOppositeEdgesLikeRust(t *testing.T) {
	group := func(fields [2]string) map[string]any {
		children := make([]any, 0, len(fields))
		for _, field := range fields {
			children = append(children, uiTreeText(field))
		}
		return uiTreeJustifyRow(1, "", "", children...)
	}
	tree := uiTreeJustifyRow(1, "wrap", "between", group([2]string{"界", "git"}), group([2]string{"75%", "2k"}))

	if width, height := uiTreeJustifyTree(t, tree).Prepare().Measure(20, 2); width != 20 || height != 1 {
		t.Fatalf("measure at width 20 = %dx%d, want 20x1", width, height)
	}
	if rows := uiTreeJustifyRows(t, tree, 20, 2); len(rows) != 1 || rows[0] != "界 git        75% 2k" {
		t.Fatalf("rows at width 20 = %#v", rows)
	}
	if rows := uiTreeJustifyRows(t, tree, 13, 2); len(rows) != 1 || rows[0] != "界 git 75% 2k" {
		t.Fatalf("rows at width 13 = %#v", rows)
	}
	if rows := uiTreeJustifyRows(t, tree, 12, 2); strings.Join(rows, "|") != "界 git|75% 2k" {
		t.Fatalf("rows at width 12 = %#v", rows)
	}
}

func TestUITreeBetweenDistributesIndivisibleCellsLikeRust(t *testing.T) {
	children := make([]any, 0, 4)
	for _, label := range []string{"a", "b", "c", "longest"} {
		children = append(children, uiTreeText(label))
	}
	tree := uiTreeJustifyRow(1, "wrap", "between", children...)
	if rows := uiTreeJustifyRows(t, tree, 8, 2); strings.Join(rows, "|") != "a   b  c|longest" {
		t.Fatalf("rows at width 8 = %#v", rows)
	}
}

func TestUITreeStartGroupsBareStringsLikeRust(t *testing.T) {
	children := []any{"L", nil, "R", uiTreeText("end")}
	if rows := uiTreeJustifyRows(t, uiTreeJustifyRow(1, "", "", children...), 12, 1); len(rows) != 1 || rows[0] != "L R end" {
		t.Fatalf("authored gaps rows = %#v", rows)
	}
	start := uiTreeJustifyRow(1, "", "start", children...)
	if rows := uiTreeJustifyRows(t, start, 12, 1); len(rows) != 1 || rows[0] != "LR end" {
		t.Fatalf("start rows = %#v", rows)
	}
}

func TestUITreeBetweenRootAndNestedRowsLikeRust(t *testing.T) {
	cases := []struct {
		label    string
		children []any
		root     string
		nested   string
	}{
		{label: "strings", children: []any{"L", "R"}, root: "LR", nested: "LR"},
		{label: "elements", children: []any{uiTreeText("L"), uiTreeText("R")}, root: "L        R", nested: "LR"},
		{label: "mixed", children: []any{"L", nil, "M", uiTreeText("R")}, root: "LM       R", nested: "LMR"},
		{label: "empty", children: []any{uiTreeText(""), uiTreeText("R")}, root: "         R", nested: "R"},
	}
	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			inner := uiTreeJustifyRow(0, "", "between", testCase.children...)
			outer := uiTreeJustifyRow(0, "", "", inner)
			root := strings.Join(uiTreeJustifyRows(t, inner, 10, 1), "\n")
			nested := strings.Join(uiTreeJustifyRows(t, outer, 10, 1), "\n")
			if root != testCase.root || nested != testCase.nested {
				t.Fatalf("root/nested = %q/%q, want %q/%q", root, nested, testCase.root, testCase.nested)
			}
		})
	}
}

func TestUITreeBetweenRoundsFractionalPositionsLikeRust(t *testing.T) {
	children := make([]any, 0, 6)
	for _, label := range []string{"A", "B", "C", "D", "E", "F"} {
		children = append(children, uiTreeText(label))
	}
	tree := uiTreeJustifyRow(0, "", "between", children...)
	if rows := uiTreeJustifyRows(t, tree, 14, 1); len(rows) != 1 || rows[0] != "A  B C  D E  F" {
		t.Fatalf("rows at width 14 = %#v", rows)
	}
}

func TestUITreeBetweenShrinksHyphenatedWordLikeRust(t *testing.T) {
	for _, justify := range []string{"", "between"} {
		tree := uiTreeJustifyRow(1, "", justify, "aa-bb", uiTreeText("!"))
		if rows := uiTreeJustifyRows(t, tree, 6, 2); strings.Join(rows, "|") != "aa-  !|bb" {
			t.Fatalf("justify %q rows = %#v", justify, rows)
		}
	}
}
