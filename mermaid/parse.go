package mermaid

import (
	"strings"
	"unicode"
)

// parseFlowchart is a strict parser for a small flowchart grammar; every
// non-comment byte must be consumed (Rust `parse::parse`).
func parseFlowchart(header string, body []string) (*graph, error) {
	tokens := strings.Fields(header)
	var dir direction
	switch {
	case len(tokens) == 1 && (tokens[0] == "flowchart" || tokens[0] == "graph"):
		// `flowchart` and `graph` default to top-down layout.
		dir = directionDown
	case len(tokens) == 2 && (tokens[0] == "flowchart" || tokens[0] == "graph"):
		parsed, err := parseDirection(tokens[1])
		if err != nil {
			return nil, err
		}
		dir = parsed
	default:
		return nil, ErrUnsupported
	}
	g := &graph{direction: dir}
	for _, statement := range body {
		rest := statement
		from, err := flowchartNodes(&rest, g)
		if err != nil {
			return nil, err
		}
		for strings.TrimLeftFunc(rest, unicode.IsSpace) != "" {
			rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
			label, sourceTip, targetTip, dashed, err := flowchartEdge(&rest)
			if err != nil {
				return nil, err
			}
			to, err := flowchartNodes(&rest, g)
			if err != nil {
				return nil, err
			}
			// Check the Cartesian expansion before allocating edges, including
			// repeated IDs.
			if len(from)*len(to) > maxEdges-len(g.edges) {
				return nil, ErrLimit
			}
			for _, source := range from {
				for _, target := range to {
					g.edges = append(g.edges, edge{
						from:      source,
						to:        target,
						label:     label,
						sourceTip: sourceTip,
						targetTip: targetTip,
						dashed:    dashed,
					})
				}
			}
			from = to
		}
	}
	if len(g.nodes) == 0 {
		return nil, ErrUnsupported
	}
	return g, nil
}

// flowchartNodes expands an `&` group into every referenced node, capping the
// group at maxEdges references (Rust `parse::nodes`).
func flowchartNodes(rest *string, g *graph) ([]int, error) {
	first, err := flowchartNode(rest, g)
	if err != nil {
		return nil, err
	}
	nodes := []int{first}
	for {
		trimmed := strings.TrimLeftFunc(*rest, unicode.IsSpace)
		if !strings.HasPrefix(trimmed, "&") {
			return nodes, nil
		}
		if len(nodes) == maxEdges {
			return nil, ErrLimit
		}
		*rest = trimmed[len("&"):]
		index, err := flowchartNode(rest, g)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, index)
	}
}

// flowchartEdge consumes one edge operator, returning its end tips, dash style
// and pipe label. Tokens are tried longest first, so `<-.->` never parses as a
// shorter operator (Rust `parse`'s edge branch).
func flowchartEdge(rest *string) (string, rune, rune, bool, error) {
	for _, token := range []struct {
		text           string
		source, target rune
		dashed         bool
	}{
		{"<-.->", '\u25c4', '\u25c4', true},
		{"<-->", '\u25c4', '\u25c4', false},
		{"-.->", '\u2500', '\u25c4', true},
		{"-.-", '\u2500', '\u2500', true},
		{"-->", '\u2500', '\u25c4', false},
		{"---", '\u2500', '\u2500', false},
	} {
		if !strings.HasPrefix(*rest, token.text) {
			continue
		}
		after := (*rest)[len(token.text):]
		// Circle/cross tips are unsupported; without a space they are not node IDs.
		if token.target == '\u2500' && (strings.HasPrefix(after, "o") || strings.HasPrefix(after, "x")) {
			return "", 0, 0, false, ErrUnsupported
		}
		*rest = strings.TrimLeftFunc(after, unicode.IsSpace)
		label := ""
		if strings.HasPrefix(*rest, "|") {
			rawLabel, remaining, err := delimitedLabel((*rest)[1:], "|")
			if err != nil {
				return "", 0, 0, false, err
			}
			parsed, err := flowchartLabel(rawLabel)
			if err != nil {
				return "", 0, 0, false, err
			}
			label = parsed
			*rest = remaining
		}
		return label, token.source, token.target, token.dashed, nil
	}
	// Spaced labels keep endpoint markers out of the text. Stop at the first
	// closing stem instead of swallowing an unsupported edge and its target.
	stem := ""
	dashed := false
	switch {
	case strings.HasPrefix(*rest, "--"):
		stem = "--"
	case strings.HasPrefix(*rest, "-."):
		stem, dashed = ".-", true
	default:
		return "", 0, 0, false, ErrUnsupported
	}
	*rest = (*rest)[len("--"):]
	index := strings.Index(*rest, stem)
	if index < 0 {
		return "", 0, 0, false, ErrUnsupported
	}
	text, remaining := (*rest)[:index], (*rest)[index+len(stem):]
	if text == "" || text == strings.TrimLeftFunc(text, unicode.IsSpace) || text == strings.TrimRightFunc(text, unicode.IsSpace) {
		return "", 0, 0, false, ErrUnsupported
	}
	if !strings.HasPrefix(remaining, ">") {
		return "", 0, 0, false, ErrUnsupported
	}
	parsed, err := flowchartLabel(strings.TrimSpace(text))
	if err != nil {
		return "", 0, 0, false, err
	}
	*rest = remaining[len(">"):]
	return parsed, '\u2500', '\u25c4', dashed, nil
}

var flowchartReservedIDs = map[string]bool{
	"end": true, "subgraph": true, "direction": true, "style": true,
	"class": true, "classDef": true, "linkStyle": true, "click": true,
}

// longerShapeDelimiters must not become punctuation inside a simpler node.
var longerShapeDelimiters = []string{"[(", "[[", "[/", "[\\", "{{"}

func flowchartNode(rest *string, g *graph) (int, error) {
	id, err := identifier(rest)
	if err != nil {
		return 0, err
	}
	if flowchartReservedIDs[id] {
		return 0, ErrUnsupported
	}
	for _, open := range longerShapeDelimiters {
		if strings.HasPrefix(*rest, open) {
			return 0, ErrUnsupported
		}
	}
	type declaration struct {
		open  string
		close string
		shape shape
	}
	var declared *declaration
	switch {
	case strings.HasPrefix(*rest, "["):
		declared = &declaration{open: "[", close: "]", shape: shapeRectangle}
	case strings.HasPrefix(*rest, "{"):
		declared = &declaration{open: "{", close: "}", shape: shapeDecision}
	case strings.HasPrefix(*rest, "(["):
		declared = &declaration{open: "([", close: "])", shape: shapeStadium}
	}
	index, err := g.node(id)
	if err != nil {
		return 0, err
	}
	if declared == nil {
		return index, nil
	}
	rawLabel, remaining, err := delimitedLabel((*rest)[len(declared.open):], declared.close)
	if err != nil {
		return 0, err
	}
	label, err := flowchartLabel(rawLabel)
	if err != nil {
		return 0, err
	}
	*rest = remaining
	current := &g.nodes[index]
	if current.declared && (current.label != label || current.shape != declared.shape) {
		return 0, ErrUnsupported
	}
	current.label = label
	current.shape = declared.shape
	current.declared = true
	return index, nil
}

func flowchartLabel(label string) (string, error) {
	// Mermaid Markdown strings require rendering beyond ordinary quoted labels.
	if strings.HasPrefix(label, "\"`") {
		return "", ErrUnsupported
	}
	resolved := label
	if strings.HasPrefix(label, "\"") {
		quoted := label[1:]
		if !strings.HasSuffix(quoted, "\"") {
			return "", ErrUnsupported
		}
		resolved = quoted[:len(quoted)-1]
	} else if strings.ContainsAny(label, "[]{}|") {
		// Unquoted labels must not carry structural delimiters.
		return "", ErrUnsupported
	}
	if strings.Contains(resolved, "\"") {
		return "", ErrUnsupported
	}
	if err := checkLabel(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// checkLabel mirrors `parse::check_label`: markup needs deliberate
// decoding/layout, but printable punctuation (including ampersands and
// comparison operators) is plain text.
func checkLabel(label string) error {
	if labelContainsMarkup(label) {
		return ErrUnsupported
	}
	if strings.TrimSpace(label) == "" {
		return ErrUnsupported
	}
	for _, ch := range label {
		// Rust's `unicode-width` reports control and format characters as
		// non-printable; reject them so a bidi isolate or ZWJ cannot reach the
		// terminal even where go-runewidth would guess a width.
		if unicode.IsControl(ch) || unicode.Is(unicode.Cf, ch) {
			return ErrUnsupported
		}
		switch ch {
		case '\u250c', '\u2510', '\u2514', '\u2518', '\u251c', '\u2524', '\u256a', '\u25c4':
			return ErrUnsupported
		}
		if charWidth(ch) <= 0 {
			return ErrUnsupported
		}
	}
	// Labels are drawn one Unicode scalar at a time. Reject ligatures whose
	// string width differs from those scalar widths rather than misaligning
	// borders or underallocating the canvas.
	sum := 0
	for _, ch := range label {
		sum += charWidth(ch)
	}
	if sum != textWidth(label) {
		return ErrUnsupported
	}
	if textWidth(label) > maxLabel {
		return ErrLimit
	}
	return nil
}

// labelContainsMarkup reports whether the label opens an HTML-like tag, which
// would need deliberate decoding/layout. A bare `<` used as a comparison
// operator is plain text.
func labelContainsMarkup(label string) bool {
	for i := 0; i < len(label); i++ {
		if label[i] != '<' {
			continue
		}
		after := label[i+1:]
		if after == "" {
			continue
		}
		ch := after[0]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '/' || ch == '!' || ch == '?' {
			return true
		}
	}
	return false
}

// identifier consumes a leading ASCII identifier and enforces the label bound.
func identifier(rest *string) (string, error) {
	*rest = strings.TrimLeftFunc(*rest, unicode.IsSpace)
	length := 0
	for length < len(*rest) {
		b := (*rest)[length]
		if (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' {
			length++
			continue
		}
		break
	}
	id := (*rest)[:length]
	if id == "" || !isASCIIAlpha(id[0]) || len(id) > maxLabel {
		return "", ErrUnsupported
	}
	*rest = (*rest)[length:]
	return id, nil
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
