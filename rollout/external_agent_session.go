package rollout

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"codex_go/session"
)

// ExternalAgentSessionRecord imports the stable message subset emitted by
// external-agent connectors. Unknown records are ignored deliberately.
func ExternalAgentSessionRecord(path string, fallback time.Time) (*session.Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	}
	type sourceMessage struct {
		Type      string          `json:"type"`
		CWD       string          `json:"cwd"`
		Timestamp string          `json:"timestamp"`
		SessionID string          `json:"sessionId"`
		Message   json.RawMessage `json:"message"`
	}
	var rows []sourceMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var row sourceMessage
		if json.Unmarshal(scanner.Bytes(), &row) == nil {
			rows = append(rows, row)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	var created, updated time.Time
	var items []session.Item
	var cwd, sourceID string
	for index, row := range rows {
		if cwd == "" {
			cwd = strings.TrimSpace(row.CWD)
		}
		if sourceID == "" {
			sourceID = strings.TrimSpace(row.SessionID)
		}
		timestamp := fallback
		if parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(row.Timestamp)); parseErr == nil {
			timestamp = parsed.UTC()
			if created.IsZero() || timestamp.Before(created) {
				created = timestamp
			}
			if updated.IsZero() || timestamp.After(updated) {
				updated = timestamp
			}
		}
		role := ""
		switch strings.ToLower(strings.TrimSpace(row.Type)) {
		case "user":
			role = "user"
		case "assistant":
			role = "assistant"
		default:
			continue
		}
		text := externalAgentMessageText(row.Message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		items = append(items, session.Item{
			ID:        fmt.Sprintf("external-item-%d", index+1),
			Type:      role + "_message",
			Role:      role,
			Text:      text,
			CreatedAt: timestamp,
		})
	}
	if created.IsZero() {
		created = fallback.UTC()
	}
	if updated.IsZero() {
		updated = created
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("external agent session contains no messages: %s", path)
	}
	if sourceID == "" {
		sourceID = fmt.Sprintf("external-%d", created.UnixNano())
	}
	return &session.Record{
		ID:        session.ThreadID(sourceID),
		SessionID: sourceID,
		Preview:   items[0].Text,
		CreatedAt: created,
		UpdatedAt: updated,
		RecencyAt: updated,
		Metadata:  session.Metadata{CWD: cwd, Source: "external_agent_import"},
		Items:     items,
	}, nil
}

func externalAgentMessageText(raw json.RawMessage) string {
	var value struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(value.Content, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(value.Content, &parts) == nil {
		for _, part := range parts {
			if strings.TrimSpace(part.Text) != "" {
				if text != "" {
					text += "\n"
				}
				text += part.Text
			}
		}
	}
	return text
}
