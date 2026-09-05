package execpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Decision string

const (
	DecisionAllow     Decision = "allow"
	DecisionPrompt    Decision = "prompt"
	DecisionForbidden Decision = "forbidden"
)

type CheckOptions struct {
	Rules                  []string
	Command                []string
	Pretty                 bool
	ResolveHostExecutables bool
}

type Policy struct {
	Rules           []PrefixRule
	NetworkRules    []NetworkRule
	HostExecutables map[string][]string
}

type PrefixRule struct {
	Pattern          [][]string
	Decision         Decision
	Justification    string
	MatchExamples    [][]string
	NotMatchExamples [][]string
	Source           string
}

type NetworkRule struct {
	Host          string
	Protocol      string
	Decision      Decision
	Justification string
}

type RuleMatch struct {
	PrefixRuleMatch PrefixRuleMatch `json:"prefixRuleMatch"`
}

type PrefixRuleMatch struct {
	MatchedPrefix   []string `json:"matchedPrefix"`
	Decision        Decision `json:"decision"`
	ResolvedProgram string   `json:"resolvedProgram,omitempty"`
	Justification   string   `json:"justification,omitempty"`
}

type CheckOutput struct {
	MatchedRules []RuleMatch `json:"matchedRules"`
	Decision     *Decision   `json:"decision,omitempty"`
}

func Check(opts *CheckOptions) (*CheckOutput, error) {
	if opts == nil {
		return nil, fmt.Errorf("execpolicy check options are nil")
	}
	policy, err := LoadPolicies(opts.Rules)
	if err != nil {
		return nil, err
	}
	output := &CheckOutput{MatchedRules: []RuleMatch{}}
	exactMatches := policy.matchesForCommand(opts.Command, false)
	if len(exactMatches) > 0 || !opts.ResolveHostExecutables {
		output.MatchedRules = exactMatches
	} else {
		output.MatchedRules = policy.matchesForCommand(opts.Command, true)
	}
	decision := strictestDecision(output.MatchedRules)
	if decision != "" {
		output.Decision = &decision
	}
	return output, nil
}

func (p *Policy) matchesForCommand(command []string, resolveHostExecutables bool) []RuleMatch {
	matches := []RuleMatch{}
	for _, rule := range p.Rules {
		matchedPrefix, resolvedProgram, ok := p.matchRule(&rule, command, resolveHostExecutables)
		if !ok {
			continue
		}
		match := RuleMatch{PrefixRuleMatch: PrefixRuleMatch{
			MatchedPrefix:   matchedPrefix,
			Decision:        rule.effectiveDecision(),
			ResolvedProgram: resolvedProgram,
			Justification:   rule.Justification,
		}}
		matches = append(matches, match)
	}
	return matches
}

func Render(output *CheckOutput, pretty bool) (string, error) {
	var data []byte
	var err error
	if pretty {
		data, err = json.MarshalIndent(output, "", "  ")
	} else {
		data, err = json.Marshal(output)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func LoadPolicies(paths []string) (*Policy, error) {
	policy := &Policy{HostExecutables: map[string][]string{}}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read policy at %s: %w", path, err)
		}
		if err := policy.Parse(path, string(data)); err != nil {
			return nil, fmt.Errorf("failed to parse policy at %s: %w", path, err)
		}
	}
	return policy, nil
}

func (p *Policy) Parse(identifier string, input string) error {
	if p == nil {
		return fmt.Errorf("policy is nil")
	}
	statements, err := collectPolicyStatements(input)
	if err != nil {
		return err
	}
	eval := newPolicyEval()
	for _, statement := range statements {
		if statement.AssignName != "" {
			eval.assign(statement.AssignName, statement.AssignValue)
			continue
		}
		switch statement.CallName {
		case "prefix_rule":
			rule, err := parsePrefixRule(eval, identifier, statement.CallBody)
			if err != nil {
				return err
			}
			p.Rules = append(p.Rules, rule)
		case "host_executable":
			name, paths, err := parseHostExecutable(eval, statement.CallBody)
			if err != nil {
				return err
			}
			if name != "" {
				p.HostExecutables[name] = paths
			}
		case "network_rule":
			rule, err := parseNetworkRule(eval, statement.CallBody)
			if err != nil {
				return err
			}
			p.NetworkRules = append(p.NetworkRules, rule)
		}
	}
	return p.validateExamples()
}

func (p *Policy) matchRule(rule *PrefixRule, command []string, resolveHostExecutables bool) ([]string, string, bool) {
	if len(rule.Pattern) == 0 || len(command) < len(rule.Pattern) {
		return nil, "", false
	}
	matched := make([]string, 0, len(rule.Pattern))
	resolvedProgram := ""
	for i, alternatives := range rule.Pattern {
		token := command[i]
		if i == 0 && resolveHostExecutables && filepath.IsAbs(token) {
			basename := executableLookupKey(filepath.Base(token))
			if containsPolicyToken(alternatives, token) {
				matched = append(matched, token)
				continue
			}
			if containsString(alternatives, basename) && p.hostExecutableAllows(basename, token) {
				matched = append(matched, basename)
				resolvedProgram = token
				continue
			}
		}
		if !containsPolicyToken(alternatives, token) {
			return nil, "", false
		}
		matched = append(matched, token)
	}
	return matched, resolvedProgram, true
}

func containsPolicyToken(alternatives []string, token string) bool {
	normalizedToken := normalizePolicyTokenPath(token)
	for _, alternative := range alternatives {
		if alternative == token || normalizePolicyTokenPath(alternative) == normalizedToken {
			return true
		}
	}
	return false
}

func normalizePolicyTokenPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) {
		return filepath.Clean(filepath.FromSlash(value))
	}
	return value
}

func (p *Policy) hostExecutableAllows(name string, path string) bool {
	allowed, ok := p.HostExecutables[name]
	if !ok {
		return true
	}
	if len(allowed) == 0 {
		return false
	}
	cleaned := filepath.Clean(path)
	for _, item := range allowed {
		if filepath.Clean(item) == cleaned {
			return true
		}
	}
	return false
}

func (p *Policy) validateExamples() error {
	for _, rule := range p.Rules {
		for _, example := range rule.NotMatchExamples {
			if _, _, ok := p.matchRule(&rule, example, true); ok {
				return fmt.Errorf("example matched when it should not: %s", strings.Join(example, " "))
			}
		}
		for _, example := range rule.MatchExamples {
			if _, _, ok := p.matchRule(&rule, example, true); !ok {
				return fmt.Errorf("example did not match: %s", strings.Join(example, " "))
			}
		}
	}
	return nil
}

func parsePrefixRule(eval *policyEval, source string, body string) (PrefixRule, error) {
	fields, err := bindCallArguments("prefix_rule", body, []string{"pattern", "decision", "match", "not_match", "justification"})
	if err != nil {
		return PrefixRule{}, err
	}
	patternRaw, ok := fields["pattern"]
	if !ok {
		return PrefixRule{}, fmt.Errorf("prefix_rule missing pattern")
	}
	pattern, err := parsePattern(eval, patternRaw)
	if err != nil {
		return PrefixRule{}, err
	}
	decision := DecisionAllow
	if raw, ok := fields["decision"]; ok {
		value, err := parsePolicyString(eval, raw, "decision")
		if err != nil {
			return PrefixRule{}, err
		}
		parsed := Decision(value)
		switch parsed {
		case DecisionAllow, DecisionPrompt, DecisionForbidden:
			decision = parsed
		default:
			return PrefixRule{}, fmt.Errorf("unsupported decision %q", parsed)
		}
	}
	justification := ""
	if raw, ok := fields["justification"]; ok {
		value, err := parsePolicyString(eval, raw, "justification")
		if err != nil {
			return PrefixRule{}, err
		}
		justification = value
		if strings.TrimSpace(justification) == "" {
			return PrefixRule{}, fmt.Errorf("justification cannot be empty")
		}
	}
	matchExamples, err := parseExamplesField(eval, fields, "match")
	if err != nil {
		return PrefixRule{}, err
	}
	notMatchExamples, err := parseExamplesField(eval, fields, "not_match")
	if err != nil {
		return PrefixRule{}, err
	}
	return PrefixRule{
		Pattern:          pattern,
		Decision:         decision,
		Justification:    justification,
		MatchExamples:    matchExamples,
		NotMatchExamples: notMatchExamples,
		Source:           source,
	}, nil
}

func parseHostExecutable(eval *policyEval, body string) (string, []string, error) {
	fields, err := bindCallArguments("host_executable", body, []string{"name", "paths"})
	if err != nil {
		return "", nil, err
	}
	name := ""
	if raw, ok := fields["name"]; ok {
		value, err := parsePolicyString(eval, raw, "host_executable name")
		if err != nil {
			return "", nil, err
		}
		name = value
	}
	if name == "" {
		return "", nil, fmt.Errorf("host_executable name cannot be empty")
	}
	if filepath.Base(name) != name {
		return "", nil, fmt.Errorf("host_executable name must be a bare executable name (got %s)", name)
	}
	var paths []string
	raw, ok := fields["paths"]
	if !ok {
		return "", nil, fmt.Errorf("host_executable missing paths")
	}
	values, err := parseStringList(eval, raw)
	if err != nil {
		return "", nil, err
	}
	for _, path := range values {
		if !filepath.IsAbs(path) {
			return "", nil, fmt.Errorf("host_executable paths must be absolute (got %s)", path)
		}
		if executableLookupKey(filepath.Base(path)) != executableLookupKey(name) {
			return "", nil, fmt.Errorf("host_executable path `%s` must have basename `%s`", path, name)
		}
		if !containsString(paths, path) {
			paths = append(paths, path)
		}
	}
	return executableLookupKey(name), paths, nil
}

func parseNetworkRule(eval *policyEval, body string) (NetworkRule, error) {
	fields, err := bindCallArguments("network_rule", body, []string{"host", "protocol", "decision", "justification"})
	if err != nil {
		return NetworkRule{}, err
	}
	hostRaw, ok := fields["host"]
	if !ok {
		return NetworkRule{}, fmt.Errorf("network_rule missing host")
	}
	hostValue, err := parsePolicyString(eval, hostRaw, "network_rule host")
	if err != nil {
		return NetworkRule{}, err
	}
	host, err := normalizeNetworkRuleHost(hostValue)
	if err != nil {
		return NetworkRule{}, err
	}
	protocolRaw, ok := fields["protocol"]
	if !ok {
		return NetworkRule{}, fmt.Errorf("network_rule missing protocol")
	}
	protocolValue, err := parsePolicyString(eval, protocolRaw, "network_rule protocol")
	if err != nil {
		return NetworkRule{}, err
	}
	protocol, err := parseNetworkRuleProtocol(protocolValue)
	if err != nil {
		return NetworkRule{}, err
	}
	decisionRaw, ok := fields["decision"]
	if !ok {
		return NetworkRule{}, fmt.Errorf("network_rule missing decision")
	}
	decisionValue, err := parsePolicyString(eval, decisionRaw, "network_rule decision")
	if err != nil {
		return NetworkRule{}, err
	}
	decision, err := parseNetworkRuleDecision(decisionValue)
	if err != nil {
		return NetworkRule{}, err
	}
	justification := ""
	if raw, ok := fields["justification"]; ok {
		value, err := parsePolicyString(eval, raw, "justification")
		if err != nil {
			return NetworkRule{}, err
		}
		justification = value
		if strings.TrimSpace(justification) == "" {
			return NetworkRule{}, fmt.Errorf("justification cannot be empty")
		}
	}
	return NetworkRule{Host: host, Protocol: protocol, Decision: decision, Justification: justification}, nil
}

func bindCallArguments(function string, body string, positionalNames []string) (map[string]string, error) {
	fields := map[string]string{}
	namedSeen := false
	positionalIndex := 0
	for _, part := range splitPolicyTopLevel(body, ',') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if name, value, ok := callArgument(part); ok {
			namedSeen = true
			if !containsString(positionalNames, name) {
				return nil, fmt.Errorf("%s got unexpected argument %s", function, name)
			}
			if _, exists := fields[name]; exists {
				return nil, fmt.Errorf("%s got multiple values for argument %s", function, name)
			}
			fields[name] = value
			continue
		}
		if namedSeen {
			return nil, fmt.Errorf("%s positional argument follows named argument", function)
		}
		if positionalIndex >= len(positionalNames) {
			return nil, fmt.Errorf("%s got too many positional arguments", function)
		}
		name := positionalNames[positionalIndex]
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("%s got multiple values for argument %s", function, name)
		}
		fields[name] = part
		positionalIndex++
	}
	return fields, nil
}

func callArgument(raw string) (string, string, bool) {
	name, ok := callArgumentName(raw)
	if !ok {
		return "", "", false
	}
	index := strings.Index(raw, "=")
	if index < 0 {
		return "", "", false
	}
	return name, strings.TrimSpace(raw[index+1:]), true
}

func callArgumentName(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	index := strings.Index(raw, "=")
	if index < 0 {
		return "", false
	}
	name := strings.TrimSpace(raw[:index])
	if name == "" {
		return "", false
	}
	for i := 0; i < len(name); i++ {
		if i == 0 && !isPolicyIdentifierStart(name[i]) {
			return "", false
		}
		if i > 0 && !isPolicyIdentifierPart(name[i]) {
			return "", false
		}
	}
	return name, true
}

func parseNetworkRuleProtocol(raw string) (string, error) {
	switch raw {
	case "http":
		return "http", nil
	case "https", "https_connect", "http-connect":
		return "https", nil
	case "socks5_tcp":
		return "socks5_tcp", nil
	case "socks5_udp":
		return "socks5_udp", nil
	default:
		return "", fmt.Errorf("network_rule protocol must be one of http, https, socks5_tcp, socks5_udp (got %s)", raw)
	}
}

func parseNetworkRuleDecision(raw string) (Decision, error) {
	if raw == "deny" {
		return DecisionForbidden, nil
	}
	decision := Decision(raw)
	switch decision {
	case DecisionAllow, DecisionPrompt, DecisionForbidden:
		return decision, nil
	default:
		return "", fmt.Errorf("unsupported decision %q", decision)
	}
}

func normalizeNetworkRuleHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "", fmt.Errorf("network_rule host cannot be empty")
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/?#") {
		return "", fmt.Errorf("network_rule host must be a hostname or IP literal (without scheme or path)")
	}
	if strings.HasPrefix(host, "[") {
		inside, rest, ok := strings.Cut(strings.TrimPrefix(host, "["), "]")
		if !ok {
			return "", fmt.Errorf("network_rule host has an invalid bracketed IPv6 literal")
		}
		if rest != "" {
			port, ok := strings.CutPrefix(rest, ":")
			if !ok || port == "" || !allASCIIDigits(port) {
				return "", fmt.Errorf("network_rule host contains an unsupported suffix: %s", raw)
			}
		}
		host = inside
	} else if strings.Count(host, ":") == 1 {
		candidate, port, ok := strings.Cut(host, ":")
		if ok && candidate != "" && port != "" && allASCIIDigits(port) {
			host = candidate
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(strings.TrimRight(host, ".")))
	if normalized == "" {
		return "", fmt.Errorf("network_rule host cannot be empty")
	}
	if strings.Contains(normalized, "*") {
		return "", fmt.Errorf("network_rule host must be a specific host; wildcards are not allowed")
	}
	if strings.IndexFunc(normalized, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) >= 0 {
		return "", fmt.Errorf("network_rule host cannot contain whitespace")
	}
	return normalized, nil
}

func allASCIIDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parsePattern(eval *policyEval, raw string) ([][]string, error) {
	values, err := parsePolicyList(eval, strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("pattern cannot be empty")
	}
	pattern := make([][]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if isPolicyListExpression(eval, value) {
			alternatives, err := parseStringList(eval, value)
			if err != nil {
				return nil, err
			}
			if len(alternatives) == 0 {
				return nil, fmt.Errorf("pattern alternatives cannot be empty")
			}
			pattern = append(pattern, alternatives)
			continue
		}
		token, err := parsePolicyString(eval, value, "pattern element")
		if err != nil {
			return nil, err
		}
		pattern = append(pattern, []string{token})
	}
	return pattern, nil
}

func parseStringList(eval *policyEval, raw string) ([]string, error) {
	values, err := parsePolicyList(eval, raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		parsed, err := parsePolicyString(eval, value, "list item")
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, nil
}

func parseExamplesField(eval *policyEval, fields map[string]string, name string) ([][]string, error) {
	raw, ok := fields[name]
	if !ok {
		return nil, nil
	}
	values, err := parsePolicyList(eval, raw)
	if err != nil {
		return nil, err
	}
	examples := make([][]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if isPolicyListExpression(eval, value) {
			tokens, err := parseStringList(eval, value)
			if err != nil {
				return nil, err
			}
			if len(tokens) == 0 {
				return nil, fmt.Errorf("example cannot be an empty list")
			}
			examples = append(examples, tokens)
			continue
		}
		rawExample, err := parsePolicyString(eval, value, "example")
		if err != nil {
			return nil, err
		}
		tokens, err := splitShellWords(rawExample)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			return nil, fmt.Errorf("example cannot be an empty string")
		}
		examples = append(examples, tokens)
	}
	return examples, nil
}

func parsePolicyList(eval *policyEval, raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if resolved, ok, err := eval.resolveIdentifier(raw); err != nil {
		return nil, err
	} else if ok {
		raw = strings.TrimSpace(resolved)
	}
	if parts := splitPolicyConcat(raw); len(parts) > 1 {
		var out []string
		for _, part := range parts {
			values, err := parsePolicyList(eval, part)
			if err != nil {
				return nil, err
			}
			out = append(out, values...)
		}
		return out, nil
	}
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil, fmt.Errorf("expected list, got %s", raw)
	}
	body := strings.TrimSpace(raw[1 : len(raw)-1])
	if body == "" {
		return nil, nil
	}
	return splitPolicyTopLevel(body, ','), nil
}

type policyStatement struct {
	CallName    string
	CallBody    string
	AssignName  string
	AssignValue string
}

func collectPolicyStatements(input string) ([]policyStatement, error) {
	var statements []policyStatement
	for i := 0; i < len(input); {
		ch := input[i]
		switch {
		case ch == '#':
			i = skipPolicyComment(input, i)
			continue
		case ch == '\'' || ch == '"':
			next, err := skipPolicyString(input, i)
			if err != nil {
				return nil, err
			}
			i = next
			continue
		case isPolicyIdentifierStart(ch):
			nameStart := i
			i++
			for i < len(input) && isPolicyIdentifierPart(input[i]) {
				i++
			}
			name := input[nameStart:i]
			next := skipPolicyWhitespace(input, i)
			if next < len(input) && input[next] == '=' {
				valueStart := skipPolicyWhitespace(input, next+1)
				valueEnd, err := readPolicyExpression(input, valueStart)
				if err != nil {
					return nil, err
				}
				value := strings.TrimSpace(input[valueStart:valueEnd])
				if value == "" {
					return nil, fmt.Errorf("empty assignment for %s", name)
				}
				statements = append(statements, policyStatement{AssignName: name, AssignValue: value})
				i = valueEnd
				continue
			}
			if next >= len(input) || input[next] != '(' {
				continue
			}
			if !isPolicyFunction(name) {
				return nil, fmt.Errorf("unknown execpolicy function %s", name)
			}
			close, err := matchingPolicyParen(input, next)
			if err != nil {
				return nil, err
			}
			statements = append(statements, policyStatement{CallName: name, CallBody: input[next+1 : close]})
			i = close + 1
		default:
			i++
		}
	}
	return statements, nil
}

func isPolicyFunction(name string) bool {
	switch name {
	case "prefix_rule", "host_executable", "network_rule":
		return true
	default:
		return false
	}
}

func matchingPolicyParen(input string, open int) (int, error) {
	depth := 0
	var quote byte
	for i := open; i < len(input); i++ {
		ch := input[i]
		if quote != 0 {
			if ch == '\\' && i+1 < len(input) {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '#':
			if depth == 0 {
				return i, nil
			}
			i = skipPolicyComment(input, i) - 1
		case '\'', '"':
			quote = ch
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return -1, fmt.Errorf("unterminated execpolicy call")
}

func skipPolicyWhitespace(input string, i int) int {
	for i < len(input) {
		switch input[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func skipPolicyComment(input string, i int) int {
	for i < len(input) && input[i] != '\n' {
		i++
	}
	return i
}

func readPolicyExpression(input string, start int) (int, error) {
	depth := 0
	var quote byte
	for i := start; i < len(input); i++ {
		ch := input[i]
		if quote != 0 {
			if ch == '\\' && i+1 < len(input) {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '#':
			if depth == 0 {
				return i, nil
			}
			i = skipPolicyComment(input, i) - 1
		case '\'', '"':
			quote = ch
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			if depth > 0 {
				depth--
			}
		case '\r', '\n':
			if depth == 0 {
				return i, nil
			}
		}
	}
	if quote != 0 {
		return len(input), fmt.Errorf("unterminated string in execpolicy")
	}
	return len(input), nil
}

func skipPolicyString(input string, i int) (int, error) {
	quote := input[i]
	i++
	for i < len(input) {
		ch := input[i]
		if ch == '\\' && i+1 < len(input) {
			i += 2
			continue
		}
		i++
		if ch == quote {
			return i, nil
		}
	}
	return i, fmt.Errorf("unterminated string in execpolicy")
}

func isPolicyIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isPolicyIdentifierPart(ch byte) bool {
	return isPolicyIdentifierStart(ch) || ch >= '0' && ch <= '9'
}

type policyEval struct {
	assignments map[string]string
}

func newPolicyEval() *policyEval {
	return &policyEval{assignments: map[string]string{}}
}

func (e *policyEval) assign(name string, value string) {
	if e == nil {
		return
	}
	e.assignments[name] = strings.TrimSpace(value)
}

func (e *policyEval) resolveIdentifier(raw string) (string, bool, error) {
	if e == nil {
		return "", false, nil
	}
	return e.resolveIdentifierWithStack(strings.TrimSpace(raw), map[string]bool{})
}

func (e *policyEval) resolveIdentifierWithStack(raw string, stack map[string]bool) (string, bool, error) {
	if !isExactPolicyIdentifier(raw) {
		return "", false, nil
	}
	value, ok := e.assignments[raw]
	if !ok {
		return "", false, nil
	}
	if stack[raw] {
		return "", false, fmt.Errorf("cyclic execpolicy assignment involving %s", raw)
	}
	stack[raw] = true
	resolved, found, err := e.resolveIdentifierWithStack(strings.TrimSpace(value), stack)
	delete(stack, raw)
	if err != nil {
		return "", false, err
	}
	if found {
		return resolved, true, nil
	}
	return value, true, nil
}

func isPolicyListExpression(eval *policyEval, raw string) bool {
	raw = strings.TrimSpace(raw)
	if resolved, ok, err := eval.resolveIdentifier(raw); err == nil && ok {
		raw = strings.TrimSpace(resolved)
	}
	if parts := splitPolicyConcat(raw); len(parts) > 1 {
		for _, part := range parts {
			if !isPolicyListExpression(eval, part) {
				return false
			}
		}
		return true
	}
	return strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]")
}

func isExactPolicyIdentifier(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || !isPolicyIdentifierStart(raw[0]) {
		return false
	}
	for i := 1; i < len(raw); i++ {
		if !isPolicyIdentifierPart(raw[i]) {
			return false
		}
	}
	return true
}

func splitPolicyTopLevel(raw string, sep byte) []string {
	var parts []string
	start := 0
	depth := 0
	var quote byte
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if quote != 0 {
			if ch == '\\' && i+1 < len(raw) {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(raw[start:]))
	return parts
}

func splitPolicyConcat(raw string) []string {
	parts := splitPolicyTopLevel(raw, '+')
	if len(parts) <= 1 {
		return nil
	}
	return parts
}

func parsePolicyString(eval *policyEval, raw string, label string) (string, error) {
	raw = strings.TrimSpace(raw)
	if resolved, ok, err := eval.resolveIdentifier(raw); err != nil {
		return "", err
	} else if ok {
		raw = strings.TrimSpace(resolved)
	}
	if parts := splitPolicyConcat(raw); len(parts) > 1 {
		var out strings.Builder
		for _, part := range parts {
			value, err := parsePolicyString(eval, part, label)
			if err != nil {
				return "", err
			}
			out.WriteString(value)
		}
		return out.String(), nil
	}
	if isPolicyFString(raw) {
		return parsePolicyFString(eval, raw, label)
	}
	if len(raw) >= 2 {
		if raw[0] == '"' && raw[len(raw)-1] == '"' || raw[0] == '\'' && raw[len(raw)-1] == '\'' {
			value, err := strconv.Unquote(raw)
			if err != nil {
				return "", fmt.Errorf("%s has invalid string literal: %w", label, err)
			}
			return value, nil
		}
	}
	return "", fmt.Errorf("%s must be a string literal", label)
}

func isPolicyFString(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) >= 3 && (raw[0] == 'f' || raw[0] == 'F') && (raw[1] == '"' && raw[len(raw)-1] == '"' || raw[1] == '\'' && raw[len(raw)-1] == '\'')
}

func parsePolicyFString(eval *policyEval, raw string, label string) (string, error) {
	content, err := strconv.Unquote(raw[1:])
	if err != nil {
		return "", fmt.Errorf("%s has invalid string literal: %w", label, err)
	}
	var out strings.Builder
	for i := 0; i < len(content); i++ {
		ch := content[i]
		switch ch {
		case '{':
			if i+1 < len(content) && content[i+1] == '{' {
				out.WriteByte('{')
				i++
				continue
			}
			close := strings.IndexByte(content[i+1:], '}')
			if close < 0 {
				return "", fmt.Errorf("%s has invalid f-string expression", label)
			}
			expr := strings.TrimSpace(content[i+1 : i+1+close])
			if expr == "" {
				return "", fmt.Errorf("%s has empty f-string expression", label)
			}
			value, err := parsePolicyString(eval, expr, label)
			if err != nil {
				return "", err
			}
			out.WriteString(value)
			i += close + 1
		case '}':
			if i+1 < len(content) && content[i+1] == '}' {
				out.WriteByte('}')
				i++
				continue
			}
			return "", fmt.Errorf("%s has unmatched f-string brace", label)
		default:
			out.WriteByte(ch)
		}
	}
	return out.String(), nil
}

func splitShellWords(raw string) ([]string, error) {
	var words []string
	var current strings.Builder
	var quote byte
	escaped := false
	active := false
	flush := func() {
		if active || current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
			active = false
		}
	}
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			current.WriteByte(ch)
			active = true
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			active = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
				active = true
				continue
			}
			current.WriteByte(ch)
			active = true
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
			active = true
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			current.WriteByte(ch)
			active = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("example string has invalid shell syntax")
	}
	if quote != 0 {
		return nil, fmt.Errorf("example string has invalid shell syntax")
	}
	flush()
	return words, nil
}

func strictestDecision(matches []RuleMatch) Decision {
	decision := Decision("")
	for _, match := range matches {
		next := match.PrefixRuleMatch.Decision
		if decisionRank(next) > decisionRank(decision) {
			decision = next
		}
	}
	return decision
}

func decisionRank(decision Decision) int {
	switch decision {
	case DecisionAllow:
		return 1
	case DecisionPrompt:
		return 2
	case DecisionForbidden:
		return 3
	default:
		return 0
	}
}

func (r *PrefixRule) effectiveDecision() Decision {
	if r == nil || r.Decision == "" {
		return DecisionAllow
	}
	return r.Decision
}

type DangerousCommandPlatform string

const (
	DangerousCommandPlatformPosix   DangerousCommandPlatform = "posix"
	DangerousCommandPlatformWindows DangerousCommandPlatform = "windows"
)

// HostDangerousCommandPlatform returns the platform where the classifier is
// running (Rust DangerousCommandPlatform::host).
func HostDangerousCommandPlatform() DangerousCommandPlatform {
	if runtime.GOOS == "windows" {
		return DangerousCommandPlatformWindows
	}
	return DangerousCommandPlatformPosix
}

func executableLookupKeyForPlatform(raw string, platform DangerousCommandPlatform) string {
	if platform != DangerousCommandPlatformWindows {
		return raw
	}
	raw = strings.ToLower(raw)
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".com"} {
		if strings.HasSuffix(raw, suffix) {
			return strings.TrimSuffix(raw, suffix)
		}
	}
	return raw
}

func executableLookupKey(raw string) string {
	return executableLookupKeyForPlatform(raw, HostDangerousCommandPlatform())
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
