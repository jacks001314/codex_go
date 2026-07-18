package network

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/textproto"
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
	ContextEnvVars     []string
	Sources            []ProxyCredentialSource
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
	return &ProxyCredentialBroker{enabled: enabled}
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
	for _, provider := range credentialProviders() {
		for _, source := range provider.Sources {
			hostBinding, ok := source.HostBinding(env)
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
	return false
}

func (b *ProxyCredentialBroker) InjectRequestHeaders(host string, headers map[string][]string) {
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
		if (&b.credentials[index]).MatchesHost(normalized) {
			matching = append(matching, &b.credentials[index])
		}
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
	seen := map[string]bool{}
	keys := []string{}
	for _, provider := range credentialProviders() {
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
	for _, key := range ProxyCredentialBrokerEnvKeys() {
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
