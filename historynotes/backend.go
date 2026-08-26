// Package historynotes mirrors Rust ext/history-notes (#39827): history and
// notes tools for token-budget sessions, backed by the Codex backend.
package historynotes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const backendTimeout = 35 * time.Second

// Backend calls the Codex backend history/notes endpoints (Rust
// HistoryNotesBackend). The embedding router resolves the base URL and auth
// headers from the effective OpenAI provider + codex backend auth.
type Backend struct {
	BaseURL   string
	ApplyAuth func(*http.Request, []byte) error
	HTTPDoer  func(*http.Request) (*http.Response, error)
}

func (b *Backend) Call(ctx context.Context, path string, sessionID string, currentAgentName string, arguments map[string]any) (json.RawMessage, error) {
	if b == nil {
		return nil, fmt.Errorf("history backend is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	arguments["context"] = map[string]any{
		"session_id":         sessionID,
		"current_agent_name": currentAgentName,
	}
	body, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("history backend arguments could not be encoded: %w", err)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(b.BaseURL), "/") + "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	requestCtx, cancel := context.WithTimeout(ctx, backendTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("history backend request could not be built: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if b.ApplyAuth != nil {
		if err := b.ApplyAuth(request, body); err != nil {
			return nil, fmt.Errorf("history backend auth failed: %w", err)
		}
	}
	do := b.HTTPDoer
	if do == nil {
		do = http.DefaultClient.Do
	}
	response, err := do(request)
	if err != nil {
		return nil, fmt.Errorf("history backend request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("history backend request failed with status %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("history backend response could not be read: %w", err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("history backend returned invalid JSON")
	}
	return json.RawMessage(data), nil
}

// ThreadHint fetches the history-notes context-window hint for a thread
// (Rust #40539 contribute_thread_context -> alpha/notes/v2/thread_hint). It
// returns the trimmed hint and true when non-empty and at most
// MaxThreadHintBytes; oversized, empty, or failed requests omit the hint.
func (b *Backend) ThreadHint(ctx context.Context, sessionID string, currentAgentName string) (string, bool) {
	data, err := b.Call(ctx, "alpha/notes/v2/thread_hint", sessionID, currentAgentName, map[string]any{})
	if err != nil {
		return "", false
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" || len(text) > MaxThreadHintBytes {
		return "", false
	}
	return text, true
}

// MaxThreadHintBytes bounds the history-notes context hint (Rust #40539).
const MaxThreadHintBytes = 4_000
