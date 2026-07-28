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
		Type        string          `json:"type"`
		CWD         string          `json:"cwd"`
		Timestamp   string          `json:"timestamp"`
		TimestampMS *int64          `json:"timestamp_ms"`
		SessionID   string          `json:"sessionId"`
		IsMeta      bool            `json:"isMeta"`
		IsSidechain bool            `json:"isSidechain"`
		Message     json.RawMessage `json:"message"`
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
		if row.IsMeta || row.IsSidechain {
			continue
		}
		if cwd == "" {
			cwd = strings.TrimSpace(row.CWD)
		}
		if sourceID == "" {
			sourceID = strings.TrimSpace(row.SessionID)
		}
		timestamp := fallback
		hasTimestamp := false
		if parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(row.Timestamp)); parseErr == nil {
			timestamp = parsed.UTC()
			hasTimestamp = true
		} else if row.TimestampMS != nil {
			timestamp = time.UnixMilli(*row.TimestampMS).UTC()
			hasTimestamp = true
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
		if role == "user" {
			text = externalCursorUserQuery(text)
		}
		if hasTimestamp {
			if created.IsZero() || timestamp.Before(created) {
				created = timestamp
			}
			if updated.IsZero() || timestamp.After(updated) {
				updated = timestamp
			}
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

// ExternalCursorSessionRecord imports Cursor agent-transcript JSONL records.
func ExternalCursorSessionRecord(path string, fallback time.Time) (*session.Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	}
	if info, statErr := file.Stat(); statErr == nil {
		fallback = info.ModTime().UTC()
	}
	type sourceMessage struct {
		Role        string          `json:"role"`
		CWD         string          `json:"cwd"`
		Timestamp   string          `json:"timestamp"`
		TimestampMS *int64          `json:"timestamp_ms"`
		IsMeta      bool            `json:"isMeta"`
		IsSidechain bool            `json:"isSidechain"`
		Message     json.RawMessage `json:"message"`
	}
	var items []session.Item
	var cwd string
	created, updated := time.Time{}, time.Time{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for index := 1; scanner.Scan(); index++ {
		var row sourceMessage
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.IsMeta || row.IsSidechain || (row.Role != "user" && row.Role != "assistant") {
			continue
		}
		text := externalAgentMessageText(row.Message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if row.Role == "user" {
			text = externalCursorUserQuery(text)
		}
		if cwd == "" {
			cwd = strings.TrimSpace(row.CWD)
		}
		timestamp := fallback
		if parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(row.Timestamp)); parseErr == nil {
			timestamp = parsed.UTC()
		} else if row.TimestampMS != nil {
			timestamp = time.UnixMilli(*row.TimestampMS).UTC()
		}
		if created.IsZero() || timestamp.Before(created) {
			created = timestamp
		}
		if updated.IsZero() || timestamp.After(updated) {
			updated = timestamp
		}
		items = append(items, session.Item{ID: fmt.Sprintf("external-item-%d", index), Type: row.Role + "_message", Role: row.Role, Text: text, CreatedAt: timestamp})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("external agent session contains no messages: %s", path)
	}
	if created.IsZero() {
		created = fallback
	}
	if updated.IsZero() {
		updated = created
	}
	sourceID := fmt.Sprintf("external-%d", created.UnixNano())
	return &session.Record{ID: session.ThreadID(sourceID), SessionID: sourceID, Preview: items[0].Text, CreatedAt: created, UpdatedAt: updated, RecencyAt: updated, Metadata: session.Metadata{CWD: cwd, Source: "external_agent_import"}, Items: items}, nil
}

func externalCursorUserQuery(text string) string {
	trimmed := strings.TrimSpace(text)
	start := strings.Index(trimmed, "<user_query>")
	end := strings.LastIndex(trimmed, "</user_query>")
	if start >= 0 && end > start && strings.TrimSpace(trimmed[end+len("</user_query>"):]) == "" {
		inner := strings.TrimSpace(trimmed[start+len("<user_query>") : end])
		if inner != "" {
			return inner
		}
	}
	return text
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
