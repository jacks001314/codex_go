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
const encryptedToolArgumentsHeader = "x-openai-encrypted-tool-arguments"
const toolOutputTruncationPolicyHeader = "x-openai-tool-output-truncation-policy"

// operationErrorPrefix mirrors Rust ext/history-notes (#41235): user-facing
// backend failures use a consistent message that omits underlying error details.
const operationErrorPrefix = "Unable to perform operation:"

// ToolTruncationPolicy is the serialized form of the output truncation policy
// forwarded to the history/notes backend (Rust protocol TruncationPolicy,
// #41062): {"mode":"bytes"|"tokens","limit":N}.
type ToolTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int    `json:"limit"`
}

// encryptedArgumentsRoute reports whether the backend route carries sensitive
// tool arguments that must be marked encrypted (Rust #41041). The JSON request
// body is unchanged; only the header signals that the route's search query /
// note text fields are encrypted.
func encryptedArgumentsRoute(path string) bool {
	switch strings.TrimSpace(path) {
	case "alpha/history/v2/search_contents",
		"alpha/notes/v2/search_contents",
		"alpha/notes/v2/append_to_file",
		"alpha/notes/v2/write_file":
		return true
	default:
		return false
	}
}

// Backend calls the Codex backend history/notes endpoints (Rust
// HistoryNotesBackend). The embedding router resolves the base URL and auth
// headers from the effective OpenAI provider + codex backend auth.
type Backend struct {
	BaseURL              string
	ApplyAuth            func(*http.Request, []byte) error
	HTTPDoer             func(*http.Request) (*http.Response, error)
	ToolTruncationPolicy *ToolTruncationPolicy
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
		return nil, fmt.Errorf("%s The backend arguments could not be encoded.", operationErrorPrefix)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(b.BaseURL), "/") + "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	requestCtx, cancel := context.WithTimeout(ctx, backendTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s The backend request could not be built.", operationErrorPrefix)
	}
	request.Header.Set("Content-Type", "application/json")
	if encryptedArgumentsRoute(path) {
		request.Header.Set(encryptedToolArgumentsHeader, "true")
	}
	if b.ToolTruncationPolicy != nil {
		encoded, err := json.Marshal(b.ToolTruncationPolicy)
		if err != nil {
			return nil, fmt.Errorf("%s Could not encode the output truncation policy.", operationErrorPrefix)
		}
		request.Header.Set(toolOutputTruncationPolicyHeader, string(encoded))
	}
	if b.ApplyAuth != nil {
		if err := b.ApplyAuth(request, body); err != nil {
			return nil, fmt.Errorf("%s Could not apply backend authentication.", operationErrorPrefix)
		}
	}
	do := b.HTTPDoer
	if do == nil {
		do = http.DefaultClient.Do
	}
	response, err := do(request)
	if err != nil {
		return nil, fmt.Errorf("%s The backend request failed.", operationErrorPrefix)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s The backend request failed with status %s.", operationErrorPrefix, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("%s The backend response could not be read.", operationErrorPrefix)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("%s The backend returned invalid JSON.", operationErrorPrefix)
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
