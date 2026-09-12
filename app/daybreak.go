package app

import (
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/codexapi"
)

// turnErrorIsCyberPolicy reports whether the server classified a turn error as a
// cybersecurity policy refusal (Rust CodexErrorInfo::CyberPolicy), so the TUI can
// render the Daybreak-aware refusal copy.
func turnErrorIsCyberPolicy(turnErr appserver.TurnError) bool {
	return normalizeCodexErrorKind(turnErr.CodexErrorInfo) == "cyberpolicy"
}

// cyberPolicyError reports whether a local turn failed with a cybersecurity
// policy refusal (Rust CodexErrorInfo::CyberPolicy).
func cyberPolicyError(err error) bool {
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return false
	}
	return apiErr.Details().Kind == codexapi.ErrorCyberPolicy
}

// normalizeCodexErrorKind reads the kind out of a codexErrorInfo payload, which
// is a bare string for most classifications and a tagged object for the ones
// carrying an HTTP status.
func normalizeCodexErrorKind(info any) string {
	text := ""
	switch value := info.(type) {
	case string:
		text = value
	case map[string]any:
		for _, key := range []string{"type", "kind"} {
			if candidate, ok := value[key].(string); ok {
				text = candidate
				break
			}
		}
	}
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.ReplaceAll(text, "_", "")
	text = strings.ReplaceAll(text, "-", "")
	return text
}
