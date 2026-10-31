package config

import (
	"fmt"
	"strconv"
	"strings"
)

// The strings below are the diagnostics serde_json 1.0 produces. Rust reports
// them through `Display for ErrorCode`, so the wording, casing and punctuation
// are part of the observable CLI output.
const (
	serdeEOFList           = "EOF while parsing a list"
	serdeEOFObject         = "EOF while parsing an object"
	serdeEOFString         = "EOF while parsing a string"
	serdeEOFValue          = "EOF while parsing a value"
	serdeExpectedColon     = "expected `:`"
	serdeExpectedListComma = "expected `,` or `]`"
	serdeExpectedObjComma  = "expected `,` or `}`"
	serdeExpectedIdent     = "expected ident"
	serdeExpectedValue     = "expected value"
	serdeInvalidEscape     = "invalid escape"
	serdeInvalidNumber     = "invalid number"
	serdeControlCharacter  = `control character (\u0000-\u001F) found while parsing a string`
	serdeKeyMustBeString   = "key must be a string"
	serdeLoneSurrogate     = "lone leading surrogate in hex escape"
	serdeTrailingComma     = "trailing comma"
	serdeTrailingChars     = "trailing characters"
	serdeHexEscapeEOF      = "unexpected end of hex escape"
)

// catalogJSONError mirrors serde_json::Error: the message plus the byte offset
// the reader stood on when the error was raised. Errors created without a
// position (the `de::Error::custom` family) are fixed up when they leave the
// enclosing container, exactly like serde_json's `fix_position`.
type catalogJSONError struct {
	msg   string
	index int
	doc   []byte
}

func (e *catalogJSONError) Error() string {
	if e.index < 0 {
		return e.msg
	}
	line, column := catalogLineColumn(e.doc, e.index)
	return fmt.Sprintf("%s at line %d column %d", e.msg, line, column)
}

func (e *catalogJSONError) fixAt(index int) {
	if e.index < 0 {
		e.index = index
	}
}

func catalogLineColumn(doc []byte, index int) (int, int) {
	if index < 0 {
		index = 0
	}
	if index > len(doc) {
		index = len(doc)
	}
	line := 1
	start := 0
	for i := 0; i < index; i++ {
		if doc[i] == '\n' {
			line++
			start = i + 1
		}
	}
	return line, index - start
}

type catalogNodeKind uint8

const (
	catalogNull catalogNodeKind = iota
	catalogBool
	catalogUint
	catalogInt
	catalogFloat
	catalogString
	catalogArray
	catalogObject
)

// catalogNode is the buffered equivalent of serde's `Content`. Numbers that
// serde_json cannot keep as u64/i64 land here as floats: with
// `arbitrary_precision` such values are re-exposed as a single-key map, which
// is why the CLI reports "invalid type: map" for them.
type catalogNode struct {
	kind    catalogNodeKind
	str     string
	uintVal uint64
	intVal  int64
	boolVal bool
	items   []*catalogNode
	members []catalogMember
}

type catalogMember struct {
	key   string
	value *catalogNode
}

type catalogJSON struct {
	doc []byte
	idx int
}

func (p *catalogJSON) at() *catalogJSONError {
	return &catalogJSONError{index: p.idx, doc: p.doc}
}

// peekError is serde_json's `peek_error`, which reports the position of the
// byte after the current index (clamped to the end of input).
func (p *catalogJSON) peekError(msg string) *catalogJSONError {
	index := p.idx + 1
	if index > len(p.doc) {
		index = len(p.doc)
	}
	return &catalogJSONError{msg: msg, index: index, doc: p.doc}
}

func (p *catalogJSON) plainError(msg string) *catalogJSONError {
	return &catalogJSONError{msg: msg, index: -1, doc: p.doc}
}

func (p *catalogJSON) skipWhitespace() (byte, bool) {
	for p.idx < len(p.doc) {
		switch p.doc[p.idx] {
		case ' ', '\n', '\t', '\r':
			p.idx++
		default:
			return p.doc[p.idx], true
		}
	}
	return 0, false
}

func (p *catalogJSON) parseIdent(rest string) error {
	for i := 0; i < len(rest); i++ {
		if p.idx >= len(p.doc) {
			return p.at().withMsg(serdeEOFValue)
		}
		c := p.doc[p.idx]
		p.idx++
		if c != rest[i] {
			return p.at().withMsg(serdeExpectedIdent)
		}
	}
	return nil
}

func (e *catalogJSONError) withMsg(msg string) *catalogJSONError {
	e.msg = msg
	return e
}

func isJSONDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *catalogJSON) parseString() (string, error) {
	var b strings.Builder
	for {
		if p.idx >= len(p.doc) {
			return "", p.at().withMsg(serdeEOFString)
		}
		c := p.doc[p.idx]
		switch {
		case c == '"':
			p.idx++
			return b.String(), nil
		case c == '\\':
			p.idx++
			if err := p.parseEscape(&b); err != nil {
				return "", err
			}
		case c < 0x20:
			p.idx++
			return "", p.at().withMsg(serdeControlCharacter)
		default:
			b.WriteByte(c)
			p.idx++
		}
	}
}

func (p *catalogJSON) parseEscape(b *strings.Builder) error {
	if p.idx >= len(p.doc) {
		return p.at().withMsg(serdeEOFString)
	}
	c := p.doc[p.idx]
	p.idx++
	switch c {
	case '"':
		b.WriteByte('"')
	case '\\':
		b.WriteByte('\\')
	case '/':
		b.WriteByte('/')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'u':
		return p.parseUnicodeEscape(b)
	default:
		return p.at().withMsg(serdeInvalidEscape)
	}
	return nil
}

func (p *catalogJSON) decodeHexEscape() (uint16, error) {
	var digits [4]byte
	for i := range digits {
		if p.idx >= len(p.doc) {
			return 0, p.at().withMsg(serdeEOFString)
		}
		digits[i] = p.doc[p.idx]
		p.idx++
	}
	var value uint16
	for _, d := range digits {
		var nibble uint16
		switch {
		case d >= '0' && d <= '9':
			nibble = uint16(d - '0')
		case d >= 'a' && d <= 'f':
			nibble = uint16(d-'a') + 10
		case d >= 'A' && d <= 'F':
			nibble = uint16(d-'A') + 10
		default:
			return 0, p.at().withMsg(serdeInvalidEscape)
		}
		value = value*16 + nibble
	}
	return value, nil
}

func (p *catalogJSON) parseUnicodeEscape(b *strings.Builder) error {
	n, err := p.decodeHexEscape()
	if err != nil {
		return err
	}
	if n >= 0xDC00 && n <= 0xDFFF {
		return p.at().withMsg(serdeLoneSurrogate)
	}
	if n < 0xD800 || n > 0xDBFF {
		b.WriteRune(rune(n))
		return nil
	}
	if p.idx >= len(p.doc) {
		return p.at().withMsg(serdeEOFString)
	}
	if p.doc[p.idx] == '\\' {
		p.idx++
	} else {
		p.idx++
		return p.at().withMsg(serdeHexEscapeEOF)
	}
	if p.idx >= len(p.doc) {
		return p.at().withMsg(serdeEOFString)
	}
	if p.doc[p.idx] == 'u' {
		p.idx++
	} else {
		p.idx++
		return p.at().withMsg(serdeHexEscapeEOF)
	}
	n2, err := p.decodeHexEscape()
	if err != nil {
		return err
	}
	if n2 < 0xDC00 || n2 > 0xDFFF {
		return p.at().withMsg(serdeLoneSurrogate)
	}
	b.WriteRune(((rune(n-0xD800) << 10) | rune(n2-0xDC00)) + 0x10000)
	return nil
}

func (p *catalogJSON) parseNumber(positive bool) (*catalogNode, error) {
	start := p.idx
	if !positive {
		start--
	}
	if p.idx >= len(p.doc) {
		return nil, p.at().withMsg(serdeEOFValue)
	}
	c := p.doc[p.idx]
	p.idx++
	switch {
	case c == '0':
		if p.idx < len(p.doc) && isJSONDigit(p.doc[p.idx]) {
			return nil, p.peekError(serdeInvalidNumber)
		}
	case c >= '1' && c <= '9':
		for p.idx < len(p.doc) && isJSONDigit(p.doc[p.idx]) {
			p.idx++
		}
	default:
		return nil, p.at().withMsg(serdeInvalidNumber)
	}
	isFloat := false
	if p.idx < len(p.doc) && p.doc[p.idx] == '.' {
		p.idx++
		digits := 0
		for p.idx < len(p.doc) && isJSONDigit(p.doc[p.idx]) {
			p.idx++
			digits++
		}
		if digits == 0 {
			if p.idx >= len(p.doc) {
				return nil, p.peekError(serdeEOFValue)
			}
			return nil, p.peekError(serdeInvalidNumber)
		}
		isFloat = true
	}
	if p.idx < len(p.doc) && (p.doc[p.idx] == 'e' || p.doc[p.idx] == 'E') {
		p.idx++
		if p.idx < len(p.doc) && (p.doc[p.idx] == '+' || p.doc[p.idx] == '-') {
			p.idx++
		}
		if p.idx >= len(p.doc) {
			return nil, p.at().withMsg(serdeEOFValue)
		}
		digit := p.doc[p.idx]
		p.idx++
		if !isJSONDigit(digit) {
			return nil, p.at().withMsg(serdeInvalidNumber)
		}
		for p.idx < len(p.doc) && isJSONDigit(p.doc[p.idx]) {
			p.idx++
		}
		isFloat = true
	}
	text := string(p.doc[start:p.idx])
	if !isFloat {
		if positive {
			if value, err := strconv.ParseUint(text, 10, 64); err == nil {
				return &catalogNode{kind: catalogUint, uintVal: value}, nil
			}
		} else if value, err := strconv.ParseInt(text, 10, 64); err == nil {
			return &catalogNode{kind: catalogInt, intVal: value}, nil
		}
	}
	return &catalogNode{kind: catalogFloat, str: text}, nil
}

func (p *catalogJSON) parseAny() (*catalogNode, error) {
	peek, ok := p.skipWhitespace()
	if !ok {
		return nil, p.peekError(serdeEOFValue)
	}
	switch {
	case peek == 'n':
		p.idx++
		if err := p.parseIdent("ull"); err != nil {
			return nil, err
		}
		return &catalogNode{kind: catalogNull}, nil
	case peek == 't':
		p.idx++
		if err := p.parseIdent("rue"); err != nil {
			return nil, err
		}
		return &catalogNode{kind: catalogBool, boolVal: true}, nil
	case peek == 'f':
		p.idx++
		if err := p.parseIdent("alse"); err != nil {
			return nil, err
		}
		return &catalogNode{kind: catalogBool, boolVal: false}, nil
	case peek == '-':
		p.idx++
		return p.parseNumber(false)
	case isJSONDigit(peek):
		return p.parseNumber(true)
	case peek == '"':
		p.idx++
		value, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return &catalogNode{kind: catalogString, str: value}, nil
	case peek == '[':
		return p.parseArrayNode()
	case peek == '{':
		return p.parseObjectNode()
	}
	return nil, p.peekError(serdeExpectedValue)
}

func (p *catalogJSON) parseArrayNode() (*catalogNode, error) {
	p.idx++
	node := &catalogNode{kind: catalogArray}
	first := true
	for {
		peek, ok := p.skipWhitespace()
		if !ok {
			return nil, p.peekError(serdeEOFList)
		}
		if peek == ']' {
			p.idx++
			return node, nil
		}
		if !first {
			if peek != ',' {
				return nil, p.peekError(serdeExpectedListComma)
			}
			p.idx++
			next, ok := p.skipWhitespace()
			if !ok {
				return nil, p.peekError(serdeEOFValue)
			}
			if next == ']' {
				return nil, p.peekError(serdeTrailingComma)
			}
		}
		first = false
		item, err := p.parseAny()
		if err != nil {
			return nil, err
		}
		node.items = append(node.items, item)
	}
}

func (p *catalogJSON) parseObjectNode() (*catalogNode, error) {
	p.idx++
	node := &catalogNode{kind: catalogObject}
	first := true
	for {
		peek, ok := p.skipWhitespace()
		if !ok {
			return nil, p.peekError(serdeEOFObject)
		}
		if peek == '}' {
			p.idx++
			return node, nil
		}
		if !first {
			if peek != ',' {
				return nil, p.peekError(serdeExpectedObjComma)
			}
			p.idx++
			next, ok := p.skipWhitespace()
			if !ok {
				return nil, p.peekError(serdeEOFValue)
			}
			if next == '}' {
				return nil, p.peekError(serdeTrailingComma)
			}
			if next != '"' {
				return nil, p.peekError(serdeKeyMustBeString)
			}
		}
		first = false
		peek, ok = p.skipWhitespace()
		if !ok {
			return nil, p.peekError(serdeEOFObject)
		}
		if peek != '"' {
			return nil, p.peekError(serdeKeyMustBeString)
		}
		p.idx++
		key, err := p.parseString()
		if err != nil {
			return nil, err
		}
		if err := p.parseObjectColon(); err != nil {
			return nil, err
		}
		value, err := p.parseAny()
		if err != nil {
			return nil, err
		}
		node.members = append(node.members, catalogMember{key: key, value: value})
	}
}

func (p *catalogJSON) parseObjectColon() error {
	peek, ok := p.skipWhitespace()
	if !ok {
		return p.peekError(serdeEOFObject)
	}
	if peek != ':' {
		return p.peekError(serdeExpectedColon)
	}
	p.idx++
	return nil
}

// peekInvalidType is serde_json's peek_invalid_type: scalars are consumed so
// the reported position is the end of the value, while `[` and `{` stay put.
func (p *catalogJSON) peekInvalidType(expected string) error {
	var peek byte
	if p.idx < len(p.doc) {
		peek = p.doc[p.idx]
	}
	var unexpected string
	switch {
	case peek == 'n':
		p.idx++
		if err := p.parseIdent("ull"); err != nil {
			return err
		}
		unexpected = "null"
	case peek == 't':
		p.idx++
		if err := p.parseIdent("rue"); err != nil {
			return err
		}
		unexpected = "boolean `true`"
	case peek == 'f':
		p.idx++
		if err := p.parseIdent("alse"); err != nil {
			return err
		}
		unexpected = "boolean `false`"
	case peek == '-':
		p.idx++
		node, err := p.parseNumber(false)
		if err != nil {
			return err
		}
		unexpected = catalogNumberUnexpected(node)
	case isJSONDigit(peek):
		node, err := p.parseNumber(true)
		if err != nil {
			return err
		}
		unexpected = catalogNumberUnexpected(node)
	case peek == '"':
		p.idx++
		value, err := p.parseString()
		if err != nil {
			return err
		}
		unexpected = "string " + catalogRustQuote(value)
	case peek == '[':
		unexpected = "sequence"
	case peek == '{':
		unexpected = "map"
	default:
		return p.peekError(serdeExpectedValue)
	}
	return p.at().withMsg("invalid type: " + unexpected + ", expected " + expected)
}

func catalogNumberUnexpected(node *catalogNode) string {
	switch node.kind {
	case catalogUint:
		return fmt.Sprintf("integer `%d`", node.uintVal)
	case catalogInt:
		return fmt.Sprintf("integer `%d`", node.intVal)
	}
	return "number"
}

func catalogRustQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u{%x}`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

type catalogTypeKind uint8

const (
	catalogTypeAny catalogTypeKind = iota
	catalogTypeString
	catalogTypeBool
	catalogTypeI32
	catalogTypeI64
	catalogTypeEnum
	catalogTypeStruct
	catalogTypeSeq
)

type catalogFieldSpec struct {
	name       string
	kind       catalogTypeKind
	optional   bool
	hasDefault bool
	variants   []string
	structTy   *catalogStructSpec
	elem       *catalogFieldSpec
}

type catalogStructSpec struct {
	name   string
	fields []catalogFieldSpec
}

type catalogValidator struct {
	doc []byte
}

func (v *catalogValidator) plain(msg string) *catalogJSONError {
	return &catalogJSONError{msg: msg, index: -1, doc: v.doc}
}

func catalogUnexpected(node *catalogNode) string {
	switch node.kind {
	case catalogNull:
		return "null"
	case catalogBool:
		if node.boolVal {
			return "boolean `true`"
		}
		return "boolean `false`"
	case catalogUint:
		return fmt.Sprintf("integer `%d`", node.uintVal)
	case catalogInt:
		return fmt.Sprintf("integer `%d`", node.intVal)
	case catalogString:
		return "string " + catalogRustQuote(node.str)
	case catalogArray:
		return "sequence"
	}
	return "map"
}

func (v *catalogValidator) invalidType(node *catalogNode, expected string) *catalogJSONError {
	return v.plain("invalid type: " + catalogUnexpected(node) + ", expected " + expected)
}

func catalogExpectedList(variants []string) string {
	switch len(variants) {
	case 0:
		return ""
	case 1:
		return "`" + variants[0] + "`"
	case 2:
		return "`" + variants[0] + "` or `" + variants[1] + "`"
	}
	quoted := make([]string, len(variants))
	for i, variant := range variants {
		quoted[i] = "`" + variant + "`"
	}
	return "one of " + strings.Join(quoted, ", ")
}

func catalogExpectation(spec *catalogFieldSpec) string {
	switch spec.kind {
	case catalogTypeString:
		return "a string"
	case catalogTypeBool:
		return "a boolean"
	case catalogTypeI32:
		return "i32"
	case catalogTypeI64:
		return "i64"
	case catalogTypeEnum:
		return "string or map"
	case catalogTypeStruct:
		return "struct " + spec.structTy.name
	case catalogTypeSeq:
		return "a sequence"
	}
	return "a value"
}

func (v *catalogValidator) checkValue(node *catalogNode, spec *catalogFieldSpec) *catalogJSONError {
	if node.kind == catalogNull {
		if spec.optional || spec.kind == catalogTypeAny {
			return nil
		}
		return v.invalidType(node, catalogExpectation(spec))
	}
	switch spec.kind {
	case catalogTypeAny:
		return nil
	case catalogTypeString:
		if node.kind != catalogString {
			return v.invalidType(node, "a string")
		}
		return nil
	case catalogTypeBool:
		if node.kind != catalogBool {
			return v.invalidType(node, "a boolean")
		}
		return nil
	case catalogTypeI32:
		return v.checkInt(node, "i32", -1<<31, 1<<31-1)
	case catalogTypeI64:
		return v.checkInt(node, "i64", -1<<63, 1<<63-1)
	case catalogTypeEnum:
		return v.checkEnum(node, spec)
	case catalogTypeStruct:
		return v.checkStruct(node, spec.structTy)
	case catalogTypeSeq:
		if node.kind != catalogArray {
			return v.invalidType(node, "a sequence")
		}
		for _, item := range node.items {
			if err := v.checkValue(item, spec.elem); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func (v *catalogValidator) checkInt(node *catalogNode, name string, min, max int64) *catalogJSONError {
	switch node.kind {
	case catalogUint:
		if node.uintVal <= uint64(max) {
			return nil
		}
		return v.plain("invalid value: integer `" + strconv.FormatUint(node.uintVal, 10) + "`, expected " + name)
	case catalogInt:
		if node.intVal >= min {
			return nil
		}
		return v.plain("invalid value: integer `" + strconv.FormatInt(node.intVal, 10) + "`, expected " + name)
	}
	return v.invalidType(node, name)
}

func (v *catalogValidator) checkEnum(node *catalogNode, spec *catalogFieldSpec) *catalogJSONError {
	switch node.kind {
	case catalogString:
		for _, variant := range spec.variants {
			if node.str == variant {
				return nil
			}
		}
		return v.plain("unknown variant `" + node.str + "`, expected " + catalogExpectedList(spec.variants))
	case catalogObject:
		if len(node.members) != 1 {
			return v.plain("invalid value: map, expected map with a single key")
		}
		member := node.members[0]
		known := false
		for _, variant := range spec.variants {
			if member.key == variant {
				known = true
				break
			}
		}
		if !known {
			return v.plain("unknown variant `" + member.key + "`, expected " + catalogExpectedList(spec.variants))
		}
		if member.value.kind != catalogNull {
			return v.invalidType(member.value, "unit")
		}
		return nil
	case catalogFloat:
		return v.plain("unknown variant `$serde_json::private::Number`, expected " + catalogExpectedList(spec.variants))
	}
	return v.invalidType(node, "string or map")
}

func (v *catalogValidator) checkStruct(node *catalogNode, spec *catalogStructSpec) *catalogJSONError {
	if node.kind == catalogFloat {
		return v.checkMissingFields(spec, nil)
	}
	if node.kind == catalogArray {
		return v.checkStructSeq(node, spec)
	}
	if node.kind != catalogObject {
		return v.invalidType(node, "struct "+spec.name)
	}
	seen := make(map[string]bool, len(spec.fields))
	for _, member := range node.members {
		var field *catalogFieldSpec
		for i := range spec.fields {
			if spec.fields[i].name == member.key {
				field = &spec.fields[i]
				break
			}
		}
		if field == nil {
			continue
		}
		if seen[field.name] {
			return v.plain("duplicate field `" + field.name + "`")
		}
		seen[field.name] = true
		if err := v.checkValue(member.value, field); err != nil {
			return err
		}
	}
	return v.checkMissingFields(spec, seen)
}

// checkStructSeq mirrors serde's ability to build a struct from a JSON
// sequence: fields are positional and a missing required slot reports the
// element count that was supplied.
func (v *catalogValidator) checkStructSeq(node *catalogNode, spec *catalogStructSpec) *catalogJSONError {
	for i := range spec.fields {
		field := &spec.fields[i]
		if i >= len(node.items) {
			if field.optional || field.hasDefault {
				continue
			}
			return v.plain(fmt.Sprintf("invalid length %d, expected struct %s with %d element%s",
				i, spec.name, len(spec.fields), catalogPlural(len(spec.fields))))
		}
		if err := v.checkValue(node.items[i], field); err != nil {
			return err
		}
	}
	if len(node.items) > len(spec.fields) {
		return v.plain(fmt.Sprintf("invalid length %d, expected %d element%s in sequence",
			len(node.items), len(spec.fields), catalogPlural(len(spec.fields))))
	}
	return nil
}

func catalogPlural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (v *catalogValidator) checkMissingFields(spec *catalogStructSpec, seen map[string]bool) *catalogJSONError {
	for i := range spec.fields {
		field := &spec.fields[i]
		if field.optional || field.hasDefault {
			continue
		}
		if seen != nil && seen[field.name] {
			continue
		}
		return v.plain("missing field `" + field.name + "`")
	}
	return nil
}

func catalogSimpleStruct(name string) *catalogStructSpec {
	return &catalogStructSpec{name: name}
}

var (
	catalogTruncationModeVariants = []string{"bytes", "tokens"}
	catalogVisibilityVariants     = []string{"list", "hide", "none"}
	catalogShellTypeVariants      = []string{"default", "local", "shell_command", "unified_exec", "disabled"}
	catalogSummaryVariants        = []string{"auto", "concise", "detailed", "none"}
	catalogVerbosityVariants      = []string{"low", "medium", "high"}
	catalogModalityVariants       = []string{"text", "image", "audio"}
	catalogWebSearchVariants      = []string{"text", "text_and_image"}
	catalogApplyPatchVariants     = []string{"freeform"}

	catalogTruncationPolicySpec = &catalogStructSpec{
		name: "TruncationPolicyConfig",
		fields: []catalogFieldSpec{
			{name: "mode", kind: catalogTypeEnum, variants: catalogTruncationModeVariants},
			{name: "limit", kind: catalogTypeI64},
		},
	}
	catalogReasoningEffortPresetSpec = &catalogStructSpec{
		name: "ReasoningEffortPreset",
		fields: []catalogFieldSpec{
			{name: "effort", kind: catalogTypeString},
			{name: "description", kind: catalogTypeString},
		},
	}
	catalogAvailabilityNuxSpec = &catalogStructSpec{
		name: "ModelAvailabilityNux",
		fields: []catalogFieldSpec{
			{name: "message", kind: catalogTypeString},
		},
	}
	catalogTokenBudgetSpec = &catalogStructSpec{
		name: "ModelTokenBudgetConfig",
		fields: []catalogFieldSpec{
			{name: "enabled", kind: catalogTypeBool, hasDefault: true},
			{name: "use_history_notes_extension", kind: catalogTypeBool, hasDefault: true},
			{name: "reminder_threshold_tokens", kind: catalogTypeI64},
			{name: "reminder_message_template", kind: catalogTypeString},
			{name: "guidance_message", kind: catalogTypeString},
			{name: "auto_compact_fallback_prompt", kind: catalogTypeString},
			{name: "auto_compact_fallback_buffer_tokens", kind: catalogTypeI64},
		},
	}
	catalogConfirmationPoliciesSpec = &catalogStructSpec{
		name: "ConfirmationPolicies",
		fields: []catalogFieldSpec{
			{name: "browser_use", kind: catalogTypeString, optional: true},
			{name: "computer_use", kind: catalogTypeString, optional: true},
		},
	}
	catalogAccessProgramsSpec = &catalogStructSpec{
		name: "ModelAccessPrograms",
		fields: []catalogFieldSpec{
			{name: "cyber", kind: catalogTypeSeq, elem: &catalogFieldSpec{kind: catalogTypeString}},
		},
	}
	catalogServiceTierSpec = &catalogStructSpec{
		name: "ModelServiceTier",
		fields: []catalogFieldSpec{
			{name: "id", kind: catalogTypeString},
			{name: "name", kind: catalogTypeString},
			{name: "description", kind: catalogTypeString},
		},
	}
	catalogModelInfoUpgradeSpec = &catalogStructSpec{
		name: "ModelInfoUpgrade",
		fields: []catalogFieldSpec{
			{name: "model", kind: catalogTypeString},
			{name: "migration_markdown", kind: catalogTypeString},
			{name: "retirement_at", kind: catalogTypeAny, optional: true},
		},
	}
	catalogModelMessagesSpec = &catalogStructSpec{
		name: "ModelMessages",
		fields: []catalogFieldSpec{
			{name: "content_filter_guidance", kind: catalogTypeString, optional: true},
			{name: "persistent_instructions", kind: catalogTypeString, optional: true},
			{name: "tools", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("ToolMessages")},
			{name: "instructions_template", kind: catalogTypeString, optional: true},
			{name: "approvals", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("ApprovalMessages")},
			{name: "collaboration_modes", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("CollaborationModeMessages")},
			{name: "auto_review", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("AutoReviewMessages")},
			{name: "permissions", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("PermissionMessages")},
			{name: "multi_agent", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("MultiAgentMessages")},
			{name: "token_budget", kind: catalogTypeStruct, optional: true, structTy: catalogTokenBudgetSpec},
			{name: "guardian_v2", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("GuardianV2ModelConfig")},
			{name: "confirmation_policies", kind: catalogTypeStruct, optional: true, structTy: catalogConfirmationPoliciesSpec},
		},
	}
	catalogModelInfoSpec = &catalogStructSpec{
		name: "ModelInfo",
		fields: []catalogFieldSpec{
			{name: "guardian", kind: catalogTypeStruct, optional: true, structTy: catalogSimpleStruct("GuardianModelPolicy")},
			{name: "slug", kind: catalogTypeString},
			{name: "display_name", kind: catalogTypeString},
			{name: "description", kind: catalogTypeString, optional: true},
			{name: "default_reasoning_level", kind: catalogTypeString, optional: true},
			{name: "supported_reasoning_levels", kind: catalogTypeSeq, elem: &catalogFieldSpec{kind: catalogTypeStruct, structTy: catalogReasoningEffortPresetSpec}},
			{name: "shell_type", kind: catalogTypeEnum, variants: catalogShellTypeVariants},
			{name: "visibility", kind: catalogTypeEnum, variants: catalogVisibilityVariants},
			{name: "supported_in_api", kind: catalogTypeBool},
			{name: "priority", kind: catalogTypeI32},
			{name: "additional_speed_tiers", kind: catalogTypeSeq, hasDefault: true, elem: &catalogFieldSpec{kind: catalogTypeString}},
			{name: "service_tiers", kind: catalogTypeSeq, hasDefault: true, elem: &catalogFieldSpec{kind: catalogTypeStruct, structTy: catalogServiceTierSpec}},
			{name: "default_service_tier", kind: catalogTypeString, optional: true},
			{name: "available_access_programs", kind: catalogTypeStruct, optional: true, structTy: catalogAccessProgramsSpec},
			{name: "availability_nux", kind: catalogTypeStruct, optional: true, structTy: catalogAvailabilityNuxSpec},
			{name: "upgrade", kind: catalogTypeStruct, optional: true, structTy: catalogModelInfoUpgradeSpec},
			{name: "model_messages", kind: catalogTypeStruct, optional: true, structTy: catalogModelMessagesSpec},
			{name: "include_skills_usage_instructions", kind: catalogTypeBool, hasDefault: true},
			{name: "include_plugin_usage_instructions", kind: catalogTypeBool, hasDefault: true},
			{name: "include_apps_usage_instructions", kind: catalogTypeBool, hasDefault: true},
			{name: "supports_reasoning_summary_parameter", kind: catalogTypeBool, hasDefault: true},
			{name: "default_reasoning_summary", kind: catalogTypeEnum, hasDefault: true, variants: catalogSummaryVariants},
			{name: "support_verbosity", kind: catalogTypeBool},
			{name: "default_verbosity", kind: catalogTypeEnum, optional: true, variants: catalogVerbosityVariants},
			{name: "apply_patch_tool_type", kind: catalogTypeEnum, optional: true, variants: catalogApplyPatchVariants},
			{name: "web_search_tool_type", kind: catalogTypeEnum, hasDefault: true, variants: catalogWebSearchVariants},
			{name: "truncation_policy", kind: catalogTypeStruct, structTy: catalogTruncationPolicySpec},
			{name: "supports_image_detail_original", kind: catalogTypeBool, hasDefault: true},
			{name: "context_window", kind: catalogTypeI64, optional: true},
			{name: "max_context_window", kind: catalogTypeI64, optional: true},
			{name: "auto_compact_token_limit", kind: catalogTypeI64, optional: true},
			{name: "comp_hash", kind: catalogTypeString, optional: true},
			{name: "effective_context_window_percent", kind: catalogTypeI64, hasDefault: true},
			{name: "experimental_supported_tools", kind: catalogTypeSeq, elem: &catalogFieldSpec{kind: catalogTypeString}},
			{name: "input_modalities", kind: catalogTypeSeq, hasDefault: true, elem: &catalogFieldSpec{kind: catalogTypeEnum, variants: catalogModalityVariants}},
			{name: "supports_search_tool", kind: catalogTypeBool, hasDefault: true},
			{name: "supports_experimental_context", kind: catalogTypeBool, hasDefault: true},
			{name: "use_responses_lite", kind: catalogTypeBool, hasDefault: true},
			{name: "supports_reasoning_effort_updates", kind: catalogTypeBool, hasDefault: true},
			{name: "node_repl_auto_review_required", kind: catalogTypeBool, hasDefault: true},
			{name: "node_repl_disabled", kind: catalogTypeBool, hasDefault: true},
			{name: "auto_review_model_override", kind: catalogTypeString, optional: true},
			{name: "model_specialty", kind: catalogTypeString, optional: true},
			{name: "tool_mode", kind: catalogTypeString, optional: true},
			{name: "multi_agent_version", kind: catalogTypeString, optional: true},
			{name: "multi_agent_reasoning_effort", kind: catalogTypeString, optional: true},
		},
	}
)

type catalogModelInfo struct {
	slug               string
	hasInstructions    bool
	instructionsInMsgs bool
}

func (p *catalogJSON) validateModelInfo(node *catalogNode, info *catalogModelInfo) *catalogJSONError {
	validator := &catalogValidator{doc: p.doc}
	if err := validator.checkStruct(node, catalogModelInfoSpec); err != nil {
		return err
	}
	for _, member := range node.members {
		switch member.key {
		case "slug":
			if member.value.kind == catalogString {
				info.slug = member.value.str
			}
		case "model_messages":
			if member.value.kind != catalogObject {
				continue
			}
			for _, nested := range member.value.members {
				if nested.key == "instructions_template" && nested.value.kind == catalogString {
					info.instructionsInMsgs = true
				}
			}
		}
	}
	return nil
}

// validateModelCatalogJSON replays `serde_json::from_str::<ModelsResponse>` over
// the catalog document and returns how many models it declared.
func validateModelCatalogJSON(doc []byte) (int, error) {
	parser := &catalogJSON{doc: doc}
	models, err := parser.parseModelsResponse()
	if err != nil {
		return 0, err
	}
	if err := parser.end(); err != nil {
		return 0, err
	}
	return len(models), nil
}

func (p *catalogJSON) end() error {
	if _, ok := p.skipWhitespace(); ok {
		return p.peekError(serdeTrailingChars)
	}
	return nil
}

func (p *catalogJSON) fixHere(err error) error {
	if problem, ok := err.(*catalogJSONError); ok {
		problem.fixAt(p.idx)
	}
	return err
}

func (p *catalogJSON) fixAfterMap(err error) error {
	return p.fixAfterClose(err, '}')
}

func (p *catalogJSON) fixAfterSeq(err error) error {
	return p.fixAfterClose(err, ']')
}

// fixAfterClose mirrors serde_json's `match (ret, end_map())` / `end_seq()`:
// the container terminator is still consumed when the visitor already failed,
// and that position is what an unpositioned error ends up reporting.
func (p *catalogJSON) fixAfterClose(err error, closer byte) error {
	problem, ok := err.(*catalogJSONError)
	if !ok || problem.index >= 0 {
		return err
	}
	if peek, ok := p.skipWhitespace(); ok && peek == closer {
		p.idx++
	}
	problem.fixAt(p.idx)
	return err
}

func (p *catalogJSON) parseModelsResponse() ([]catalogModelInfo, error) {
	peek, ok := p.skipWhitespace()
	if !ok {
		return nil, p.peekError(serdeEOFValue)
	}
	switch peek {
	case '{':
		p.idx++
		return p.parseModelsResponseMap()
	case '[':
		p.idx++
		return p.parseModelsResponseSeq()
	}
	return nil, p.peekInvalidType("struct ModelsResponse")
}

func (p *catalogJSON) parseModelsResponseMap() ([]catalogModelInfo, error) {
	var models []catalogModelInfo
	sawModels := false
	first := true
	for {
		peek, ok := p.skipWhitespace()
		if !ok {
			return nil, p.fixAfterMap(p.peekError(serdeEOFObject))
		}
		if peek == '}' {
			p.idx++
			if !sawModels {
				return nil, &catalogJSONError{msg: "missing field `models`", index: p.idx, doc: p.doc}
			}
			return models, nil
		}
		if !first {
			if peek != ',' {
				return nil, p.fixAfterMap(p.peekError(serdeExpectedObjComma))
			}
			p.idx++
			next, ok := p.skipWhitespace()
			if !ok {
				return nil, p.fixAfterMap(p.peekError(serdeEOFValue))
			}
			if next == '}' {
				return nil, p.fixAfterMap(p.peekError(serdeTrailingComma))
			}
			if next != '"' {
				return nil, p.fixAfterMap(p.peekError(serdeKeyMustBeString))
			}
		}
		first = false
		peek, ok = p.skipWhitespace()
		if !ok {
			return nil, p.fixAfterMap(p.peekError(serdeEOFObject))
		}
		if peek != '"' {
			return nil, p.fixAfterMap(p.peekError(serdeKeyMustBeString))
		}
		p.idx++
		key, err := p.parseString()
		if err != nil {
			return nil, p.fixAfterMap(err)
		}
		if key == "models" && sawModels {
			return nil, p.at().withMsg("duplicate field `models`")
		}
		if err := p.parseObjectColon(); err != nil {
			return nil, p.fixAfterMap(err)
		}
		if key != "models" {
			if _, err := p.parseAny(); err != nil {
				return nil, p.fixAfterMap(err)
			}
			continue
		}
		sawModels = true
		parsed, listErr := p.parseModelList()
		if listErr != nil {
			return nil, p.fixAfterMap(listErr)
		}
		models = parsed
		if missing := p.missingInstructions(models); missing != nil {
			return nil, p.fixAfterMap(missing)
		}
	}
}

func (p *catalogJSON) missingInstructions(models []catalogModelInfo) error {
	for i := range models {
		if models[i].hasInstructions {
			continue
		}
		return p.plainError("model `" + models[i].slug + "` is missing both `base_instructions` and `model_messages.instructions_template`")
	}
	return nil
}

func (p *catalogJSON) parseModelsResponseSeq() ([]catalogModelInfo, error) {
	peek, ok := p.skipWhitespace()
	if !ok {
		return nil, p.fixAfterSeq(p.peekError(serdeEOFList))
	}
	if peek == ']' {
		p.idx++
		return nil, &catalogJSONError{msg: "invalid length 0, expected struct ModelsResponse with 1 element", index: p.idx, doc: p.doc}
	}
	models, err := p.parseModelList()
	if err != nil {
		return nil, err
	}
	if missing := p.missingInstructions(models); missing != nil {
		return nil, p.fixAfterSeq(missing)
	}
	peek, ok = p.skipWhitespace()
	if !ok {
		return nil, p.peekError(serdeEOFList)
	}
	switch peek {
	case ']':
		p.idx++
		return models, nil
	case ',':
		p.idx++
		if next, ok := p.skipWhitespace(); ok && next == ']' {
			return nil, p.peekError(serdeTrailingComma)
		}
		return nil, p.peekError(serdeTrailingChars)
	}
	return nil, p.peekError(serdeTrailingChars)
}

func (p *catalogJSON) parseModelList() ([]catalogModelInfo, error) {
	peek, ok := p.skipWhitespace()
	if !ok {
		return nil, p.peekError(serdeEOFValue)
	}
	if peek != '[' {
		return nil, p.peekInvalidType("a sequence")
	}
	p.idx++
	var models []catalogModelInfo
	first := true
	for {
		peek, ok := p.skipWhitespace()
		if !ok {
			return nil, p.fixAfterSeq(p.peekError(serdeEOFList))
		}
		if peek == ']' {
			p.idx++
			return models, nil
		}
		if !first {
			if peek != ',' {
				return nil, p.fixAfterSeq(p.peekError(serdeExpectedListComma))
			}
			p.idx++
			next, ok := p.skipWhitespace()
			if !ok {
				return nil, p.fixAfterSeq(p.peekError(serdeEOFValue))
			}
			if next == ']' {
				return nil, p.fixAfterSeq(p.peekError(serdeTrailingComma))
			}
		}
		first = false
		model, err := p.parseModelElement()
		if err != nil {
			return nil, p.fixAfterSeq(err)
		}
		models = append(models, model)
	}
}

func (p *catalogJSON) parseModelElement() (catalogModelInfo, error) {
	peek, ok := p.skipWhitespace()
	if !ok {
		return catalogModelInfo{}, p.peekError(serdeEOFValue)
	}
	if peek != '{' {
		return catalogModelInfo{}, p.peekInvalidType("struct ModelInfoWithLegacyBaseInstructions")
	}
	return p.parseLegacyModelObject()
}

// parseLegacyModelObject mirrors ModelInfoWithLegacyBaseInstructions: the
// deprecated `base_instructions` field is read from the stream (so its errors
// carry the value's own position and a duplicate is reported at the key), while
// every other member is buffered into the flattened ModelInfo and validated
// once the object is complete.
func (p *catalogJSON) parseLegacyModelObject() (catalogModelInfo, error) {
	p.idx++
	var buffered []catalogMember
	var info catalogModelInfo
	sawBase := false
	hasBase := false
	first := true
	for {
		peek, ok := p.skipWhitespace()
		if !ok {
			return info, p.fixAfterMap(p.peekError(serdeEOFObject))
		}
		if peek == '}' {
			p.idx++
			break
		}
		if !first {
			if peek != ',' {
				return info, p.fixAfterMap(p.peekError(serdeExpectedObjComma))
			}
			p.idx++
			next, ok := p.skipWhitespace()
			if !ok {
				return info, p.fixAfterMap(p.peekError(serdeEOFValue))
			}
			if next == '}' {
				return info, p.fixAfterMap(p.peekError(serdeTrailingComma))
			}
			if next != '"' {
				return info, p.fixAfterMap(p.peekError(serdeKeyMustBeString))
			}
		}
		first = false
		peek, ok = p.skipWhitespace()
		if !ok {
			return info, p.fixAfterMap(p.peekError(serdeEOFObject))
		}
		if peek != '"' {
			return info, p.fixAfterMap(p.peekError(serdeKeyMustBeString))
		}
		p.idx++
		key, err := p.parseString()
		if err != nil {
			return info, p.fixAfterMap(err)
		}
		if key == "base_instructions" {
			if sawBase {
				return info, p.fixHere(p.plainError("duplicate field `base_instructions`"))
			}
			sawBase = true
			if err := p.parseObjectColon(); err != nil {
				return info, p.fixAfterMap(err)
			}
			value, ok := p.skipWhitespace()
			if !ok {
				return info, p.fixAfterMap(p.peekError(serdeEOFValue))
			}
			switch {
			case value == '"':
				p.idx++
				text, err := p.parseString()
				if err != nil {
					return info, p.fixAfterMap(err)
				}
				_ = text
				hasBase = true
			case value == 'n':
				p.idx++
				if err := p.parseIdent("ull"); err != nil {
					return info, p.fixAfterMap(err)
				}
			default:
				return info, p.fixAfterMap(p.peekInvalidType("a string"))
			}
			continue
		}
		if err := p.parseObjectColon(); err != nil {
			return info, p.fixAfterMap(err)
		}
		value, err := p.parseAny()
		if err != nil {
			return info, p.fixAfterMap(err)
		}
		buffered = append(buffered, catalogMember{key: key, value: value})
	}
	node := &catalogNode{kind: catalogObject, members: buffered}
	if err := p.validateModelInfo(node, &info); err != nil {
		return info, p.fixHere(err)
	}
	info.hasInstructions = hasBase || info.instructionsInMsgs
	return info, nil
}
