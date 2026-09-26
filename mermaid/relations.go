package mermaid

import (
	"strings"
	"unicode"
)

// parseRelations handles the class, ER and flat state grammars, preserving
// members, endpoint cardinalities and relationship kinds (Rust `relations::parse`).
func parseRelations(header string, body []string) (*graph, error) {
	if header == "stateDiagram" || header == "stateDiagram-v2" {
		return parseState(body)
	}
	er := header == "erDiagram"
	g := &graph{}
	block := -1
	sawDirection := false
	for _, line := range body {
		if block >= 0 {
			if line == "}" {
				block = -1
				continue
			}
			var member string
			if er {
				parsed, err := erAttribute(line)
				if err != nil {
					return nil, err
				}
				member = parsed
			} else {
				if err := checkLabel(line); err != nil {
					return nil, err
				}
				member = line
			}
			current := &g.nodes[block]
			if len(current.members) == 16 {
				return nil, ErrLimit
			}
			current.members = append(current.members, member)
			continue
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
		declaration := !er && strings.HasPrefix(line, "class ")
		rest := line
		if !er {
			rest = strings.TrimPrefix(line, "class ")
		}
		id, err := identifier(&rest)
		if err != nil {
			return nil, err
		}
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		title := id == "accTitle" || (er && strings.EqualFold(id, "accTitle"))
		description := id == "accDescr" || (er && strings.EqualFold(id, "accDescr"))
		if !declaration && ((title && strings.HasPrefix(rest, ":")) ||
			(description && (strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "{")))) {
			return nil, ErrUnsupported
		}
		from, err := g.node(id)
		if err != nil {
			return nil, err
		}
		if rest == "{" && (er || declaration) {
			block = from
			continue
		}
		if declaration || (er && rest == "") {
			if rest != "" {
				return nil, ErrUnsupported
			}
			continue
		}
		if !er && strings.HasPrefix(rest, ":") && !strings.HasPrefix(rest, "::") {
			member := strings.TrimSpace(rest[1:])
			if err := checkLabel(member); err != nil {
				return nil, err
			}
			if len(g.nodes[from].members) == 16 {
				return nil, ErrLimit
			}
			g.nodes[from].members = append(g.nodes[from].members, member)
			continue
		}
		sourceCard := ""
		if !er {
			sourceCard, err = cardinality(&rest)
			if err != nil {
				return nil, err
			}
		}
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		split := relationSplit(rest)
		if split < 0 {
			return nil, ErrUnsupported
		}
		if split > 2 {
			return nil, ErrUnsupported
		}
		left := rest[:split]
		dashed := rest[split:split+2] == ".."
		rest = rest[split+2:]
		var sourceTip, targetTip rune
		var targetCard string
		if er {
			switch left {
			case "||":
				sourceCard = "1"
			case "|o":
				sourceCard = "0..1"
			case "}|":
				sourceCard = "1..many"
			case "}o":
				sourceCard = "0..many"
			default:
				return nil, ErrUnsupported
			}
			if len(rest) < 2 {
				return nil, ErrUnsupported
			}
			switch rest[:2] {
			case "||":
				targetCard = "1"
			case "o|":
				targetCard = "0..1"
			case "|{":
				targetCard = "1..many"
			case "o{":
				targetCard = "0..many"
			default:
				return nil, ErrUnsupported
			}
			rest = rest[2:]
			sourceTip, targetTip = '\u2500', '\u2500'
		} else {
			switch left {
			case "":
				sourceTip = '\u2500'
			case "<":
				sourceTip = '\u25c4'
			case "<|":
				sourceTip = '\u25c1'
			case "*":
				sourceTip = '\u25c6'
			case "o":
				sourceTip = '\u25c7'
			default:
				return nil, ErrUnsupported
			}
			targetTip = '\u2500'
			targetTokens := []struct {
				token string
				tip   rune
			}{{"|>", '\u25c1'}, {">", '\u25c4'}, {"*", '\u25c6'}, {"o", '\u25c7'}}
			for _, candidate := range targetTokens {
				after, ok := strings.CutPrefix(rest, candidate.token)
				if !ok {
					continue
				}
				// A longer identifier wins over the single-letter aggregation token.
				if candidate.token == "o" && after != "" && isASCIIIdentifierByte(after[0]) {
					continue
				}
				targetTip = candidate.tip
				rest = after
				break
			}
			targetCard, err = cardinality(&rest)
			if err != nil {
				return nil, err
			}
		}
		targetID, err := identifier(&rest)
		if err != nil {
			return nil, err
		}
		to, err := g.node(targetID)
		if err != nil {
			return nil, err
		}
		rest = strings.TrimSpace(rest)
		label := ""
		switch {
		case strings.HasPrefix(rest, ":") && !strings.HasPrefix(rest, "::"):
			label = strings.TrimSpace(rest[1:])
			if err := checkLabel(label); err != nil {
				return nil, err
			}
		case rest != "" || er:
			return nil, ErrUnsupported
		}
		if len(g.edges) == maxEdges {
			return nil, ErrLimit
		}
		edgeLabel := label
		if sourceCard != "" {
			if label == "" {
				edgeLabel = "(" + sourceCard + ")"
			} else {
				edgeLabel = "(" + sourceCard + ") " + label
			}
		}
		targetLabel := ""
		if targetCard != "" {
			targetLabel = "(" + targetCard + ")"
		}
		g.edges = append(g.edges, edge{
			from:        from,
			to:          to,
			label:       edgeLabel,
			targetLabel: targetLabel,
			sourceTip:   sourceTip,
			targetTip:   targetTip,
			dashed:      dashed,
		})
	}
	if block >= 0 || len(g.nodes) == 0 {
		return nil, ErrUnsupported
	}
	return g, nil
}

// relationSplit returns the byte index of the nearest `--` or `..` separator, or
// -1 when neither appears (Rust's chained `find` + `min`).
func relationSplit(rest string) int {
	index := -1
	for _, token := range []string{"--", ".."} {
		if found := strings.Index(rest, token); found >= 0 && (index < 0 || found < index) {
			index = found
		}
	}
	return index
}

func isASCIIIdentifierByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// cardinality consumes an optional quoted cardinality label.
func cardinality(rest *string) (string, error) {
	*rest = strings.TrimLeftFunc(*rest, unicode.IsSpace)
	after, ok := strings.CutPrefix(*rest, "\"")
	if !ok {
		return "", nil
	}
	index := strings.Index(after, "\"")
	if index < 0 {
		return "", ErrUnsupported
	}
	value := after[:index]
	if err := checkLabel(value); err != nil {
		return "", err
	}
	*rest = after[index+1:]
	return value, nil
}

// erAttribute renders one ER entity attribute line.
func erAttribute(line string) (string, error) {
	rest := line
	dataType, err := identifier(&rest)
	if err != nil {
		return "", err
	}
	if rest == "" || !unicode.IsSpace(rune(rest[0])) {
		return "", ErrUnsupported
	}
	name, err := identifier(&rest)
	if err != nil {
		return "", err
	}
	keys := strings.TrimSpace(rest)
	var comment *string
	if index := strings.Index(keys, "\""); index >= 0 {
		rawComment := keys[index+1:]
		keys = strings.TrimSpace(keys[:index])
		if !strings.HasSuffix(rawComment, "\"") {
			return "", ErrUnsupported
		}
		value := rawComment[:len(rawComment)-1]
		if err := checkLabel(value); err != nil {
			return "", err
		}
		comment = &value
	}
	if keys != "" {
		for _, key := range strings.Split(keys, ",") {
			switch strings.TrimSpace(key) {
			case "PK", "FK", "UK":
			default:
				return "", ErrUnsupported
			}
		}
	}
	result := dataType + " " + name
	if keys != "" {
		result += " " + keys
	}
	if comment != nil {
		result += " \u2014 " + *comment
	}
	if err := checkLabel(result); err != nil {
		return "", err
	}
	return result, nil
}
