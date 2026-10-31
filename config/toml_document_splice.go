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
	if spliced, ok := spliceInlineMCPTablesIntoDocument(document, servers); ok {
		return spliced, true
	}
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
		var inlineRegion string
		inlineOK := false
		if first >= 0 {
			inlineRegion, inlineOK = spliceMCPHeaderBodyRegion(document, items[first], first == last, servers)
		}
		if inlineOK {
			region = inlineRegion
		} else {
			region = renderMCPTables(servers, explicitHeader)
		}
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

// renderMCPTables renders the replacement servers the way toml_edit prints a
// table of tables, keeping an explicit `[mcp_servers]` header one blank line
// above them.
func renderMCPTables(servers *OrderedTable, explicitHeader bool) string {
	rendered := renderTOML(map[string]any{"mcp_servers": servers})
	if explicitHeader {
		// A document that spelled `[mcp_servers]` as its own table keeps that
		// header (toml_edit only sets an implicit table when it inserts a
		// missing key), with the servers one blank line below it.
		rendered = "[mcp_servers]\n\n" + rendered
	}
	return rendered
}

// spliceMCPHeaderBodyRegion handles a `[mcp_servers]` table whose body spells
// every server inline: toml_edit edits those values in place, so the header and
// the inline spelling survive and only new servers become sub-tables.
func spliceMCPHeaderBodyRegion(document string, header tomlDocumentItem, alone bool, servers *OrderedTable) (string, bool) {
	if !header.table || header.array || !alone {
		return "", false
	}
	if len(header.keyPath) != 1 || header.keyPath[0] != "mcp_servers" {
		return "", false
	}
	lineEnd := tomlLineEnd(document, header.start)
	headerLine := strings.TrimRight(document[header.start:lineEnd], " \t\r\n")
	body := document[lineEnd:header.bodyEnd]
	entries := make([]tomlInlineEntry, 0, 4)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			return "", false
		}
		keyStart := strings.Index(line, trimmed)
		keyEnd, name, ok := scanTOMLInlineKey(line, keyStart)
		if !ok {
			return "", false
		}
		index := keyEnd
		for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
			index++
		}
		if index >= len(line) || line[index] != '=' {
			return "", false
		}
		index++
		spaceStart := index
		for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
			index++
		}
		valuePrefix := line[spaceStart:index]
		if index >= len(line) || line[index] != '{' {
			return "", false
		}
		valueEnd, ok := scanTOMLInlineTableEnd(line, index)
		if !ok {
			return "", false
		}
		if strings.TrimSpace(line[valueEnd:]) != "" {
			return "", false
		}
		entries = append(entries, tomlInlineEntry{
			keyText:     line[keyStart:keyEnd],
			name:        name,
			valuePrefix: valuePrefix,
			valueText:   line[index:valueEnd],
		})
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.name] = true
	}
	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, ok := servers.Values[entry.name]; !ok {
			continue
		}
		text := entry.valueText
		if nested, ok := parseTOMLInlineTable(entry.valueText); ok {
			if replacement, ok := mcpServerInlineEntries(servers.Values[entry.name]); ok {
				text = renderTOMLInlineTable(mergeTOMLInlineTable(nested, replacement))
			}
		}
		kept = append(kept, entry.keyText+" "+"="+entry.valuePrefix+text)
	}
	var added *OrderedTable
	for _, name := range servers.Keys {
		if names[name] {
			continue
		}
		if _, ok := servers.Values[name]; !ok {
			continue
		}
		if added == nil {
			added = NewOrderedTable()
		}
		added.Set(name, servers.Values[name])
	}
	var region strings.Builder
	region.WriteString(headerLine)
	region.WriteByte('\n')
	if len(kept) > 0 {
		region.WriteString(strings.Join(kept, "\n"))
		region.WriteByte('\n')
	}
	if added != nil {
		region.WriteByte('\n')
		region.WriteString(renderTOML(map[string]any{"mcp_servers": added}))
	}
	return region.String(), true
}

// tomlInlineEntry is one `key = value` pair of an inline table together with
// the decor toml_edit keeps for it: the comma and spaces that separate two
// pairs belong to the following key's prefix, and the whitespace before the
// closing brace belongs to the value it follows.
type tomlInlineEntry struct {
	keyPrefix   string
	keyText     string
	name        string
	keySuffix   string
	valuePrefix string
	valueText   string
	valueSuffix string
}

// tomlRootValue is one root-level key/value of a document: the start of its key,
// the offset of its `=`, and the offset just past the newline that closes it
// (which also covers a trailing comment).
type tomlRootValue struct {
	keyStart int
	equal    int
	bodyEnd  int
	key      string
}

// scanTOMLRootValues lists the root-level key/values of a document in order,
// stopping at the first table header. scanTOMLDocument groups a run of root
// key/values into one item, which is enough to splice whole tables but not to
// pull a single key out of the run.
func scanTOMLRootValues(text string) []tomlRootValue {
	values := make([]tomlRootValue, 0, 8)
	for i := 0; i < len(text); {
		j := i
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		if j >= len(text) || text[j] == '\n' {
			i = tomlLineEnd(text, j)
			continue
		}
		if text[j] == '#' {
			i = tomlLineEnd(text, j)
			continue
		}
		if text[j] == '[' {
			break
		}
		end, key := scanTOMLKeyValue(text, j)
		if key == "" || end <= j {
			break
		}
		equal := strings.IndexByte(text[j:end], '=')
		if equal < 0 {
			break
		}
		values = append(values, tomlRootValue{keyStart: j, equal: j + equal, bodyEnd: end, key: key})
		i = end
	}
	return values
}

// spliceInlineMCPTablesIntoDocument rewrites a document that spells
// `mcp_servers` as an inline table. toml_edit converts that value into a
// regular table (document_helpers::table_from_inline), so servers that the edit
// keeps stay inline under a header that carries the key-s first decor, the key
// is printed after the other root values, and only genuinely new servers become
// `[mcp_servers.<name>]` tables.
func spliceInlineMCPTablesIntoDocument(document string, servers *OrderedTable) (string, bool) {
	items, _ := scanTOMLDocument(document)
	for _, item := range items {
		if item.table && len(item.keyPath) > 0 && item.keyPath[0] == "mcp_servers" {
			// A document that also spells servers as tables is left alone: the
			// lenient parse keeps both items and toml_edit edits the table.
			return "", false
		}
	}
	rootValues := scanTOMLRootValues(document)
	inlineIndex := -1
	for index, value := range rootValues {
		if value.key == "mcp_servers" {
			inlineIndex = index
			break
		}
	}
	if inlineIndex < 0 {
		return "", false
	}
	value := rootValues[inlineIndex]
	rawKey := strings.TrimRight(document[value.keyStart:value.equal], " \t")
	keySuffix := document[value.keyStart+len(rawKey) : value.equal]
	valueStart := value.equal + 1
	for valueStart < len(document) && (document[valueStart] == ' ' || document[valueStart] == '\t') {
		valueStart++
	}
	if valueStart >= len(document) || document[valueStart] != '{' {
		return "", false
	}
	valueEnd, ok := scanTOMLInlineTableEnd(document, valueStart)
	if !ok {
		return "", false
	}
	existing, ok := parseTOMLInlineTable(document[valueStart:valueEnd])
	if !ok {
		return "", false
	}
	previousEnd := 0
	if inlineIndex > 0 {
		previousEnd = rootValues[inlineIndex-1].bodyEnd
	}
	decor := document[previousEnd:value.keyStart]
	base := document[:previousEnd] + document[value.bodyEnd:]

	names := make(map[string]bool, len(existing))
	for _, entry := range existing {
		names[entry.name] = true
	}
	var kept []string
	if servers != nil {
		for _, entry := range existing {
			if _, ok := servers.Values[entry.name]; !ok {
				continue
			}
			line := entry.keyText + " " + "=" + entry.valuePrefix + entry.valueText
			if nested, ok := parseTOMLInlineTable(entry.valueText); ok {
				if replacement, ok := mcpServerInlineEntries(servers.Values[entry.name]); ok {
					line = entry.keyText + " " + "=" + entry.valuePrefix +
						renderTOMLInlineTable(mergeTOMLInlineTable(nested, replacement))
				}
			}
			kept = append(kept, line)
		}
	}
	var added *OrderedTable
	if servers != nil {
		for _, name := range servers.Keys {
			if names[name] {
				continue
			}
			if _, ok := servers.Values[name]; !ok {
				continue
			}
			if added == nil {
				added = NewOrderedTable()
			}
			added.Set(name, servers.Values[name])
		}
	}

	var block strings.Builder
	if len(kept) > 0 {
		block.WriteString("[")
		block.WriteString(decor)
		block.WriteString(rawKey)
		block.WriteString(keySuffix)
		block.WriteString("]\n")
		for _, line := range kept {
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}
	if added != nil {
		if block.Len() > 0 {
			block.WriteByte('\n')
		}
		block.WriteString(renderTOML(map[string]any{"mcp_servers": added}))
	}

	insert := 0
	for _, remaining := range scanTOMLRootValues(base) {
		insert = remaining.bodyEnd
	}
	separator := ""
	if insert > 0 && block.Len() > 0 {
		separator = "\n"
	}
	return base[:insert] + separator + block.String() + base[insert:], true
}

// scanTOMLInlineTableEnd returns the offset just past the bracket that closes
// the inline table or array starting at pos.
func scanTOMLInlineTableEnd(text string, pos int) (int, bool) {
	if pos >= len(text) || (text[pos] != '{' && text[pos] != '[') {
		return 0, false
	}
	open := text[pos]
	close := byte('}')
	if open == '[' {
		close = ']'
	}
	depth := 0
	for i := pos; i < len(text); {
		switch text[i] {
		case '"':
			i = tomlSkipBasicString(text, i)
			continue
		case '\'':
			i = tomlSkipLiteralString(text, i)
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1, text[i] == close
			}
		}
		i++
	}
	return 0, false
}

// parseTOMLInlineTable reads the inline table at the start of text into the
// pairs toml_edit keeps, including the decor that decides how a merge renders.
func parseTOMLInlineTable(text string) ([]tomlInlineEntry, bool) {
	if text == "" || text[0] != '{' {
		return nil, false
	}
	index := 1
	prefix := ""
	entries := make([]tomlInlineEntry, 0, 4)
	for {
		keyStart := index
		for keyStart < len(text) && (text[keyStart] == ' ' || text[keyStart] == '\t' || text[keyStart] == '\n') {
			keyStart++
		}
		prefix += text[index:keyStart]
		index = keyStart
		if index >= len(text) {
			return nil, false
		}
		if text[index] == '}' {
			if len(entries) == 0 {
				if prefix != "" {
					return nil, false
				}
				return entries, true
			}
			entries[len(entries)-1].valueSuffix = prefix
			return entries, true
		}
		keyEnd, name, ok := scanTOMLInlineKey(text, index)
		if !ok {
			return nil, false
		}
		entry := tomlInlineEntry{keyPrefix: prefix, keyText: text[index:keyEnd], name: name}
		index = keyEnd
		spaceStart := index
		for index < len(text) && (text[index] == ' ' || text[index] == '\t') {
			index++
		}
		entry.keySuffix = text[spaceStart:index]
		if index >= len(text) || text[index] != '=' {
			return nil, false
		}
		index++
		spaceStart = index
		for index < len(text) && (text[index] == ' ' || text[index] == '\t') {
			index++
		}
		entry.valuePrefix = text[spaceStart:index]
		valueEnd, ok := scanTOMLInlineValueEnd(text, index)
		if !ok {
			return nil, false
		}
		entry.valueText = text[index:valueEnd]
		index = valueEnd
		spaceStart = index
		for index < len(text) && (text[index] == ' ' || text[index] == '\t') {
			index++
		}
		entry.valueSuffix = text[spaceStart:index]
		if index >= len(text) {
			return nil, false
		}
		entries = append(entries, entry)
		switch text[index] {
		case ',':
			prefix = ","
			index++
		case '}':
			return entries, true
		default:
			return nil, false
		}
	}
}

// scanTOMLInlineKey reads a bare or quoted inline-table key.
func scanTOMLInlineKey(text string, pos int) (int, string, bool) {
	if pos >= len(text) {
		return 0, "", false
	}
	switch text[pos] {
	case '"':
		end := tomlSkipBasicString(text, pos)
		if end <= pos+1 || end > len(text) {
			return 0, "", false
		}
		return end, tomlUnquoteKey(text[pos:end]), true
	case '\'':
		end := tomlSkipLiteralString(text, pos)
		if end <= pos+1 || end > len(text) {
			return 0, "", false
		}
		return end, tomlUnquoteKey(text[pos:end]), true
	}
	end := pos
	for end < len(text) {
		c := text[end]
		bare := c == '_' || c == '-' || (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !bare {
			break
		}
		end++
	}
	if end == pos {
		return 0, "", false
	}
	return end, text[pos:end], true
}

// scanTOMLInlineValueEnd returns the offset just past a value written inside an
// inline table.
func scanTOMLInlineValueEnd(text string, pos int) (int, bool) {
	if pos >= len(text) {
		return 0, false
	}
	switch text[pos] {
	case '{', '[':
		return scanTOMLInlineTableEnd(text, pos)
	case '"':
		end := tomlSkipBasicString(text, pos)
		if end <= pos+1 || end > len(text) {
			return 0, false
		}
		return end, true
	case '\'':
		end := tomlSkipLiteralString(text, pos)
		if end <= pos+1 || end > len(text) {
			return 0, false
		}
		return end, true
	}
	end := pos
	for end < len(text) {
		switch text[end] {
		case ',', '}', ']', ' ', '\t', '\n':
			return end, end > pos
		}
		end++
	}
	return end, end > pos
}

// renderTOMLInlineTable prints the pairs with the decor toml_edit keeps.
func renderTOMLInlineTable(entries []tomlInlineEntry) string {
	var builder strings.Builder
	builder.WriteByte('{')
	for _, entry := range entries {
		builder.WriteString(entry.keyPrefix)
		builder.WriteString(entry.keyText)
		builder.WriteString(entry.keySuffix)
		builder.WriteByte('=')
		builder.WriteString(entry.valuePrefix)
		builder.WriteString(entry.valueText)
		builder.WriteString(entry.valueSuffix)
	}
	builder.WriteByte('}')
	return builder.String()
}

// mergeTOMLInlineTable mirrors document_helpers::merge_inline_table: pairs the
// replacement drops disappear, pairs it keeps keep their original decor and
// take its value, and new pairs are appended with the default decor.
func mergeTOMLInlineTable(existing, replacement []tomlInlineEntry) []tomlInlineEntry {
	byName := make(map[string]tomlInlineEntry, len(replacement))
	for _, entry := range replacement {
		byName[entry.name] = entry
	}
	merged := make([]tomlInlineEntry, 0, len(replacement))
	for _, entry := range existing {
		updated, ok := byName[entry.name]
		if !ok {
			continue
		}
		entry.valueText = updated.valueText
		merged = append(merged, entry)
	}
	present := make(map[string]bool, len(merged))
	for _, entry := range merged {
		present[entry.name] = true
	}
	for _, entry := range replacement {
		if present[entry.name] {
			continue
		}
		if len(merged) == 0 {
			entry.keyPrefix = " "
		} else {
			entry.keyPrefix = ", "
		}
		merged = append(merged, entry)
	}
	return merged
}

// mcpServerInlineEntries renders one server config as the pairs toml_edit's
// serializer produces: default decor, insertion order, one space before the
// closing brace.
func mcpServerInlineEntries(value any) ([]tomlInlineEntry, bool) {
	values, keys, _, ok := tableView(value)
	if !ok || len(keys) == 0 {
		return nil, false
	}
	entries := make([]tomlInlineEntry, 0, len(keys))
	for _, key := range keys {
		text, ok := tomlInlineValueText(values[key])
		if !ok {
			return nil, false
		}
		entries = append(entries, tomlInlineEntry{
			keyText:     formatTOMLInlineKey(key),
			name:        key,
			keySuffix:   " ",
			valuePrefix: " ",
			valueText:   text,
		})
	}
	entries[len(entries)-1].valueSuffix = " "
	return entries, true
}

// tomlInlineValueText renders a server field the way it appears inside an
// inline table.
func tomlInlineValueText(value any) (string, bool) {
	if _, _, _, nested := tableView(value); nested {
		values, keys, _, _ := tableView(value)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			text, ok := tomlInlineValueText(values[key])
			if !ok {
				return "", false
			}
			parts = append(parts, formatTOMLInlineKey(key)+" = "+text)
		}
		return "{ " + strings.Join(parts, ", ") + " }", true
	}
	if _, arrayTable := tomlArrayTables(value); arrayTable {
		return "", false
	}
	return formatTOMLValue(value), true
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
