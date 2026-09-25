package retainedctx

import "strings"

// HeartbeatContentKind is Rust's `HEARTBEAT_CONTENT_KIND`.
const HeartbeatContentKind = "user.heartbeat"

// UserInputOrigin mirrors `history::UserInputOrigin`: provenance of one accepted
// input, independent of the active turn's trigger.
type UserInputOrigin string

const (
	// UserInputOriginUser is ordinary or unclassified input, never eligible for
	// heartbeat coalescing.
	UserInputOriginUser UserInputOrigin = "user"
	// UserInputOriginHeartbeat is input submitted by a scheduled heartbeat,
	// including when it steers an active turn.
	UserInputOriginHeartbeat UserInputOrigin = "heartbeat"
)

// IsUser reports whether the origin is the ordinary-user default, which is the
// value serde omits when serializing retained messages.
func (o UserInputOrigin) IsUser() bool { return o == UserInputOriginUser }

// normalizeOrigin collapses Go's zero value onto Rust's `User` default so an
// unset origin never round-trips into a different stored value.
func normalizeOrigin(origin UserInputOrigin) UserInputOrigin {
	if origin == "" {
		return UserInputOriginUser
	}
	return origin
}

// UserInputOriginFromTurnTrigger mirrors `UserInputOrigin::from_turn_trigger`.
func UserInputOriginFromTurnTrigger(trigger *string) UserInputOrigin {
	if trigger != nil && *trigger == "automation_heartbeat_scheduled" {
		return UserInputOriginHeartbeat
	}
	return UserInputOriginUser
}

// UserInputOriginFromMessage mirrors `UserInputOrigin::from_message` for a
// Responses API message: only a user message whose internal passthrough metadata
// carries exactly the heartbeat content kind is a heartbeat. A nil
// contentItemKinds slice is the absent metadata case.
func UserInputOriginFromMessage(role string, contentItemKinds []string) UserInputOrigin {
	if role == "user" && len(contentItemKinds) == 1 && contentItemKinds[0] == HeartbeatContentKind {
		return UserInputOriginHeartbeat
	}
	return UserInputOriginUser
}

// Heartbeat is a bounded scheduler envelope. Instruction equality excludes only
// invocation time.
type Heartbeat struct {
	AutomationID string
	Timestamp    string
	Instructions string
}

// HeartbeatFromMessage mirrors `Heartbeat::from_message`: the message must be a
// heartbeat whose content is exactly one input text item.
func HeartbeatFromMessage(origin UserInputOrigin, content []string) (Heartbeat, bool) {
	if origin != UserInputOriginHeartbeat || len(content) != 1 {
		return Heartbeat{}, false
	}
	return ParseHeartbeat(content[0])
}

// ParseHeartbeat mirrors `Heartbeat::parse`. Unfamiliar wrappers, extra content,
// oversized inputs and unknown origins stay unparsed.
func ParseHeartbeat(text string) (Heartbeat, bool) {
	// Reserve the retained section's order label and its per-line "user: " prefix.
	// Oversized definitions keep their existing representation in every consumer.
	if len(text)+rustLineCount(text)*6+64 > 3600 {
		return Heartbeat{}, false
	}
	// The scheduler terminates the envelope with a newline; preserve body whitespace.
	trimmed := strings.TrimSuffix(text, "\n")
	rest, ok := strings.CutPrefix(trimmed, "<heartbeat>\n  <automation_id>")
	if !ok {
		return Heartbeat{}, false
	}
	automationID, rest, ok := strings.Cut(rest, "</automation_id>\n  <current_time_iso>")
	if !ok {
		return Heartbeat{}, false
	}
	timestamp, rest, ok := strings.Cut(rest, "</current_time_iso>\n  <instructions>\n")
	if !ok {
		return Heartbeat{}, false
	}
	instructions, ok := strings.CutSuffix(rest, "\n  </instructions>\n</heartbeat>")
	if !ok {
		return Heartbeat{}, false
	}
	if automationID == "" ||
		len(automationID) > 128 ||
		!allBytes(automationID, func(c byte) bool {
			return isASCIIAlphanumeric(c) || c == '-' || c == '_'
		}) ||
		timestamp == "" ||
		len(timestamp) > 64 ||
		!allBytes(timestamp, func(c byte) bool {
			return (c >= '0' && c <= '9') || c == 'T' || c == 'Z' || c == ':' || c == '.' || c == '+' || c == '-'
		}) {
		return Heartbeat{}, false
	}
	return Heartbeat{AutomationID: automationID, Timestamp: timestamp, Instructions: instructions}, true
}

// rustLineCount counts `str::lines()` the way Rust does: split at `\n` or `\r\n`
// with an optional final line ending.
func rustLineCount(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return count
	}
	return count + 1
}

func allBytes(text string, predicate func(byte) bool) bool {
	for i := 0; i < len(text); i++ {
		if !predicate(text[i]) {
			return false
		}
	}
	return true
}

func isASCIIAlphanumeric(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
