package mermaid

import (
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

// Mirrors Rust's `quoted_flowchart_labels_match_unquoted_labels`.
func TestQuotedFlowchartLabelsMatchUnquotedLabels(t *testing.T) {
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
		"\"\x1b\"",
	} {
		for _, source := range []string{
			"flowchart TD; A[" + label + "]",
			"flowchart TD; A -->|" + label + "| B",
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
		"flowchart TD; A -.-> B",
		"flowchart TD; A & B --> C",
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
		"flowchart TD; A[foo;bar]",
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

// Mirrors Rust's `source_graph_and_width_limits`.
func TestSourceGraphAndWidthLimits(t *testing.T) {
	var manyNodes strings.Builder
	for n := 0; n < 17; n++ {
		manyNodes.WriteString("N" + strconv.Itoa(n) + ";")
	}
	for _, source := range []string{
		strings.Repeat(" ", 16*1024+1),
		"graph TD; A[" + strings.Repeat("x", 41) + "]",
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
