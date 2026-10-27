package network

import (
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gobwas/glob"
)

const (
	matcherPatternPrefix = "pattern:"
	matcherLiteralPrefix = "literal:"
)

type ProxyMITMHookConfig struct {
	Host    string
	Match   ProxyMITMHookMatchConfig
	Actions ProxyMITMHookActionsConfig
}

type ProxyMITMHookMatchConfig struct {
	Schemes      []string
	Ports        []int
	Methods      []string
	PathPrefixes []string
	Query        map[string][]string
	Headers      map[string][]string
	Body         *ProxyMITMHookBodyConfig
}

type ProxyMITMHookActionsConfig struct {
	StripRequestHeaders    []string
	InjectRequestHeaders   []ProxyInjectedHeaderConfig
	AWSSigV4               *AWSSigV4Config
	BodySecretSubstitution *BodySecretSubstitutionConfig
}

type ProxyInjectedHeaderConfig struct {
	Placeholder  *string
	Name         string
	SecretEnvVar *string
	SecretFile   *string
	Prefix       string
}

type ProxyMITMHookBodyConfig struct {
	Raw any
}

type ProxyMITMHook struct {
	Host    string
	Matcher ProxyMITMHookMatcher
	Actions ProxyMITMHookActions
}

type ProxyMITMHookMatcher struct {
	Schemes      []string
	Ports        []int
	Methods      []string
	PathPrefixes []ProxyPathMatcher
	Query        []ProxyQueryConstraint
	Headers      []ProxyHeaderConstraint
	Body         *ProxyMITMHookBodyMatcher
}

type ProxyQueryConstraint struct {
	Name          string
	AllowedValues []ProxyValueMatcher
}

type ProxyHeaderConstraint struct {
	Name          string
	AllowedValues []ProxyValueMatcher
}

type ProxyMITMHookActions struct {
	StripRequestHeaders    []string
	InjectRequestHeaders   []ProxyResolvedInjectedHeader
	AWSSigV4               *AWSSigV4Signer
	BodySecretSubstitution *BodySecretSubstitution
}

type ProxyResolvedInjectedHeader struct {
	Placeholder string
	Name        string
	Value       string
	Source      ProxySecretSource
}

type ProxySecretSource struct {
	Kind string
	Name string
}

type ProxyMITMHookBodyMatcher struct {
	Raw any
}

type ProxyPathMatcher struct {
	Kind    string
	Pattern string
}

type ProxyValueMatcher struct {
	Kind    string
	Pattern string
}

type ProxyMITMHooksByHost map[string][]ProxyMITMHook

type ProxyHookEvaluationKind string

const (
	ProxyHookEvaluationNoHooksForHost ProxyHookEvaluationKind = "no_hooks_for_host"
	ProxyHookEvaluationMatched        ProxyHookEvaluationKind = "matched"
	ProxyHookEvaluationHookedNoMatch  ProxyHookEvaluationKind = "hooked_host_no_match"
)

type ProxyHookEvaluation struct {
	Kind    ProxyHookEvaluationKind
	Actions ProxyMITMHookActions
}

// ProxyHookOrigin is the authenticated tunnel destination, independent of guest
// HTTP authority headers.
type ProxyHookOrigin struct {
	Scheme string
	Port   int
}

type ProxyHTTPRequest struct {
	Method  string
	Path    string
	Query   string
	Headers map[string][]string
}

type ProxySecretEnvResolver func(string) (string, bool)
type ProxySecretFileReader func(string) (string, error)

func ValidateProxyMITMHookConfig(config ProxyConfig) error {
	if ca := config.Network.MITMCA; ca != nil {
		if !config.Network.MITM {
			return fmt.Errorf("network.mitm_ca requires network.mitm = true")
		}
		if !filepath.IsAbs(ca.CertificateFile) {
			return fmt.Errorf("network.mitm_ca.certificate_file must be an absolute path")
		}
		if !filepath.IsAbs(ca.PrivateKeyFile) {
			return fmt.Errorf("network.mitm_ca.private_key_file must be an absolute path")
		}
	}
	hooks := config.Network.MITMHooks
	if len(hooks) == 0 {
		return nil
	}
	if !config.Network.MITM {
		return fmt.Errorf("network.mitm_hooks requires network.mitm = true")
	}
	for index, hook := range hooks {
		if _, err := normalizeHookHost(hook.Host); err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].host: %w", index, err)
		}
		methods, err := normalizeMethods(hook.Match.Methods)
		if err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].match.methods: %w", index, err)
		}
		if len(methods) == 0 {
			return fmt.Errorf("network.mitm_hooks[%d].match.methods must not be empty", index)
		}
		pathPrefixes, err := compilePathMatchers(hook.Match.PathPrefixes)
		if err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].match.path_prefixes: %w", index, err)
		}
		if len(pathPrefixes) == 0 {
			return fmt.Errorf("network.mitm_hooks[%d].match.path_prefixes must not be empty", index)
		}
		if err := validateHookOriginConstraints(hook.Match.Schemes, hook.Match.Ports); err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].match: %w", index, err)
		}
		if hook.Match.Body != nil {
			return fmt.Errorf("network.mitm_hooks[%d].match.body is reserved for a future release and is not yet supported", index)
		}
		if err := validateQueryConstraints(hook.Match.Query); err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].match.query: %w", index, err)
		}
		if err := validateHeaderConstraints(hook.Match.Headers); err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].match.headers: %w", index, err)
		}
		if err := validateStripRequestHeaders(hook.Actions.StripRequestHeaders); err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].actions.strip_request_headers: %w", index, err)
		}
		if err := validateInjectedHeaders(hook.Actions.InjectRequestHeaders); err != nil {
			return fmt.Errorf("invalid network.mitm_hooks[%d].actions.inject_request_headers: %w", index, err)
		}
		if hook.Actions.AWSSigV4 != nil {
			if err := hook.Actions.AWSSigV4.Validate(); err != nil {
				return fmt.Errorf("invalid network.mitm_hooks[%d].actions.aws_sigv4: %w", index, err)
			}
		}
		if hook.Actions.BodySecretSubstitution != nil {
			if err := hook.Actions.BodySecretSubstitution.Validate(); err != nil {
				return fmt.Errorf("invalid network.mitm_hooks[%d].actions.body_secret_substitution: %w", index, err)
			}
		}
	}
	return nil
}

func CompileProxyMITMHooks(config ProxyConfig) (ProxyMITMHooksByHost, error) {
	return CompileProxyMITMHooksWithResolvers(
		config,
		func(name string) (string, bool) { return os.LookupEnv(name) },
		readProxySecretFile,
	)
}

func CompileProxyMITMHooksWithResolvers(config ProxyConfig, resolveEnv ProxySecretEnvResolver, readFile ProxySecretFileReader) (ProxyMITMHooksByHost, error) {
	if err := ValidateProxyMITMHookConfig(config); err != nil {
		return nil, err
	}
	if resolveEnv == nil {
		resolveEnv = func(string) (string, bool) { return "", false }
	}
	if readFile == nil {
		readFile = func(path string) (string, error) { return "", fmt.Errorf("missing secret file reader for %s", path) }
	}
	hooksByHost := ProxyMITMHooksByHost{}
	var signers []*AWSSigV4Signer
	for _, hook := range config.Network.MITMHooks {
		host, err := normalizeHookHost(hook.Host)
		if err != nil {
			return nil, err
		}
		methods, err := normalizeMethods(hook.Match.Methods)
		if err != nil {
			return nil, err
		}
		pathPrefixes, err := compilePathMatchers(hook.Match.PathPrefixes)
		if err != nil {
			return nil, err
		}
		query, err := compileQueryConstraints(hook.Match.Query)
		if err != nil {
			return nil, err
		}
		headers, err := compileHeaderConstraints(hook.Match.Headers)
		if err != nil {
			return nil, err
		}
		stripHeaders, err := parseHeaderNames(hook.Actions.StripRequestHeaders)
		if err != nil {
			return nil, err
		}
		injectedHeaders, err := compileInjectedHeaders(hook.Actions.InjectRequestHeaders, resolveEnv, readFile)
		if err != nil {
			return nil, err
		}
		var substitution *BodySecretSubstitution
		if hook.Actions.BodySecretSubstitution != nil {
			substitution, err = hook.Actions.BodySecretSubstitution.Compile(resolveEnv, readFile)
			if err != nil {
				return nil, fmt.Errorf("failed to compile body substitution action for %s: %w", host, err)
			}
		}
		var awsSigner *AWSSigV4Signer
		if hook.Actions.AWSSigV4 != nil {
			awsSigner, err = hook.Actions.AWSSigV4.Compile(resolveEnv, readFile)
			if err != nil {
				return nil, fmt.Errorf("failed to compile AWS signing action for %s: %w", host, err)
			}
			signers = append(signers, awsSigner)
		}
		hooksByHost[host] = append(hooksByHost[host], ProxyMITMHook{
			Host: host,
			Matcher: ProxyMITMHookMatcher{
				Schemes:      append([]string(nil), hook.Match.Schemes...),
				Ports:        append([]int(nil), hook.Match.Ports...),
				Methods:      methods,
				PathPrefixes: pathPrefixes,
				Query:        query,
				Headers:      headers,
			},
			Actions: ProxyMITMHookActions{
				StripRequestHeaders:    stripHeaders,
				InjectRequestHeaders:   injectedHeaders,
				AWSSigV4:               awsSigner,
				BodySecretSubstitution: substitution,
			},
		})
	}
	if err := ValidateGuestPlaceholders(signers); err != nil {
		return nil, err
	}
	return hooksByHost, nil
}

func EvaluateProxyMITMHooks(hooksByHost ProxyMITMHooksByHost, host string, req ProxyHTTPRequest) ProxyHookEvaluation {
	return EvaluateProxyMITMHooksAtOrigin(hooksByHost, host, ProxyHookOrigin{}, req)
}

// EvaluateProxyMITMHooksAtOrigin mirrors Rust's evaluate_mitm_hooks: scoped
// hooks match the authenticated destination as well as the request.
func EvaluateProxyMITMHooksAtOrigin(hooksByHost ProxyMITMHooksByHost, host string, origin ProxyHookOrigin, req ProxyHTTPRequest) ProxyHookEvaluation {
	normalizedHost := NormalizeProxyHost(host)
	hooks := hooksByHost[normalizedHost]
	if len(hooks) == 0 {
		return ProxyHookEvaluation{Kind: ProxyHookEvaluationNoHooksForHost}
	}
	for _, hook := range hooks {
		if hookMatches(&hook, &origin, &req) {
			return ProxyHookEvaluation{Kind: ProxyHookEvaluationMatched, Actions: hook.Actions}
		}
	}
	return ProxyHookEvaluation{Kind: ProxyHookEvaluationHookedNoMatch}
}

// evaluateProxyMITMHookRequest mirrors
// NetworkProxyState::evaluate_mitm_hook_request
// (network-proxy/src/runtime.rs:982-1013, #52661): when the request host itself
// has no hooks, a brokered credential alias bound to one of the configured
// hooked hosts still yields HookedHostNoMatch, so the real credential is never
// substituted for an unhooked alias.
func evaluateProxyMITMHookRequest(hooksByHost ProxyMITMHooksByHost, broker *ProxyCredentialBroker, host string, origin ProxyHookOrigin, req ProxyHTTPRequest, credentialDestinations []string) ProxyHookEvaluation {
	evaluation := EvaluateProxyMITMHooksAtOrigin(hooksByHost, host, origin, req)
	if evaluation.Kind != ProxyHookEvaluationNoHooksForHost || len(hooksByHost) == 0 {
		return evaluation
	}
	hookedHosts := make([]string, 0, len(hooksByHost))
	for hookedHost := range hooksByHost {
		hookedHosts = append(hookedHosts, hookedHost)
	}
	for _, destination := range credentialDestinations {
		if broker.RequestMatchesHookedHostAlias(destination, req.Headers, hookedHosts) {
			return ProxyHookEvaluation{Kind: ProxyHookEvaluationHookedNoMatch}
		}
	}
	return evaluation
}

func NewProxyHTTPRequestFromHTTP(req *http.Request) ProxyHTTPRequest {
	headers := map[string][]string{}
	if req != nil {
		for key, values := range req.Header {
			headers[key] = append([]string(nil), values...)
		}
	}
	out := ProxyHTTPRequest{Headers: headers}
	if req == nil {
		return out
	}
	out.Method = req.Method
	if req.URL != nil {
		out.Path = req.URL.Path
		out.Query = req.URL.RawQuery
	}
	return out
}

func (m *ProxyPathMatcher) Matches(candidate string) bool {
	if m == nil {
		return false
	}
	if m.Kind == "glob" {
		return globMatch(m.Pattern, candidate, true)
	}
	return strings.HasPrefix(candidate, m.Pattern)
}

func (m *ProxyValueMatcher) Matches(candidate string) bool {
	if m == nil {
		return false
	}
	if m.Kind == "glob" {
		return globMatch(m.Pattern, candidate, false)
	}
	return candidate == m.Pattern
}

func hookMatches(hook *ProxyMITMHook, origin *ProxyHookOrigin, req *ProxyHTTPRequest) bool {
	if origin != nil {
		if len(hook.Matcher.Schemes) > 0 && !stringInSliceFold(hook.Matcher.Schemes, origin.Scheme) {
			return false
		}
		if len(hook.Matcher.Ports) > 0 && !intInSlice(hook.Matcher.Ports, origin.Port) {
			return false
		}
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = "GET"
	}
	if !stringInSlice(method, hook.Matcher.Methods) {
		return false
	}
	path := req.Path
	if path == "" {
		path = "/"
	}
	if !pathMatches(hook.Matcher.PathPrefixes, path) {
		return false
	}
	if !queryMatches(hook.Matcher.Query, req.Query) {
		return false
	}
	return headersMatch(hook.Matcher.Headers, req.Headers)
}

func stringInSliceFold(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}

func intInSlice(values []int, candidate int) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func pathMatches(matchers []ProxyPathMatcher, path string) bool {
	for index := range matchers {
		if (&matchers[index]).Matches(path) {
			return true
		}
	}
	return false
}

func queryMatches(constraints []ProxyQueryConstraint, rawQuery string) bool {
	if len(constraints) == 0 {
		return true
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		values = url.Values{}
	}
	for _, constraint := range constraints {
		// Origins disagree on which occurrence wins. Authorize only a single value.
		actual := values[constraint.Name]
		if len(actual) != 1 || !anyValueMatches(actual, constraint.AllowedValues) {
			return false
		}
	}
	return true
}

func headersMatch(constraints []ProxyHeaderConstraint, headers map[string][]string) bool {
	if len(constraints) == 0 {
		return true
	}
	canonical := map[string][]string{}
	for key, values := range headers {
		canonical[textproto.CanonicalMIMEHeaderKey(key)] = append(canonical[textproto.CanonicalMIMEHeaderKey(key)], values...)
	}
	for _, constraint := range constraints {
		actual := canonical[textproto.CanonicalMIMEHeaderKey(constraint.Name)]
		if len(actual) == 0 {
			return false
		}
		if len(constraint.AllowedValues) == 0 {
			continue
		}
		if len(actual) != 1 {
			return false
		}
		candidate := actual[0]
		matched := false
		for index := range constraint.AllowedValues {
			allowed := &constraint.AllowedValues[index]
			// A field line can combine multiple logical values. A glob must not
			// consume the comma and accidentally authorize a second list member.
			if strings.Contains(candidate, ",") && allowed.Kind != "literal" {
				continue
			}
			if allowed.Matches(candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func anyValueMatches(actual []string, allowed []ProxyValueMatcher) bool {
	if len(actual) == 0 {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	for _, value := range actual {
		for index := range allowed {
			if (&allowed[index]).Matches(value) {
				return true
			}
		}
	}
	return false
}

func compilePathMatchers(pathPrefixes []string) ([]ProxyPathMatcher, error) {
	matchers := make([]ProxyPathMatcher, 0, len(pathPrefixes))
	for _, prefix := range pathPrefixes {
		kind, pattern, err := parseMatcherPattern(prefix)
		if err != nil {
			return nil, err
		}
		if kind == "literal" {
			if pattern == "" {
				return nil, fmt.Errorf("path_prefixes must not contain empty entries")
			}
			matchers = append(matchers, ProxyPathMatcher{Kind: "prefix", Pattern: pattern})
			continue
		}
		if err := validateGlobPattern(pattern); err != nil {
			return nil, err
		}
		matchers = append(matchers, ProxyPathMatcher{Kind: "glob", Pattern: pattern})
	}
	return matchers, nil
}

func compileValueMatchers(values []string) ([]ProxyValueMatcher, error) {
	matchers := make([]ProxyValueMatcher, 0, len(values))
	for _, value := range values {
		kind, pattern, err := parseMatcherPattern(value)
		if err != nil {
			return nil, err
		}
		if kind == "literal" {
			matchers = append(matchers, ProxyValueMatcher{Kind: "exact", Pattern: pattern})
			continue
		}
		if err := validateGlobPattern(pattern); err != nil {
			return nil, err
		}
		matchers = append(matchers, ProxyValueMatcher{Kind: "glob", Pattern: pattern})
	}
	return matchers, nil
}

func parseMatcherPattern(pattern string) (string, string, error) {
	if literal, ok := strings.CutPrefix(pattern, matcherLiteralPrefix); ok {
		return "literal", literal, nil
	}
	globPattern, ok := strings.CutPrefix(pattern, matcherPatternPrefix)
	if !ok {
		return "literal", pattern, nil
	}
	if globPattern == "" {
		return "", "", fmt.Errorf("glob pattern must not be empty")
	}
	return "glob", globPattern, nil
}

func normalizeHookHost(host string) (string, error) {
	normalized := NormalizeProxyHost(host)
	if normalized == "" {
		return "", fmt.Errorf("host must not be empty")
	}
	if strings.Contains(normalized, "*") {
		return "", fmt.Errorf("MITM hook hosts must be exact hosts and cannot contain wildcards")
	}
	return normalized, nil
}

func normalizeMethods(methods []string) ([]string, error) {
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		trimmed := strings.TrimSpace(method)
		if trimmed == "" {
			return nil, fmt.Errorf("methods must not contain empty entries")
		}
		// Keep case-insensitive configuration for standard methods. Extension
		// methods have case-sensitive names that must match what the origin receives.
		out = append(out, standardProxyMethod(trimmed))
	}
	return out, nil
}

var standardProxyMethods = []string{"GET", "HEAD", "POST", "PUT", "DELETE", "CONNECT", "OPTIONS", "TRACE", "PATCH"}

func standardProxyMethod(method string) string {
	for _, standard := range standardProxyMethods {
		if strings.EqualFold(standard, method) {
			return standard
		}
	}
	return method
}

func validateHookOriginConstraints(schemes []string, ports []int) error {
	for _, scheme := range schemes {
		if !strings.EqualFold(scheme, "http") && !strings.EqualFold(scheme, "https") {
			return fmt.Errorf("MITM hook schemes must be HTTP/HTTPS and ports must be nonzero")
		}
	}
	for _, port := range ports {
		if port == 0 {
			return fmt.Errorf("MITM hook schemes must be HTTP/HTTPS and ports must be nonzero")
		}
	}
	return nil
}

func validateQueryConstraints(query map[string][]string) error {
	keys := sortedMapKeys(query)
	for _, name := range keys {
		if name == "" {
			return fmt.Errorf("query keys must not be empty")
		}
		values := query[name]
		if len(values) == 0 {
			return fmt.Errorf("query key %q must list at least one allowed value", name)
		}
		if _, err := compileValueMatchers(values); err != nil {
			return fmt.Errorf("invalid matcher for query key %q: %w", name, err)
		}
	}
	return nil
}

func validateHeaderConstraints(headers map[string][]string) error {
	keys := sortedMapKeys(headers)
	for _, name := range keys {
		if _, err := parseHeaderName(name); err != nil {
			return err
		}
		if _, err := compileValueMatchers(headers[name]); err != nil {
			return fmt.Errorf("invalid matcher for header %q: %w", name, err)
		}
	}
	return nil
}

func validateStripRequestHeaders(names []string) error {
	for _, name := range names {
		if _, err := parseHeaderName(name); err != nil {
			return err
		}
	}
	return nil
}

func validateInjectedHeaders(headers []ProxyInjectedHeaderConfig) error {
	for _, header := range headers {
		name, err := parseHeaderName(header.Name)
		if err != nil {
			return err
		}
		// Rama records User-Agent as a string in its upstream connection spans.
		if name == "User-Agent" {
			return fmt.Errorf("proxy secrets cannot be injected into User-Agent")
		}
		if header.Placeholder != nil {
			if _, forbidden := substitutionForbiddenHeaders[name]; *header.Placeholder == "" || forbidden {
				return fmt.Errorf("invalid secret substitution header or placeholder")
			}
			if err := validateHeaderValue(*header.Placeholder); err != nil {
				return fmt.Errorf("invalid placeholder for injected header %s: %w", header.Name, err)
			}
		}
		switch {
		case header.SecretEnvVar != nil && header.SecretFile == nil:
			if strings.TrimSpace(*header.SecretEnvVar) == "" {
				return fmt.Errorf("secret_env_var must not be empty")
			}
		case header.SecretEnvVar == nil && header.SecretFile != nil:
			if _, err := parseSecretFile(*header.SecretFile); err != nil {
				return err
			}
		default:
			return fmt.Errorf("expected exactly one of secret_env_var or secret_file")
		}
	}
	return nil
}

func compileQueryConstraints(query map[string][]string) ([]ProxyQueryConstraint, error) {
	keys := sortedMapKeys(query)
	constraints := make([]ProxyQueryConstraint, 0, len(keys))
	for _, name := range keys {
		matchers, err := compileValueMatchers(query[name])
		if err != nil {
			return nil, err
		}
		constraints = append(constraints, ProxyQueryConstraint{Name: name, AllowedValues: matchers})
	}
	return constraints, nil
}

func compileHeaderConstraints(headers map[string][]string) ([]ProxyHeaderConstraint, error) {
	keys := sortedMapKeys(headers)
	constraints := make([]ProxyHeaderConstraint, 0, len(keys))
	for _, name := range keys {
		normalized, err := parseHeaderName(name)
		if err != nil {
			return nil, err
		}
		matchers, err := compileValueMatchers(headers[name])
		if err != nil {
			return nil, err
		}
		constraints = append(constraints, ProxyHeaderConstraint{Name: normalized, AllowedValues: matchers})
	}
	return constraints, nil
}

func parseHeaderNames(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, name := range names {
		normalized, err := parseHeaderName(name)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func compileInjectedHeaders(headers []ProxyInjectedHeaderConfig, resolveEnv ProxySecretEnvResolver, readFile ProxySecretFileReader) ([]ProxyResolvedInjectedHeader, error) {
	out := make([]ProxyResolvedInjectedHeader, 0, len(headers))
	for _, header := range headers {
		compiled, err := compileInjectedHeader(&header, resolveEnv, readFile)
		if err != nil {
			return nil, fmt.Errorf("failed to compile injected header %s: %w", header.Name, err)
		}
		out = append(out, compiled)
	}
	return out, nil
}

func compileInjectedHeader(header *ProxyInjectedHeaderConfig, resolveEnv ProxySecretEnvResolver, readFile ProxySecretFileReader) (ProxyResolvedInjectedHeader, error) {
	name, err := parseHeaderName(header.Name)
	if err != nil {
		return ProxyResolvedInjectedHeader{}, err
	}
	var secret string
	var source ProxySecretSource
	switch {
	case header.SecretEnvVar != nil && header.SecretFile == nil:
		value, ok := resolveEnv(*header.SecretEnvVar)
		if !ok {
			return ProxyResolvedInjectedHeader{}, fmt.Errorf("missing required environment variable %s", *header.SecretEnvVar)
		}
		secret = value
		source = ProxySecretSource{Kind: "env_var", Name: *header.SecretEnvVar}
	case header.SecretEnvVar == nil && header.SecretFile != nil:
		path, err := parseSecretFile(*header.SecretFile)
		if err != nil {
			return ProxyResolvedInjectedHeader{}, err
		}
		value, err := readFile(path)
		if err != nil {
			return ProxyResolvedInjectedHeader{}, err
		}
		secret = value
		source = ProxySecretSource{Kind: "file", Name: path}
	default:
		return ProxyResolvedInjectedHeader{}, fmt.Errorf("expected exactly one of secret_env_var or secret_file")
	}
	value := header.Prefix + secret
	if err := validateHeaderValue(value); err != nil {
		return ProxyResolvedInjectedHeader{}, fmt.Errorf("invalid value for injected header %s: %w", header.Name, err)
	}
	placeholder := ""
	if header.Placeholder != nil {
		placeholder = *header.Placeholder
	}
	return ProxyResolvedInjectedHeader{Placeholder: placeholder, Name: name, Value: value, Source: source}, nil
}

var substitutionForbiddenHeaders = map[string]struct{}{
	"Host":              {},
	"Content-Length":    {},
	"Transfer-Encoding": {},
	"Connection":        {},
	"Trailer":           {},
	"Upgrade":           {},
}

func parseHeaderName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("invalid header name %q", name)
	}
	for _, ch := range trimmed {
		if ch > 127 || !isHTTPTokenRune(ch) {
			return "", fmt.Errorf("invalid header name %q", name)
		}
	}
	return textproto.CanonicalMIMEHeaderKey(trimmed), nil
}

func validateHeaderValue(value string) error {
	for _, ch := range value {
		if ch == '\r' || ch == '\n' || ch == 0 {
			return fmt.Errorf("contains invalid control character")
		}
	}
	return nil
}

// ProxySecretFileMaxBytes bounds secret files read for MITM hook actions.
const ProxySecretFileMaxBytes = 64 * 1024

func readProxySecretFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("failed to read secret file %s: %w", path, err)
	}
	if info.Size() > ProxySecretFileMaxBytes {
		return "", fmt.Errorf("secret file %s exceeds %d bytes", path, ProxySecretFileMaxBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read secret file %s: %w", path, err)
	}
	return strings.TrimSpace(string(content)), nil
}

func parseSecretFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("secret_file must not be empty")
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("secret_file must be an absolute path: %q", path)
	}
	return trimmed, nil
}

func isHTTPTokenRune(ch rune) bool {
	if ch < 33 || ch > 126 {
		return false
	}
	switch ch {
	case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"', '/', '[', ']', '?', '=', '{', '}':
		return false
	}
	return true
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sortedMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validateGlobPattern(pattern string) error {
	_, err := compileGlob(pattern, false)
	if err != nil {
		return fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
	}
	return nil
}

func globMatch(pattern string, candidate string, literalSeparator bool) bool {
	matcher, err := compileGlob(pattern, literalSeparator)
	if err != nil {
		return false
	}
	return matcher.Match(candidate)
}

func compileGlob(pattern string, literalSeparator bool) (glob.Glob, error) {
	if literalSeparator {
		return glob.Compile(pattern, '/')
	}
	return glob.Compile(pattern)
}
