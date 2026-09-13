package telemetry

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Rust parity: codex-rs/otel/src/trace_context.rs plus the
// opentelemetry_sdk TraceContextPropagator it delegates carrier parsing to.
//
// The configured `otel.tracestate` members live beside the process-global
// tracer provider: they are merged into every propagated W3C tracestate so
// selected fields follow the trace across process boundaries.

// W3C trace-context carrier names.
const (
	TraceparentHeader = "traceparent"
	TracestateHeader  = "tracestate"
	TraceparentEnvVar = "TRACEPARENT"
	TracestateEnvVar  = "TRACESTATE"
)

// TraceContext is a parsed W3C trace context, mirroring the fields the
// propagator extracts from a `traceparent`/`tracestate` carrier pair.
type TraceContext struct {
	TraceID    string
	SpanID     string
	Sampled    bool
	TraceState string
}

// validSpanIDs reports whether the span carries the ids the propagator accepts:
// lowercase hex of the W3C length and not the all-zero id.
func validSpanIDs(traceID string, spanID string) bool {
	if strings.ToLower(traceID) != traceID || strings.ToLower(spanID) != spanID {
		return false
	}
	if _, ok := parseNonZeroHex(traceID, 32); !ok {
		return false
	}
	_, ok := parseNonZeroHex(spanID, 16)
	return ok
}

var (
	tracestateEntriesMu sync.RWMutex
	tracestateEntries   map[string]map[string]string

	traceContextEnvOnce sync.Once
	traceContextEnv     TraceContext
	traceContextEnvOK   bool
)

// SetTracestateEntries validates and installs the configured tracestate members
// (Rust's set_tracestate_entries). The provider installs them at startup and
// clears them when no exporter is enabled.
func SetTracestateEntries(entries map[string]map[string]string) error {
	if err := ValidateTracestateEntries(entries); err != nil {
		return err
	}
	tracestateEntriesMu.Lock()
	defer tracestateEntriesMu.Unlock()
	tracestateEntries = cloneTracestateEntries(entries)
	return nil
}

// TracestateEntries returns a copy of the installed configured tracestate.
func TracestateEntries() map[string]map[string]string {
	tracestateEntriesMu.RLock()
	defer tracestateEntriesMu.RUnlock()
	return cloneTracestateEntries(tracestateEntries)
}

func cloneTracestateEntries(entries map[string]map[string]string) map[string]map[string]string {
	if len(entries) == 0 {
		return nil
	}
	cloned := make(map[string]map[string]string, len(entries))
	for memberKey, fields := range entries {
		clonedFields := make(map[string]string, len(fields))
		for fieldKey, value := range fields {
			clonedFields[fieldKey] = value
		}
		cloned[memberKey] = clonedFields
	}
	return cloned
}

// ParseTraceContext extracts the W3C trace context from a carrier pair,
// mirroring the propagator's extraction: an absent or malformed `traceparent`
// yields no context (and callers must not fall back to an unrelated active
// span), while a malformed `tracestate` is dropped rather than rejected.
func ParseTraceContext(traceparent string, tracestate string) (TraceContext, bool) {
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) < 4 {
		return TraceContext{}, false
	}
	version, err := strconv.ParseUint(parts[0], 16, 8)
	if err != nil || version > 254 || (version == 0 && len(parts) != 4) {
		return TraceContext{}, false
	}
	if strings.ToLower(parts[1]) != parts[1] || strings.ToLower(parts[2]) != parts[2] {
		return TraceContext{}, false
	}
	traceID, ok := parseNonZeroHex(parts[1], 32)
	if !ok {
		return TraceContext{}, false
	}
	spanID, ok := parseNonZeroHex(parts[2], 16)
	if !ok {
		return TraceContext{}, false
	}
	flags, err := strconv.ParseUint(parts[3], 16, 8)
	if err != nil || (version == 0 && flags > 2) {
		return TraceContext{}, false
	}
	context := TraceContext{
		TraceID:    traceID,
		SpanID:     spanID,
		Sampled:    flags&0x01 != 0,
		TraceState: normalizeTracestateHeader(tracestate),
	}
	return context, true
}

// parseNonZeroHex reports whether value is exactly size lowercase hex digits
// and not the all-zero id the propagator rejects.
func parseNonZeroHex(value string, size int) (string, bool) {
	if len(value) != size {
		return "", false
	}
	if strings.Trim(value, "0123456789abcdef") != "" {
		return "", false
	}
	if strings.Trim(value, "0") == "" {
		return "", false
	}
	return value, true
}

// normalizeTracestateHeader mirrors TraceState::from_str: a malformed header is
// replaced by an empty state instead of failing the extraction.
func normalizeTracestateHeader(tracestate string) string {
	members, ok := parseTracestateMembers(tracestate)
	if !ok {
		return ""
	}
	return formatTracestateMembers(members)
}

// TraceContextFromEnv returns the trace context carried by TRACEPARENT /
// TRACESTATE, read once per process like Rust's TRACEPARENT_CONTEXT.
func TraceContextFromEnv() (TraceContext, bool) {
	traceContextEnvOnce.Do(func() {
		traceparent := os.Getenv(TraceparentEnvVar)
		if strings.TrimSpace(traceparent) == "" {
			return
		}
		traceContextEnv, traceContextEnvOK = ParseTraceContext(traceparent, os.Getenv(TracestateEnvVar))
	})
	return traceContextEnv, traceContextEnvOK
}

// resetTraceContextEnvCache restores the lazy TRACEPARENT read between tests.
func resetTraceContextEnvCache() {
	traceContextEnvOnce = sync.Once{}
	traceContextEnv = TraceContext{}
	traceContextEnvOK = false
}

// TraceContext reports the span's W3C carrier, or false for a span that has no
// valid context. The tracestate merges the span's own tracestate with the
// configured members (Rust's span_w3c_trace_context).
func (s *Span) TraceContext() (TraceContext, bool) {
	if s == nil || !validSpanIDs(s.TraceID, s.SpanID) {
		return TraceContext{}, false
	}
	traceState := MergeTracestateEntries(s.TraceState, TracestateEntries())
	return TraceContext{
		TraceID:    s.TraceID,
		SpanID:     s.SpanID,
		Sampled:    s.sampled,
		TraceState: traceState,
	}, true
}

// W3CTraceContext reports the span's W3C carrier pair (Rust's
// span_w3c_trace_context): the traceparent built from its ids and sampling
// decision, and its tracestate merged with the configured entries.
func (s *Span) W3CTraceContext() (traceparent string, tracestate string, ok bool) {
	trace, ok := s.TraceContext()
	if !ok {
		return "", "", false
	}
	return formatTraceparent(trace), trace.TraceState, true
}

// InjectTraceHeaders writes this span's W3C trace context into headers,
// replacing any existing carrier so a reused request header map stays
// consistent with the span.
func (s *Span) InjectTraceHeaders(headers http.Header) bool {
	context, ok := s.TraceContext()
	if !ok || headers == nil {
		return false
	}
	headers.Set(TraceparentHeader, formatTraceparent(context))
	if context.TraceState != "" {
		headers.Set(TracestateHeader, context.TraceState)
	} else {
		headers.Del(TracestateHeader)
	}
	return true
}

// SetParentContext continues the span from the given inbound trace context: it
// adopts the trace id, records the remote span as parent, carries the parent's
// tracestate, and inherits the remote sampling decision like Rust's
// parent-based sampler.
func (s *Span) SetParentContext(context TraceContext) bool {
	if s == nil || !validSpanIDs(context.TraceID, context.SpanID) {
		return false
	}
	s.TraceID = context.TraceID
	s.ParentSpanID = context.SpanID
	s.TraceState = context.TraceState
	s.sampled = context.Sampled
	return true
}

func formatTraceparent(context TraceContext) string {
	flags := uint64(0)
	if context.Sampled {
		flags = 1
	}
	return fmt.Sprintf("00-%s-%s-%02x", context.TraceID, context.SpanID, flags)
}

// MergeTracestateEntries merges the configured members into one incoming
// tracestate header, upserting the configured fields of an existing member and
// leaving unrelated members untouched (Rust's merge_tracestate_entries).
func MergeTracestateEntries(tracestate string, configured map[string]map[string]string) string {
	members, _ := parseTracestateMembers(tracestate)
	for _, memberKey := range sortedMemberKeysDescending(configured) {
		fields := configured[memberKey]
		existing, found := memberValue(members, memberKey)
		value := mergeTracestateMemberFields(existing, found, fields)
		// TraceState::insert rejects an invalid member and stops merging, as
		// the configured entries were validated when they were installed.
		if !traceStateValidKey(memberKey) || !traceStateValidValue(value) {
			break
		}
		members = insertTracestateMember(members, memberKey, value)
	}
	return formatTracestateMembers(members)
}

// mergeTracestateMemberFields upserts the configured fields inside one member
// value, keeping the fields that were already present.
func mergeTracestateMemberFields(existing string, found bool, configured map[string]string) string {
	fields := []string{}
	seen := map[string]bool{}
	if found {
		for _, field := range strings.Split(existing, ";") {
			if field == "" {
				continue
			}
			if separator := strings.Index(field, ":"); separator >= 0 {
				fieldKey := field[:separator]
				if value, ok := configured[fieldKey]; ok {
					if !seen[fieldKey] {
						fields = append(fields, fieldKey+":"+value)
						seen[fieldKey] = true
					}
					continue
				}
				seen[fieldKey] = true
			}
			fields = append(fields, field)
		}
	}
	for _, fieldKey := range sortedFieldKeys(configured) {
		if seen[fieldKey] {
			continue
		}
		fields = append(fields, fieldKey+":"+configured[fieldKey])
	}
	return strings.Join(fields, ";")
}

// ValidateTracestateEntries mirrors codex-otel's validate_tracestate_entries:
// configured members are encoded into header values and validated as one
// tracestate list, so malformed config cannot produce a header that downstream
// W3C extractors reject.
func ValidateTracestateEntries(entries map[string]map[string]string) error {
	members := make([]tracestateMember, 0, len(entries))
	for _, memberKey := range sortedFieldKeys(entries) {
		key, value, err := encodeTracestateMemberFields(memberKey, entries[memberKey])
		if err != nil {
			return err
		}
		members = append(members, tracestateMember{key: key, value: value})
	}
	return validateTracestateList(members)
}

// ValidateTracestateMember mirrors codex-otel's validate_tracestate_member.
func ValidateTracestateMember(memberKey string, fields map[string]string) error {
	key, value, err := encodeTracestateMemberFields(memberKey, fields)
	if err != nil {
		return err
	}
	return validateTracestateList([]tracestateMember{{key: key, value: value}})
}

// encodeTracestateMemberFields joins the configured fields into the one opaque
// member value the config format maps onto a W3C tracestate member.
func encodeTracestateMemberFields(memberKey string, fields map[string]string) (string, string, error) {
	encoded := make([]string, 0, len(fields))
	for _, fieldKey := range sortedFieldKeys(fields) {
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

type tracestateMember struct {
	key   string
	value string
}

// parseTracestateMembers mirrors TraceState::from_str: members are split on
// commas, every member must contain '=', and one invalid member discards the
// whole header.
func parseTracestateMembers(tracestate string) ([]tracestateMember, bool) {
	if strings.TrimSpace(tracestate) == "" {
		return nil, true
	}
	parts := strings.Split(tracestate, ",")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	members := make([]tracestateMember, 0, len(parts))
	for _, part := range parts {
		separator := strings.Index(part, "=")
		if separator < 0 {
			return nil, false
		}
		members = append(members, tracestateMember{
			key:   part[:separator],
			value: strings.TrimLeft(part[separator:], "="),
		})
	}
	if err := validateTracestateList(members); err != nil {
		return nil, false
	}
	return members, true
}

func formatTracestateMembers(members []tracestateMember) string {
	if len(members) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(members))
	for _, member := range members {
		formatted = append(formatted, member.key+"="+member.value)
	}
	return strings.Join(formatted, ",")
}

func memberValue(members []tracestateMember, key string) (string, bool) {
	for _, member := range members {
		if member.key == key {
			return member.value, true
		}
	}
	return "", false
}

// insertTracestateMember replaces an existing member and moves it to the front,
// mirroring TraceState::insert.
func insertTracestateMember(members []tracestateMember, key string, value string) []tracestateMember {
	remaining := make([]tracestateMember, 0, len(members)+1)
	remaining = append(remaining, tracestateMember{key: key, value: value})
	for _, member := range members {
		if member.key != key {
			remaining = append(remaining, member)
		}
	}
	return remaining
}

// validateTracestateList mirrors TraceState::from_key_value for the members
// this package builds.
func validateTracestateList(members []tracestateMember) error {
	for _, member := range members {
		if !traceStateValidKey(member.key) {
			return fmt.Errorf("%s is not a valid key in TraceState, see https://www.w3.org/TR/trace-context/#key for more details", member.key)
		}
		if !traceStateValidValue(member.value) {
			return fmt.Errorf("%s is not a valid value in TraceState, see https://www.w3.org/TR/trace-context/#value for more details", member.value)
		}
	}
	return nil
}

// traceStateValidKey mirrors opentelemetry's TraceState::valid_key.
func traceStateValidKey(key string) bool {
	if len(key) > 256 {
		return false
	}
	vendorStart := -1
	for index := 0; index < len(key); index++ {
		byteValue := key[index]
		lower := byteValue >= 'a' && byteValue <= 'z'
		digit := byteValue >= '0' && byteValue <= '9'
		special := byteValue == '_' || byteValue == '-' || byteValue == '*' || byteValue == '/'
		if !(lower || digit || special || byteValue == '@') {
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
	if !valueIsTracestateMemberValue(value) {
		return false
	}
	return value[len(value)-1] != ' '
}

func valueIsTracestateMemberValue(value string) bool {
	for index := 0; index < len(value); index++ {
		if !isTracestateMemberValueByte(value[index]) {
			return false
		}
	}
	return true
}

func isTracestateMemberValueByte(byteValue byte) bool {
	return byteValue >= ' ' && byteValue <= '~' && byteValue != ',' && byteValue != '='
}

func sortedFieldKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMemberKeysDescending(values map[string]map[string]string) []string {
	keys := sortedFieldKeys(values)
	for left, right := 0, len(keys)-1; left < right; left, right = left+1, right-1 {
		keys[left], keys[right] = keys[right], keys[left]
	}
	return keys
}
