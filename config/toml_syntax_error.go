package config

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Rust's TOML parser (toml_edit through the toml crate) reports syntax failures
// with its own wording and span, so the same document fails with a different
// sentence under go-toml. The tables below translate the messages this build can
// produce; anything unknown keeps go-toml's text and position.

const (
	// tomlSyntaxExpectation is the message toml_edit emits when a bare scalar is
	// found where a value or a key was expected.
	tomlSyntaxExpectation = "string values must be quoted, expected literal string"
	// tomlSyntaxTrailing is the message for junk after a complete value.
	tomlSyntaxTrailing = "unexpected key or value, expected newline, `#`"
	// tomlSyntaxEscapes lists the escapes toml_edit accepts.
	tomlSyntaxEscapes = "missing escaped value, expected `b`, `e`, `f`, `n`, `r`, `\\`, `\"`, `x`, `u`, `U`"
)

// tomlSyntaxDiagnostic is one translated parse failure: the message Rust prints
// plus the 1-based column and caret width of its snippet.
type tomlSyntaxDiagnostic struct {
	Message string
	Column  int
	Caret   int
}

// TOMLDiagnostic renders a TOML syntax failure the way the Rust bootstrap loader
// does: anyhow's numbered cause chain, where the first cause carries the file
// position and the second is the toml crate's parse-error snippet.
func TOMLDiagnostic(path, text string, err error) error {
	line, diagnostic, ok := translateTOMLSyntaxError(text, err)
	if !ok {
		return err
	}
	return errors.New(renderTOMLDiagnostic(path, line, diagnostic, tomlSourceLine(text, line)))
}

// TOMLDiagnosticPosition reports the translated position and message of a TOML
// syntax failure, for callers that print `path:line:column: message` (the
// loader's own ConfigLoadError display) instead of the snippet.
func TOMLDiagnosticPosition(text string, err error) (line, column int, message string, ok bool) {
	line, diagnostic, ok := translateTOMLSyntaxError(text, err)
	if !ok {
		return 0, 0, "", false
	}
	return line, diagnostic.Column, diagnostic.Message, true
}

func translateTOMLSyntaxError(text string, err error) (int, tomlSyntaxDiagnostic, bool) {
	var decodeErr *toml.DecodeError
	if !errors.As(err, &decodeErr) {
		return 0, tomlSyntaxDiagnostic{}, false
	}
	line, column := decodeErr.Position()
	raw := strings.TrimPrefix(decodeErr.Error(), "toml: ")
	return line, translateTOMLSyntaxMessage(raw, text, line, column), true
}

// renderTOMLDiagnostic lays the failure out like anyhow's two-cause chain. The
// snippet lines carry three extra spaces so that, after the caller's four-space
// `Caused by:` indent, they line up under the text that follows `1: `.
func renderTOMLDiagnostic(path string, line int, diagnostic tomlSyntaxDiagnostic, source string) string {
	gutter := len(strconv.Itoa(line))
	bar := strings.Repeat(" ", gutter+1)
	var b strings.Builder
	fmt.Fprintf(&b, "0: %s:%d:%d: %s\n", path, line, diagnostic.Column, diagnostic.Message)
	fmt.Fprintf(&b, "1: TOML parse error at line %d, column %d\n", line, diagnostic.Column)
	fmt.Fprintf(&b, "   %s|\n", bar)
	fmt.Fprintf(&b, "   %*d | %s\n", gutter, line, source)
	fmt.Fprintf(&b, "   %s|%s%s\n", bar, strings.Repeat(" ", diagnostic.Column), strings.Repeat("^", diagnostic.Caret))
	fmt.Fprintf(&b, "   %s", diagnostic.Message)
	return b.String()
}

// TOMLConfigSnippet renders the excerpt Rust prints under `Error loading
// config.toml:`: the file position line followed by the quoted source line and a
// caret, with no trailing message.
func TOMLConfigSnippet(path, text string, err error) (string, bool) {
	line, diagnostic, ok := translateTOMLSyntaxError(text, err)
	if !ok {
		return "", false
	}
	guard := len(strconv.Itoa(line)) + 1
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:%d: %s\n", path, line, diagnostic.Column, diagnostic.Message)
	fmt.Fprintf(&b, "%s|\n", strings.Repeat(" ", guard))
	fmt.Fprintf(&b, "%*d | %s\n", len(strconv.Itoa(line)), line, tomlSourceLine(text, line))
	fmt.Fprintf(&b, "%s|%s%s", strings.Repeat(" ", guard), strings.Repeat(" ", diagnostic.Column), strings.Repeat("^", diagnostic.Caret))
	return b.String(), true
}

func tomlSourceLine(text string, line int) string {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimSuffix(lines[line-1], "\r")
}

func translateTOMLSyntaxMessage(raw, text string, line, column int) tomlSyntaxDiagnostic {
	if start, _ := tomlBareRunSpan(tomlSourceLine(text, line), column); tomlRedundantSign(tomlRunText(tomlSourceLine(text, line), start, start+1)) {
		return tomlSyntaxDiagnostic{"redundant numeric sign, expected nothing", start + 1, 1}
	}
	switch {
	case raw == "expected ']' to close table name":
		return tomlSyntaxDiagnostic{"unclosed table, expected `]`", column, 1}
	case raw == "expected ']]' to close array table name":
		return tomlSyntaxDiagnostic{"unclosed array table, expected `]`", column + 1, 1}
	case raw == "array is incomplete":
		return tomlSyntaxDiagnostic{"unclosed array, expected `]`", column, 1}
	case raw == "inline table is incomplete":
		return tomlSyntaxDiagnostic{"unclosed inline table, expected `}`", column, 1}
	case raw == "expected '=' after key":
		// toml_edit reports the unfinished inline table, not the missing `=`.
		if tomlOpenInlineTable(tomlSourceLine(text, line), column) {
			return tomlSyntaxDiagnostic{"unclosed inline table, expected `}`", column, 1}
		}
		return tomlSyntaxDiagnostic{"key with no value, expected `=`", column, 1}
	case raw == "basic strings cannot have new lines":
		return tomlSyntaxDiagnostic{"invalid basic string, expected `\"`", column, 1}
	case raw == "literal strings cannot have new lines":
		return tomlSyntaxDiagnostic{"invalid literal string, expected `'`", column, 1}
	case raw == "multiline basic string not terminated by \"\"\"":
		return tomlSyntaxDiagnostic{"invalid multi-line basic string, expected `\"`", column + 1, 1}
	case raw == "multiline literal string not terminated by '''":
		return tomlSyntaxDiagnostic{"invalid multi-line literal string, expected `'`", column + 1, 1}
	case raw == "decimal point must be followed by a digit":
		return tomlSyntaxDiagnostic{"invalid fraction, expected digits", column, 1}
	case raw == "exponent must contain at least one digit":
		return tomlSyntaxDiagnostic{"invalid exponent, expected digits", column, 1}
	case raw == "number must have at least one digit between underscores":
		return tomlSyntaxDiagnostic{"`_` may only go between digits, expected nothing", column, 1}
	case raw == "integers cannot have leading zeroes":
		return tomlSyntaxDiagnostic{"unexpected leading zero, expected nothing", column, 1}
	case raw == "sign is not allowed on numbers with a radix prefix":
		return tomlSyntaxDiagnostic{"integers with a radix cannot be signed, expected nothing", column, 1}
	case raw == "expected value but got U+002C ','":
		return tomlSyntaxDiagnostic{"extra comma in array, expected value", column, 1}
	case raw == "unexpected comma in inline table":
		return tomlSyntaxDiagnostic{"extra comma in inline table, expected key", column, 1}
	case raw == "escape sequence is not a valid unicode code point":
		return tomlSyntaxDiagnostic{"invalid value, expected unicode hexadecimal value", column + 2, 1}
	case raw == "invalid hexadecimal digit in unicode escape sequence":
		return tomlSyntaxDiagnostic{"too few unicode value digits, expected unicode hexadecimal value", column, 1}
	case strings.HasPrefix(raw, "invalid character at start of key"):
		return tomlSyntaxDiagnostic{"unquoted keys cannot be empty, expected letters, numbers, `-`, `_`", column, 1}
	case strings.HasPrefix(raw, "invalid escape character "):
		return tomlSyntaxDiagnostic{tomlSyntaxEscapes, column + 1, 1}
	case strings.HasPrefix(raw, "hour cannot be greater"):
		return tomlValueSpanDiagnostic("invalid time, expected hour between 00 and 23", text, line, column)
	case strings.HasPrefix(raw, "minutes cannot be greater"):
		return tomlValueSpanDiagnostic("invalid time, expected minute between 00 and 59", text, line, column)
	case strings.HasPrefix(raw, "seconds cannot be greater"):
		return tomlValueSpanDiagnostic("invalid time, expected second between 00 and 60", text, line, column)
	case raw == "impossible date":
		source := tomlSourceLine(text, line)
		start, end := tomlBareRunSpan(source, column)
		return tomlSyntaxDiagnostic{tomlDateExpectation(tomlRunText(source, start, end)), start, end - start + 1}
	case strings.HasPrefix(raw, "radix prefix must be followed by at least one digit"):
		source := tomlSourceLine(text, line)
		start, end := tomlBareRunSpan(source, column)
		token := tomlRunText(source, start, end)
		if len(token) <= 2 {
			return tomlSyntaxDiagnostic{"integer number overflowed", start, end - start + 1}
		}
		if token[2] == '_' {
			return tomlSyntaxDiagnostic{"`_` may only go between digits, expected nothing", start + 2, 1}
		}
		return tomlSyntaxDiagnostic{"invalid " + tomlRadixName(token) + " number", start + 2, 1}
	case strings.HasSuffix(raw, "number is too large to fit in a 64-bit signed integer"):
		source := tomlSourceLine(text, line)
		start, end := tomlBareRunSpan(source, column)
		token := tomlRunText(source, start, end)
		decimal := token
		if value, ok := new(big.Int).SetString(token, 0); ok {
			decimal = value.String()
		}
		message := fmt.Sprintf("invalid type: integer `%s` as i128, expected any valid TOML value", decimal)
		return tomlSyntaxDiagnostic{message, start, end - start + 1}
	case strings.HasPrefix(raw, "unable to parse float"):
		return tomlValueSpanDiagnostic("floating-point number overflowed", text, line, column)
	case raw == "dates are expected to have the format YYYY-MM-DD":
		source := tomlSourceLine(text, line)
		start, end := tomlBareRunSpan(source, column)
		return tomlSyntaxDiagnostic{tomlDateFormatExpectation(tomlRunText(source, start, end)), start, end - start + 1}
	case strings.HasPrefix(raw, "times are expected to have the format"):
		source := tomlSourceLine(text, line)
		start, end := tomlBareRunSpan(source, column)
		return tomlSyntaxDiagnostic{tomlTimeFormatExpectation(tomlRunText(source, start, end)), start, end - start + 1}
	case raw == "incomplete seconds":
		return tomlValueSpanDiagnostic("invalid time, expected second", text, line, column)
	case raw == "unicode escape sequence is too short":
		source := tomlSourceLine(text, line)
		return tomlSyntaxDiagnostic{"too few unicode value digits, expected unicode hexadecimal value", column + 2 + tomlHexDigits(source, column), 1}
	case strings.HasPrefix(raw, "unexpected character ") && strings.HasSuffix(raw, " at start of value"):
		switch tomlErrorChar(raw) {
		case '.':
			return tomlSyntaxDiagnostic{"invalid mantissa, expected digits", column, 1}
		case '}':
			return tomlSyntaxDiagnostic{"invalid literal string, expected `'`", column, 1}
		}
		return tomlSyntaxDiagnostic{tomlSyntaxExpectation, column, 1}
	case strings.HasPrefix(raw, "expected digit but got"):
		return tomlValueSpanDiagnostic(tomlSyntaxExpectation, text, line, column)
	case strings.HasPrefix(raw, "expected newline but got"):
		return tomlTrailingValueDiagnostic(raw, text, line, column)
	case strings.HasPrefix(raw, "expected ',' or ']' after array value"),
		strings.HasPrefix(raw, "expected ',' or '}' after inline table key-value"):
		return tomlValueSpanDiagnostic(tomlSyntaxExpectation, text, line, column)
	case strings.HasPrefix(raw, "key ") && strings.HasSuffix(raw, " is already defined"):
		key := strings.TrimSuffix(strings.TrimPrefix(raw, "key "), " is already defined")
		return tomlSyntaxDiagnostic{"duplicate key", tomlKeyColumn(tomlSourceLine(text, line), key, column), 1}
	case strings.HasPrefix(raw, "key ") && strings.HasSuffix(raw, " should be a table, not a value"):
		key := strings.TrimSuffix(strings.TrimPrefix(raw, "key "), " should be a table, not a value")
		return tomlSyntaxDiagnostic{"duplicate key", tomlKeyColumn(tomlSourceLine(text, line), key, column), 1}
	case strings.HasPrefix(raw, "key ") && strings.HasSuffix(raw, " already exists as a value"):
		key := strings.TrimSuffix(strings.TrimPrefix(raw, "key "), " already exists as a value")
		valueType := tomlDeclaredValueType(text, key)
		return tomlSyntaxDiagnostic{fmt.Sprintf("cannot extend value of type %s with a dotted key", valueType), column, 1}
	case strings.HasPrefix(raw, "table ") && strings.HasSuffix(raw, " already exists as defined by a dotted key"):
		key := strings.TrimSuffix(strings.TrimPrefix(raw, "table "), " already exists as defined by a dotted key")
		return tomlSyntaxDiagnostic{"duplicate key", tomlKeyColumn(tomlSourceLine(text, line), key, column), 1}
	case strings.HasPrefix(raw, "table ") && strings.HasSuffix(raw, " already exists as an array of tables"):
		return tomlSyntaxDiagnostic{"duplicate key", column, 1}
	case strings.HasPrefix(raw, "table ") && strings.HasSuffix(raw, " already exists"):
		return tomlSyntaxDiagnostic{"duplicate key", column, 1}
	}
	return tomlSyntaxDiagnostic{raw, column, 1}
}

// tomlTrailingValueDiagnostic classifies the junk a complete value is followed
// by: toml_edit reports a folded bare value, a second key/value pair, or plain
// trailing punctuation, each with a different message and span.
func tomlTrailingValueDiagnostic(raw, text string, line, column int) tomlSyntaxDiagnostic {
	source := tomlSourceLine(text, line)
	character := tomlErrorChar(raw)
	if character == '.' {
		return tomlSyntaxDiagnostic{"invalid float, expected nothing", column, 2}
	}
	if !tomlBareChar(character) {
		return tomlSyntaxDiagnostic{tomlSyntaxTrailing, column, 1}
	}
	start, end := tomlBareRunSpan(source, column)
	run := tomlRunText(source, start, end)
	if tomlLooksLikeDatetime(run) {
		return tomlSyntaxDiagnostic{"invalid datetime", start, end - start + 1}
	}
	if tomlRunAfterQuote(source, start) {
		return tomlSyntaxDiagnostic{tomlSyntaxTrailing, column, 1}
	}
	if equals := strings.IndexByte(source[column-1:], '='); equals >= 0 {
		return tomlSyntaxDiagnostic{tomlSyntaxTrailing, column + equals, 1}
	}
	return tomlSyntaxDiagnostic{tomlSyntaxExpectation, start, end - start + 1}
}

func tomlValueSpanDiagnostic(message, text string, line, column int) tomlSyntaxDiagnostic {
	start, end := tomlBareRunSpan(tomlSourceLine(text, line), column)
	return tomlSyntaxDiagnostic{message, start, end - start + 1}
}

// tomlErrorChar returns the character a go-toml message quotes as U+XXXX.
func tomlErrorChar(raw string) rune {
	index := strings.Index(raw, "U+")
	if index < 0 || index+6 > len(raw) {
		return 0
	}
	value, err := strconv.ParseUint(raw[index+2:index+6], 16, 32)
	if err != nil {
		return 0
	}
	return rune(value)
}

// tomlBareRunSpan widens a go-toml position to the bare value run toml_edit
// reports the span of: the scalar characters plus the single spaces that join
// them, stopping at a delimiter or `=`.
func tomlBareRunSpan(source string, column int) (int, int) {
	start := column
	if start > len(source)+1 {
		start = len(source) + 1
	}
	for start > 1 && start-2 < len(source) {
		current := source[start-2]
		if tomlBareByte(current) {
			start--
			continue
		}
		if (current == ' ' || current == '\t') && start >= 3 && tomlBareByte(source[start-3]) {
			start--
			continue
		}
		break
	}
	return start, tomlBareRunEnd(source, start)
}

func tomlBareRunEnd(source string, start int) int {
	end := start
	for end < len(source) {
		if tomlBareByte(source[end]) {
			end++
			continue
		}
		if (source[end] == ' ' || source[end] == '\t') && end+1 < len(source) && tomlBareByte(source[end+1]) {
			end++
			continue
		}
		break
	}
	return end
}

func tomlBareByte(character byte) bool {
	switch {
	case character >= '0' && character <= '9':
		return true
	case character >= 'a' && character <= 'z':
		return true
	case character >= 'A' && character <= 'Z':
		return true
	}
	switch character {
	case '_', '-', '+', '.', ':':
		return true
	}
	return false
}

func tomlBareChar(character rune) bool {
	if character > 127 {
		return true
	}
	return tomlBareByte(byte(character))
}

func tomlRunText(source string, start, end int) string {
	if start < 1 || end < start || start > len(source) {
		return ""
	}
	if end > len(source) {
		end = len(source)
	}
	return source[start-1 : end]
}

// tomlValueText returns everything after the first `=` of the line.
// tomlRunAfterQuote reports whether the run starting at start follows a closing
// quote, which makes the junk unsupported rather than an unquoted value.
func tomlRunAfterQuote(source string, start int) bool {
	for index := start - 2; index >= 0; index-- {
		switch source[index] {
		case ' ', '\t':
			continue
		case '"', '\'':
			return true
		}
		return false
	}
	return false
}

// tomlHexDigits counts the hexadecimal digits of the unicode escape starting at
// the backslash go-toml reported.
func tomlHexDigits(source string, column int) int {
	digits := 0
	for index := column + 1; index < len(source); index++ {
		character := source[index]
		switch {
		case character >= '0' && character <= '9',
			character >= 'a' && character <= 'f',
			character >= 'A' && character <= 'F':
			digits++
			continue
		}
		break
	}
	return digits
}

// tomlDateFormatExpectation names the component a truncated date is missing.
func tomlDateFormatExpectation(run string) string {
	switch {
	case run == "":
		return "invalid date"
	case len(run) == 5 && run[4] == '-':
		return "invalid date, expected month"
	case len(run) == 7:
		return "invalid date, expected `-` (MM-DD)"
	case len(run) == 8 && run[7] == '-':
		return "invalid date, expected day"
	case len(run) >= 6 && run[5] == '-' && len(run) > 7 && run[6] != '-':
		return "invalid date, expected a two-digit month (MM)"
	}
	return "invalid date"
}

// tomlTimeFormatExpectation names the component a truncated time is missing.
func tomlTimeFormatExpectation(run string) string {
	switch {
	case run == "":
		return "invalid time"
	case strings.HasSuffix(run, "T"):
		return "invalid time, expected hour"
	case strings.HasSuffix(run, ":"):
		return "invalid time, expected minute"
	case len(run) >= 2 && run[len(run)-3] == 'T':
		return "invalid time, expected `:` (HH:MM)"
	}
	return "invalid time"
}

// tomlOpenInlineTable reports whether the text before column opened an inline
// table that is still unclosed on this line.
func tomlOpenInlineTable(source string, column int) bool {
	open, closed := 0, 0
	for index := 0; index < len(source) && index < column; index++ {
		switch source[index] {
		case '{':
			open++
		case '}':
			closed++
		}
	}
	return open > closed
}

// tomlRedundantSign reports a value that starts with two signs, which toml_edit
// rejects before it tries to parse a number.
func tomlRedundantSign(prefix string) bool {
	return len(prefix) == 2 && (prefix[0] == '+' || prefix[0] == '-') && (prefix[1] == '+' || prefix[1] == '-')
}

func tomlValueText(source string) string {
	if equals := strings.IndexByte(source, '='); equals >= 0 {
		return source[equals+1:]
	}
	return ""
}

func tomlLooksLikeDatetime(run string) bool {
	if len(run) < 10 {
		return false
	}
	pattern := "dddd-dd-dd"
	for index := 0; index < len(pattern); index++ {
		character := run[index]
		if pattern[index] == 'd' {
			if character < '0' || character > '9' {
				return false
			}
			continue
		}
		if character != '-' {
			return false
		}
	}
	return true
}

// tomlDateExpectation names the out-of-range component of an impossible date.
func tomlDateExpectation(run string) string {
	const monthExpectation = "invalid date, expected month between 01 and 12"
	if len(run) < 10 {
		return "invalid date"
	}
	year, yearErr := strconv.Atoi(run[0:4])
	month, monthErr := strconv.Atoi(run[5:7])
	day, dayErr := strconv.Atoi(run[8:10])
	if yearErr != nil || monthErr != nil || dayErr != nil {
		return "invalid date"
	}
	if month < 1 || month > 12 {
		return monthExpectation
	}
	last := tomlLastDayOfMonth(year, month)
	if day < 1 || day > last {
		return fmt.Sprintf("invalid date, expected day between 01 and %02d", last)
	}
	return "invalid date"
}

func tomlLastDayOfMonth(year, month int) int {
	switch month {
	case 2:
		if (year%4 == 0 && year%100 != 0) || year%400 == 0 {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

func tomlRadixName(token string) string {
	if len(token) < 2 || token[0] != '0' {
		return "hexadecimal"
	}
	switch token[1] {
	case 'b':
		return "binary"
	case 'o':
		return "octal"
	}
	return "hexadecimal"
}

// tomlKeyColumn points at the last segment of the key inside the offending
// line, which is where toml_edit reports duplicate and shadowing keys.
func tomlKeyColumn(source, key string, fallback int) int {
	if source == "" || key == "" {
		return fallback
	}
	for index := len(source) - len(key); index >= 0; index-- {
		if source[index:index+len(key)] != key {
			continue
		}
		if index > 0 && tomlKeyByte(source[index-1]) {
			continue
		}
		if index+len(key) < len(source) && tomlKeyByte(source[index+len(key)]) {
			continue
		}
		return index + 1
	}
	return fallback
}

func tomlKeyByte(character byte) bool {
	switch {
	case character >= '0' && character <= '9':
		return true
	case character >= 'a' && character <= 'z':
		return true
	case character >= 'A' && character <= 'Z':
		return true
	}
	return character == '_' || character == '-'
}

// tomlDeclaredValueType infers the TOML type of the value already assigned to
// key, which toml_edit names when a dotted key tries to extend it.
func tomlDeclaredValueType(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		equals := strings.IndexByte(line, '=')
		if equals < 0 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(line[:equals]), "\"'")
		if name != key {
			continue
		}
		value := strings.TrimSpace(line[equals+1:])
		if value == "" {
			continue
		}
		switch {
		case strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'"):
			return "string"
		case strings.HasPrefix(value, "["):
			return "array"
		case strings.HasPrefix(value, "{"):
			return "inline table"
		case strings.HasPrefix(value, "true") || strings.HasPrefix(value, "false"):
			return "boolean"
		case strings.ContainsAny(value, ".eE") && strings.ContainsAny(value, "0123456789"):
			return "float"
		case strings.ContainsAny(value, "0123456789-+"):
			return "integer"
		}
		return "string"
	}
	return "value"
}
