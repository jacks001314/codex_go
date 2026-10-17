package agentboard

// Service-computed permissions and structured denials. Codex exposes these
// values without evaluating ACLs locally.
//
// Rust parity: codex-rs/ext/agent-message-board/src/permissions.rs (#52568).

import (
	"encoding/json"
	"errors"
	"strings"
)

// BoardPermissions describes the effective board actions for the caller.
type BoardPermissions struct {
	CreateChannel     bool `json:"create_channel"`
	EditMetadata      bool `json:"edit_metadata"`
	ManagePermissions bool `json:"manage_permissions"`
}

// ChannelPermissions describes effective channel actions. Replying does not
// imply permission to start a new thread.
type ChannelPermissions struct {
	Read         bool `json:"read"`
	Post         bool `json:"post"`
	Reply        bool `json:"reply"`
	EditMetadata bool `json:"edit_metadata"`
}

// PermissionDenied is a service denial retained through the board error
// interface. Action and Resource are optional diagnostics.
type PermissionDenied struct {
	Message  string  `json:"message"`
	Action   *string `json:"action,omitempty"`
	Resource *string `json:"resource,omitempty"`
}

func (e *PermissionDenied) Error() string {
	if e == nil {
		return ""
	}
	return "permission_denied: " + e.Message
}

// ModelResponse renders the denial as bounded model-facing JSON. The complete
// payload stays below the smaller of the call budget and 768 bytes.
func (e *PermissionDenied) ModelResponse(budget int) (map[string]any, error) {
	if e == nil {
		return nil, errors.New("permission denial is nil")
	}
	limit := 512
	maxBytes := 768
	if budget < maxBytes {
		maxBytes = budget
	}
	for {
		truncated := runeCount(e.Message) > limit ||
			(e.Action != nil && runeCount(*e.Action) > limit) ||
			(e.Resource != nil && runeCount(*e.Resource) > limit)
		payload := map[string]any{
			"code":    "permission_denied",
			"message": truncateRunes(e.Message, limit),
		}
		if e.Action != nil {
			payload["action"] = truncateRunes(*e.Action, limit)
		}
		if e.Resource != nil {
			payload["resource"] = truncateRunes(*e.Resource, limit)
		}
		if truncated {
			payload["truncated"] = true
		}
		result := map[string]any{"error": payload}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= maxBytes {
			return result, nil
		}
		if limit == 0 {
			return nil, errors.New("permission_denied: output budget too small for the error")
		}
		limit /= 2
	}
}

func runeCount(value string) int {
	return len([]rune(value))
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func permissionDenied(message string, action string, resource string) error {
	denied := &PermissionDenied{Message: strings.TrimSpace(message)}
	if strings.TrimSpace(action) != "" {
		value := strings.TrimSpace(action)
		denied.Action = &value
	}
	if strings.TrimSpace(resource) != "" {
		value := strings.TrimSpace(resource)
		denied.Resource = &value
	}
	return denied
}
