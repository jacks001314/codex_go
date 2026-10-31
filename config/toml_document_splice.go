package config

import "strings"

// tomlDocumentItem is one top-level construct of a config document: a
// root-level key/value (which may span several lines) or a table header with
// the body lines that follow it. Blank and comment lines between constructs
// belong to the next construct's decor, the way toml_edit attaches them, so a
// removed construct takes its own decoration with it.
type tomlDocumentItem struct {
	start   int      // first byte of the construct's own first line
	bodyEnd int      // first byte after the construct's last content line
	table   bool     // false for a root-level key/value
	array   bool     // [[name]] header
	keyPath []string // table key path
	rootKey string   // first key segment of a root-level key/value
}

// scanTOMLDocument splits a document into its top-level constructs and reports
// where document-level trailing trivia begins: the blank and comment lines that
// follow the last construct stay at the end of the file when toml_edit inserts
// or replaces tables (DocumentMut prints them after every table).
func scanTOMLDocument(text string) ([]tomlDocumentItem, int) {
	items := make([]tomlDocumentItem, 0, 8)
	current := -1
	lastContentEnd := 0
	for i := 0; i < len(text); {
		lineStart := i
		j := i
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		if j >= len(text) {
			break
		}
		if text[j] == '\n' {
			i = j + 1
			continue
		}
		if text[j] == '#' {
			i = tomlLineEnd(text, j)
			continue
		}
		if text[j] == '[' {
			if end, path, array, ok := parseTOMLTableHeader(text, j); ok {
				bodyEnd := tomlLineEnd(text, end)
				items = append(items, tomlDocumentItem{
					start:   lineStart,
					bodyEnd: bodyEnd,
					table:   true,
					array:   array,
					keyPath: path,
				})
				current = len(items) - 1
				lastContentEnd = bodyEnd
				i = bodyEnd
				continue
			}
		}
		end, rootKey := scanTOMLKeyValue(text, j)
		if end <= lineStart {
			end = tomlLineEnd(text, j)
		}
		if current < 0 {
			items = append(items, tomlDocumentItem{start: lineStart, bodyEnd: end, rootKey: rootKey})
			current = len(items) - 1
		} else {
			items[current].bodyEnd = end
		}
		lastContentEnd = end
		i = end
	}
	return items, lastContentEnd
}

// parseTOMLTableHeader reads a `[path]` or `[[path]]` header that starts at the
// bracket in position j. A header must close on its own line.
func parseTOMLTableHeader(text string, j int) (int, []string, bool, bool) {
	i := j + 1
	array := false
	if i < len(text) && text[i] == '[' {
		array = true
		i++
	}
	start := i
	for i < len(text) {
		switch text[i] {
		case '"':
			i = tomlSkipBasicString(text, i)
			continue
		case '\'':
			i = tomlSkipLiteralString(text, i)
			continue
		case '\n':
			return 0, nil, false, false
		case ']':
			if array {
				if i+1 < len(text) && text[i+1] == ']' {
					return i + 2, splitTOMLKeyPath(text[start:i]), true, true
				}
				i++
				continue
			}
			return i + 1, splitTOMLKeyPath(text[start:i]), false, true
		}
		i++
	}
	return 0, nil, false, false
}

// splitTOMLKeyPath splits a dotted key into its segments, keeping dots that sit
// inside quoted segments.
func splitTOMLKeyPath(raw string) []string {
	var segments []string
	var builder strings.Builder
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '"':
			end := tomlSkipBasicString(raw, i)
			builder.WriteString(tomlUnquoteKey(raw[i:end]))
			i = end
		case '\'':
			end := tomlSkipLiteralString(raw, i)
			builder.WriteString(strings.Trim(raw[i:end], "'"))
			i = end
		case '.':
			segments = append(segments, builder.String())
			builder.Reset()
			i++
		default:
			builder.WriteByte(raw[i])
			i++
		}
	}
	segments = append(segments, builder.String())
	for index := range segments {
		segments[index] = strings.TrimSpace(segments[index])
	}
	return segments
}

// scanTOMLKeyValue consumes one `key = value` construct starting at the key in
// position pos and reports where it ends plus the first key segment.
func scanTOMLKeyValue(text string, pos int) (int, string) {
	i := pos
	keyEnd := -1
	for i < len(text) {
		switch text[i] {
		case '"':
			i = tomlSkipBasicString(text, i)
			continue
		case '\'':
			i = tomlSkipLiteralString(text, i)
			continue
		case '\n':
			return i + 1, ""
		case '#':
			return tomlLineEnd(text, i), ""
		case '=':
			if keyEnd < 0 {
				keyEnd = i
			}
			return scanTOMLValueEnd(text, i+1), tomlUnquoteKey(strings.TrimSpace(text[pos:keyEnd]))
		}
		i++
	}
	if keyEnd < 0 {
		return len(text), ""
	}
	return len(text), tomlUnquoteKey(strings.TrimSpace(text[pos:keyEnd]))
}

// scanTOMLValueEnd returns the offset just past the newline that closes a value,
// skipping strings, multi-line strings, arrays and inline tables.
func scanTOMLValueEnd(text string, i int) int {
	depth := 0
	for i < len(text) {
		if strings.HasPrefix(text[i:], `"""`) {
			i = tomlSkipMultilineString(text, i, `"""`)
			continue
		}
		if strings.HasPrefix(text[i:], "'''") {
			i = tomlSkipMultilineString(text, i, "'''")
			continue
		}
		switch text[i] {
		case '"':
			i = tomlSkipBasicString(text, i)
			continue
		case '\'':
			i = tomlSkipLiteralString(text, i)
			continue
		case '#':
			i = tomlLineEnd(text, i) - 1
			continue
		case '[', '{':
			depth++
		case ']', '}':
			if depth > 0 {
				depth--
			}
		case '\n':
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return len(text)
}

func tomlLineEnd(text string, i int) int {
	if end := strings.IndexByte(text[i:], '\n'); end >= 0 {
		return i + end + 1
	}
	return len(text)
}

func tomlSkipBasicString(text string, i int) int {
	i++
	for i < len(text) {
		switch text[i] {
		case '\\':
			i += 2
			continue
		case '"':
			return i + 1
		case '\n':
			return i + 1
		}
		i++
	}
	return len(text)
}

func tomlSkipLiteralString(text string, i int) int {
	if end := strings.IndexByte(text[i+1:], '\''); end >= 0 {
		return i + 1 + end + 1
	}
	return len(text)
}

func tomlSkipMultilineString(text string, i int, delimiter string) int {
	i += len(delimiter)
	for i < len(text) {
		if strings.HasPrefix(text[i:], delimiter) {
			return i + len(delimiter)
		}
		if text[i] == '\\' {
			i += 2
			continue
		}
		i++
	}
	return len(text)
}

// spliceMCPTablesIntoDocument rewrites only the mcp_servers region of document,
// the way toml_edit edits a document in place (core/src/config/edit.rs
// replace_mcp_servers): the existing tables are removed together with the blank
// and comment lines that decorate them, the replacement tables are rendered with
// toml_edit's one-blank-line default decor, and every byte outside the region
// (unrelated comments, multi-line strings, multi-line arrays, blank lines and
// the document's trailing trivia) stays as it was. It refuses documents that
// spell mcp_servers as an inline table, where toml_edit renders a
// `[mcp_servers ]` header instead.
func spliceMCPTablesIntoDocument(document string, servers *OrderedTable) (string, bool) {
	// toml_edit re-emits every line it keeps with a bare newline, so a CRLF
	// document comes back as LF (DocumentMut writes through its own encoder).
	document = strings.ReplaceAll(document, "\r\n", "\n")
	items, trailingStart := scanTOMLDocument(document)
	first, last := -1, -1
	explicitHeader := false
	for index, item := range items {
		if item.table && !item.array && len(item.keyPath) > 0 && item.keyPath[0] == "mcp_servers" {
			if first < 0 {
				first = index
			}
			last = index
			if len(item.keyPath) == 1 {
				explicitHeader = true
			}
			continue
		}
		if !item.table && item.rootKey == "mcp_servers" {
			return "", false
		}
	}
	region := ""
	if servers != nil && len(servers.Keys) > 0 {
		rendered := renderTOML(map[string]any{"mcp_servers": servers})
		if explicitHeader {
			// A document that spelled `[mcp_servers]` as its own table keeps that
			// header (toml_edit only sets an implicit table when it inserts a
			// missing key), with the servers one blank line below it.
			rendered = "[mcp_servers]\n\n" + rendered
		}
		region = rendered
	}
	if first < 0 {
		if region == "" {
			return document, true
		}
		head := tomlTerminate(document[:trailingStart])
		separator := ""
		if head != "" {
			separator = "\n"
		}
		return head + separator + region + document[trailingStart:], true
	}
	start := 0
	if first > 0 {
		start = items[first-1].bodyEnd
	}
	head := tomlTerminate(document[:start])
	separator := "\n"
	if region == "" {
		// Nothing replaces the region (the last server was removed), so the
		// default decor has nothing to decorate.
		separator = ""
	} else if head == "" {
		// A document whose first construct is a table gets no leading blank
		// line: toml_edit only falls back to the default decor when a root
		// key/value or an earlier table has already been printed
		// (DEFAULT_TABLE_DECOR is skipped for the first table of a document).
		separator = ""
	}
	return head + separator + region + document[items[last].bodyEnd:], true
}

// tomlTerminate closes the last line of a kept document prefix: toml_edit
// writes a newline after every key/value and table header it keeps, so a file
// that ended mid-line comes back with that newline.
func tomlTerminate(prefix string) string {
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		return prefix + "\n"
	}
	return prefix
}
