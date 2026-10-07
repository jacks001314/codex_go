package mermaid

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// decodeRustSnapshot turns the `\uXXXX` escapes of a captured Rust snapshot back
// into the diagram's Unicode output.
func decodeRustSnapshot(t *testing.T, encoded string) string {
	t.Helper()
	var builder strings.Builder
	for i := 0; i < len(encoded); i++ {
		if encoded[i] == '\\' && i+5 < len(encoded) && encoded[i+1] == 'u' {
			value, err := strconv.ParseUint(encoded[i+2:i+6], 16, 32)
			if err == nil {
				builder.WriteRune(rune(value))
				i += 5
				continue
			}
		}
		builder.WriteByte(encoded[i])
	}
	return builder.String()
}

// Mirrors Rust's `branches_merges_and_retry_loop` snapshot.
func TestRenderBranchesMergesAndRetryLoopMatchesRust(t *testing.T) {
	const source = "flowchart TD\nA[Checkout] --> B{In stock?}\nB -->|yes| C[Reserve]\nB -->|no| D[Waitlist]\nC --> E{Paid?}\nE -->|yes| F[Ship]\nE -->|no| G[Retry payment]\nG --> E\nD --> H[Notify buyer]\nF --> H"
	const expected = `\u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510
\u2502 Checkout      \u2502
\u2502               \u251c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510
\u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518        \u2502
                         \u2502
                         \u2502
\u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510        \u2502
\u2502 \u25c7 In stock?   \u2502        \u2502
\u2502               \u251c\u25c4\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518
\u2502               \u251c\u2500\u2500yes\u2500\u2500\u2500\u2500\u2500\u2510
\u2502               \u251c\u2500\u2500no\u2500\u2500\u2500\u2500\u2500\u2500\u256a\u2500\u2510
\u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518          \u2502 \u2502
`
	want := decodeRustSnapshot(t, expected)
	got, err := Render(source, 100)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.HasPrefix(got, want) {
		t.Fatalf("Render() prefix mismatch:\n%s\nwant prefix:\n%s", got, want)
	}
}

// Mirrors Rust's `stadium_declarations_and_references`: repeated declarations of
// the same node must agree, and a bare reference does not clear a declaration.
func TestStadiumDeclarationsAndReferencesMatchRust(t *testing.T) {
	graph, err := parseFlowchart("flowchart TD", []string{"A", "A([Ready?])", "A", "A([Ready?])"})
	if err != nil {
		t.Fatalf("parseFlowchart() error = %v", err)
	}
	if len(graph.nodes) != 1 {
		t.Fatalf("nodes = %#v, want one", graph.nodes)
	}
	node := graph.nodes[0]
	if node.id != "A" || node.label != "Ready?" || node.shape != shapeStadium || !node.declared || len(node.members) != 0 {
		t.Fatalf("node = %#v", node)
	}
}

// Mirrors Rust's `equivalent_flowchart_forms_preserve_graph` (#48895 renamed
// `quoted_flowchart_labels_match_unquoted_labels`): every equivalent spelling
// produces the same graph.
func TestEquivalentFlowchartFormsPreserveGraph(t *testing.T) {
	quoted := []string{
		`A["Review & confirm"] -->|"Yes & continue"| B{"Ready?"}`,
		`B --> C(["Checkout"])`,
		`A[Review & confirm]`,
	}
	unquoted := make([]string, 0, len(quoted))
	for _, line := range quoted {
		unquoted = append(unquoted, strings.ReplaceAll(line, `"`, ""))
	}
	withQuotes, err := parseFlowchart("flowchart TD", quoted)
	if err != nil {
		t.Fatalf("quoted parse error = %v", err)
	}
	withoutQuotes, err := parseFlowchart("flowchart TD", unquoted)
	if err != nil {
		t.Fatalf("unquoted parse error = %v", err)
	}
	if !graphsEqual(withQuotes, withoutQuotes) {
		t.Fatalf("quoted graph = %#v, want %#v", withQuotes, withoutQuotes)
	}
	for _, testCase := range []struct {
		infix string
		pipe  string
	}{
		{"-- Yes -->", "-->|Yes|"},
		{`-- "Yes & continue" -->`, `-->|"Yes & continue"|`},
		{"-. retry .->", "-.->|retry|"},
	} {
		infix, err := parseFlowchart("flowchart", []string{"A " + testCase.infix + " B --> C"})
		if err != nil {
			t.Fatalf("parseFlowchart(infix %q) error = %v", testCase.infix, err)
		}
		pipe, err := parseFlowchart("graph TB", []string{"A " + testCase.pipe + " B --> C"})
		if err != nil {
			t.Fatalf("parseFlowchart(pipe %q) error = %v", testCase.pipe, err)
		}
		if !graphsEqual(infix, pipe) {
			t.Fatalf("infix %q = %#v, want %#v", testCase.infix, infix, pipe)
		}
	}
	grouped, err := parseFlowchart("graph", []string{"A[Input & config] & B -- send --> C & D -.-> E"})
	if err != nil {
		t.Fatalf("parseFlowchart(grouped) error = %v", err)
	}
	expanded, err := parseFlowchart("flowchart TD", []string{
		"A[Input & config]",
		"B",
		"C",
		"D",
		"E",
		"A -->|send| C",
		"A -->|send| D",
		"B -->|send| C",
		"B -->|send| D",
		"C -.-> E",
		"D -.-> E",
	})
	if err != nil {
		t.Fatalf("parseFlowchart(expanded) error = %v", err)
	}
	if !graphsEqual(grouped, expanded) {
		t.Fatalf("grouped graph = %#v, want %#v", grouped, expanded)
	}
	spaced, err := parseFlowchart("flowchart", []string{"A--- oB", "A-.- xB"})
	if err != nil {
		t.Fatalf("parseFlowchart(spaced) error = %v", err)
	}
	declared, err := parseFlowchart("flowchart", []string{"A", "oB", "xB"})
	if err != nil {
		t.Fatalf("parseFlowchart(declared) error = %v", err)
	}
	if !nodesEqual(spaced.nodes, declared.nodes) {
		t.Fatalf("spaced nodes = %#v, want %#v", spaced.nodes, declared.nodes)
	}
	repeated, err := parseFlowchart("flowchart", []string{"A --> B & B"})
	if err != nil {
		t.Fatalf("parseFlowchart(repeated) error = %v", err)
	}
	twice, err := parseFlowchart("flowchart TD", []string{"A --> B", "A --> B"})
	if err != nil {
		t.Fatalf("parseFlowchart(twice) error = %v", err)
	}
	if !graphsEqual(repeated, twice) {
		t.Fatalf("repeated graph = %#v, want %#v", repeated, twice)
	}
	for _, testCase := range []struct {
		source string
		label  string
	}{
		{`A["(Database)"]`, "(Database)"},
		{`A["/Input/"]`, "/Input/"},
	} {
		graph, err := parseFlowchart("flowchart TD", []string{testCase.source})
		if err != nil {
			t.Fatalf("parse %q error = %v", testCase.source, err)
		}
		if len(graph.nodes) != 1 || graph.nodes[0].label != testCase.label || graph.nodes[0].shape != shapeRectangle {
			t.Fatalf("parse %q = %#v", testCase.source, graph.nodes)
		}
	}
}

// Mirrors Rust's `quoted_flowchart_labels_reject_malformed_and_unsafe_text`.
func TestQuotedFlowchartLabelsRejectMalformedAndUnsafeText(t *testing.T) {
	for _, label := range []string{
		`"unfinished`,
		`unfinished"`,
		`"embedded"quote"`,
		`""`,
		`"<b>HTML</b>"`,
		`"before <b"`,
		"\"\x1b\"",
	} {
		for _, source := range []string{
			"flowchart TD; A[" + label + "]",
			"flowchart TD; A -->|" + label + "| B",
			"flowchart TD; A -- " + label + " --> B",
		} {
			if _, err := Render(source, 100); err != ErrUnsupported {
				t.Fatalf("Render(%q) error = %v, want ErrUnsupported", source, err)
			}
		}
	}
}

// Mirrors Rust's `rejects_partial_or_unsupported_input`: every entry must keep
// the source fallback rather than misrendering (including #48489's longer shape
// delimiters and Markdown strings).
func TestFlowchartRejectsUnsupportedInput(t *testing.T) {
	sources := []string{
		"flowchart TD; P --> Q; A[(Database)]",
		"flowchart TD; P --> Q; A[/Input/]",
		`flowchart TD; P --> Q; A[\Output\]`,
		`flowchart TD; P --> Q; A[/Trapezoid\]`,
		`flowchart TD; P --> Q; A[\Inverse/]`,
		"flowchart TD; P --> Q; A[[Subroutine]]",
		"flowchart TD; P --> Q; A{{Hexagon}}",
		"flowchart TD; P --> Q; A[\"`hello **world**`\"]",
		"flowchart TD; P --> Q; A{\"`Decision`\"}",
		"flowchart TD; P --> Q; A([\"`Stadium`\"])",
		"flowchart TD; P --> Q; A -->|\"`Caption`\"| B",
		"flowchart TD; subgraph X; A; end",
		"flowchart TD; A --> B; garbage syntax",
		"flowchart TD; A --> B &",
		"flowchart TD; A && B",
		"flowchart TD; A -- Yes B",
		"flowchart TD; A -. retry --> B",
		"flowchart TD; A ==> B",
		"flowchart TD; A -- hello --- B --> C",
		"flowchart TD; A -. hello .- B -.-> C",
		"flowchart TD; A -- hello ----> B",
		"flowchart TD; A -. hello ..-> B",
		"flowchart TD; A -- hello o--> B",
		"flowchart TD; A --oB --> C",
		"flowchart TD; A -. hello -.-> B",
		"flowchart TD; A---oB",
		"flowchart TD; A-.-xB",
		"flowchart TD; A[one]; A[two]",
		"flowchart TD; A[<b>HTML</b>]",
		"flowchart TD; A[&#27;]",
		"flowchart TD; A[\u001b]",
		"flowchart TD; A[e\u0301]",
		"flowchart TD; A[\U0001f44d\U0001f3fd]",
		"flowchart TD; A[\U0001f469\u200d\U0001f4bb]",
		"flowchart TD; A[\u2708\ufe0f]",
		"flowchart TD; A[zero\u200bwidth]",
		"flowchart TD; A[left\u202eright]",
		"flowchart TD; A[\u2066isolated\u2069]",
		"flowchart TD; A[unclosed",
		"flowchart TD; click A",
		"flowchart TD; A((circle))",
		"flowchart TD; A(rounded)",
		"flowchart TD; A([unclosed]",
		"flowchart TD; A([unclosed)",
		"flowchart TD; A([label]) trailing",
		"flowchart TD; A([])",
		"flowchart TD; A([nested[label]])",
		"flowchart TD; A([one]); A([two])",
		"flowchart TD; A([same]); A[same]",
		"flowchart TD; A{same}; A([same])",
		"flowchart TD; A -->|unclosed B",
		"flowchart TD; A -->|yes\u2510| B",
	}
	for _, source := range sources {
		if _, err := Render(source, 100); err != ErrUnsupported {
			t.Fatalf("Render(%q) error = %v, want ErrUnsupported", source, err)
		}
	}
	// Rust's `unicode-width` 0.2 collapses the Arabic lam-alef ligature, so
	// `A[\u0644\u0627]` fails `check_label_text`'s ligature check and is rejected
	// there; `textWidth` reproduces that collapse (width_arabic.go), so Go rejects
	// it too.
	if _, err := Render("flowchart TD; A[\u0644\u0627]", 100); err != ErrUnsupported {
		t.Fatalf("Render(arabic label) error = %v, want ErrUnsupported", err)
	}
}

// TestPreservesPunctuationAndSemicolonsLikeRust mirrors Rust #48814: flowchart
// labels may carry printable punctuation and semicolons (protected by their
// delimiters), quotes protect flowchart delimiters, and class bodies keep their
// semicolons as member text.
func TestPreservesPunctuationAndSemicolonsLikeRust(t *testing.T) {
	output, err := Render("flowchart TD; A[foo;bar] --> B", 100)
	if err != nil {
		t.Fatalf("Render(A[foo;bar]) error = %v", err)
	}
	if !strings.Contains(output, "foo;bar") {
		t.Fatalf("semicolon label not preserved: %q", output)
	}
	for _, source := range []string{
		"flowchart TD; A[\"a[b|c{d}e]\"]",
		"flowchart TD; A -->|\"x;y|z\"| B",
		"classDiagram\nclass A; A --> B : {ok}",
		"classDiagram\nclass A; class B {\n+id;name\n}",
		"classDiagram\nclass A {\nstring a;b\nObject[] elementData\n}\nA : +get()",
		"stateDiagram-v2; state \"a;b\" as A; A-->B: [ready]",
		"erDiagram; A {; string value \"a;b\"; }",
		"sequenceDiagram\nU->>A: data[0] = {x: 1}",
		"sequenceDiagram; A->>B: say \"hello; B->>A: world\"",
	} {
		if _, err := Render(source, 100); err != nil {
			t.Fatalf("Render(%q) error = %v, want success", source, err)
		}
	}
}

// Mirrors Rust's #48814 addition to `quoted_flowchart_labels_match_unquoted_labels`:
// quoted flowchart delimiters protect `[`, `{`, `|`, `;`, and `<`/`?` text so the
// parsed graph keeps the literal label and edge text.
func TestQuotedFlowchartDelimitersProtectPunctuation(t *testing.T) {
	statements, err := splitStatements(`graph TD;A["chimpansen hoppar ()[]"] -->|"x | y; z"| B{"x < 3?"};`)
	if err != nil {
		t.Fatalf("splitStatements error = %v", err)
	}
	parsed, err := parseFlowchart(statements[0], statements[1:])
	if err != nil {
		t.Fatalf("parseFlowchart error = %v", err)
	}
	want := &graph{}
	a, _ := want.node("A")
	b, _ := want.node("B")
	want.nodes[a].label = "chimpansen hoppar ()[]"
	want.nodes[a].declared = true
	want.nodes[b].label = "x < 3?"
	want.nodes[b].shape = shapeDecision
	want.nodes[b].declared = true
	want.edges = append(want.edges, directedEdge(a, b, "x | y; z"))
	if !graphsEqual(parsed, want) {
		t.Fatalf("parse = %#v, want %#v", parsed, want)
	}
	// `;`-separated class statements render the same as newline-separated ones.
	for _, source := range []string{
		"classDiagram\nclass A; A --> B : {ok}",
		"classDiagram\nclass A; class B {\n+id\n}",
	} {
		withSeparators, err := Render(source, 100)
		if err != nil {
			t.Fatalf("Render(%q) error = %v", source, err)
		}
		withNewlines, err := Render(strings.ReplaceAll(source, ";", "\n"), 100)
		if err != nil {
			t.Fatalf("Render(newlines) error = %v", err)
		}
		if withSeparators != withNewlines {
			t.Fatalf("separator mismatch for %q:\n%s\nwant:\n%s", source, withSeparators, withNewlines)
		}
	}
}

// Mirrors Rust's `entity_labels_keep_source_fallback` (#48814): undecoded entity
// escapes must be rejected across every family before statement splitting can
// truncate them into literal label text.
func TestEntityLabelsKeepSourceFallback(t *testing.T) {
	for _, entity := range []string{"&amp;", "&#38;", "&#x26;", "#9829;", "#semi;"} {
		for _, source := range []string{
			"sequenceDiagram\nA->>B: " + entity,
			"classDiagram\nclass A {\n" + entity + "\n}",
			"stateDiagram-v2; A-->B: " + entity,
			"erDiagram\nA {\nstring value \"" + entity + "\"\n}",
			"flowchart TD; A[\"" + entity + "\"]",
			"flowchart TD; A -->|\"" + entity + "\"| B",
		} {
			if _, err := Render(source, 100); err != ErrUnsupported {
				t.Fatalf("Render(%q) error = %v, want ErrUnsupported", source, err)
			}
		}
	}
}

// Mirrors Rust's `flowchart_ampersands_preserve_statement_separators`: bare
// ampersands are plain label text, and comments do not affect splitting.
func TestFlowchartAmpersandsPreserveStatementSeparators(t *testing.T) {
	const source = "flowchart TD; A[R&D] -->|R&D| B[Review & confirm]; B --> C"
	withComment, err := Render("%% &amp;\n"+source, 100)
	if err != nil {
		t.Fatalf("Render(with comment) error = %v", err)
	}
	split, err := Render(strings.ReplaceAll(source, ";", "\n"), 100)
	if err != nil {
		t.Fatalf("Render(split) error = %v", err)
	}
	if withComment != split {
		t.Fatalf("comment changed output:\n%s\nwant:\n%s", withComment, split)
	}
	if !strings.Contains(split, "Review & confirm") {
		t.Fatalf("output missing ampersand label:\n%s", split)
	}
}

// Mirrors Rust's `source_graph_and_width_limits`.
func TestSourceGraphAndWidthLimits(t *testing.T) {
	var manyNodes strings.Builder
	for n := 0; n < 17; n++ {
		manyNodes.WriteString("N" + strconv.Itoa(n) + ";")
	}
	const grouped = "A & B & C & D --> E & F & G & H & I & J"
	if _, err := Render("graph; "+grouped, 200); err != nil {
		t.Fatalf("Render(grouped graph) error = %v", err)
	}
	for _, source := range []string{
		strings.Repeat(" ", 16*1024+1),
		"graph TD; A[" + strings.Repeat("x", 41) + "]",
		"graph; A -- " + strings.Repeat("x", 41) + " --> B",
		"graph; A --> E; " + grouped,
		"graph; " + strings.Repeat("A & ", 24) + "A --> B",
		"graph TD; A[\"" + strings.Repeat("[]", 21) + "\"]",
		"graph TD; A([" + strings.Repeat("x", 41) + "])",
		"graph TD; " + manyNodes.String(),
		"graph TD; " + strings.Repeat("A-->B;", 25),
	} {
		if _, err := Render(source, 200); err != ErrLimit {
			t.Fatalf("Render(limit case) error = %v, want ErrLimit", err)
		}
	}
	output, err := Render("graph TD; A --> B", 100)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	width := 0
	for _, line := range strings.Split(output, "\n") {
		if w := textWidth(line); w > width {
			width = w
		}
	}
	if got, err := Render("graph TD; A --> B", width); err != nil || got != output {
		t.Fatalf("Render(width) = (%q, %v), want the same output", got, err)
	}
	if _, err := Render("graph TD; A --> B", width-1); err != ErrTooWide {
		t.Fatalf("Render(width-1) error = %v, want ErrTooWide", err)
	}
	if _, err := Render("graph TD; A", 0); err != ErrTooWide {
		t.Fatalf("Render(max_width 0) error = %v, want ErrTooWide", err)
	}
}

// Mirrors Rust's `class_relationship_endpoints` additions in #48489: a target
// identifier beginning with `o` wins over the aggregation token.
func TestClassRelationshipTargetsBeginningWithO(t *testing.T) {
	cases := []struct {
		line        string
		target      string
		targetTip   rune
		targetLabel string
		dashed      bool
	}{
		{"A --orange", "orange", '\u2500', "", false},
		{"A ..o_range", "o_range", '\u2500', "", true},
		{"A --o B", "B", '\u25c7', "", false},
		{`A ..o"many" B`, "B", '\u25c7', "(many)", true},
	}
	for _, testCase := range cases {
		parsed, err := parseRelations("classDiagram", []string{testCase.line})
		if err != nil {
			t.Fatalf("parseRelations(%q) error = %v", testCase.line, err)
		}
		want := &graph{}
		from, _ := want.node("A")
		to, _ := want.node(testCase.target)
		want.edges = append(want.edges, edge{
			from:        from,
			to:          to,
			targetLabel: testCase.targetLabel,
			sourceTip:   '\u2500',
			targetTip:   testCase.targetTip,
			dashed:      testCase.dashed,
		})
		if !graphsEqual(parsed, want) {
			t.Fatalf("parseRelations(%q) = %#v, want %#v", testCase.line, parsed, want)
		}
	}
}

// Mirrors Rust's `unicode_labels_and_later_declarations` state additions in
// #48489: aliases and descriptions accumulate in source order, keeping identity.
func TestStateDescriptionsAccumulateInSourceOrder(t *testing.T) {
	for _, descriptions := range [][]string{
		{"S: Ready"},
		{"S: Ready", "S: Working"},
		{`state "Ready" as S`, "S: Working"},
		{"S: Ready", `state "Working" as S`},
		{`state "Ready" as S`, `state "Working" as S`},
	} {
		body := append([]string{"S --> T"}, descriptions...)
		body = append(body, "T --> S")
		parsed, err := parseState(body)
		if err != nil {
			t.Fatalf("parseState(%v) error = %v", descriptions, err)
		}
		want, err := parseState([]string{"S --> T", "T --> S"})
		if err != nil {
			t.Fatalf("parseState(base) error = %v", err)
		}
		members := []string{}
		if len(descriptions) > 1 {
			members = []string{"Working"}
		}
		want.nodes[0] = node{id: "S", label: "Ready", shape: shapeRectangle, declared: true, members: members}
		if !graphsEqual(parsed, want) {
			t.Fatalf("parseState(%v) = %#v, want %#v", descriptions, parsed, want)
		}
	}
}

// Mirrors Rust's `rejects_incomplete_and_unsupported_families`, including the
// #48814 additions that must stay rejected after punctuation/semicolon support.
func TestRejectsIncompleteAndUnsupportedFamilies(t *testing.T) {
	for _, source := range []string{
		"sequenceDiagram; A->>B: hello; nonsense",
		"sequenceDiagram; A->>B: hello; activate B",
		"sequenceDiagram; participant A as x; participant A as y",
		"sequenceDiagram; alt ready; A->>B: hi",
		"sequenceDiagram; A->>B: hi; end",
		"sequenceDiagram; loop retry; else no; end",
		"sequenceDiagram; alt one; else two; else three; end",
		"sequenceDiagram; A->>B: <br/>",
		"sequenceDiagram; A->>B: \u001b[31m",
		"sequenceDiagram; participant A as e\u0301",
		"stateDiagram-v2; state Processing {; A-->B; }",
		"stateDiagram-v2; A --> B: ok; garbage text",
		"stateDiagram-v2; accDescr: Order lifecycle; [*] --> Ready",
		"stateDiagram; ACCDESCR: Order lifecycle; [*] --> Ready",
		"stateDiagram-v2; A:::highlight; A --> B",
		"stateDiagram-v2; A --> B:::highlight",
		"classDiagram; accTitle: Account model; class Account",
		"classDiagram; class A {; +foo()",
		"classDiagram; class A; A --? B",
		"classDiagram; A --> B; click A",
		"classDiagram; class A {; +<html>; }",
		"erDiagram; A ||--o{ B",
		"erDiagram; A ||--|| B: \"owns\"",
		"erDiagram; A {; string value \"x\"junk\"; }",
		"classDiagram\nclass A {\nstring a;b }\n}",
		"erDiagram; A ||--?? B : owns",
		"erDiagram; A {; int x KEY; }",
		"erDiagram; A {; int x PK \"open comment; }",
		"erDiagram; A {; int x; }; A ||--|| B : uses; trailing junk",
		"erDiagram; accDescr {; A model; }; CUSTOMER",
		"erDiagram; ACCDESCR {; A model; }; CUSTOMER",
		"erDiagram; A ||--|| B:::highlight : owns",
		"%%{init: {}}%%\nclassDiagram; class A",
	} {
		if _, err := Render(source, 200); err != ErrUnsupported {
			t.Fatalf("Render(%q) error = %v, want ErrUnsupported", source, err)
		}
	}
}

// Mirrors Rust's `family_limits` state-description additions in #48489.
func TestStateDescriptionLimit(t *testing.T) {
	for _, source := range []string{
		"stateDiagram-v2\n" + strings.Repeat("S: Description\n", 18),
		"stateDiagram-v2\n" + strings.Repeat("state \"Description\" as S\n", 18),
	} {
		if _, err := Render(source, 200); err != ErrLimit {
			t.Fatalf("Render(state limit) error = %v, want ErrLimit", err)
		}
	}
}

// Mirrors Rust's `equivalent_flowchart_forms_preserve_graph` and
// `source_graph_and_width_limits` additions in #48895: a direction-less header
// defaults to top-down, every new edge spelling carries its own end tips and
// dash style, and `&` groups expand into the Cartesian product of edges.
func TestFlowchartExpandedSyntaxVectorsMatchRust(t *testing.T) {
	withoutDirection, err := parseFlowchart("flowchart", []string{"A --> B"})
	if err != nil {
		t.Fatalf("parseFlowchart(no direction) error = %v", err)
	}
	withDirection, err := parseFlowchart("flowchart TD", []string{"A --> B"})
	if err != nil {
		t.Fatalf("parseFlowchart(flowchart TD) error = %v", err)
	}
	if !graphsEqual(withoutDirection, withDirection) {
		t.Fatalf("direction-less graph = %#v, want %#v", withoutDirection, withDirection)
	}

	for _, testCase := range []struct {
		operator             string
		sourceTip, targetTip rune
		dashed               bool
	}{
		{"-->", '\u2500', '\u25c4', false},
		{"---", '\u2500', '\u2500', false},
		{"-.->", '\u2500', '\u25c4', true},
		{"-.-", '\u2500', '\u2500', true},
		{"<-->", '\u25c4', '\u25c4', false},
		{"<-.->", '\u25c4', '\u25c4', true},
	} {
		parsed, err := parseFlowchart("flowchart", []string{"A" + testCase.operator + "B"})
		if err != nil {
			t.Fatalf("parseFlowchart(A%sB) error = %v", testCase.operator, err)
		}
		want := &graph{direction: directionDown}
		from, _ := want.node("A")
		to, _ := want.node("B")
		want.edges = append(want.edges, edge{
			from:      from,
			to:        to,
			sourceTip: testCase.sourceTip,
			targetTip: testCase.targetTip,
			dashed:    testCase.dashed,
		})
		if !graphsEqual(parsed, want) {
			t.Fatalf("parseFlowchart(A%sB) = %#v, want %#v", testCase.operator, parsed, want)
		}
	}

	group, err := parseFlowchart("flowchart", []string{"A & B --> C & D"})
	if err != nil {
		t.Fatalf("parseFlowchart(group) error = %v", err)
	}
	grouped := &graph{direction: directionDown}
	a, _ := grouped.node("A")
	b, _ := grouped.node("B")
	c, _ := grouped.node("C")
	d, _ := grouped.node("D")
	for _, pair := range [][2]int{{a, c}, {a, d}, {b, c}, {b, d}} {
		grouped.edges = append(grouped.edges, directedEdge(pair[0], pair[1], ""))
	}
	if !graphsEqual(group, grouped) {
		t.Fatalf("parseFlowchart(A & B --> C & D) = %#v, want %#v", group, grouped)
	}

	chained, err := parseFlowchart("flowchart", []string{"A & B -- go --> C & D -. no .-> E"})
	if err != nil {
		t.Fatalf("parseFlowchart(chained) error = %v", err)
	}
	chainedWant := &graph{direction: directionDown}
	ca, _ := chainedWant.node("A")
	cb, _ := chainedWant.node("B")
	cc, _ := chainedWant.node("C")
	cd, _ := chainedWant.node("D")
	ce, _ := chainedWant.node("E")
	for _, pair := range [][2]int{{ca, cc}, {ca, cd}, {cb, cc}, {cb, cd}} {
		chainedWant.edges = append(chainedWant.edges, directedEdge(pair[0], pair[1], "go"))
	}
	chainedWant.edges = append(chainedWant.edges, edge{from: cc, to: ce, label: "no", sourceTip: '\u2500', targetTip: '\u25c4', dashed: true})
	chainedWant.edges = append(chainedWant.edges, edge{from: cd, to: ce, label: "no", sourceTip: '\u2500', targetTip: '\u25c4', dashed: true})
	if !graphsEqual(chained, chainedWant) {
		t.Fatalf("parseFlowchart(chained) = %#v, want %#v", chained, chainedWant)
	}
}

// Mirrors Rust's `graph_relationship_endpoints` #48895 additions: the new
// operators reach the renderer with their own end tips, labels and dash style.
func TestFlowchartRelationshipEndpointsReachRenderer(t *testing.T) {
	for _, testCase := range []struct {
		operator             string
		sourceTip, targetTip rune
		dashed               bool
	}{
		{"---", '\u2500', '\u2500', false},
		{"-.->", '\u2500', '\u25c4', true},
		{"-.-", '\u2500', '\u2500', true},
		{"<-->", '\u25c4', '\u25c4', false},
		{"<-.->", '\u25c4', '\u25c4', true},
	} {
		output, err := Render("flowchart; A"+testCase.operator+"|uses|B", 100)
		if err != nil {
			t.Fatalf("Render(A%s) error = %v", testCase.operator, err)
		}
		var ports []string
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(line, "\u251c") {
				ports = append(ports, line)
			}
		}
		if len(ports) < 2 {
			t.Fatalf("A%s: ports = %q (output:\n%s)", testCase.operator, ports, output)
		}
		if !strings.Contains(ports[0], "\u251c"+string(testCase.sourceTip)) {
			t.Fatalf("A%s: source port = %q, want %q", testCase.operator, ports[0], "\u251c"+string(testCase.sourceTip))
		}
		if !strings.Contains(ports[1], "\u251c"+string(testCase.targetTip)) {
			t.Fatalf("A%s: target port = %q, want %q", testCase.operator, ports[1], "\u251c"+string(testCase.targetTip))
		}
		if !strings.Contains(ports[0], "uses") {
			t.Fatalf("A%s: source port = %q, want label %q", testCase.operator, ports[0], "uses")
		}
		if strings.Contains(output, "\u2506") != testCase.dashed {
			t.Fatalf("A%s: dashed = %v (output:\n%s)", testCase.operator, strings.Contains(output, "\u2506"), output)
		}
	}
	for _, testCase := range []struct {
		infix string
		pipe  string
	}{
		{"A -- send --> B", "A -->|send| B"},
		{"B -. no .-> C", "B -.->|no| C"},
	} {
		infix, err := Render("flowchart; "+testCase.infix, 100)
		if err != nil {
			t.Fatalf("Render(%q) error = %v", testCase.infix, err)
		}
		pipe, err := Render("flowchart; "+testCase.pipe, 100)
		if err != nil {
			t.Fatalf("Render(%q) error = %v", testCase.pipe, err)
		}
		if infix != pipe {
			t.Fatalf("Render(%q) = %q, want %q", testCase.infix, infix, pipe)
		}
	}
}

// Mirrors Rust's `stadiums_with_other_shapes_in_every_direction` snapshot
// #48895: the fixture below is the Rust snapshot recorded at
// `codex-rs/mermaid/src/snapshots/codex_mermaid__tests__stadiums_with_other_shapes_in_every_direction.snap`
// for commit 44fe510ce3, so every direction must render the new operators, the
// `&` group expansion and the spaced infix labels exactly as Rust does.
func TestStadiumsWithOtherShapesInEveryDirectionMatchesRust(t *testing.T) {
	const source = "flowchart %s; A([\u8bf7\u6c42]) -- go --> B[Work] & C{Done?}; B -. no .-> C; C <--> A; B --- A; C -.- B; A <-.-> B"
	const expected = `TD
╭─────────╮
│ 请求    │
│         ├──go───┐
│         ├──go───╪─┐
│         ├◄──────╪─╪───┐
│         ├───────╪─╪───╪─┐
│         ├◄┄┄┄┄┄┄╪┄╪┄┄┄╪┄╪┄┄┄┐
╰─────────╯       │ │   │ │   ┆
                  │ │   │ │   ┆
                  │ │   │ │   ┆
┌─────────┐       │ │   │ │   ┆
│ Work    │       │ │   │ │   ┆
│         ├◄──────┘ │   │ │   ┆
│         ├─┄no┄┄┄┄┄╪┄┐ │ │   ┆
│         ├─────────╪─╪─╪─┘   ┆
│         ├─┄┄┄┄┄┄┄┄╪┄╪┄╪┄┄┄┐ ┆
│         ├◄┄┄┄┄┄┄┄┄╪┄╪┄╪┄┄┄╪┄┘
└─────────┘         │ ┆ │   ┆
                    │ ┆ │   ┆
                    │ ┆ │   ┆
┌─────────┐         │ ┆ │   ┆
│ ◇ Done? │         │ ┆ │   ┆
│         ├◄────────┘ ┆ │   ┆
│         ├◄┄┄┄┄┄┄┄┄┄┄┘ │   ┆
│         ├◄────────────┘   ┆
│         ├─┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘
└─────────┘

BT
┌─────────┐
│ ◇ Done? │
│         ├◄────────┐
│         ├◄┄┄┄┄┄┄┄┄╪┄┐
│         ├◄────────╪─╪─┐
│         ├─┄┄┄┄┄┄┄┄╪┄╪┄╪┄┄┄┐
└─────────┘         │ ┆ │   ┆
                    │ ┆ │   ┆
                    │ ┆ │   ┆
┌─────────┐         │ ┆ │   ┆
│ Work    │         │ ┆ │   ┆
│         ├◄──────┐ │ ┆ │   ┆
│         ├─┄no┄┄┄╪┄╪┄┘ │   ┆
│         ├───────╪─╪───╪─┐ ┆
│         ├─┄┄┄┄┄┄╪┄╪┄┄┄╪┄╪┄┘
│         ├◄┄┄┄┄┄┄╪┄╪┄┄┄╪┄╪┄┄┄┐
└─────────┘       │ │   │ │   ┆
                  │ │   │ │   ┆
                  │ │   │ │   ┆
╭─────────╮       │ │   │ │   ┆
│ 请求    │       │ │   │ │   ┆
│         ├──go───┘ │   │ │   ┆
│         ├──go─────┘   │ │   ┆
│         ├◄────────────┘ │   ┆
│         ├───────────────┘   ┆
│         ├◄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘
╰─────────╯

LR
╭────────────────────╮  ┌────────────────────┐  ┌────────────────┐
│ 请求               │  │ Work               │  │ ◇ Done?        │
╰┬───┬───┬───┬───┬───╯  └┬───┬───┬───┬───┬───┘  └┬───┬───┬───┬───┘
 │   │   ▲   │   ▲       ▲   │   │   │   ▲       ▲   ▲   ▲   │
 │go │go │   │   ┆       │   ┆no │   ┆   ┆       │   ┆   │   ┆
 │   │   │   │   ┆       │   ┆   │   ┆   ┆       │   ┆   │   ┆
 │   │   │   │   ┆       │   ┆   │   ┆   ┆       │   ┆   │   ┆
 └───╪───╪───╪───╪───────┘   ┆   │   ┆   ┆       │   ┆   │   ┆
     │   │   │   ┆           ┆   │   ┆   ┆       │   ┆   │   ┆
     └───╪───╪───╪───────────╪───╪───╪───╪───────┘   ┆   │   ┆
         │   │   ┆           ┆   │   ┆   ┆           ┆   │   ┆
         │   │   ┆           └┄┄┄╪┄┄┄╪┄┄┄╪┄┄┄┄┄┄┄┄┄┄┄┘   │   ┆
         │   │   ┆               │   ┆   ┆               │   ┆
         └───╪───╪───────────────╪───╪───╪───────────────┘   ┆
             │   ┆               │   ┆   ┆                   ┆
             └───╪───────────────┘   ┆   ┆                   ┆
                 ┆                   ┆   ┆                   ┆
                 ┆                   └┄┄┄╪┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘
                 ┆                       ┆
                 └┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘

RL
┌────────────────┐  ┌────────────────────┐  ╭────────────────────╮
│ ◇ Done?        │  │ Work               │  │ 请求               │
└┬───┬───┬───┬───┘  └┬───┬───┬───┬───┬───┘  ╰┬───┬───┬───┬───┬───╯
 ▲   ▲   ▲   │       ▲   │   │   │   ▲       │   │   ▲   │   ▲
 │   ┆   │   ┆       │   ┆no │   ┆   ┆       │go │go │   │   ┆
 │   ┆   │   ┆       │   ┆   │   ┆   ┆       │   │   │   │   ┆
 │   ┆   │   ┆       │   ┆   │   ┆   ┆       │   │   │   │   ┆
 │   ┆   │   ┆       └───╪───╪───╪───╪───────┘   │   │   │   ┆
 │   ┆   │   ┆           ┆   │   ┆   ┆           │   │   │   ┆
 └───╪───╪───╪───────────╪───╪───╪───╪───────────┘   │   │   ┆
     ┆   │   ┆           ┆   │   ┆   ┆               │   │   ┆
     └┄┄┄╪┄┄┄╪┄┄┄┄┄┄┄┄┄┄┄┘   │   ┆   ┆               │   │   ┆
         │   ┆               │   ┆   ┆               │   │   ┆
         └───╪───────────────╪───╪───╪───────────────┘   │   ┆
             ┆               │   ┆   ┆                   │   ┆
             ┆               └───╪───╪───────────────────┘   ┆
             ┆                   ┆   ┆                       ┆
             └┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘   ┆                       ┆
                                     ┆                       ┆
                                     └┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘`
	var cases []string
	for _, direction := range []string{"TD", "BT", "LR", "RL"} {
		rendered, err := Render(fmt.Sprintf(source, direction), 100)
		if err != nil {
			t.Fatalf("%s: Render error = %v", direction, err)
		}
		width := 0
		for _, line := range strings.Split(rendered, "\n") {
			if w := textWidth(line); w > width {
				width = w
			}
		}
		if got, err := Render(fmt.Sprintf(source, direction), width); err != nil || got != rendered {
			t.Fatalf("%s: Render(width) = (%q, %v), want the same output", direction, got, err)
		}
		if _, err := Render(fmt.Sprintf(source, direction), width-1); err != ErrTooWide {
			t.Fatalf("%s: Render(width-1) error = %v, want ErrTooWide", direction, err)
		}
		cases = append(cases, direction+"\n"+rendered)
	}
	got := strings.Join(cases, "\n\n")
	if got == expected {
		return
	}
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(expected, "\n")
	if len(gotLines) != len(wantLines) {
		t.Errorf("rendered line count = %d, want %d", len(gotLines), len(wantLines))
	}
	for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
		if gotLines[i] != wantLines[i] {
			t.Fatalf("rendered line %d = %q, want %q", i, gotLines[i], wantLines[i])
		}
	}
	t.Fatalf("rendered diagram does not match the Rust snapshot:\n%s", got)
}

func nodesEqual(a, b []node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.id != y.id || x.label != y.label || x.shape != y.shape || x.declared != y.declared || strings.Join(x.members, "\u0000") != strings.Join(y.members, "\u0000") {
			return false
		}
	}
	return true
}

func graphsEqual(a, b *graph) bool {
	if a.direction != b.direction || len(a.nodes) != len(b.nodes) || len(a.edges) != len(b.edges) {
		return false
	}
	for i := range a.nodes {
		x, y := a.nodes[i], b.nodes[i]
		if x.id != y.id || x.label != y.label || x.shape != y.shape || x.declared != y.declared || strings.Join(x.members, "\x00") != strings.Join(y.members, "\x00") {
			return false
		}
	}
	for i := range a.edges {
		if a.edges[i] != b.edges[i] {
			return false
		}
	}
	return true
}

const sequenceFixture = `sequenceDiagram
    actor U as Buyer
    participant A as API
    participant S as 库存
    participant P as Payments
    U->>A: Place order
    A->>S: Reserve items
    S-->>A: Reservation
    opt Items reserved
        loop Up to 3 attempts
            A->>P: Charge card
            P->>P: Check fraud
            P-->>A: Payment status
            alt Approved
                Note over A,P: Payment recorded
                A-->>U: Order confirmed
            else Declined
                A->>S: Release items
                A-->>U: Payment failed
            end
        end
    end`

const stateFixture = `stateDiagram-v2
    state "Payment pending" as Charging
    [*] --> Draft
    Draft --> Validating: submit
    Validating --> Charging: valid
    Validating --> Rejected: invalid
    Charging --> Packing: paid
    Charging --> Rejected: declined
    Packing --> Shipped: dispatch
    Shipped --> Delivered: received
    Delivered --> [*]
    Rejected --> Draft: revise
    Charging: Retry up to 3 times
    Draft: Order drafted
    Draft: Awaiting submission`

const classFixture = `classDiagram
    class order {
        +String id
        +Status status
        +submit()
        +cancel()
    }
    class LineItem {
        +int quantity
        +Decimal price
        +subtotal()
    }
    class Payment {
        +Decimal amount
        +authorize()
    }
    class CardPayment {
        +String lastFour
        +authorize()
    }
    order "1" *-- "1..*" LineItem : contains
    order "1" --> "1" Payment : pays with
    Payment <|-- CardPayment
    CardPayment ..order : updates`

const erFixture = `erDiagram
    CUSTOMER ||--o{ ORDER : places
    ORDER ||--|{ LINE_ITEM : contains
    PRODUCT ||..o{ LINE_ITEM : appears_in
    CUSTOMER {
        int id PK
        string email UK
        string name
    }
    ORDER {
        int id PK
        int customer_id FK
        string status
    }
    LINE_ITEM {
        int order_id PK, FK "order key"
        int product_id PK, FK
        int quantity
    }
    PRODUCT {
        int id PK
        string name
        decimal price
    }`

// Mirrors Rust's `complex_families`: each family renders at the widest width,
// re-renders identically at that width, and fails one column narrower.
func TestComplexFamiliesMatchRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		source  string
		markers []string
	}{
		{"sequence", sequenceFixture, []string{"Buyer (actor)", "Note: Payment recorded", "else Declined"}},
		{"state", stateFixture, []string{"\u25cf initial", "\u25ce final", "Order drafted", "Awaiting submission"}},
		{"class", classFixture, []string{"submit()", "pays with", "updates"}},
		{"er", erFixture, []string{"places", "order key"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			output, err := Render(testCase.source, 180)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			for _, marker := range testCase.markers {
				if !strings.Contains(output, marker) {
					t.Fatalf("output missing %q:\n%s", marker, output)
				}
			}
			width := 0
			for _, line := range strings.Split(output, "\n") {
				if w := textWidth(line); w > width {
					width = w
				}
			}
			if got, err := Render(testCase.source, width); err != nil || got != output {
				t.Fatalf("Render(width) = (%q, %v), want the same output", got, err)
			}
			if _, err := Render(testCase.source, width-1); err != ErrTooWide {
				t.Fatalf("Render(width-1) error = %v, want ErrTooWide", err)
			}
		})
	}
}

// Mirrors Rust's `unicode_labels_and_later_declarations` for flowcharts.
func TestUnicodeFlowchartLabelsAndLaterDeclarations(t *testing.T) {
	for _, direction := range []string{"TD", "BT", "LR", "RL"} {
		source := "%% heading\ngraph " + direction + "; A -->|\u51c6\u5907| B; A[\u8bf7\u6c42]; B{R\u00e9ponse?}; B -->|retry| A; B --> C[Ship \U0001f680]"
		output, err := Render(source, 160)
		if err != nil {
			t.Fatalf("Render(%s) error = %v", direction, err)
		}
		for _, label := range []string{"\u8bf7\u6c42", "R\u00e9ponse?", "Ship \U0001f680", "\u51c6\u5907", "retry"} {
			if !strings.Contains(output, label) {
				t.Fatalf("%s: output missing %q:\n%s", direction, label, output)
			}
		}
		width := 0
		for _, line := range strings.Split(output, "\n") {
			if w := textWidth(line); w > width {
				width = w
			}
		}
		if got, err := Render(source, width); err != nil || got != output {
			t.Fatalf("%s: Render(width) = (%q, %v)", direction, got, err)
		}
		if _, err := Render(source, width-1); err != ErrTooWide {
			t.Fatalf("%s: Render(width-1) error = %v, want ErrTooWide", direction, err)
		}
	}
}
