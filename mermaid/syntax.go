package mermaid

import "strings"

// splitStatements splits a diagram source into family-aware statements
// (Rust codex-rs/mermaid/src/syntax.rs::statements, #48814). Sequence quotes are
// literal text; class bodies consume member lines whole; flowchart label
// delimiters and other quoted tokens protect semicolons, except in
// colon-delimited class/state text where quotes are literal too.
func splitStatements(source string) ([]string, error) {
	var result []string
	classBody := false
	for _, line := range splitSourceLines(source) {
		rest := strings.TrimSpace(line)
		if strings.HasPrefix(rest, "%%{") {
			return nil, ErrUnsupported
		}
		if strings.HasPrefix(rest, "%%") {
			continue
		}
		// Mermaid `#name;`/`#number;` and HTML `&name;`/`&#number;` need decoding.
		// Reject them before statement splitting can discard the semicolon and turn
		// them into literal text.
		if hasUnsupportedEntity(rest) {
			return nil, ErrUnsupported
		}
		for rest != "" {
			header := ""
			if len(result) > 0 {
				header = result[0]
			}
			if header == "classDiagram" &&
				(classBody || (strings.HasPrefix(rest, "class ") && classDeclarationHasBrace(rest))) {
				// Compact bodies are not supported here; let the class parser reject them.
				classBody = rest != "}"
				result = append(result, rest)
				break
			}
			colonText := header == "classDiagram" || header == "stateDiagram" || header == "stateDiagram-v2"
			flowchart := false
			if fields := strings.Fields(header); len(fields) > 0 {
				flowchart = fields[0] == "flowchart" || fields[0] == "graph"
			}
			var close byte
			quoted := false
			literal := header == "sequenceDiagram"
			end := -1
			for index := 0; index < len(rest); index++ {
				ch := rest[index]
				switch {
				case ch == '"' && !literal:
					quoted = !quoted
				case ch == ':' && colonText && !quoted:
					literal = true
				case ch == ';' && !quoted && close == 0:
					end = index
				case flowchart && !quoted:
					switch {
					case close != 0 && close == ch:
						close = 0
					case close == 0 && ch == '[':
						close = ']'
					case close == 0 && ch == '{':
						close = '}'
					case close == 0 && ch == '|':
						close = '|'
					}
				}
				if end >= 0 {
					break
				}
			}
			statement, remaining := rest, ""
			if end >= 0 {
				statement, remaining = rest[:end], rest[end+1:]
			}
			if strings.TrimSpace(statement) != "" {
				result = append(result, strings.TrimSpace(statement))
			}
			rest = strings.TrimSpace(remaining)
		}
	}
	return result, nil
}

func hasUnsupportedEntity(rest string) bool {
	for i := 0; i < len(rest); i++ {
		if rest[i] != '#' && rest[i] != '&' {
			continue
		}
		after := rest[i+1:]
		length := 0
		for length < len(after) && isEntityNameByte(after[length]) {
			length++
		}
		if length > 0 && strings.HasPrefix(after[length:], ";") {
			return true
		}
	}
	return false
}

func isEntityNameByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

func classDeclarationHasBrace(rest string) bool {
	index := strings.IndexByte(rest, '{')
	if index < 0 {
		return false
	}
	return !strings.Contains(rest[:index], ";")
}

// delimitedLabel consumes a flowchart label up to its closing delimiter,
// protecting delimiters inside a leading quoted token. Quotes do not use
// backslash escaping in Mermaid; entities remain unsupported
// (Rust codex-rs/mermaid/src/syntax.rs::delimited_label, #48814).
func delimitedLabel(text, close string) (string, string, error) {
	if strings.HasPrefix(text, "\"") {
		quoted := text[1:]
		index := strings.IndexByte(quoted, '"')
		if index < 0 {
			return "", "", ErrUnsupported
		}
		end := index + 2
		rest := text[end:]
		if !strings.HasPrefix(rest, close) {
			return "", "", ErrUnsupported
		}
		return text[:end], rest[len(close):], nil
	}
	index := strings.Index(text, close)
	if index < 0 {
		return "", "", ErrUnsupported
	}
	return text[:index], text[index+len(close):], nil
}
