package mermaid

import (
	"strings"
	"unicode"
)

// parseState handles flat state machines, with distinct initial/final
// pseudostates and labeled transitions. Aliases and descriptions accumulate in
// source order: first the title, then body rows (Rust `state::parse`).
func parseState(body []string) (*graph, error) {
	g := &graph{}
	sawDirection := false
	for _, line := range body {
		switch line {
		case "state", "direction", "note", "end", "hide":
			return nil, ErrUnsupported
		}
		if strings.HasPrefix(line, "direction ") {
			if sawDirection {
				return nil, ErrUnsupported
			}
			dir, err := parseDirection(strings.TrimSpace(line[len("direction "):]))
			if err != nil {
				return nil, err
			}
			g.direction = dir
			sawDirection = true
			continue
		}
		rest := line
		if after, ok := strings.CutPrefix(rest, "state \""); ok {
			index := strings.Index(after, "\"")
			if index < 0 {
				return nil, ErrUnsupported
			}
			label := after[:index]
			if err := checkLabel(label); err != nil {
				return nil, err
			}
			rest = strings.TrimLeftFunc(after[index+1:], unicode.IsSpace)
			rest, ok = strings.CutPrefix(rest, "as ")
			if !ok {
				return nil, ErrUnsupported
			}
			id, err := identifier(&rest)
			if err != nil {
				return nil, err
			}
			nodeIndex, err := g.node(id)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(rest) != "" {
				return nil, ErrUnsupported
			}
			if err := addStateDescription(&g.nodes[nodeIndex], label); err != nil {
				return nil, err
			}
			continue
		}
		var from int
		if after, ok := strings.CutPrefix(rest, "[*]"); ok {
			rest = after
			index, err := g.node("[initial]")
			if err != nil {
				return nil, err
			}
			g.nodes[index].label = "\u25cf initial"
			from = index
		} else {
			id, err := identifier(&rest)
			if err != nil {
				return nil, err
			}
			if (strings.EqualFold(id, "accTitle") || strings.EqualFold(id, "accDescr")) &&
				strings.HasPrefix(strings.TrimLeftFunc(rest, unicode.IsSpace), ":") {
				return nil, ErrUnsupported
			}
			from, err = g.node(id)
			if err != nil {
				return nil, err
			}
		}
		rest = strings.TrimSpace(rest)
		if rest == "" && g.nodes[from].id != "[initial]" {
			continue
		}
		if strings.HasPrefix(rest, ":") && !strings.HasPrefix(rest, "::") {
			if g.nodes[from].id == "[initial]" {
				return nil, ErrUnsupported
			}
			description := strings.TrimSpace(rest[1:])
			if err := checkLabel(description); err != nil {
				return nil, err
			}
			if err := addStateDescription(&g.nodes[from], description); err != nil {
				return nil, err
			}
			continue
		}
		after, ok := strings.CutPrefix(rest, "-->")
		if !ok {
			return nil, ErrUnsupported
		}
		rest = strings.TrimLeftFunc(after, unicode.IsSpace)
		var to int
		if after, ok := strings.CutPrefix(rest, "[*]"); ok {
			rest = after
			index, err := g.node("[final]")
			if err != nil {
				return nil, err
			}
			g.nodes[index].label = "\u25ce final"
			to = index
		} else {
			id, err := identifier(&rest)
			if err != nil {
				return nil, err
			}
			to, err = g.node(id)
			if err != nil {
				return nil, err
			}
		}
		label := ""
		trimmed := strings.TrimSpace(rest)
		switch {
		case strings.HasPrefix(trimmed, ":") && !strings.HasPrefix(trimmed, "::"):
			label = strings.TrimSpace(trimmed[1:])
			if err := checkLabel(label); err != nil {
				return nil, err
			}
		case trimmed == "":
		default:
			return nil, ErrUnsupported
		}
		if len(g.edges) == maxEdges {
			return nil, ErrLimit
		}
		g.edges = append(g.edges, directedEdge(from, to, label))
	}
	if len(g.nodes) == 0 {
		return nil, ErrUnsupported
	}
	return g, nil
}

// addStateDescription accumulates a state's aliases and descriptions: the first
// becomes the node title, the rest become body rows bounded to 16.
func addStateDescription(n *node, description string) error {
	if !n.declared {
		n.label = description
		n.declared = true
		return nil
	}
	if len(n.members) == 16 {
		return ErrLimit
	}
	n.members = append(n.members, description)
	return nil
}
