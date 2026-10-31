package cli

import (
	"strconv"
	"strings"
)

// The --startup-client-info value parser mirrors Rust's
// serde_json::from_str::<ClientInfo>. ClientInfo is `{ name: String,
// version: String, title: Option<String> }` (the title field is optional), and
// serde reports syntax and type errors with the byte position it stopped at,
// which this scanner reproduces.

const startupClientInfoOption = "--startup-client-info <STARTUP_CLIENT_INFO>"

type clapJSONKind int

const (
	clapJSONObject clapJSONKind = iota
	clapJSONArray
	clapJSONString
	clapJSONNumber
	clapJSONBool
	clapJSONNull
)

type clapJSONField struct {
	key    string
	keyEnd int
	value  *clapJSONNode
}

type clapJSONNode struct {
	kind    clapJSONKind
	start   int
	end     int
	str     string
	number  string
	isFloat bool
	fields  []clapJSONField
	elems   []*clapJSONNode
	// trailingComma holds the byte offset just past a comma that closed the
	// container. serde only reports it after every preceding value validated.
	trailingComma int
	scanner       *clapJSONScanner
}

type clapJSONScanner struct {
	src []byte
	i   int
}

// position converts a byte offset into serde's 1-based line and column: the
// column counts the bytes already consumed on that line, so an offset at the
// start of the input reports column 0.
func (s *clapJSONScanner) position(offset int) (int, int) {
	line, column := 1, 0
	if offset > len(s.src) {
		offset = len(s.src)
	}
	for _, b := range s.src[:offset] {
		if b == '\n' {
			line++
			column = 0
			continue
		}
		column++
	}
	return line, column
}

func (s *clapJSONScanner) detail(prefix string, offset int) string {
	line, column := s.position(offset)
	return prefix + " at line " + strconv.Itoa(line) + " column " + strconv.Itoa(column)
}

func (s *clapJSONScanner) skipSpace() {
	for s.i < len(s.src) {
		switch s.src[s.i] {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return
		}
	}
}

func (s *clapJSONScanner) parseValue() (*clapJSONNode, string) {
	s.skipSpace()
	if s.i >= len(s.src) {
		return nil, s.detail("EOF while parsing a value", s.i)
	}
	switch b := s.src[s.i]; {
	case b == '{':
		return s.parseObject()
	case b == '[':
		return s.parseArray()
	case b == '"':
		return s.parseString()
	case b == 't' || b == 'f' || b == 'n':
		return s.parseLiteral()
	case b == '-' || (b >= '0' && b <= '9'):
		return s.parseNumber()
	default:
		return nil, s.detail("expected value", s.i+1)
	}
}

func (s *clapJSONScanner) parseObject() (*clapJSONNode, string) {
	node := &clapJSONNode{kind: clapJSONObject, start: s.i, scanner: s}
	s.i++
	s.skipSpace()
	if s.i >= len(s.src) {
		return nil, s.detail("EOF while parsing an object", s.i)
	}
	if s.src[s.i] == '}' {
		s.i++
		node.end = s.i
		return node, ""
	}
	for {
		s.skipSpace()
		if s.i >= len(s.src) {
			return nil, s.detail("EOF while parsing an object", s.i)
		}
		if s.src[s.i] != '"' {
			return nil, s.detail("key must be a string", s.i+1)
		}
		key, detail := s.parseString()
		if detail != "" {
			return nil, detail
		}
		s.skipSpace()
		if s.i >= len(s.src) {
			return nil, s.detail("EOF while parsing an object", s.i)
		}
		if s.src[s.i] != ':' {
			return nil, s.detail("expected `:`", s.i+1)
		}
		s.i++
		value, detail := s.parseValue()
		if detail != "" {
			return nil, detail
		}
		node.fields = append(node.fields, clapJSONField{key: key.str, keyEnd: key.end, value: value})
		s.skipSpace()
		if s.i >= len(s.src) {
			return nil, s.detail("EOF while parsing an object", s.i)
		}
		switch s.src[s.i] {
		case ',':
			s.i++
			s.skipSpace()
			if s.i < len(s.src) && s.src[s.i] == '}' {
				node.trailingComma = s.i + 1
				s.i++
				node.end = s.i
				return node, ""
			}
		case '}':
			s.i++
			node.end = s.i
			return node, ""
		default:
			return nil, s.detail("expected `,` or `}`", s.i+1)
		}
	}
}

func (s *clapJSONScanner) parseArray() (*clapJSONNode, string) {
	node := &clapJSONNode{kind: clapJSONArray, start: s.i, scanner: s}
	s.i++
	s.skipSpace()
	if s.i >= len(s.src) {
		return nil, s.detail("EOF while parsing a list", s.i)
	}
	if s.src[s.i] == ']' {
		s.i++
		node.end = s.i
		return node, ""
	}
	for {
		value, detail := s.parseValue()
		if detail != "" {
			return nil, detail
		}
		node.elems = append(node.elems, value)
		s.skipSpace()
		if s.i >= len(s.src) {
			return nil, s.detail("EOF while parsing a list", s.i)
		}
		switch s.src[s.i] {
		case ',':
			s.i++
			s.skipSpace()
			if s.i < len(s.src) && s.src[s.i] == ']' {
				node.trailingComma = s.i + 1
				s.i++
				node.end = s.i
				return node, ""
			}
		case ']':
			s.i++
			node.end = s.i
			return node, ""
		default:
			return nil, s.detail("expected `,` or `]`", s.i+1)
		}
	}
}

func (s *clapJSONScanner) parseString() (*clapJSONNode, string) {
	node := &clapJSONNode{kind: clapJSONString, start: s.i, scanner: s}
	begin := s.i
	s.i++
	for s.i < len(s.src) {
		switch s.src[s.i] {
		case '\\':
			if s.i+1 >= len(s.src) {
				return nil, s.detail("EOF while parsing a string", s.i+1)
			}
			s.i += 2
		case '"':
			s.i++
			node.end = s.i
			raw := string(s.src[begin:s.i])
			if decoded, err := strconv.Unquote(raw); err == nil {
				node.str = decoded
			} else {
				node.str = raw[1 : len(raw)-1]
			}
			return node, ""
		default:
			s.i++
		}
	}
	return nil, s.detail("EOF while parsing a string", s.i)
}

func (s *clapJSONScanner) parseLiteral() (*clapJSONNode, string) {
	begin := s.i
	rest := string(s.src[s.i:])
	node := &clapJSONNode{start: s.i, scanner: s}
	for _, literal := range []string{"true", "false", "null"} {
		if !strings.HasPrefix(rest, literal) {
			continue
		}
		node.kind = clapJSONBool
		if literal == "null" {
			node.kind = clapJSONNull
		} else {
			node.str = literal
		}
		s.i = begin + len(literal)
		node.end = s.i
		return node, ""
	}
	for _, literal := range []string{"true", "false", "null"} {
		if len(rest) < len(literal) && strings.HasPrefix(literal, rest) {
			// serde reads an identifier byte by byte, so a truncated literal at
			// the end of the input is an EOF diagnostic.
			return nil, s.detail("EOF while parsing a value", len(s.src))
		}
	}
	mismatch := 0
	for _, literal := range []string{"true", "false", "null"} {
		common := 0
		for common < len(rest) && common < len(literal) && rest[common] == literal[common] {
			common++
		}
		if common > mismatch {
			mismatch = common
		}
	}
	return nil, s.detail("expected ident", begin+mismatch+1)
}

func (s *clapJSONScanner) parseNumber() (*clapJSONNode, string) {
	node := &clapJSONNode{kind: clapJSONNumber, start: s.i, scanner: s}
	begin := s.i
	if s.src[s.i] == '-' {
		s.i++
	}
	for s.i < len(s.src) {
		b := s.src[s.i]
		if b >= '0' && b <= '9' {
			s.i++
			continue
		}
		switch b {
		case '.', 'e', 'E', '+', '-':
			node.isFloat = true
			s.i++
			continue
		}
		break
	}
	node.number = string(s.src[begin:s.i])
	node.end = s.i
	if node.number == "-" || node.number == "" {
		return nil, s.detail("expected value", begin+1)
	}
	return node, ""
}

// validateStartupClientInfo applies clap's serde value parser for
// `--startup-client-info <STARTUP_CLIENT_INFO>`.
func validateStartupClientInfo(value string) error {
	invalid := func(detail string) error {
		return &UsageError{Message: "invalid value '" + value + "' for '" + startupClientInfoOption + "': " + detail}
	}
	scanner := &clapJSONScanner{src: []byte(value)}
	node, detail := scanner.parseValue()
	if detail != "" {
		return invalid(detail)
	}
	if detail := clapClientInfoDetail(node); detail != "" {
		return invalid(detail)
	}
	scanner.skipSpace()
	if scanner.i < len(scanner.src) {
		return invalid(scanner.detail("trailing characters", scanner.i+1))
	}
	return nil
}

// clapClientInfoDetail reports the serde diagnostic for a decoded value, or ""
// when it deserialises into ClientInfo.
func clapClientInfoDetail(node *clapJSONNode) string {
	switch node.kind {
	case clapJSONObject:
		seen := make(map[string]bool, len(node.fields))
		for _, field := range node.fields {
			switch field.key {
			case "name", "version", "title":
			default:
				continue
			}
			if seen[field.key] {
				return node.scanner.detail("duplicate field `"+field.key+"`", field.keyEnd)
			}
			seen[field.key] = true
			if field.key == "title" && field.value.kind == clapJSONNull {
				continue
			}
			if detail := clapClientInfoStringDetail(node.scanner, field.value); detail != "" {
				return detail
			}
		}
		if node.trailingComma > 0 {
			return node.scanner.detail("trailing comma", node.trailingComma)
		}
		for _, required := range []string{"name", "version"} {
			if !seen[required] {
				return node.scanner.detail("missing field `"+required+"`", node.end)
			}
		}
		return ""
	case clapJSONArray:
		// serde reads at most three elements (name, version, title) and only
		// then notices surplus input, so the element type errors come first.
		bound := len(node.elems)
		if bound > 3 {
			bound = 3
		}
		for _, elem := range node.elems[:bound] {
			if detail := clapClientInfoStringDetail(node.scanner, elem); detail != "" {
				return detail
			}
		}
		if len(node.elems) > 3 {
			return node.scanner.detail("trailing characters", node.elems[3].start+1)
		}
		if node.trailingComma > 0 {
			return node.scanner.detail("trailing comma", node.trailingComma)
		}
		if len(node.elems) != 3 {
			return node.scanner.detail("invalid length "+strconv.Itoa(len(node.elems))+", expected struct ClientInfo with 3 elements", node.end)
		}
		return ""
	default:
		return clapClientInfoScalarDetail(node, "struct ClientInfo")
	}
}

func clapClientInfoStringDetail(scanner *clapJSONScanner, node *clapJSONNode) string {
	if node.kind == clapJSONString {
		return ""
	}
	return clapClientInfoScalarDetail(node, "a string")
}

func clapClientInfoScalarDetail(node *clapJSONNode, expected string) string {
	scanner := node.scanner
	switch node.kind {
	case clapJSONNull:
		return scanner.detail("invalid type: null, expected "+expected, node.end)
	case clapJSONBool:
		return scanner.detail("invalid type: boolean `"+node.str+"`, expected "+expected, node.end)
	case clapJSONString:
		return scanner.detail("invalid type: string "+strconv.Quote(node.str)+", expected "+expected, node.end)
	case clapJSONNumber:
		if node.isFloat {
			return scanner.detail("invalid type: number, expected "+expected, node.end)
		}
		return scanner.detail("invalid type: integer `"+node.number+"`, expected "+expected, node.end)
	case clapJSONObject:
		// serde reports the byte it last consumed before the container, which
		// is the position just before the opening brace.
		return scanner.detail("invalid type: map, expected "+expected, node.start)
	case clapJSONArray:
		return scanner.detail("invalid type: sequence, expected "+expected, node.start)
	}
	return scanner.detail("expected "+expected, node.end)
}
