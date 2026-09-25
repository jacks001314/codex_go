package model

import (
	"net/http"
	"strconv"
)

// Rust parity: codex-api/src/responses_headers.rs (#48200).
//
// Responses event payloads can carry a JSON `headers` object that stands in for
// the HTTP headers of a wrapped transport. Conversion accepts string, number and
// boolean values and ignores invalid header names or unsupported/invalid values,
// so a malformed event can never inject a header.

// jsonHeadersToHTTPHeaders converts a JSON headers object the way Rust's
// `json_headers_to_http_headers` does, returning nil when nothing survives.
func jsonHeadersToHTTPHeaders(headers map[string]any) http.Header {
	if len(headers) == 0 {
		return nil
	}
	mapped := http.Header{}
	for name, value := range headers {
		if !validHTTPHeaderName(name) {
			continue
		}
		text, ok := jsonHeaderValue(value)
		if !ok {
			continue
		}
		mapped.Set(name, text)
	}
	if len(mapped) == 0 {
		return nil
	}
	return mapped
}

// jsonHeaderValue mirrors `json_header_value`: only scalars are usable, and the
// rendered text must be a valid header value.
func jsonHeaderValue(value any) (string, bool) {
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case float64:
		// Rust's `Number::to_string` prints the JSON representation; Go decodes
		// JSON numbers to float64, so integral values lose their ".0" suffix.
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	case bool:
		if typed {
			text = "true"
		} else {
			text = "false"
		}
	default:
		return "", false
	}
	if !validHTTPHeaderValue(text) {
		return "", false
	}
	return text, true
}

// validHTTPHeaderName mirrors `HeaderName::from_bytes`: only RFC 7230 token
// characters are accepted.
func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isHTTPTokenByte(name[i]) {
			return false
		}
	}
	return true
}

func isHTTPTokenByte(value byte) bool {
	switch {
	case value >= '0' && value <= '9':
		return true
	case value >= 'a' && value <= 'z':
		return true
	case value >= 'A' && value <= 'Z':
		return true
	}
	switch value {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

// validHTTPHeaderValue mirrors `HeaderValue::from_str`: visible ASCII plus tab,
// with no leading or trailing whitespace.
func validHTTPHeaderValue(value string) bool {
	if value == "" {
		return true
	}
	for i := 0; i < len(value); i++ {
		character := value[i]
		if character == '\t' || (character >= 0x20 && character <= 0x7E) {
			continue
		}
		return false
	}
	if value[0] == ' ' || value[0] == '\t' {
		return false
	}
	last := value[len(value)-1]
	return last != ' ' && last != '\t'
}
