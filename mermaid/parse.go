package mermaid

import (
	"strings"
	"unicode"
)

// parseFlowchart is a strict parser for a small flowchart grammar; every
// non-comment byte must be consumed (Rust `parse::parse`).
func parseFlowchart(header string, body []string) (*graph, error) {
	tokens := strings.Fields(header)
	if len(tokens) != 2 || (tokens[0] != "flowchart" && tokens[0] != "graph") {
		return nil, ErrUnsupported
	}
	dir, err := parseDirection(tokens[1])
	if err != nil {
		return nil, err
	}
	g := &graph{direction: dir}
	for _, statement := range body {
		rest := statement
		from, err := flowchartNode(&rest, g)
		if err != nil {
			return nil, err
		}
		for strings.TrimLeftFunc(rest, unicode.IsSpace) != "" {
			rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
			if !strings.HasPrefix(rest, "-->") {
				return nil, ErrUnsupported
			}
			rest = strings.TrimLeftFunc(rest[len("-->"):], unicode.IsSpace)
			label := ""
			if strings.HasPrefix(rest, "|") {
				rawLabel, remaining, err := delimitedLabel(rest[1:], "|")
				if err != nil {
					return nil, err
				}
				parsed, err := flowchartLabel(rawLabel)
				if err != nil {
					return nil, err
				}
				label = parsed
				rest = remaining
			}
			to, err := flowchartNode(&rest, g)
			if err != nil {
				return nil, err
			}
			if len(g.edges) == maxEdges {
				return nil, ErrLimit
			}
			g.edges = append(g.edges, directedEdge(from, to, label))
			from = to
		}
	}
	if len(g.nodes) == 0 {
		return nil, ErrUnsupported
	}
	return g, nil
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
