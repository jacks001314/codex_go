package execpolicy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// policyBuiltinSignature records the parameters a policy builtin accepts, in
// declaration order, so the front end can reproduce Starlark's binder errors.
type policyBuiltinSignature struct {
	parameters []string
	required   []string
}

var policyBuiltinSignatures = map[string]policyBuiltinSignature{
	"prefix_rule": {
		parameters: []string{"pattern", "decision", "match", "not_match", "justification"},
		required:   []string{"pattern"},
	},
	"network_rule": {
		parameters: []string{"host", "protocol", "decision", "justification"},
		required:   []string{"host", "protocol", "decision"},
	},
	"host_executable": {
		parameters: []string{"name", "paths"},
		required:   []string{"name", "paths"},
	},
}

// policyGlobalNames lists the Starlark standard globals a policy file may
// mention; they are builtins rather than policy calls, so a statement using one
// is not a scope error.
var policyGlobalNames = []string{
	"None", "True", "False", "abs", "all", "any", "bool", "bytes", "chr",
	"dict", "dir", "enumerate", "fail", "float", "getattr", "hasattr", "hash",
	"int", "len", "list", "max", "min", "ord", "range", "repr", "reversed",
	"sorted", "str", "tuple", "zip",
}

// isPolicyGlobalName reports whether a top-level call names a policy builtin or
// a Starlark global instead of an undefined variable.
func isPolicyGlobalName(name string) bool {
	if _, ok := policyBuiltinSignatures[name]; ok {
		return true
	}
	for _, candidate := range policyGlobalNames {
		if candidate == name {
			return true
		}
	}
	return false
}

// policyCallShapeDiagnostic reproduces the argument checks Starlark runs while
// the module is compiled: a repeated named argument and a positional argument
// after a named one are analysis errors that beat any runtime failure.
func policyCallShapeDiagnostic(call *policyFrontCall, src *policySource) *policyDiagnostic {
	if _, ok := policyBuiltinSignatures[call.name]; !ok {
		return nil
	}
	seen := map[string]bool{}
	sawNamed := false
	for _, arg := range call.args {
		if arg.positional {
			if sawNamed {
				return newPolicyDiagnostic(src, policyAnalysisDiagnostic, "positional argument after non positional", arg.span)
			}
			continue
		}
		sawNamed = true
		if seen[arg.name] {
			return newPolicyDiagnostic(src, policyAnalysisDiagnostic, "repeated named argument", arg.nameSpan)
		}
		seen[arg.name] = true
	}
	return nil
}

// bindPolicyCallArguments mirrors Starlark's binder at call time: misplaced
// arguments aside, it reports missing, extra and duplicated parameters, then
// returns the call body in the `name=value` form the evaluator understands.
func bindPolicyCallArguments(call *policyFrontCall, src *policySource) (string, *policyDiagnostic) {
	signature := policyBuiltinSignatures[call.name]
	positional := 0
	bound := map[string]string{}
	fromPositional := map[string]bool{}
	var extraNamed []string
	for _, arg := range call.args {
		if arg.positional {
			positional++
			if positional > len(signature.parameters) {
				continue
			}
			name := signature.parameters[positional-1]
			bound[name] = arg.value
			fromPositional[name] = true
			continue
		}
		if !containsString(signature.parameters, arg.name) {
			extraNamed = append(extraNamed, arg.name)
			continue
		}
		if fromPositional[arg.name] {
			return "", newPolicyRuntimeDiagnostic(src, call, fmt.Sprintf("Argument `%s` occurs more than once", arg.name))
		}
		bound[arg.name] = arg.value
	}
	if positional > len(signature.parameters) {
		return "", newPolicyRuntimeDiagnostic(src, call,
			fmt.Sprintf("Found %d extra positional argument(s) for call to %s", positional-len(signature.parameters), call.name))
	}
	for _, name := range signature.required {
		if _, ok := bound[name]; !ok {
			return "", newPolicyRuntimeDiagnostic(src, call,
				fmt.Sprintf("Missing parameter `%s` for call to `%s`", name, call.name))
		}
	}
	if len(extraNamed) > 0 {
		quoted := make([]string, 0, len(extraNamed))
		for _, name := range extraNamed {
			quoted = append(quoted, "`"+name+"`")
		}
		return "", newPolicyRuntimeDiagnostic(src, call,
			fmt.Sprintf("Found %s extra named parameter(s) for call to %s", strings.Join(quoted, " "), call.name))
	}
	parts := make([]string, 0, len(signature.parameters))
	for _, name := range signature.parameters {
		value, ok := bound[name]
		if !ok {
			continue
		}
		parts = append(parts, name+"="+value)
	}
	return strings.Join(parts, ", "), nil
}

// policyVariableNotFoundDiagnostic mirrors starlark's scope error for a call to
// a name that is neither a policy builtin nor a Starlark global, including the
// spelling suggestion starlark offers.
func policyVariableNotFoundDiagnostic(src *policySource, call *policyFrontCall) *policyDiagnostic {
	nameStart := int(call.span.start)
	nameEnd := nameStart + len(call.name)
	message := fmt.Sprintf("Variable `%s` not found", call.name)
	if suggestion := policyNameSuggestion(call.name, nil); suggestion != "" {
		message = fmt.Sprintf("Variable `%s` not found, did you mean `%s`?", call.name, suggestion)
	}
	return newPolicyDiagnostic(src, policyAnalysisDiagnostic, message,
		policySpan{start: nameStart, end: nameEnd})
}

// policyNameSuggestion ports starlark's did-you-mean: Levenshtein distance with
// a budget of one edit for one- or two-character names and two edits otherwise,
// choosing the first minimum over the sorted candidate names.
func policyNameSuggestion(name string, locals []string) string {
	if name == "" {
		return ""
	}
	maxDistance := 2
	if len(name) <= 2 {
		maxDistance = 1
	}
	candidates := make([]string, 0, len(policyGlobalNames)+len(policyBuiltinSignatures)+len(locals))
	candidates = append(candidates, policyGlobalNames...)
	candidates = append(candidates, locals...)
	for builtin := range policyBuiltinSignatures {
		candidates = append(candidates, builtin)
	}
	sort.Strings(candidates)
	best := ""
	bestDistance := maxDistance + 1
	for _, candidate := range candidates {
		distance := policyLevenshtein(name, candidate)
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	if bestDistance > maxDistance {
		return ""
	}
	return best
}

func policyLevenshtein(left string, right string) int {
	if left == right {
		return 0
	}
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for j := 0; j <= len(right); j++ {
		previous[j] = j
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			current[j] = minInt(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func minInt(values ...int) int {
	lowest := values[0]
	for _, value := range values[1:] {
		if value < lowest {
			lowest = value
		}
	}
	return lowest
}

// starlarkTypeName renders the bare Starlark type name the rule builders
// report, for example `int` or `string`.
func starlarkTypeName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "NoneType"
	}
	switch raw[0] {
	case '"', '\'':
		return "string"
	case '[':
		return "list"
	case '{':
		return "dict"
	case '(':
		return "tuple"
	}
	switch raw {
	case "None":
		return "NoneType"
	case "True", "False":
		return "bool"
	}
	if isPolicyIntegerLiteral(raw) {
		return "int"
	}
	return raw
}

// starlarkValueType renders a raw argument expression the way starlark's type
// errors do, for example `string (repr: "ls")` or `int (repr: 1)`.
func starlarkValueType(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "NoneType"
	}
	switch raw[0] {
	case '"', '\'':
		return "string (repr: " + raw + ")"
	case '[':
		return "list"
	case '{':
		return "dict"
	case '(':
		return "tuple"
	}
	if raw == "None" {
		return "NoneType"
	}
	if raw == "True" || raw == "False" {
		return "bool (repr: " + raw + ")"
	}
	if isPolicyIntegerLiteral(raw) {
		return "int (repr: " + raw + ")"
	}
	return raw
}

func isPolicyIntegerLiteral(raw string) bool {
	if raw == "" {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	return true
}

// policyTypeError marks a value whose Starlark type does not match the one the
// builtin expects. Call sites turn it into starlark's binder wording, which
// names the parameter and prints the offending value.
type policyTypeError struct {
	raw string
}

func (e *policyTypeError) Error() string { return "value has the wrong type" }

// policyParameterTypeMessage renders starlark's `Type of parameter ...` error,
// for example:
//
//	Type of parameter `decision` doesn't match, expected `str`, actual `int (repr: 1)`
func policyParameterTypeMessage(parameter string, expected string, err error) (string, bool) {
	var typeErr *policyTypeError
	if !errors.As(err, &typeErr) {
		return "", false
	}
	return fmt.Sprintf("Type of parameter `%s` doesn't match, expected `%s`, actual `%s`",
		parameter, expected, starlarkValueType(typeErr.raw)), true
}

// policyValueDiagnostic reproduces Starlark's scope resolution inside an
// argument or assignment value: an unknown name is `Variable ... not found`
// with a spelling suggestion, and a module local used before its assignment is
// `Local variable ... referenced before assignment`.
func policyValueDiagnostic(src *policySource, span policySpan, declared map[string]bool, assigned map[string]bool) *policyDiagnostic {
	if span.end <= span.start || span.end > len(src.text) {
		return nil
	}
	text := src.text[span.start:span.end]
	if strings.Contains(text, "for ") || strings.Contains(text, "lambda") || strings.Contains(text, "def ") {
		return nil
	}
	tokens := lexPolicySource(text)
	for i, token := range tokens {
		if token.kind != policyTokenIdentifier {
			continue
		}
		name := token.text
		if isPolicyKeyword(name) || isPolicyGlobalName(name) || assigned[name] {
			continue
		}
		if i+1 < len(tokens) && tokens[i+1].kind == policyTokenString &&
			tokens[i+1].start == token.end && isPolicyStringPrefix(name) {
			// `f"..."`, `r"..."` and friends are one literal, not a name.
			continue
		}
		if i > 0 && isPolicyPunctToken(tokens[i-1], ".") {
			continue
		}
		if i+1 < len(tokens) && (isPolicyPunctToken(tokens[i+1], "(") || isPolicyPunctToken(tokens[i+1], ".") || isPolicyPunctToken(tokens[i+1], "=")) {
			continue
		}
		identifierSpan := policySpan{start: span.start + token.start, end: span.start + token.end}
		if declared[name] {
			return newPolicyDiagnostic(src, policyAnalysisDiagnostic,
				fmt.Sprintf("Local variable `%s` referenced before assignment", name), identifierSpan)
		}
		message := fmt.Sprintf("Variable `%s` not found", name)
		if suggestion := policyNameSuggestion(name, policySortedNames(declared)); suggestion != "" {
			message = fmt.Sprintf("Variable `%s` not found, did you mean `%s`?", name, suggestion)
		}
		return newPolicyDiagnostic(src, policyAnalysisDiagnostic, message, identifierSpan)
	}
	return nil
}

func isPolicyPunctToken(token policyToken, text string) bool {
	return token.kind == policyTokenPunct && token.text == text
}

// isPolicyKeyword lists the Starlark words that are never variable references.
func isPolicyKeyword(name string) bool {
	switch name {
	case "and", "break", "continue", "def", "elif", "else", "for", "if", "in",
		"lambda", "load", "not", "or", "pass", "return", "while":
		return true
	}
	return false
}

// policySortedNames lists the declared module locals as suggestion candidates.
func policySortedNames(declared map[string]bool) []string {
	if len(declared) == 0 {
		return nil
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	return names
}

func isPolicyStringPrefix(name string) bool {
	switch strings.ToLower(name) {
	case "f", "r", "b", "u", "fr", "rf", "br", "rb", "ur", "ru":
		return true
	}
	return false
}
