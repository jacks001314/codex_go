package config

import (
	"fmt"
	"sort"
	"strings"
)

// Rust parity: codex-rs/core/src/config/otel.rs (resolve_config with
// resolve_span_attributes / resolve_tracestate) and the validators it calls in
// codex-otel (validate_span_attributes, validate_tracestate_member(s) with
// encode_tracestate_member_fields, and opentelemetry's
// TraceState::from_key_value).
//
// Go has no OTEL provider yet, so the exporter kinds and the provider wiring are
// not resolved here. The user-visible half of the resolution is the trace
// metadata: malformed `otel.span_attributes` / `otel.tracestate` entries are
// filtered out and reported through config.startup_warnings rather than failing
// startup.

// otelDefaultEnvironment mirrors Rust's DEFAULT_OTEL_ENVIRONMENT. It is recorded
// here so the later provider slice resolves the same default.
const otelDefaultEnvironment = "dev"

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
