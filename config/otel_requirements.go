package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"codex_go/protocol"
)

// OtelRequirements mirrors Rust's OtelRequirementsToml / app-server
// OtelRequirements (Rust #57638, #57649, #57658). The same shape is used for
// requirements parsing, runtime application and the configRequirements/read
// projection; API responses redact exporter header values at the app-server
// boundary.
type OtelRequirements struct {
	ToolResult             *OtelToolResultLogConfig     `json:"tool_result"`
	LogUserPrompt          *bool                        `json:"log_user_prompt"`
	LogSSEDeltas           *bool                        `json:"log_sse_deltas"`
	LogAgentResponses      *bool                        `json:"log_agent_responses"`
	LogGuardianAssessments *bool                        `json:"log_guardian_assessments"`
	Environment            *string                      `json:"environment"`
	Exporter               *OtelExporter                `json:"exporter"`
	TraceExporter          *OtelExporter                `json:"trace_exporter"`
	MetricsExporter        *OtelExporter                `json:"metrics_exporter"`
	SpanAttributes         map[string]string            `json:"span_attributes"`
	Tracestate             map[string]map[string]string `json:"tracestate"`
}

type OtelToolResultLogConfig struct {
	MaxBytes int `json:"max_bytes"`
}

// OtelRequirementTLSConfig mirrors the app-server wire names (kebab-case).
type OtelRequirementTLSConfig struct {
	CACertificate     *string `json:"ca-certificate"`
	ClientCertificate *string `json:"client-certificate"`
	ClientPrivateKey  *string `json:"client-private-key"`
}

// OtelExporter mirrors Rust's externally-tagged OtelExporterKind:
// "none"/"statsig" or a single-key otlp-http/otlp-grpc object.
type OtelExporter struct {
	Kind     string
	Endpoint string
	Headers  map[string]string
	Protocol string
	TLS      *OtelRequirementTLSConfig
}

func (e OtelExporter) MarshalJSON() ([]byte, error) {
	switch e.Kind {
	case "none", "statsig":
		return json.Marshal(e.Kind)
	case "otlp-http":
		return json.Marshal(map[string]any{"otlp-http": e.otlpBody(true)})
	case "otlp-grpc":
		return json.Marshal(map[string]any{"otlp-grpc": e.otlpBody(false)})
	default:
		return nil, fmt.Errorf("unknown OTEL exporter kind %q", e.Kind)
	}
}

func (e OtelExporter) otlpBody(withProtocol bool) map[string]any {
	headers := e.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	body := map[string]any{
		"endpoint": e.Endpoint,
		"headers":  headers,
		"tls":      e.TLS,
	}
	if withProtocol {
		body["protocol"] = e.Protocol
	}
	return body
}

func (e *OtelExporter) UnmarshalJSON(data []byte) error {
	var plain string
	if err := json.Unmarshal(data, &plain); err == nil {
		parsed, err := otelExporterFromAny(plain)
		if err != nil {
			return err
		}
		*e = parsed
		return nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil {
		return err
	}
	parsed, err := otelExporterFromAny(body)
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}

func otelRequirementsEmpty(value *OtelRequirements) bool {
	if value == nil {
		return true
	}
	return value.ToolResult == nil &&
		value.LogUserPrompt == nil &&
		value.LogSSEDeltas == nil &&
		value.LogAgentResponses == nil &&
		value.LogGuardianAssessments == nil &&
		value.Environment == nil &&
		value.Exporter == nil &&
		value.TraceExporter == nil &&
		value.MetricsExporter == nil &&
		!otelMetadataPresent(value)
}

func otelMetadataPresent(value *OtelRequirements) bool {
	if value == nil {
		return false
	}
	if len(value.SpanAttributes) > 0 {
		return true
	}
	for _, fields := range value.Tracestate {
		if len(fields) > 0 {
			return true
		}
	}
	return false
}

func otelRequirementsFromMap(values map[string]any) (*OtelRequirements, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if err := rejectUnknownOtelRequirementKeys(values); err != nil {
		return nil, err
	}
	out := &OtelRequirements{}
	if raw, ok := anyKey(values, "tool_result", "toolResult"); ok && raw != nil {
		nested, valid := raw.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("otel.tool_result must be a table")
		}
		limit := &OtelToolResultLogConfig{MaxBytes: protocol.DefaultToolResultLogConfig().MaxBytes}
		if rawLimit, ok := nested["max_bytes"]; ok && rawLimit != nil {
			number, valid := otelConfigIntValue(rawLimit)
			if !valid || number < 0 {
				return nil, fmt.Errorf("otel.tool_result.max_bytes must be a non-negative integer")
			}
			limit.MaxBytes = int(number)
		}
		out.ToolResult = limit
	}
	for _, field := range []struct {
		keys []string
		dst  **bool
	}{
		{[]string{"log_user_prompt", "logUserPrompt"}, &out.LogUserPrompt},
		{[]string{"log_sse_deltas", "logSSEDeltas"}, &out.LogSSEDeltas},
		{[]string{"log_agent_responses", "logAgentResponses"}, &out.LogAgentResponses},
		{[]string{"log_guardian_assessments", "logGuardianAssessments"}, &out.LogGuardianAssessments},
	} {
		raw, ok := anyKey(values, field.keys...)
		if !ok || raw == nil {
			continue
		}
		value, valid := raw.(bool)
		if !valid {
			return nil, fmt.Errorf("otel.%s must be a boolean", field.keys[0])
		}
		*field.dst = &value
	}
	if raw, ok := anyKey(values, "environment"); ok && raw != nil {
		value, valid := raw.(string)
		if !valid {
			return nil, fmt.Errorf("otel.environment must be a string")
		}
		out.Environment = &value
	}
	for _, field := range []struct {
		key string
		dst **OtelExporter
	}{
		{"exporter", &out.Exporter},
		{"trace_exporter", &out.TraceExporter},
		{"metrics_exporter", &out.MetricsExporter},
	} {
		raw, ok := values[field.key]
		if !ok || raw == nil {
			continue
		}
		parsed, err := otelExporterFromAny(raw)
		if err != nil {
			return nil, fmt.Errorf("otel.%s: %w", field.key, err)
		}
		*field.dst = &parsed
	}
	if raw, ok := anyKey(values, "span_attributes", "spanAttributes"); ok && raw != nil {
		nested, valid := raw.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("otel.span_attributes must be a table")
		}
		attributes := map[string]string{}
		for key, raw := range nested {
			value, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("otel.span_attributes.%s must be a string", key)
			}
			attributes[key] = value
		}
		if len(attributes) > 0 {
			out.SpanAttributes = attributes
		}
	}
	if raw, ok := anyKey(values, "tracestate"); ok && raw != nil {
		nested, valid := raw.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("otel.tracestate must be a table")
		}
		members := map[string]map[string]string{}
		for member, rawFields := range nested {
			fields, ok := rawFields.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("otel.tracestate.%s must be a table", member)
			}
			parsed := map[string]string{}
			for key, raw := range fields {
				value, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("otel.tracestate.%s.%s must be a string", member, key)
				}
				parsed[key] = value
			}
			if len(parsed) > 0 {
				members[member] = parsed
			}
		}
		if len(members) > 0 {
			out.Tracestate = members
		}
	}
	if otelRequirementsEmpty(out) {
		return nil, nil
	}
	return out, nil
}

// rejectUnknownOtelRequirementKeys mirrors serde(deny_unknown_fields) on Rust's
// OtelRequirementsToml: a typo in requirements.toml fails loading instead of
// being silently ignored.
func rejectUnknownOtelRequirementKeys(values map[string]any) error {
	for key := range values {
		switch key {
		case "tool_result", "toolResult",
			"log_user_prompt", "logUserPrompt",
			"log_sse_deltas", "logSSEDeltas",
			"log_agent_responses", "logAgentResponses",
			"log_guardian_assessments", "logGuardianAssessments",
			"environment",
			"exporter", "trace_exporter", "metrics_exporter",
			"span_attributes", "spanAttributes",
			"tracestate":
		default:
			return fmt.Errorf("otel: unknown field `%s`", key)
		}
	}
	return nil
}

func otelExporterFromAny(raw any) (OtelExporter, error) {
	switch typed := raw.(type) {
	case string:
		kind := strings.TrimSpace(typed)
		if kind != OtelExporterKindNone && kind != OtelExporterKindStatsig {
			return OtelExporter{}, fmt.Errorf("unknown exporter variant %q", typed)
		}
		return OtelExporter{Kind: kind}, nil
	case map[string]any:
		return otelExporterFromMap(typed)
	case map[string]json.RawMessage:
		values := map[string]any{}
		for key, rawValue := range typed {
			var decoded any
			if err := json.Unmarshal(rawValue, &decoded); err != nil {
				return OtelExporter{}, err
			}
			values[key] = decoded
		}
		return otelExporterFromMap(values)
	default:
		return OtelExporter{}, fmt.Errorf("must be a string or a single-variant table")
	}
}

func otelExporterFromMap(values map[string]any) (OtelExporter, error) {
	if len(values) != 1 {
		return OtelExporter{}, fmt.Errorf("must name exactly one exporter variant")
	}
	for variant, raw := range values {
		table, ok := raw.(map[string]any)
		if !ok {
			return OtelExporter{}, fmt.Errorf("%s must be a table", variant)
		}
		out := OtelExporter{Kind: variant}
		endpoint, ok := table["endpoint"].(string)
		if !ok {
			return OtelExporter{}, fmt.Errorf("%s.endpoint must be a string", variant)
		}
		out.Endpoint = endpoint
		if headers, ok := table["headers"].(map[string]any); ok {
			out.Headers = map[string]string{}
			for key, rawValue := range headers {
				value, ok := rawValue.(string)
				if !ok {
					return OtelExporter{}, fmt.Errorf("%s.headers.%s must be a string", variant, key)
				}
				out.Headers[key] = value
			}
		}
		switch variant {
		case OtelExporterKindOtlpHTTP:
			protocol, ok := table["protocol"].(string)
			if !ok {
				return OtelExporter{}, fmt.Errorf("%s.protocol must be a string", variant)
			}
			if protocol != OtelHTTPProtocolBinary && protocol != OtelHTTPProtocolJSON {
				return OtelExporter{}, fmt.Errorf("%s has an unknown protocol %q", variant, protocol)
			}
			out.Protocol = protocol
		case OtelExporterKindOtlpGRPC:
		default:
			return OtelExporter{}, fmt.Errorf("unknown exporter variant %q", variant)
		}
		if tlsValues, ok := table["tls"].(map[string]any); ok {
			out.TLS = &OtelRequirementTLSConfig{}
			if value, ok := stringAnyKey(tlsValues, "ca-certificate", "ca_certificate"); ok {
				out.TLS.CACertificate = &value
			}
			if value, ok := stringAnyKey(tlsValues, "client-certificate", "client_certificate"); ok {
				out.TLS.ClientCertificate = &value
			}
			if value, ok := stringAnyKey(tlsValues, "client-private-key", "client_private_key"); ok {
				out.TLS.ClientPrivateKey = &value
			}
		}
		return out, nil
	}
	return OtelExporter{}, fmt.Errorf("must name exactly one exporter variant")
}

// ToConfigValue renders the exporter in the TOML/config shape consumed by
// resolveConfig (a string for none/statsig, one kebab-key table otherwise).
func (e *OtelExporter) ToConfigValue() any {
	if e == nil {
		return nil
	}
	if e.Kind == OtelExporterKindNone || e.Kind == OtelExporterKindStatsig {
		return e.Kind
	}
	body := map[string]any{"endpoint": e.Endpoint}
	if len(e.Headers) > 0 {
		headers := map[string]any{}
		for key, value := range e.Headers {
			headers[key] = value
		}
		body["headers"] = headers
	}
	if e.Kind == OtelExporterKindOtlpHTTP {
		body["protocol"] = e.Protocol
	}
	if e.TLS != nil {
		tls := map[string]any{}
		if e.TLS.CACertificate != nil {
			tls["ca-certificate"] = *e.TLS.CACertificate
		}
		if e.TLS.ClientCertificate != nil {
			tls["client-certificate"] = *e.TLS.ClientCertificate
		}
		if e.TLS.ClientPrivateKey != nil {
			tls["client-private-key"] = *e.TLS.ClientPrivateKey
		}
		if len(tls) > 0 {
			body["tls"] = tls
		}
	}
	return map[string]any{e.Kind: body}
}

// resolveOtelRequirementPaths turns relative TLS paths into paths relative to
// the requirements document that declared them (Rust #57649).
func resolveOtelRequirementPaths(requirements *ConfigRequirements, context *ConfigPathContext) error {
	if requirements == nil || requirements.Otel == nil || context == nil {
		return nil
	}
	for _, exporter := range []*OtelExporter{
		requirements.Otel.Exporter,
		requirements.Otel.TraceExporter,
		requirements.Otel.MetricsExporter,
	} {
		if exporter == nil || exporter.TLS == nil {
			continue
		}
		for _, path := range []*string{
			exporter.TLS.CACertificate,
			exporter.TLS.ClientCertificate,
			exporter.TLS.ClientPrivateKey,
		} {
			if path == nil || strings.TrimSpace(*path) == "" {
				continue
			}
			resolved, err := context.ResolvePathString(*path)
			if err != nil {
				return err
			}
			*path = resolved
		}
	}
	return nil
}

// applyManagedOtelOverrides mirrors Rust's apply_otel_requirements: managed
// exact values replace user values, exporters are replaced atomically and
// unmanaged span attributes / tracestate entries are preserved.
func applyManagedOtelOverrides(values map[string]any, requirements *ConfigRequirements) {
	if values == nil || requirements == nil || otelRequirementsEmpty(requirements.Otel) {
		return
	}
	req := requirements.Otel
	otel, _ := values["otel"].(map[string]any)
	if otel == nil {
		otel = map[string]any{}
	}
	if req.ToolResult != nil {
		otel["tool_result"] = map[string]any{"max_bytes": req.ToolResult.MaxBytes}
	}
	if req.LogUserPrompt != nil {
		otel["log_user_prompt"] = *req.LogUserPrompt
	}
	if req.LogSSEDeltas != nil {
		otel["log_sse_deltas"] = *req.LogSSEDeltas
	}
	if req.LogAgentResponses != nil {
		otel["log_agent_responses"] = *req.LogAgentResponses
	}
	if req.LogGuardianAssessments != nil {
		otel["log_guardian_assessments"] = *req.LogGuardianAssessments
	}
	if req.Environment != nil {
		otel["environment"] = *req.Environment
	}
	if req.Exporter != nil {
		otel["exporter"] = req.Exporter.ToConfigValue()
	}
	if req.TraceExporter != nil {
		otel["trace_exporter"] = req.TraceExporter.ToConfigValue()
	}
	if req.MetricsExporter != nil {
		otel["metrics_exporter"] = req.MetricsExporter.ToConfigValue()
	}
	if len(req.SpanAttributes) > 0 {
		attributes, _ := otel["span_attributes"].(map[string]any)
		if attributes == nil {
			attributes = map[string]any{}
		}
		for key, value := range req.SpanAttributes {
			attributes[key] = value
		}
		otel["span_attributes"] = attributes
	}
	if len(req.Tracestate) > 0 {
		tracestate, _ := otel["tracestate"].(map[string]any)
		if tracestate == nil {
			tracestate = map[string]any{}
		}
		for member, fields := range req.Tracestate {
			if len(fields) == 0 {
				continue
			}
			configured, _ := tracestate[member].(map[string]any)
			if configured == nil {
				configured = map[string]any{}
			}
			for key, value := range fields {
				configured[key] = value
			}
			tracestate[member] = configured
		}
		otel["tracestate"] = tracestate
	}
	values["otel"] = otel
}

// otelRequirementsConflict reports whether a configured OTEL value differs
// from a managed one, for the single source-aware override warning.
func otelRequirementsConflict(values map[string]any, requirements *OtelRequirements) bool {
	if requirements == nil || otelRequirementsEmpty(requirements) {
		return false
	}
	otel, _ := values["otel"].(map[string]any)
	conflict := false
	scalar := func(key string, managed any) {
		if configured, ok := otel[key]; ok && !reflect.DeepEqual(configured, managed) {
			conflict = true
		}
	}
	if requirements.ToolResult != nil {
		managed := map[string]any{"max_bytes": int64(requirements.ToolResult.MaxBytes)}
		if configured, ok := otel["tool_result"].(map[string]any); ok {
			if current, ok := configured["max_bytes"]; !ok || !otelNumbersEqual(current, requirements.ToolResult.MaxBytes) {
				conflict = true
			}
		} else if _, ok := otel["tool_result"]; ok && !reflect.DeepEqual(managed, otel["tool_result"]) {
			conflict = true
		}
	}
	if requirements.LogUserPrompt != nil {
		scalar("log_user_prompt", *requirements.LogUserPrompt)
	}
	if requirements.LogSSEDeltas != nil {
		scalar("log_sse_deltas", *requirements.LogSSEDeltas)
	}
	if requirements.LogAgentResponses != nil {
		scalar("log_agent_responses", *requirements.LogAgentResponses)
	}
	if requirements.LogGuardianAssessments != nil {
		scalar("log_guardian_assessments", *requirements.LogGuardianAssessments)
	}
	if requirements.Environment != nil {
		scalar("environment", *requirements.Environment)
	}
	for _, field := range []struct {
		key      string
		exporter *OtelExporter
	}{
		{"exporter", requirements.Exporter},
		{"trace_exporter", requirements.TraceExporter},
		{"metrics_exporter", requirements.MetricsExporter},
	} {
		if field.exporter == nil {
			continue
		}
		if configured, ok := otel[field.key]; ok && !reflect.DeepEqual(configured, field.exporter.ToConfigValue()) {
			conflict = true
		}
	}
	for key, value := range requirements.SpanAttributes {
		attributes, _ := otel["span_attributes"].(map[string]any)
		if configured, ok := attributes[key]; ok && configured != value {
			conflict = true
		}
	}
	for member, fields := range requirements.Tracestate {
		tracestate, _ := otel["tracestate"].(map[string]any)
		configured, _ := tracestate[member].(map[string]any)
		for key, value := range fields {
			if current, ok := configured[key]; ok && current != value {
				conflict = true
			}
		}
	}
	return conflict
}

func otelNumbersEqual(value any, expected int) bool {
	number, ok := otelConfigIntValue(value)
	return ok && int(number) == expected
}

// otelRequirementForConfigPath returns the managed OTEL field affected by a
// configuration write, including exact span-attribute and tracestate entries
// (Rust #57658 leaves unmanaged siblings writable).
func otelRequirementForConfigPath(requirements *OtelRequirements, segments []string) string {
	if requirements == nil || otelRequirementsEmpty(requirements) || len(segments) == 0 || segments[0] != "otel" {
		return ""
	}
	for _, field := range []struct {
		name   string
		exists bool
	}{
		{"tool_result", requirements.ToolResult != nil},
		{"log_user_prompt", requirements.LogUserPrompt != nil},
		{"log_sse_deltas", requirements.LogSSEDeltas != nil},
		{"log_agent_responses", requirements.LogAgentResponses != nil},
		{"log_guardian_assessments", requirements.LogGuardianAssessments != nil},
		{"environment", requirements.Environment != nil},
		{"exporter", requirements.Exporter != nil},
		{"trace_exporter", requirements.TraceExporter != nil},
		{"metrics_exporter", requirements.MetricsExporter != nil},
	} {
		if field.exists && configPathOverlaps(segments, []string{"otel", field.name}) {
			return "otel." + field.name
		}
	}
	if len(segments) >= 2 && segments[1] == "span_attributes" {
		if len(segments) == 2 {
			return "otel.span_attributes"
		}
		if _, ok := requirements.SpanAttributes[segments[2]]; ok {
			return "otel.span_attributes"
		}
	}
	if len(segments) >= 2 && segments[1] == "tracestate" {
		if len(segments) == 2 {
			return "otel.tracestate"
		}
		fields, ok := requirements.Tracestate[segments[2]]
		if !ok {
			return ""
		}
		if len(segments) == 3 {
			return "otel.tracestate"
		}
		if _, ok := fields[segments[3]]; ok {
			return "otel.tracestate"
		}
	}
	return ""
}

func configPathOverlaps(segments []string, managed []string) bool {
	length := len(segments)
	if len(managed) < length {
		length = len(managed)
	}
	for index := 0; index < length; index++ {
		if segments[index] != managed[index] {
			return false
		}
	}
	return true
}

func cloneOtelRequirements(value *OtelRequirements) *OtelRequirements {
	if value == nil {
		return nil
	}
	out := *value
	out.LogUserPrompt = cloneBoolPtr(value.LogUserPrompt)
	out.LogSSEDeltas = cloneBoolPtr(value.LogSSEDeltas)
	out.LogAgentResponses = cloneBoolPtr(value.LogAgentResponses)
	out.LogGuardianAssessments = cloneBoolPtr(value.LogGuardianAssessments)
	out.Environment = cloneStringPtr(value.Environment)
	if value.ToolResult != nil {
		limit := *value.ToolResult
		out.ToolResult = &limit
	}
	out.Exporter = cloneOtelExporter(value.Exporter)
	out.TraceExporter = cloneOtelExporter(value.TraceExporter)
	out.MetricsExporter = cloneOtelExporter(value.MetricsExporter)
	if len(value.SpanAttributes) > 0 {
		out.SpanAttributes = map[string]string{}
		for key, attribute := range value.SpanAttributes {
			out.SpanAttributes[key] = attribute
		}
	}
	if len(value.Tracestate) > 0 {
		out.Tracestate = map[string]map[string]string{}
		for member, fields := range value.Tracestate {
			copied := map[string]string{}
			for key, field := range fields {
				copied[key] = field
			}
			out.Tracestate[member] = copied
		}
	}
	return &out
}

func cloneOtelExporter(value *OtelExporter) *OtelExporter {
	if value == nil {
		return nil
	}
	out := *value
	if len(value.Headers) > 0 {
		out.Headers = map[string]string{}
		for key, header := range value.Headers {
			out.Headers[key] = header
		}
	}
	if value.TLS != nil {
		tls := *value.TLS
		tls.CACertificate = cloneStringPtr(value.TLS.CACertificate)
		tls.ClientCertificate = cloneStringPtr(value.TLS.ClientCertificate)
		tls.ClientPrivateKey = cloneStringPtr(value.TLS.ClientPrivateKey)
		out.TLS = &tls
	}
	return &out
}

// mergeOtelRequirements applies higher-priority exact values while keeping
// unmanaged metadata from the lower layer (Rust's merge_otel_requirements).
func mergeOtelRequirements(lower, higher *OtelRequirements) *OtelRequirements {
	if higher == nil || otelRequirementsEmpty(higher) {
		return lower
	}
	out := cloneOtelRequirements(lower)
	if out == nil {
		out = &OtelRequirements{}
	}
	if higher.ToolResult != nil {
		limit := *higher.ToolResult
		out.ToolResult = &limit
	}
	if higher.LogUserPrompt != nil {
		out.LogUserPrompt = cloneBoolPtr(higher.LogUserPrompt)
	}
	if higher.LogSSEDeltas != nil {
		out.LogSSEDeltas = cloneBoolPtr(higher.LogSSEDeltas)
	}
	if higher.LogAgentResponses != nil {
		out.LogAgentResponses = cloneBoolPtr(higher.LogAgentResponses)
	}
	if higher.LogGuardianAssessments != nil {
		out.LogGuardianAssessments = cloneBoolPtr(higher.LogGuardianAssessments)
	}
	if higher.Environment != nil {
		out.Environment = cloneStringPtr(higher.Environment)
	}
	if higher.Exporter != nil {
		out.Exporter = cloneOtelExporter(higher.Exporter)
	}
	if higher.TraceExporter != nil {
		out.TraceExporter = cloneOtelExporter(higher.TraceExporter)
	}
	if higher.MetricsExporter != nil {
		out.MetricsExporter = cloneOtelExporter(higher.MetricsExporter)
	}
	if len(higher.SpanAttributes) > 0 {
		if out.SpanAttributes == nil {
			out.SpanAttributes = map[string]string{}
		}
		for key, value := range higher.SpanAttributes {
			out.SpanAttributes[key] = value
		}
	}
	for member, fields := range higher.Tracestate {
		if len(fields) == 0 {
			continue
		}
		if out.Tracestate == nil {
			out.Tracestate = map[string]map[string]string{}
		}
		if out.Tracestate[member] == nil {
			out.Tracestate[member] = map[string]string{}
		}
		for key, value := range fields {
			out.Tracestate[member][key] = value
		}
	}
	return out
}

// redactedOtelRequirements returns the API projection with every exporter
// header value replaced by [redacted] (Rust #57654).
// wireOtelRequirements is the app-server ConfigRequirements.otel shape (Rust
// #57638). The managed TOML surface carries three extra requirements
// (`log_sse_deltas`, `span_attributes`, `tracestate`) that the protocol type
// does not expose, so they stay off the wire.
type wireOtelRequirements struct {
	ToolResult             *OtelToolResultLogConfig `json:"tool_result"`
	LogUserPrompt          *bool                    `json:"log_user_prompt"`
	LogAgentResponses      *bool                    `json:"log_agent_responses"`
	LogGuardianAssessments *bool                    `json:"log_guardian_assessments"`
	Environment            *string                  `json:"environment"`
	Exporter               *OtelExporter            `json:"exporter"`
	TraceExporter          *OtelExporter            `json:"trace_exporter"`
	MetricsExporter        *OtelExporter            `json:"metrics_exporter"`
}

// otelRequirementsForWire projects managed OTEL requirements onto the
// app-server wire shape, redacting exporter header values (Rust #57654).
func otelRequirementsForWire(value *OtelRequirements) *wireOtelRequirements {
	redacted := redactedOtelRequirements(value)
	if redacted == nil {
		return nil
	}
	out := &wireOtelRequirements{
		ToolResult:             redacted.ToolResult,
		LogUserPrompt:          cloneBoolPtr(redacted.LogUserPrompt),
		LogAgentResponses:      cloneBoolPtr(redacted.LogAgentResponses),
		LogGuardianAssessments: cloneBoolPtr(redacted.LogGuardianAssessments),
		Environment:            cloneStringPtr(redacted.Environment),
		Exporter:               redacted.Exporter,
		TraceExporter:          redacted.TraceExporter,
		MetricsExporter:        redacted.MetricsExporter,
	}
	if redacted.ToolResult != nil {
		copied := *redacted.ToolResult
		out.ToolResult = &copied
	}
	return out
}

func redactedOtelRequirements(value *OtelRequirements) *OtelRequirements {
	out := cloneOtelRequirements(value)
	if out == nil {
		return nil
	}
	out.Exporter = redactedOtelExporter(out.Exporter)
	out.TraceExporter = redactedOtelExporter(out.TraceExporter)
	out.MetricsExporter = redactedOtelExporter(out.MetricsExporter)
	return out
}

func redactedOtelExporter(value *OtelExporter) *OtelExporter {
	out := cloneOtelExporter(value)
	if out == nil || len(out.Headers) == 0 {
		return out
	}
	for key := range out.Headers {
		out.Headers[key] = "[redacted]"
	}
	return out
}

// RedactOtelExporterHeaders masks header values in a raw config `otel` table
// (effective config, source layers and write-override metadata) while keeping
// header names.
func RedactOtelExporterHeaders(otel any) {
	table, ok := otel.(map[string]any)
	if !ok {
		return
	}
	for _, field := range []string{"exporter", "trace_exporter", "metrics_exporter"} {
		exporter, ok := table[field].(map[string]any)
		if !ok {
			continue
		}
		for _, config := range exporter {
			body, ok := config.(map[string]any)
			if !ok {
				continue
			}
			headers, ok := body["headers"].(map[string]any)
			if !ok {
				continue
			}
			for name := range headers {
				headers[name] = "[redacted]"
			}
		}
	}
}
