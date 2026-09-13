package config

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"codex_go/protocol"
)

// Rust parity: codex-rs/core/src/config/otel.rs (resolve_config with
// resolve_span_attributes / resolve_tracestate) and the validators it calls in
// codex-otel, plus the codex_config::types::OtelConfig shape it produces.
//
// Go has no OTEL provider yet, so this resolves the effective `otel` settings
// (exporter kinds, environment, log_user_prompt, and the tool-result log byte
// limit) for a future provider. The user-visible half of the resolution is the
// trace metadata: malformed `otel.span_attributes` / `otel.tracestate` entries
// are filtered out and reported through config.startup_warnings rather than
// failing startup.

// DefaultOtelEnvironment mirrors Rust's DEFAULT_OTEL_ENVIRONMENT.
const DefaultOtelEnvironment = "dev"

// Otel exporter kind tags, mirroring codex_config::types::OtelExporterKind's
// kebab-case serde representation.
const (
	OtelExporterKindNone     = "none"
	OtelExporterKindStatsig  = "statsig"
	OtelExporterKindOtlpHTTP = "otlp-http"
	OtelExporterKindOtlpGRPC = "otlp-grpc"
)

// OTLP HTTP protocol tags, mirroring OtelHttpProtocol.
const (
	OtelHTTPProtocolBinary = "binary"
	OtelHTTPProtocolJSON   = "json"
)

// OtelTLSConfig mirrors codex_config::types::OtelTlsConfig: the paths of the CA
// certificate and the optional mTLS client identity.
type OtelTLSConfig struct {
	CACertificate     string
	ClientCertificate string
	ClientPrivateKey  string
}

// OtelExporterKind mirrors codex_config::types::OtelExporterKind. Kind selects
// the variant; the remaining fields are populated for the OTLP variants.
type OtelExporterKind struct {
	Kind     string
	Endpoint string
	Headers  map[string]string
	Protocol string
	TLS      *OtelTLSConfig
}

// OtelConfig mirrors codex_config::types::OtelConfig: the effective settings
// after defaults are applied.
type OtelConfig struct {
	ToolResult      protocol.ToolResultLogConfig
	LogUserPrompt   bool
	Environment     string
	Exporter        OtelExporterKind
	TraceExporter   OtelExporterKind
	MetricsExporter OtelExporterKind
	SpanAttributes  map[string]string
	Tracestate      map[string]map[string]string
}

// DefaultOtelConfig mirrors OtelConfig::default: no log/trace exporter, the
// Statsig metrics route, the dev environment, and the default tool-result byte
// limit.
func DefaultOtelConfig() OtelConfig {
	return OtelConfig{
		ToolResult:      protocol.DefaultToolResultLogConfig(),
		Environment:     DefaultOtelEnvironment,
		Exporter:        OtelExporterKind{Kind: OtelExporterKindNone},
		TraceExporter:   OtelExporterKind{Kind: OtelExporterKindNone},
		MetricsExporter: OtelExporterKind{Kind: OtelExporterKindStatsig},
		SpanAttributes:  map[string]string{},
		Tracestate:      map[string]map[string]string{},
	}
}

// ResolveOtelConfig mirrors codex-rs/core/src/config/otel.rs::resolve_config:
// apply the defaults, keep the raw exporter kinds, and sanitize the trace
// metadata (the only part that can warn rather than fail startup).
func ResolveOtelConfig(values map[string]any) (OtelConfig, []string) {
	resolved := DefaultOtelConfig()
	otel, _ := values["otel"].(map[string]any)
	if otel == nil {
		return resolved, nil
	}
	if raw, ok := otelConfigInt(otel["tool_result"]); ok {
		resolved.ToolResult = protocol.ToolResultLogConfig{MaxBytes: raw}
	}
	if logUserPrompt, ok := otel["log_user_prompt"].(bool); ok {
		resolved.LogUserPrompt = logUserPrompt
	}
	if environment, ok := otel["environment"].(string); ok && environment != "" {
		resolved.Environment = environment
	}
	if exporter, ok := otelExporterKind(otel["exporter"]); ok {
		resolved.Exporter = exporter
	}
	if exporter, ok := otelExporterKind(otel["trace_exporter"]); ok {
		resolved.TraceExporter = exporter
	}
	if exporter, ok := otelExporterKind(otel["metrics_exporter"]); ok {
		resolved.MetricsExporter = exporter
	}
	metadata, warnings := ResolveOtelTraceMetadata(values)
	resolved.SpanAttributes = metadata.SpanAttributes
	resolved.Tracestate = metadata.Tracestate
	return resolved, warnings
}

// ValidateOtelConfigValues mirrors the typed deserialization of Rust's
// OtelConfigToml: every field has a required shape, so a wrong shape (a
// non-table `otel`, a non-string span attribute, an unknown exporter variant, a
// missing OTLP endpoint or protocol, and so on) fails the config load. Unknown
// fields are still ignored, as serde does for this struct.
func ValidateOtelConfigValues(values map[string]any) error {
	if values == nil {
		return nil
	}
	raw, ok := values["otel"]
	if !ok || raw == nil {
		return nil
	}
	otel, ok := raw.(map[string]any)
	if !ok {
		return errors.New("otel must be a table")
	}
	if value, ok := otel["log_user_prompt"]; ok && value != nil {
		if _, isBool := value.(bool); !isBool {
			return errors.New("otel.log_user_prompt must be a boolean")
		}
	}
	if value, ok := otel["environment"]; ok && value != nil {
		if _, isString := value.(string); !isString {
			return errors.New("otel.environment must be a string")
		}
	}
	if value, ok := otel["tool_result"]; ok && value != nil {
		table, isTable := value.(map[string]any)
		if !isTable {
			return errors.New("otel.tool_result must be a table")
		}
		if maxBytes, ok := table["max_bytes"]; ok && maxBytes != nil {
			if _, valid := otelConfigIntValue(maxBytes); !valid {
				return errors.New("otel.tool_result.max_bytes must be a non-negative integer")
			}
		}
	}
	if value, ok := otel["span_attributes"]; ok && value != nil {
		table, isTable := value.(map[string]any)
		if !isTable {
			return errors.New("otel.span_attributes must be a table")
		}
		for _, key := range sortedStringKeys(table) {
			if _, isString := table[key].(string); !isString {
				return fmt.Errorf("otel.span_attributes.%s must be a string", key)
			}
		}
	}
	if value, ok := otel["tracestate"]; ok && value != nil {
		table, isTable := value.(map[string]any)
		if !isTable {
			return errors.New("otel.tracestate must be a table")
		}
		for _, member := range sortedStringKeys(table) {
			fields, isTable := table[member].(map[string]any)
			if !isTable {
				return fmt.Errorf("otel.tracestate.%s must be a table", member)
			}
			for _, field := range sortedStringKeys(fields) {
				if _, isString := fields[field].(string); !isString {
					return fmt.Errorf("otel.tracestate.%s.%s must be a string", member, field)
				}
			}
		}
	}
	for _, key := range []string{"exporter", "trace_exporter", "metrics_exporter"} {
		value, ok := otel[key]
		if !ok || value == nil {
			continue
		}
		if err := validateOtelExporterKind(key, value); err != nil {
			return err
		}
	}
	return nil
}

// validateOtelExporterKind mirrors serde's externally tagged OtelExporterKind:
// "none"/"statsig", or a single-key otlp-http/otlp-grpc table with the required
// endpoint (and, for HTTP, the protocol).
func validateOtelExporterKind(key string, value any) error {
	switch typed := value.(type) {
	case string:
		if typed != OtelExporterKindNone && typed != OtelExporterKindStatsig {
			return fmt.Errorf("otel.%s has an unknown exporter variant %q", key, typed)
		}
		return nil
	case map[string]any:
		if len(typed) != 1 {
			return fmt.Errorf("otel.%s must name exactly one exporter variant", key)
		}
		for variant, raw := range typed {
			table, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("otel.%s.%s must be a table", key, variant)
			}
			switch variant {
			case OtelExporterKindOtlpHTTP:
				if _, ok := table["endpoint"].(string); !ok {
					return fmt.Errorf("otel.%s.%s.endpoint must be a string", key, variant)
				}
				protocol, ok := table["protocol"].(string)
				if !ok {
					return fmt.Errorf("otel.%s.%s.protocol must be a string", key, variant)
				}
				if protocol != OtelHTTPProtocolBinary && protocol != OtelHTTPProtocolJSON {
					return fmt.Errorf("otel.%s.%s has an unknown protocol %q", key, variant, protocol)
				}
			case OtelExporterKindOtlpGRPC:
				if _, ok := table["endpoint"].(string); !ok {
					return fmt.Errorf("otel.%s.%s.endpoint must be a string", key, variant)
				}
			default:
				return fmt.Errorf("otel.%s has an unknown exporter variant %q", key, variant)
			}
			if err := validateOtelHeaders(key, variant, table["headers"]); err != nil {
				return err
			}
			if err := validateOtelExporterTLS(key, variant, table["tls"]); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("otel.%s must be an exporter name or table", key)
	}
}

func validateOtelHeaders(key string, variant string, value any) error {
	if value == nil {
		return nil
	}
	headers, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("otel.%s.%s.headers must be a table", key, variant)
	}
	for _, header := range sortedStringKeys(headers) {
		if _, isString := headers[header].(string); !isString {
			return fmt.Errorf("otel.%s.%s.headers.%s must be a string", key, variant, header)
		}
	}
	return nil
}

func validateOtelExporterTLS(key string, variant string, value any) error {
	if value == nil {
		return nil
	}
	table, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("otel.%s.%s.tls must be a table", key, variant)
	}
	for _, field := range []string{"ca_certificate", "client_certificate", "client_private_key"} {
		raw, ok := table[field]
		if !ok || raw == nil {
			continue
		}
		if _, isString := raw.(string); !isString {
			return fmt.Errorf("otel.%s.%s.tls.%s must be a string", key, variant, field)
		}
	}
	return nil
}

// Otel returns the resolved OTEL settings for this config (Rust's
// `Config::otel`), applying the `OtelConfig` defaults to the raw values.
func (c *Config) Otel() OtelConfig {
	if c == nil {
		return DefaultOtelConfig()
	}
	resolved, _ := ResolveOtelConfig(c.Values)
	return resolved
}

// otelExporterKind parses one exporter kind from the untyped config values. An
// absent or malformed value keeps the caller's default, which mirrors the Go
// loader's tolerant shape handling (Rust's typed deserialization would fail
// startup instead).
func otelExporterKind(value any) (OtelExporterKind, bool) {
	switch typed := value.(type) {
	case string:
		switch typed {
		case OtelExporterKindNone:
			return OtelExporterKind{Kind: OtelExporterKindNone}, true
		case OtelExporterKindStatsig:
			return OtelExporterKind{Kind: OtelExporterKindStatsig}, true
		}
	case map[string]any:
		if len(typed) != 1 {
			return OtelExporterKind{}, false
		}
		for kind, raw := range typed {
			switch kind {
			case OtelExporterKindOtlpHTTP:
				return otelOtlpHTTPExporter(raw)
			case OtelExporterKindOtlpGRPC:
				return otelOtlpGRPCExporter(raw)
			}
		}
	}
	return OtelExporterKind{}, false
}

func otelOtlpHTTPExporter(value any) (OtelExporterKind, bool) {
	table, ok := value.(map[string]any)
	if !ok {
		return OtelExporterKind{}, false
	}
	endpoint, ok := table["endpoint"].(string)
	if !ok || endpoint == "" {
		return OtelExporterKind{}, false
	}
	// Rust's OtlpHttpKind has no serde default for protocol, so a missing or
	// unknown protocol is not a valid exporter.
	protocol, ok := table["protocol"].(string)
	if !ok || (protocol != OtelHTTPProtocolBinary && protocol != OtelHTTPProtocolJSON) {
		return OtelExporterKind{}, false
	}
	return OtelExporterKind{
		Kind:     OtelExporterKindOtlpHTTP,
		Endpoint: endpoint,
		Headers:  otelStringMap(table["headers"]),
		Protocol: protocol,
		TLS:      otelTLSConfig(table["tls"]),
	}, true
}

func otelOtlpGRPCExporter(value any) (OtelExporterKind, bool) {
	table, ok := value.(map[string]any)
	if !ok {
		return OtelExporterKind{}, false
	}
	endpoint, ok := table["endpoint"].(string)
	if !ok || endpoint == "" {
		return OtelExporterKind{}, false
	}
	return OtelExporterKind{
		Kind:     OtelExporterKindOtlpGRPC,
		Endpoint: endpoint,
		Headers:  otelStringMap(table["headers"]),
		TLS:      otelTLSConfig(table["tls"]),
	}, true
}

// otelTLSConfig reads `tls` as an OtelTlsConfig table; absent means "no
// explicit TLS settings".
func otelTLSConfig(value any) *OtelTLSConfig {
	table, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	config := &OtelTLSConfig{}
	if path, ok := table["ca_certificate"].(string); ok {
		config.CACertificate = path
	}
	if path, ok := table["client_certificate"].(string); ok {
		config.ClientCertificate = path
	}
	if path, ok := table["client_private_key"].(string); ok {
		config.ClientPrivateKey = path
	}
	return config
}

// otelConfigInt reads `otel.tool_result.max_bytes` from its table.
func otelConfigInt(value any) (int, bool) {
	table, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	return otelConfigIntValue(table["max_bytes"])
}

// otelConfigIntValue reads a non-negative integer config value (int64, int, an
// integral float64, or a numeric string).
func otelConfigIntValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int64:
		if typed >= 0 {
			return int(typed), true
		}
	case int:
		if typed >= 0 {
			return typed, true
		}
	case float64:
		if typed >= 0 && typed == float64(int64(typed)) {
			return int(typed), true
		}
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil && parsed >= 0 {
			return int(parsed), true
		}
	}
	return 0, false
}

// OtelTraceMetadata is the resolved trace metadata after Rust's filtering.
type OtelTraceMetadata struct {
	// SpanAttributes are the valid `otel.span_attributes` entries.
	SpanAttributes map[string]string
	// Tracestate holds the valid tracestate members and their fields.
	Tracestate map[string]map[string]string
}

// OtelStartupWarnings returns the invalid-`otel.*`-config startup warnings for
// the configured trace metadata.
func OtelStartupWarnings(values map[string]any) []string {
	_, warnings := ResolveOtelTraceMetadata(values)
	return warnings
}

// ResolveOtelTraceMetadata mirrors Rust's resolve_config trace-metadata half:
// valid entries are kept, malformed ones are dropped with a startup warning.
func ResolveOtelTraceMetadata(values map[string]any) (OtelTraceMetadata, []string) {
	otel, _ := values["otel"].(map[string]any)
	metadata := OtelTraceMetadata{
		SpanAttributes: map[string]string{},
		Tracestate:     map[string]map[string]string{},
	}
	if otel == nil {
		return metadata, nil
	}
	var warnings []string
	metadata.SpanAttributes = resolveOtelSpanAttributes(otelStringMap(otel["span_attributes"]), &warnings)
	metadata.Tracestate = resolveOtelTracestate(otelTracestateMap(otel["tracestate"]), &warnings)
	return metadata, warnings
}

func resolveOtelSpanAttributes(attributes map[string]string, warnings *[]string) map[string]string {
	valid := map[string]string{}
	for _, key := range sortedStringKeys(attributes) {
		if err := validateOtelSpanAttribute(key); err != nil {
			pushInvalidOtelConfigWarning("otel.span_attributes", err, warnings)
			continue
		}
		valid[key] = attributes[key]
	}
	return valid
}

func resolveOtelTracestate(tracestate map[string]map[string]string, warnings *[]string) map[string]map[string]string {
	valid := map[string]map[string]string{}
	for _, memberKey := range sortedStringKeys(tracestate) {
		fields := resolveOtelTracestateMemberFields(memberKey, tracestate[memberKey], warnings)
		if len(fields) == 0 {
			continue
		}
		if err := validateOtelTracestateMember(memberKey, fields); err != nil {
			pushInvalidOtelConfigWarning("otel.tracestate", err, warnings)
			continue
		}
		valid[memberKey] = fields
	}
	// Members can be valid individually while the combined W3C tracestate header
	// is not, so validate the filtered set before accepting it.
	if err := validateOtelTracestateEntries(valid); err != nil {
		pushInvalidOtelConfigWarning("otel.tracestate", err, warnings)
		return map[string]map[string]string{}
	}
	return valid
}

func resolveOtelTracestateMemberFields(memberKey string, fields map[string]string, warnings *[]string) map[string]string {
	valid := map[string]string{}
	for _, fieldKey := range sortedStringKeys(fields) {
		single := map[string]string{fieldKey: fields[fieldKey]}
		if err := validateOtelTracestateMember(memberKey, single); err != nil {
			pushInvalidOtelConfigWarning("otel.tracestate", err, warnings)
			continue
		}
		valid[fieldKey] = fields[fieldKey]
	}
	return valid
}

func pushInvalidOtelConfigWarning(configKey string, err error, warnings *[]string) {
	*warnings = append(*warnings, fmt.Sprintf("Ignoring invalid `%s` config: %v", configKey, err))
}

// validateOtelSpanAttribute mirrors codex-otel's validate_span_attributes.
func validateOtelSpanAttribute(key string) error {
	if key == "" {
		return fmt.Errorf("configured span attribute key must not be empty")
	}
	return nil
}

// validateOtelTracestateMember mirrors codex-otel's validate_tracestate_member.
func validateOtelTracestateMember(memberKey string, fields map[string]string) error {
	key, value, err := encodeOtelTracestateMemberFields(memberKey, fields)
	if err != nil {
		return err
	}
	return validateTraceStatePairs([]traceStatePair{{key: key, value: value}})
}

// validateOtelTracestateEntries mirrors codex-otel's validate_tracestate_entries.
func validateOtelTracestateEntries(entries map[string]map[string]string) error {
	pairs := make([]traceStatePair, 0, len(entries))
	for _, memberKey := range sortedStringKeys(entries) {
		key, value, err := encodeOtelTracestateMemberFields(memberKey, entries[memberKey])
		if err != nil {
			return err
		}
		pairs = append(pairs, traceStatePair{key: key, value: value})
	}
	return validateTraceStatePairs(pairs)
}

// encodeOtelTracestateMemberFields mirrors codex-otel's
// encode_tracestate_member_fields: configured fields are joined into one opaque
// member value, and both the field grammar and the final header value are
// validated.
func encodeOtelTracestateMemberFields(memberKey string, fields map[string]string) (string, string, error) {
	encoded := make([]string, 0, len(fields))
	for _, fieldKey := range sortedStringKeys(fields) {
		value := fields[fieldKey]
		if !isConfiguredTracestateFieldKey(fieldKey) {
			return "", "", fmt.Errorf("invalid configured tracestate field key %s.%s", memberKey, fieldKey)
		}
		if !isConfiguredTracestateFieldValue(value) {
			return "", "", fmt.Errorf("invalid configured tracestate value for %s.%s", memberKey, fieldKey)
		}
		encoded = append(encoded, fieldKey+":"+value)
	}
	value := strings.Join(encoded, ";")
	if !isHeaderSafeTracestateMemberValue(value) {
		return "", "", fmt.Errorf("invalid configured tracestate value for %s", memberKey)
	}
	return memberKey, value, nil
}

type traceStatePair struct {
	key   string
	value string
}

// validateTraceStatePairs mirrors opentelemetry's TraceState::from_key_value.
func validateTraceStatePairs(pairs []traceStatePair) error {
	for _, pair := range pairs {
		if !traceStateValidKey(pair.key) {
			return fmt.Errorf("%s is not a valid key in TraceState, see https://www.w3.org/TR/trace-context/#key for more details", pair.key)
		}
		if !traceStateValidValue(pair.value) {
			return fmt.Errorf("%s is not a valid value in TraceState, see https://www.w3.org/TR/trace-context/#value for more details", pair.value)
		}
	}
	return nil
}

// traceStateValidKey mirrors opentelemetry's TraceState::valid_key.
func traceStateValidKey(key string) bool {
	if len(key) > 256 {
		return false
	}
	allowedSpecial := func(byteValue byte) bool {
		return byteValue == '_' || byteValue == '-' || byteValue == '*' || byteValue == '/'
	}
	vendorStart := -1
	for index := 0; index < len(key); index++ {
		byteValue := key[index]
		lower := byteValue >= 'a' && byteValue <= 'z'
		digit := byteValue >= '0' && byteValue <= '9'
		if !(lower || digit || allowedSpecial(byteValue) || byteValue == '@') {
			return false
		}
		if index == 0 && !lower && !digit {
			return false
		}
		if byteValue == '@' {
			if vendorStart >= 0 || index+14 < len(key) {
				return false
			}
			vendorStart = index
		} else if vendorStart >= 0 && index == vendorStart+1 && !lower && !digit {
			return false
		}
	}
	return true
}

// traceStateValidValue mirrors opentelemetry's TraceState::valid_value.
func traceStateValidValue(value string) bool {
	return len(value) <= 256 && !strings.ContainsAny(value, ",=")
}

func isConfiguredTracestateFieldKey(fieldKey string) bool {
	if fieldKey == "" {
		return false
	}
	for index := 0; index < len(fieldKey); index++ {
		byteValue := fieldKey[index]
		if byteValue < '!' || byteValue > '~' {
			return false
		}
		switch byteValue {
		case ':', ';', ',', '=':
			return false
		}
	}
	return true
}

func isConfiguredTracestateFieldValue(value string) bool {
	for index := 0; index < len(value); index++ {
		byteValue := value[index]
		if !isTracestateMemberValueByte(byteValue) || byteValue == ';' {
			return false
		}
	}
	return true
}

func isHeaderSafeTracestateMemberValue(value string) bool {
	if value == "" {
		return true
	}
	for index := 0; index < len(value); index++ {
		if !isTracestateMemberValueByte(value[index]) {
			return false
		}
	}
	return value[len(value)-1] != ' '
}

func isTracestateMemberValueByte(byteValue byte) bool {
	return byteValue >= ' ' && byteValue <= '~' && byteValue != ',' && byteValue != '='
}

// otelStringMap reads a table of strings from the untyped config values.
func otelStringMap(value any) map[string]string {
	table, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(table))
	for key, raw := range table {
		if text, ok := raw.(string); ok {
			out[key] = text
		}
	}
	return out
}

// otelTracestateMap reads `otel.tracestate` as members of string fields.
func otelTracestateMap(value any) map[string]map[string]string {
	table, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]map[string]string, len(table))
	for memberKey, raw := range table {
		if fields := otelStringMap(raw); fields != nil {
			out[memberKey] = fields
		}
	}
	return out
}

func sortedStringKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
