package rollout

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// attachedHistoryPrefixLines bounds the header emitted before an attached
// fork's own content, matching Rust thread-store's `attached_history_start`
// (#57256): session metadata, the settings snapshot, model guidance, and the
// inherited abort marker.
const attachedHistoryPrefixLines = 4

// AttachedHistoryStart returns the ordinal where an attached fork's visible
// history begins. A mid-turn fork starts its own rollout with the parent's
// abort marker; client-visible views must begin after that marker so inherited
// interruptions never appear as the child's activity.
func AttachedHistoryStart(path string, start uint64) (uint64, error) {
	reader, err := OpenRolloutLineReader(path)
	if err != nil {
		return start, err
	}
	defer reader.Close()
	for i := 0; i < attachedHistoryPrefixLines; i++ {
		raw, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return start, err
		}
		var line Line
		if err := json.Unmarshal(bytes.TrimSpace(raw), &line); err != nil {
			break
		}
		switch line.Type {
		case "session_meta", "response_item":
			continue
		case "event_msg":
			switch attachedPayloadType(line.Payload) {
			case "thread_settings_applied":
				continue
			case "turn_aborted":
				if line.Ordinal == nil || *line.Ordinal == ^uint64(0) {
					return start, nil
				}
				return *line.Ordinal + 1, nil
			default:
				return start, nil
			}
		default:
			return start, nil
		}
	}
	return start, nil
}

func attachedPayloadType(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var decoded struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(decoded.Type)
}
