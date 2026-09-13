package telemetry

import (
	"errors"
	"sort"
	"strings"
)

// Rust parity: codex-rs/otel/src/metrics/validation.rs + tags.rs and
// codex-utils-string's sanitize_metric_tag_value. Metric names and tag
// components are restricted to a small character set because they travel as
// OTLP attribute/metric names, and tag values are sanitized (and bounded) before
// they leave the process.

var (
	ErrEmptyMetricName          = errors.New("metric name must not be empty")
	ErrInvalidMetricName        = errors.New("metric name must contain only alphanumeric characters, '.', '_' or '-'")
	ErrEmptyMetricTag           = errors.New("metric tag component must not be empty")
	ErrInvalidMetricTag         = errors.New("metric tag component must contain only alphanumeric characters, '.', '_', '-' or '/'")
	ErrNegativeCounterIncrement = errors.New("metric counter increment must not be negative")
)

// Metric tag keys used by the session telemetry (Rust metrics/tags.rs).
const (
	AppVersionTag    = "app.version"
	AuthModeTag      = "auth_mode"
	ModelTag         = "model"
	OriginatorTag    = "originator"
	ServiceNameTag   = "service_name"
	SessionSourceTag = "session_source"
)

const (
	otherOriginatorTagValue = "other"
	// maxMetricTagValueLength mirrors sanitize_metric_tag_value's 256-byte bound.
	maxMetricTagValueLength = 256
)

// knownOriginatorTagValues mirrors Rust's low-cardinality allow-list.
var knownOriginatorTagValues = []string{
	"codex_desktop",
	"codex-app-server",
	"codex_mcp_server",
	"codex_cli_rs",
	"codex-tui",
	"codex_vscode",
	"none",
	"codex_exec",
	"codex-cli",
	"codex_sdk_ts",
	"codex-app-server-sdk",
}

// SanitizeMetricTagValue maps a value onto the metric tag alphabet, trimming
// underscores, falling back to "unspecified", and bounding the length
// (codex-utils-string::sanitize_metric_tag_value).
func SanitizeMetricTagValue(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		if isMetricTagRune(character) {
			builder.WriteRune(character)
			continue
		}
		builder.WriteByte('_')
	}
	sanitized := strings.Trim(builder.String(), "_")
	hasAlphanumeric := false
	for _, character := range sanitized {
		if isASCIIAlphanumeric(character) {
			hasAlphanumeric = true
			break
		}
	}
	if sanitized == "" || !hasAlphanumeric {
		return "unspecified"
	}
	if len(sanitized) <= maxMetricTagValueLength {
		return sanitized
	}
	// Rust truncates the sanitized value to the first 256 bytes; the sanitized
	// alphabet is ASCII, so slicing bytes cannot split a rune.
	return sanitized[:maxMetricTagValueLength]
}

// BoundedOriginatorTagValue returns a known low-cardinality originator value, or
// "other" (Rust bounded_originator_tag_value).
func BoundedOriginatorTagValue(originator string) string {
	sanitized := SanitizeMetricTagValue(originator)
	for _, known := range knownOriginatorTagValues {
		if known == sanitized {
			return known
		}
	}
	return otherOriginatorTagValue
}

// MetricTagValue is one validated metric attribute.
type MetricTagValue struct {
	Key   string
	Value string
}

// SessionMetricTags builds the session-scoped metric tags in Rust's order.
func SessionMetricTags(authMode string, sessionSource string, originator string, serviceName string, model string, appVersion string) ([]MetricTagValue, error) {
	tags := make([]MetricTagValue, 0, 6)
	for _, candidate := range []MetricTagValue{
		{Key: AuthModeTag, Value: authMode},
		{Key: SessionSourceTag, Value: sessionSource},
		{Key: OriginatorTag, Value: originator},
		{Key: ServiceNameTag, Value: serviceName},
		{Key: ModelTag, Value: model},
		{Key: AppVersionTag, Value: appVersion},
	} {
		if candidate.Value == "" {
			continue
		}
		if err := validateMetricTagKey(candidate.Key); err != nil {
			return nil, err
		}
		if err := validateMetricTagValue(candidate.Value); err != nil {
			return nil, err
		}
		tags = append(tags, candidate)
	}
	return tags, nil
}

// validateMetricName mirrors validate_metric_name.
func validateMetricName(name string) error {
	if name == "" {
		return ErrEmptyMetricName
	}
	for _, character := range name {
		if isASCIIAlphanumeric(character) || character == '.' || character == '_' || character == '-' {
			continue
		}
		return ErrInvalidMetricName
	}
	return nil
}

func validateMetricTagKey(key string) error {
	return validateMetricTagComponent(key)
}

func validateMetricTagValue(value string) error {
	return validateMetricTagComponent(value)
}

func validateMetricTagComponent(value string) error {
	if value == "" {
		return ErrEmptyMetricTag
	}
	for _, character := range value {
		if isASCIIAlphanumeric(character) || character == '.' || character == '_' || character == '-' || character == '/' {
			continue
		}
		return ErrInvalidMetricTag
	}
	return nil
}

// mergeMetricTags merges default tags with the call's tags (the call wins) and
// returns them in sorted key order, mirroring the Rust client's BTreeMap merge.
func mergeMetricTags(defaults map[string]string, tags map[string]string) ([]MetricTagValue, error) {
	merged := make(map[string]string, len(defaults)+len(tags))
	for key, value := range defaults {
		merged[key] = value
	}
	for key, value := range tags {
		if err := validateMetricTagKey(key); err != nil {
			return nil, err
		}
		if err := validateMetricTagValue(value); err != nil {
			return nil, err
		}
		merged[key] = value
	}
	if len(merged) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]MetricTagValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, MetricTagValue{Key: key, Value: merged[key]})
	}
	return out, nil
}

func isMetricTagRune(character rune) bool {
	return isASCIIAlphanumeric(character) || character == '.' || character == '_' || character == '-' || character == '/'
}

func isASCIIAlphanumeric(character rune) bool {
	return character >= '0' && character <= '9' ||
		character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z'
}
