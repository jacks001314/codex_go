package rollout

// CodexErrorInfoOther is Rust's fallback classification for an unrecognized
// saved error (codex_protocol::protocol::CodexErrorInfo::Other).
const CodexErrorInfoOther = "other"

// codexErrorInfoUnitTags are the classifications without a payload. Both the
// core snake_case spellings Rust writes into rollouts and the camelCase
// spellings the app-server protocol uses are recognized.
var codexErrorInfoUnitTags = map[string]bool{
	"context_window_exceeded":       true,
	"contextWindowExceeded":         true,
	"session_budget_exceeded":       true,
	"sessionBudgetExceeded":         true,
	"usage_limit_exceeded":          true,
	"usageLimitExceeded":            true,
	"rate_limit_exceeded":           true,
	"rateLimitExceeded":             true,
	"server_overloaded":             true,
	"serverOverloaded":              true,
	"flex_unavailable":              true,
	"flexUnavailable":               true,
	"cyber_policy":                  true,
	"cyberPolicy":                   true,
	"bio_policy":                    true,
	"bioPolicy":                     true,
	"misalignment_policy_violation": true,
	"misalignmentPolicyViolation":   true,
	"internal_server_error":         true,
	"internalServerError":           true,
	"unauthorized":                  true,
	"bad_request":                   true,
	"badRequest":                    true,
	"sandbox_error":                 true,
	"sandboxError":                  true,
	"thread_rollback_failed":        true,
	"threadRollbackFailed":          true,
	"other":                         true,
}

// codexErrorInfoStatusTags carry an optional HTTP status code.
var codexErrorInfoStatusTags = map[string]bool{
	"http_connection_failed":            true,
	"httpConnectionFailed":              true,
	"response_stream_connection_failed": true,
	"responseStreamConnectionFailed":    true,
	"response_stream_disconnected":      true,
	"responseStreamDisconnected":        true,
	"response_too_many_failed_attempts": true,
	"responseTooManyFailedAttempts":     true,
}

// codexErrorInfoTurnKindTags carry the non-steerable turn kind.
var codexErrorInfoTurnKindTags = map[string]bool{
	"active_turn_not_steerable": true,
	"activeTurnNotSteerable":    true,
}

// normalizeCodexErrorInfoClassification mirrors Rust #46482's CodexErrorInfo
// deserialization for saved records: known classifications are preserved,
// unknown ones become "other" so the enclosing record stays readable, and a
// known classification whose payload is invalid is rejected. The bool result
// reports whether the record is readable.
func normalizeCodexErrorInfoClassification(raw any) (any, bool) {
	switch value := raw.(type) {
	case nil:
		return nil, true
	case string:
		if codexErrorInfoUnitTags[value] {
			return value, true
		}
		if codexErrorInfoStatusTags[value] || codexErrorInfoTurnKindTags[value] {
			// A payload-carrying classification must not arrive as a bare
			// string (Rust's enum deserialization rejects it).
			return nil, false
		}
		return CodexErrorInfoOther, true
	case map[string]any:
		if len(value) != 1 {
			return CodexErrorInfoOther, true
		}
		for tag, payload := range value {
			switch {
			case codexErrorInfoUnitTags[tag]:
				return value, true
			case codexErrorInfoStatusTags[tag]:
				if !validCodexErrorInfoStatusPayload(payload) {
					return nil, false
				}
				return value, true
			case codexErrorInfoTurnKindTags[tag]:
				if !validCodexErrorInfoTurnKindPayload(payload) {
					return nil, false
				}
				return value, true
			default:
				return CodexErrorInfoOther, true
			}
		}
	}
	return raw, true
}

func validCodexErrorInfoStatusPayload(payload any) bool {
	if payload == nil {
		return true
	}
	fields, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range fields {
		switch key {
		case "http_status_code", "httpStatusCode":
			if value == nil {
				continue
			}
			status, ok := uint64FromAnyValue(value)
			if !ok || status > 65535 {
				return false
			}
		default:
			// Unknown payload fields are ignored, matching serde's default.
		}
	}
	return true
}

func validCodexErrorInfoTurnKindPayload(payload any) bool {
	fields, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range fields {
		switch key {
		case "turn_kind", "turnKind":
			text, ok := value.(string)
			if !ok {
				return false
			}
			// Rust's NonSteerableTurnKind only accepts review and compact.
			return text == "review" || text == "compact"
		}
	}
	return false
}

func uint64FromAnyValue(value any) (uint64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed < 0 || typed != float64(uint64(typed)) {
			return 0, false
		}
		return uint64(typed), true
	case int:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case int64:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case uint64:
		return typed, true
	}
	return 0, false
}
