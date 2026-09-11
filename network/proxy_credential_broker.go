package network

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/textproto"
	"os"
	"runtime"
	"strings"
	"sync"
)

const (
	CredentialBrokerActiveEnvKey = "CODEX_NETWORK_PROXY_CREDENTIAL_BROKER_ACTIVE"
	BrokeredCredentialsEnvKey    = "CODEX_NETWORK_PROXY_BROKERED_CREDENTIALS"
)

type ProxyCredentialBroker struct {
	mu          sync.RWMutex
	enabled     bool
	providers   []*ProxyCredentialProvider
	hints       map[string]string
	credentials []ProxyCredentialRecord
}

type ProxyCredentialRecord struct {
	EnvVar      string
	Provider    *ProxyCredentialProvider
	HostBinding ProxyCredentialHostBinding
	RealValue   string
	DummyValue  string
}

type ProxyCredentialProvider struct {
	ContextEnvVars []string
	Sources        []ProxyCredentialSource
	// Destinations are the authorized injection destinations for configured
	// providers (#44056); built-in providers leave this nil and bind hosts
	// directly.
	Destinations []CredentialDestination
	// DestinationEnvKeys holds environment variables that contribute a
	// destination at runtime (configured url_prefix_from_env, #44068).
	DestinationEnvKeys []string
	DummyValue         func(string) string
	RequestHeader      func(map[string][]string) (string, bool)
	RequestHeaderValue func(string) (string, bool)
	InsertHeader       func(map[string][]string, string)
}

type ProxyCredentialSource struct {
	EnvVars     []string
	HostBinding func(map[string]string) (ProxyCredentialHostBinding, bool)
}

type ProxyCredentialHostBinding struct {
	ExactHosts []string
	Suffixes   []string
}

func NewProxyCredentialBroker(enabled bool) *ProxyCredentialBroker {
	return &ProxyCredentialBroker{enabled: enabled, providers: credentialProviders()}
}

// NewProxyCredentialBrokerWithProviders builds a broker that also serves
// declarative, config-backed credential families (#44056).
func NewProxyCredentialBrokerWithProviders(enabled bool, configured []*ProxyCredentialProvider) *ProxyCredentialBroker {
	providers := append([]*ProxyCredentialProvider(nil), credentialProviders()...)
	for _, provider := range configured {
		if provider != nil {
			providers = append(providers, provider)
		}
	}
	return &ProxyCredentialBroker{enabled: enabled, providers: providers}
}

func (b *ProxyCredentialBroker) Enabled() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.enabled
}

func (b *ProxyCredentialBroker) VirtualizeChildEnv(env map[string]string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.enabled {
		delete(env, CredentialBrokerActiveEnvKey)
		delete(env, BrokeredCredentialsEnvKey)
		return
	}
	env[CredentialBrokerActiveEnvKey] = "1"
	resolvedEnv := envWithHintFallbacks(env, b.hints)
	for _, provider := range b.providers {
		for _, source := range provider.Sources {
			hostBinding, ok := source.HostBinding(resolvedEnv)
			if !ok {
				continue
			}
			for _, envVar := range source.EnvVars {
				b.virtualizeEnvVar(env, envVar, provider, hostBinding)
			}
		}
	}
	b.updateBrokeredCredentialsMarker(env)
}

func (b *ProxyCredentialBroker) HostRequiresMITM(host string) bool {
	if b == nil {
		return false
	}
	normalized := NormalizeProxyHost(host)
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.enabled {
		return false
	}
	for index := range b.credentials {
		if (&b.credentials[index]).MatchesHost(normalized) {
			return true
		}
	}
	// Declarative providers require MITM for their HTTPS destinations even
	// before any command has virtualized credentials into their env (#44056).
	for _, provider := range b.providers {
		for _, destination := range provider.Destinations {
			if destination.Scheme != "https" {
				continue
			}
			if destination.Wildcard {
				if strings.HasSuffix(normalized, "."+destination.Host) {
					return true
				}
				continue
			}
			if normalized == destination.Host {
				return true
			}
		}
	}
	return false
}

// HostRequiresHTTPInterception mirrors Rust
// credential_broker.rs host_protocols_for_environment's `http` bit: a
// configured provider destination that authorizes plaintext HTTP for the
// destination requires the tunnel to be intercepted as plaintext HTTP rather
// than relayed opaquely (#44089).
func (b *ProxyCredentialBroker) HostRequiresHTTPInterception(host string, port uint16) bool {
	if b == nil {
		return false
	}
	normalized := NormalizeProxyHost(host)
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.enabled {
		return false
	}
	matches := func(provider *ProxyCredentialProvider) bool {
		if provider == nil {
			return false
		}
		for _, destination := range provider.Destinations {
			if destination.Scheme == "http" && destination.MatchesHost(normalized, port) {
				return true
			}
		}
		return false
	}
	for index := range b.credentials {
		if matches(b.credentials[index].Provider) {
			return true
		}
	}
	for _, provider := range b.providers {
		if matches(provider) {
			return true
		}
	}
	return false
}

func (b *ProxyCredentialBroker) InjectRequestHeaders(host string, headers map[string][]string) {
	b.InjectRequestHeadersForDestination("", host, 0, "", headers)
}

// InjectRequestHeadersForDestination injects the matching credential for a
// concrete request destination. Configured providers are only authorized for
// destinations they declared (scheme/host/port/path); built-in providers keep
// their host-binding behavior (network-proxy/src/credential_broker.rs #44056).
func (b *ProxyCredentialBroker) InjectRequestHeadersForDestination(scheme string, host string, port uint16, path string, headers map[string][]string) {
	if b == nil {
		return
	}
	normalized := NormalizeProxyHost(host)
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.enabled {
		return
	}
	matching := make([]*ProxyCredentialRecord, 0)
	for index := range b.credentials {
		credential := &b.credentials[index]
		if !credential.MatchesHost(normalized) {
			continue
		}
		if credential.Provider != nil && !credential.Provider.authorizesDestination(scheme, normalized, port, path) {
			continue
		}
		matching = append(matching, credential)
	}
	credential := selectCredential(headers, matching)
	if credential == nil {
		return
	}
	headerValue, ok := credential.Provider.RequestHeaderValue(credential.RealValue)
	if !ok {
		return
	}
	credential.Provider.InsertHeader(headers, headerValue)
}

// authorizesDestination reports whether a provider may substitute credentials
// for the request destination. Providers without declared destinations are the
// built-ins, whose credential records already carry a host binding.
func (p *ProxyCredentialProvider) authorizesDestination(scheme string, host string, port uint16, path string) bool {
	if p == nil || len(p.Destinations) == 0 {
		return true
	}
	if scheme == "" {
		for _, destination := range p.Destinations {
			if destination.Wildcard && destination.MatchesHost(host, destination.Port) {
				return true
			}
		}
		return false
	}
	for _, destination := range p.Destinations {
		if destination.MatchesRequest(scheme, host, port, path) {
			return true
		}
	}
	return false
}

func ProxyBrokeredCredentialDummyEnvKeys(env map[string]string) []string {
	marker, ok := env[BrokeredCredentialsEnvKey]
	if !ok {
		return nil
	}
	var entries [][2]string
	if err := json.Unmarshal([]byte(marker), &entries); err != nil {
		return nil
	}
	supported := map[string]bool{}
	for _, key := range ProxyCredentialBrokerEnvKeys() {
		supported[key] = true
	}
	out := []string{}
	for _, entry := range entries {
		key := entry[0]
		dummyValue := entry[1]
		if supported[key] && env[key] == dummyValue {
			out = append(out, key)
		}
	}
	return out
}

func ProxyBrokeredCredentialEnvKeys(env map[string]string) []string {
	if env[CredentialBrokerActiveEnvKey] != "1" {
		return nil
	}
	return ProxyCredentialBrokerEnvKeys()
}

func ProxyCredentialBrokerEnvKeys() []string {
	return credentialEnvKeys(credentialProviders())
}

func credentialEnvKeys(providers []*ProxyCredentialProvider) []string {
	seen := map[string]bool{}
	keys := []string{}
	for _, provider := range providers {
		for _, key := range provider.ContextEnvVars {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
		for _, source := range provider.Sources {
			for _, key := range source.EnvVars {
				if !seen[key] {
					seen[key] = true
					keys = append(keys, key)
				}
			}
		}
	}
	return keys
}

// envKeys returns every environment key the broker virtualizes, including
// declarative providers (#44056).
func (b *ProxyCredentialBroker) envKeys() []string {
	if b == nil {
		return nil
	}
	return credentialEnvKeys(b.providers)
}

// SetDestinationHints retains local destination context (built-in provider
// context keys such as GH_HOST and configured url_prefix_from_env values)
// without adding it to child environments, mirroring Rust
// CredentialBrokerContext::capture (#44068). A value present in the supplied
// environment wins; otherwise the process environment is consulted.
func (b *ProxyCredentialBroker) SetDestinationHints(env map[string]string) {
	if b == nil {
		return
	}
	hints := map[string]string{}
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		if value, ok := env[key]; ok {
			hints[key] = value
			return
		}
		if value, ok := os.LookupEnv(key); ok {
			hints[key] = value
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, provider := range b.providers {
		for _, key := range provider.ContextEnvVars {
			add(key)
		}
		for _, key := range provider.DestinationEnvKeys {
			add(key)
		}
	}
	b.hints = hints
	if runtime.GOOS == "windows" && b.enabled && b.hasAmbiguousWindowsProviderEnv(env) {
		slog.Warn("credential brokerage disabled because shell environment overrides contain conflicting case-insensitive provider keys")
		b.enabled = false
	}
}

// hasAmbiguousWindowsProviderEnv mirrors #44068: on Windows, provider overrides
// that differ only by case with different values make brokerage ambiguous, so
// it is disabled rather than guessing which key wins.
func (b *ProxyCredentialBroker) hasAmbiguousWindowsProviderEnv(env map[string]string) bool {
	providerKeys := map[string]bool{}
	markProviderKey := func(key string) {
		if key = strings.TrimSpace(key); key != "" {
			providerKeys[strings.ToLower(key)] = true
		}
	}
	for _, provider := range b.providers {
		for _, key := range provider.ContextEnvVars {
			markProviderKey(key)
		}
		for _, key := range provider.DestinationEnvKeys {
			markProviderKey(key)
		}
		for _, source := range provider.Sources {
			for _, key := range source.EnvVars {
				markProviderKey(key)
			}
		}
	}
	for key, value := range env {
		if !providerKeys[strings.ToLower(key)] {
			continue
		}
		for candidate, candidateValue := range env {
			if candidate == key || !strings.EqualFold(candidate, key) {
				continue
			}
			if value != candidateValue {
				return true
			}
		}
	}
	return false
}

// envWithHintFallbacks resolves destination hints for a child environment
// without changing what the child sees: a present value - including an empty
// one - overrides the fallback (Rust CredentialBrokerContext::with_fallbacks).
func envWithHintFallbacks(env map[string]string, hints map[string]string) map[string]string {
	if len(hints) == 0 {
		return env
	}
	var resolved map[string]string
	for key, value := range hints {
		if _, present := env[key]; present {
			continue
		}
		if resolved == nil {
			resolved = make(map[string]string, len(env)+len(hints))
			for envKey, envValue := range env {
				resolved[envKey] = envValue
			}
		}
		resolved[key] = value
	}
	if resolved == nil {
		return env
	}
	return resolved
}

func (r *ProxyCredentialRecord) MatchesHost(host string) bool {
	if r == nil {
		return false
	}
	return (&r.HostBinding).MatchesHost(host)
}

func (b *ProxyCredentialBroker) virtualizeEnvVar(env map[string]string, envVar string, provider *ProxyCredentialProvider, hostBinding ProxyCredentialHostBinding) {
	realValue, ok := brokerableCredentialValue(env, b.credentials, envVar, provider)
	if !ok {
		return
	}
	dummyValue := b.register(envVar, provider, hostBinding, realValue)
	env[envVar] = dummyValue
}

func (b *ProxyCredentialBroker) register(envVar string, provider *ProxyCredentialProvider, hostBinding ProxyCredentialHostBinding, realValue string) string {
	for _, credential := range b.credentials {
		if credential.EnvVar == envVar && credential.Provider == provider && (&credential.HostBinding).Equal(hostBinding) && credential.RealValue == realValue {
			return credential.DummyValue
		}
	}
	dummyValue := ""
	for dummyValue == "" || dummyValue == realValue || b.isDummyValue(dummyValue) {
		dummyValue = provider.DummyValue(realValue)
	}
	b.credentials = append(b.credentials, ProxyCredentialRecord{
		EnvVar:      envVar,
		Provider:    provider,
		HostBinding: hostBinding,
		RealValue:   realValue,
		DummyValue:  dummyValue,
	})
	return dummyValue
}

func (b *ProxyCredentialBroker) isDummyValue(value string) bool {
	for _, credential := range b.credentials {
		if credential.DummyValue == value {
			return true
		}
	}
	return false
}

func (b *ProxyCredentialBroker) updateBrokeredCredentialsMarker(env map[string]string) {
	entries := [][2]string{}
	for _, key := range b.envKeys() {
		value, ok := env[key]
		if ok && b.isDummyValue(value) {
			entries = append(entries, [2]string{key, value})
		}
	}
	if data, err := json.Marshal(entries); err == nil {
		env[BrokeredCredentialsEnvKey] = string(data)
	} else {
		delete(env, BrokeredCredentialsEnvKey)
	}
}

func (h *ProxyCredentialHostBinding) MatchesHost(host string) bool {
	if h == nil {
		return false
	}
	normalized := NormalizeProxyHost(host)
	for _, exact := range h.ExactHosts {
		if normalized == NormalizeProxyHost(exact) {
			return true
		}
	}
	for _, suffix := range h.Suffixes {
		if strings.HasSuffix(normalized, strings.ToLower(suffix)) {
			return true
		}
	}
	return false
}

func (h *ProxyCredentialHostBinding) Equal(other ProxyCredentialHostBinding) bool {
	if h == nil {
		return false
	}
	if len(h.ExactHosts) != len(other.ExactHosts) || len(h.Suffixes) != len(other.Suffixes) {
		return false
	}
	for index := range h.ExactHosts {
		if h.ExactHosts[index] != other.ExactHosts[index] {
			return false
		}
	}
	for index := range h.Suffixes {
		if h.Suffixes[index] != other.Suffixes[index] {
			return false
		}
	}
	return true
}

func brokerableCredentialValue(env map[string]string, credentials []ProxyCredentialRecord, envVar string, provider *ProxyCredentialProvider) (string, bool) {
	realValue := strings.TrimSpace(env[envVar])
	if realValue == "" {
		return "", false
	}
	for _, credential := range credentials {
		if credential.DummyValue == realValue {
			return "", false
		}
	}
	if _, ok := provider.RequestHeaderValue(realValue); !ok {
		return "", false
	}
	return realValue, true
}

func selectCredential(headers map[string][]string, matching []*ProxyCredentialRecord) *ProxyCredentialRecord {
	var selected *ProxyCredentialRecord
	for _, credential := range matching {
		headerValue, ok := credential.Provider.RequestHeader(headers)
		if !ok || !strings.Contains(headerValue, credential.DummyValue) {
			continue
		}
		if selected != nil {
			return nil
		}
		selected = credential
	}
	return selected
}

func credentialProviders() []*ProxyCredentialProvider {
	return []*ProxyCredentialProvider{githubCredentialProvider(), openAICredentialProvider()}
}

func githubCredentialProvider() *ProxyCredentialProvider {
	return &githubProvider
}

func openAICredentialProvider() *ProxyCredentialProvider {
	return &openAIProvider
}

var githubProvider = ProxyCredentialProvider{
	ContextEnvVars: []string{"GH_HOST"},
	Sources: []ProxyCredentialSource{
		{
			EnvVars: []string{"GH_TOKEN", "GITHUB_TOKEN"},
			HostBinding: func(map[string]string) (ProxyCredentialHostBinding, bool) {
				return ProxyCredentialHostBinding{
					ExactHosts: []string{"api.github.com", "github.com"},
					Suffixes:   []string{".ghe.com"},
				}, true
			},
		},
		{
			EnvVars: []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"},
			HostBinding: func(env map[string]string) (ProxyCredentialHostBinding, bool) {
				host := githubHostHint(env)
				if host == "" || githubCloudHost(host) {
					return ProxyCredentialHostBinding{}, false
				}
				return ProxyCredentialHostBinding{ExactHosts: []string{host}}, true
			},
		},
	},
	DummyValue:         githubDummyValue,
	RequestHeader:      authorizationHeader,
	RequestHeaderValue: bearerHeaderValue,
	InsertHeader:       insertAuthorizationHeader,
}

var openAIProvider = ProxyCredentialProvider{
	Sources: []ProxyCredentialSource{
		{
			EnvVars: []string{"OPENAI_API_KEY"},
			HostBinding: func(map[string]string) (ProxyCredentialHostBinding, bool) {
				return ProxyCredentialHostBinding{ExactHosts: []string{"api.openai.com"}}, true
			},
		},
	},
	DummyValue:         openAIDummyValue,
	RequestHeader:      authorizationHeader,
	RequestHeaderValue: bearerHeaderValue,
	InsertHeader:       insertAuthorizationHeader,
}

func githubDummyValue(realValue string) string {
	return shapedDummyValue(realValue, githubTokenPrefix(realValue), 40)
}

func openAIDummyValue(realValue string) string {
	return shapedDummyValue(realValue, openAIAPIKeyPrefix(realValue), 51)
}

func authorizationHeader(headers map[string][]string) (string, bool) {
	values := headers[textproto.CanonicalMIMEHeaderKey("authorization")]
	if len(values) == 0 {
		values = headers["authorization"]
	}
	if len(values) == 0 {
		values = headers["AUTHORIZATION"]
	}
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func bearerHeaderValue(value string) (string, bool) {
	headerValue := "Bearer " + value
	if err := validateHeaderValue(headerValue); err != nil {
		return "", false
	}
	return headerValue, true
}

func insertAuthorizationHeader(headers map[string][]string, value string) {
	headers[textproto.CanonicalMIMEHeaderKey("authorization")] = []string{value}
}

func githubCloudHost(host string) bool {
	normalized := NormalizeProxyHost(host)
	if normalized == "api.github.com" || normalized == "github.com" {
		return true
	}
	return strings.HasSuffix(normalized, ".ghe.com")
}

func githubTokenPrefix(value string) string {
	prefixes := []string{"github_pat_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return prefix
		}
	}
	return "ghp_"
}

func githubHostHint(env map[string]string) string {
	return NormalizeProxyHost(env["GH_HOST"])
}

func openAIAPIKeyPrefix(value string) string {
	if !strings.HasPrefix(value, "sk-") {
		return "sk-"
	}
	suffix := strings.TrimPrefix(value, "sk-")
	separator := strings.Index(suffix, "-")
	if separator < 0 {
		return "sk-"
	}
	return value[:separator+4]
}

func shapedDummyValue(realValue string, prefix string, minimumLen int) string {
	targetLen := len(realValue)
	if targetLen < minimumLen {
		targetLen = minimumLen
	}
	if targetLen < len(prefix)+16 {
		targetLen = len(prefix) + 16
	}
	seed := randomCredentialSeed(targetLen)
	if seed == "" {
		seed = hashedCredentialSeed(realValue, targetLen)
	}
	var builder strings.Builder
	builder.Grow(targetLen)
	builder.WriteString(prefix)
	for index := len(prefix); index < targetLen; index++ {
		template := byte(0)
		if index < len(realValue) {
			template = realValue[index]
		}
		if template != 0 && !isASCIIAlphanumeric(template) {
			builder.WriteByte(template)
			continue
		}
		builder.WriteByte(seed[index%len(seed)])
	}
	return builder.String()
}

func randomCredentialSeed(length int) string {
	if length <= 0 {
		return ""
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	for index, value := range buf {
		buf[index] = alphabet[int(value)%len(alphabet)]
	}
	return string(buf)
}

func hashedCredentialSeed(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	text := hex.EncodeToString(sum[:])
	if length <= len(text) {
		return text
	}
	var builder strings.Builder
	for builder.Len() < length {
		builder.WriteString(text)
	}
	return builder.String()[:length]
}

func isASCIIAlphanumeric(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
