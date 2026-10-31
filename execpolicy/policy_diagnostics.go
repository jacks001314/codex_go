package execpolicy

import (
	"fmt"
	"strconv"
	"strings"
)

// This file ports the slice of starlark_rst's front end that a policy file
// exposes: statement and argument boundaries with byte spans, the builtin
// signature checks, and the `error: ...` code frames 0.166 prints when a policy
// fails to load. The evaluator itself still works on raw text (execpolicy.go);
// this front end only decides where an error sits and how it reads.

type policyDiagnosticKind int

const (
	// A parse error never compiled, so it prints without a traceback.
	policyParseDiagnostic policyDiagnosticKind = iota
	// A scope/analysis error (repeated or misplaced arguments) also prints
	// just the code frame.
	policyAnalysisDiagnostic
	// A runtime error raised inside a builtin adds the Starlark traceback.
	policyRuntimeDiagnostic
)

type policySpan struct {
	start int
	end   int
}

type policyDiagnostic struct {
	src     *policySource
	kind    policyDiagnosticKind
	message string
	span    policySpan
	// callLine and callText feed the traceback of a runtime error.
	callLine int
	callText string
	// emptyFrame marks starlark_rst's zero-length span: the arrow still
	// prints, but there is no source line to underline.
	emptyFrame bool
}

func newPolicyDiagnostic(src *policySource, kind policyDiagnosticKind, message string, span policySpan) *policyDiagnostic {
	return &policyDiagnostic{src: src, kind: kind, message: message, span: span}
}

func newPolicyRuntimeDiagnostic(src *policySource, call *policyFrontCall, message string) *policyDiagnostic {
	diag := newPolicyDiagnostic(src, policyRuntimeDiagnostic, message, call.span)
	diag.callLine = call.line
	diag.callText = src.lineText(call.line)
	return diag
}

// Error renders the diagnostic exactly as starlark_rst does, frame included.
func (d *policyDiagnostic) Error() string {
	var b strings.Builder
	if d.kind == policyRuntimeDiagnostic {
		b.WriteString("Traceback (most recent call last):\n")
		fmt.Fprintf(&b, "  * %s:%d, in <module>\n", d.src.identifier, d.callLine)
		b.WriteString("      " + strings.TrimRight(d.callText, " \t") + "\n")
	}
	b.WriteString("error: " + d.message + "\n")
	if d.emptyFrame {
		fmt.Fprintf(&b, "--> %s:1:1\n", d.src.identifier)
		b.WriteString(" |\n |\n")
		return b.String()
	}
	startLine, startColumn := d.src.position(d.span.start)
	endLine, endColumn := d.src.position(d.span.end)
	digits := len(strconv.Itoa(startLine))
	gutter := strings.Repeat(" ", digits+1) + "|"
	fmt.Fprintf(&b, "%s--> %s:%d:%d\n", strings.Repeat(" ", digits), d.src.identifier, startLine, startColumn)
	b.WriteString(gutter + "\n")
	if startLine == endLine {
		fmt.Fprintf(&b, "%d | %s\n", startLine, d.src.lineText(startLine))
		carets := d.span.end - d.span.start
		if carets < 1 {
			carets = 1
		}
		b.WriteString(gutter + " " + strings.Repeat(" ", startColumn-1) + strings.Repeat("^", carets) + "\n")
		b.WriteString(gutter + "\n")
		return b.String()
	}
	for line := startLine; line <= endLine; line++ {
		text := d.src.lineText(line)
		switch {
		case line == startLine:
			prefix := ""
			if startColumn > 1 && startColumn-1 <= len(text) {
				prefix = text[:startColumn-1]
				text = text[startColumn-1:]
			}
			fmt.Fprintf(&b, "%d | %s/ %s\n", line, prefix, text)
		case line == endLine:
			limit := endColumn - 1
			if limit > len(text) {
				limit = len(text)
			}
			fmt.Fprintf(&b, "%d | | %s\n", line, text[:limit])
		default:
			fmt.Fprintf(&b, "%d | | %s\n", line, text)
		}
	}
	b.WriteString(gutter + " |" + strings.Repeat("_", endColumn-1) + "^\n")
	b.WriteString(gutter + "\n")
	return b.String()
}

// policySource maps byte offsets back to the 1-based line/column pairs the
// frames print.
type policySource struct {
	identifier string
	text       string
	lineStarts []int
}

func newPolicySource(identifier, text string) *policySource {
	src := &policySource{identifier: identifier, text: text, lineStarts: []int{0}}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			src.lineStarts = append(src.lineStarts, i+1)
		}
	}
	return src
}

func (s *policySource) position(offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s.text) {
		offset = len(s.text)
	}
	low, high := 0, len(s.lineStarts)-1
	for low < high {
		mid := (low + high + 1) / 2
		if s.lineStarts[mid] <= offset {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return low + 1, offset - s.lineStarts[low] + 1
}

func (s *policySource) lineText(line int) string {
	if line < 1 || line > len(s.lineStarts) {
		return ""
	}
	start := s.lineStarts[line-1]
	end := len(s.text)
	if line < len(s.lineStarts) {
		end = s.lineStarts[line] - 1
	}
	return strings.TrimRight(s.text[start:end], "\r")
}

type policyTokenKind int

const (
	policyTokenEOF policyTokenKind = iota
	policyTokenNewline
	policyTokenIdentifier
	policyTokenString
	policyTokenNumber
	policyTokenPunct
)

type policyToken struct {
	kind       policyTokenKind
	text       string
	start, end int
}

func lexPolicySource(text string) []policyToken {
	tokens := make([]policyToken, 0, 16)
	for i := 0; i < len(text); {
		switch ch := text[i]; {
		case ch == ' ' || ch == '\t' || ch == '\r':
			i++
		case ch == '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case ch == '\n':
			tokens = append(tokens, policyToken{kind: policyTokenNewline, start: i, end: i + 1})
			i++
		case ch == '"' || ch == '\'':
			end := scanPolicyStringToken(text, i)
			tokens = append(tokens, policyToken{kind: policyTokenString, text: text[i:end], start: i, end: end})
			i = end
		case isPolicyIdentifierStart(ch):
			start := i
			i++
			for i < len(text) && isPolicyIdentifierPart(text[i]) {
				i++
			}
			tokens = append(tokens, policyToken{kind: policyTokenIdentifier, text: text[start:i], start: start, end: i})
		case ch >= '0' && ch <= '9':
			start := i
			for i < len(text) && (text[i] >= '0' && text[i] <= '9' || text[i] == '.') {
				i++
			}
			tokens = append(tokens, policyToken{kind: policyTokenNumber, text: text[start:i], start: start, end: i})
		default:
			tokens = append(tokens, policyToken{kind: policyTokenPunct, text: string(ch), start: i, end: i + 1})
			i++
		}
	}
	return append(tokens, policyToken{kind: policyTokenEOF, start: len(text), end: len(text)})
}

// scanPolicyStringToken returns the offset just past a string literal. An
// unterminated literal stops at the end of its line, which is where the
// statement parser reports the failure.
func scanPolicyStringToken(text string, start int) int {
	quote := text[start]
	triple := start+2 < len(text) && text[start+1] == quote && text[start+2] == quote
	i := start + 1
	if triple {
		i = start + 3
	}
	for i < len(text) {
		if text[i] == '\\' && i+1 < len(text) {
			i += 2
			continue
		}
		if text[i] == quote {
			if !triple {
				return i + 1
			}
			if i+2 < len(text) && text[i+1] == quote && text[i+2] == quote {
				return i + 3
			}
		}
		if text[i] == '\n' && !triple {
			return i
		}
		i++
	}
	return len(text)
}

type policyFrontArg struct {
	name       string
	nameSpan   policySpan
	value      string
	span       policySpan
	positional bool
}

type policyFrontCall struct {
	name string
	span policySpan
	line int
	args []policyFrontArg
}

type policyFrontStatement struct {
	assignName  string
	assignValue string
	assignSpan  policySpan
	call        *policyFrontCall
}

// parsePolicyProgram splits a policy file into ordered statements. A failed
// statement returns a parse diagnostic, which is what makes Go reject the
// stray tokens 0.166 rejects.
func parsePolicyProgram(src *policySource) ([]policyFrontStatement, *policyDiagnostic) {
	tokens := lexPolicySource(src.text)
	statements := make([]policyFrontStatement, 0, len(tokens)/8)
	index := 0
	for {
		for tokens[index].kind == policyTokenNewline {
			index++
		}
		if tokens[index].kind == policyTokenEOF {
			return statements, nil
		}
		start := index
		if tokens[index].kind == policyTokenIdentifier && isPolicyAssignmentOperator(tokens, index) {
			name := tokens[index].text
			index += 2
			if tokens[index].kind == policyTokenNewline || tokens[index].kind == policyTokenEOF {
				return nil, newPolicyDiagnostic(src, policyParseDiagnostic,
					"Parse error: unexpected new line, expected expression",
					policySpan{start: tokens[index].start, end: tokens[index].start}).withEmptyFrame()
			}
			valueStart := tokens[index].start
			end, diag := scanPolicyExpression(tokens, index, src)
			if diag != nil {
				return nil, diag
			}
			valueEnd := tokens[end-1].end
			statements = append(statements, policyFrontStatement{
				assignName:  name,
				assignValue: strings.TrimSpace(src.text[valueStart:valueEnd]),
				assignSpan:  policySpan{start: valueStart, end: valueEnd},
			})
			index = end
		} else {
			end, diag := scanPolicyExpression(tokens, index, src)
			if diag != nil {
				return nil, diag
			}
			if call, ok := policyCallFromTokens(tokens[start:end], src); ok {
				statements = append(statements, policyFrontStatement{call: call})
			}
			index = end
		}
		if tokens[index].kind == policyTokenEOF {
			return statements, nil
		}
		if tokens[index].kind != policyTokenNewline {
			return nil, newPolicyDiagnostic(src, policyParseDiagnostic,
				fmt.Sprintf("Parse error: unexpected symbol '%s', expected new line", tokens[index].text),
				policySpan{start: tokens[index].start, end: tokens[index].end})
		}
		index++
	}
}

func isPolicyAssignmentOperator(tokens []policyToken, index int) bool {
	if index+1 >= len(tokens) {
		return false
	}
	next := tokens[index+1]
	if next.kind != policyTokenPunct || next.text != "=" {
		return false
	}
	if index+2 < len(tokens) {
		after := tokens[index+2]
		if after.kind == policyTokenPunct && after.text == "=" {
			return false
		}
	}
	return true
}

// scanPolicyExpression returns the index of the token that terminates the
// expression: a newline or EOF at bracket depth zero, or the first closing
// bracket that has no opener (which the caller reports as a stray token).
func scanPolicyExpression(tokens []policyToken, start int, src *policySource) (int, *policyDiagnostic) {
	var open []string
	for i := start; i < len(tokens); i++ {
		token := tokens[i]
		switch token.kind {
		case policyTokenEOF:
			if len(open) == 0 {
				return i, nil
			}
			return 0, newPolicyDiagnostic(src, policyParseDiagnostic,
				"Parse error: "+policyUnexpectedEndMessage(open[len(open)-1]),
				policySpan{start: len(src.text), end: len(src.text)}).withEmptyFrame()
		case policyTokenNewline:
			if len(open) == 0 {
				return i, nil
			}
		case policyTokenPunct:
			switch token.text {
			case "(", "[", "{":
				open = append(open, token.text)
			case ")", "]", "}":
				if len(open) == 0 {
					return i, nil
				}
				open = open[:len(open)-1]
			}
		}
	}
	return len(tokens) - 1, nil
}

func (d *policyDiagnostic) withEmptyFrame() *policyDiagnostic {
	d.emptyFrame = true
	d.span = policySpan{}
	return d
}

func policyUnexpectedEndMessage(opener string) string {
	switch opener {
	case "[":
		return "unexpected new line, expected symbol ']'"
	case "{":
		return "unexpected new line, expected symbol '}'"
	}
	return "unexpected new line, expected expression"
}

// policyCallFromTokens recognizes `name(arg, ...)` that spans the whole
// statement; anything else is an expression whose value the policy ignores.
func policyCallFromTokens(segment []policyToken, src *policySource) (*policyFrontCall, bool) {
	if len(segment) < 3 {
		return nil, false
	}
	if segment[0].kind != policyTokenIdentifier {
		return nil, false
	}
	if segment[1].kind != policyTokenPunct || segment[1].text != "(" {
		return nil, false
	}
	if segment[len(segment)-1].kind != policyTokenPunct || segment[len(segment)-1].text != ")" {
		return nil, false
	}
	depth := 0
	for i := 1; i < len(segment); i++ {
		token := segment[i]
		if token.kind != policyTokenPunct {
			continue
		}
		switch token.text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 && i != len(segment)-1 {
				return nil, false
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	call := &policyFrontCall{
		name: segment[0].text,
		span: policySpan{start: segment[0].start, end: segment[len(segment)-1].end},
	}
	call.line, _ = src.position(call.span.start)
	arguments, ok := splitPolicyArguments(segment[2:len(segment)-1], src)
	if !ok {
		return nil, false
	}
	call.args = arguments
	return call, true
}

func splitPolicyArguments(tokens []policyToken, src *policySource) ([]policyFrontArg, bool) {
	args := make([]policyFrontArg, 0, len(tokens)/4)
	depth := 0
	segmentStart := 0
	flush := func(end int) bool {
		segment := trimPolicyNewlines(tokens[segmentStart:end])
		segmentStart = end + 1
		if len(segment) == 0 {
			return true
		}
		arg := policyFrontArg{
			value: strings.TrimSpace(src.text[segment[0].start:segment[len(segment)-1].end]),
			span:  policySpan{start: segment[0].start, end: segment[len(segment)-1].end},
		}
		named := len(segment) >= 2 && segment[0].kind == policyTokenIdentifier &&
			segment[1].kind == policyTokenPunct && segment[1].text == "=" &&
			!(len(segment) >= 3 && segment[2].kind == policyTokenPunct && segment[2].text == "=")
		if named {
			valueTokens := trimPolicyNewlines(segment[2:])
			if len(valueTokens) == 0 {
				return false
			}
			arg.name = segment[0].text
			arg.nameSpan = policySpan{start: segment[0].start, end: segment[0].end}
			arg.value = strings.TrimSpace(src.text[valueTokens[0].start:valueTokens[len(valueTokens)-1].end])
			arg.span = policySpan{start: valueTokens[0].start, end: valueTokens[len(valueTokens)-1].end}
		} else {
			arg.positional = true
		}
		if arg.value == "" {
			return false
		}
		args = append(args, arg)
		return true
	}
	for i, token := range tokens {
		if token.kind == policyTokenPunct {
			switch token.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			case ",":
				if depth == 0 {
					if !flush(i) {
						return nil, false
					}
				}
			}
		}
	}
	if !flush(len(tokens)) {
		return nil, false
	}
	return args, true
}

func trimPolicyNewlines(tokens []policyToken) []policyToken {
	for len(tokens) > 0 && tokens[0].kind == policyTokenNewline {
		tokens = tokens[1:]
	}
	for len(tokens) > 0 && tokens[len(tokens)-1].kind == policyTokenNewline {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}
