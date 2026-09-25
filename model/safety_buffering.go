package model

import (
	"net/http"
	"strings"
)

// Rust parity: codex-api/src/safety_buffering.rs.
//
// The response-side safety-buffering treatment is carried by two headers rather
// than by the payload: an enabled flag and the faster model to fall back to when
// the payload omits its own wire `retry_model`.

const (
	xCodexSafetyBufferingEnabledHeader     = "x-codex-safety-buffering-enabled"
	xCodexSafetyBufferingFasterModelHeader = "x-codex-safety-buffering-faster-model"
)

// safetyBufferingTreatment mirrors Rust's `SafetyBufferingTreatment`.
type safetyBufferingTreatment struct {
	// fasterModel is the model the payload falls back to when it does not carry
	// its own wire `retry_model`.
	fasterModel *string
}

// safetyBufferingTreatmentFromHeaders mirrors Rust's `treatment_from_headers`: a
// response only carries a treatment when at least one of the two headers is
// present, and the faster model comes from its own header.
func safetyBufferingTreatmentFromHeaders(headers http.Header) (safetyBufferingTreatment, bool) {
	if len(headers) == 0 {
		return safetyBufferingTreatment{}, false
	}
	_, enabled := headerValuesPresent(headers, xCodexSafetyBufferingEnabledHeader)
	model, modelPresent := headerValuesPresent(headers, xCodexSafetyBufferingFasterModelHeader)
	if !enabled && !modelPresent {
		return safetyBufferingTreatment{}, false
	}
	treatment := safetyBufferingTreatment{}
	if modelPresent {
		value := model
		treatment.fasterModel = &value
	}
	return treatment, true
}

// safetyBufferingTreatmentFromJSONHeaders converts a Responses event's JSON
// `headers` object into a treatment, the WebSocket path's form of the same rule
// (Rust's `json_headers_to_http_headers` plus `treatment_from_headers`).
func safetyBufferingTreatmentFromJSONHeaders(headers map[string]any) (safetyBufferingTreatment, bool) {
	return safetyBufferingTreatmentFromHeaders(jsonHeadersToHTTPHeaders(headers))
}

// headerValuesPresent reports whether any spelling of name is present and returns
// the first (canonical) value in that case.
func headerValuesPresent(headers http.Header, name string) (string, bool) {
	if headers == nil {
		return "", false
	}
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		if len(values) == 0 {
			return "", true
		}
		return values[0], true
	}
	return "", false
}
