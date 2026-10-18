package chatgptapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// ChatgptTurnCost is the workspace-visible estimate for one ChatGPT turn.
// Missing amounts and settlement IDs stay nil so callers never interpret an
// absent value as a zero-cost or fully settled turn (Rust #41944).
type ChatgptTurnCost struct {
	TurnID                  string   `json:"turn_id"`
	Model                   *string  `json:"model,omitempty"`
	EstimatedUsageUSDMicros *int64   `json:"estimated_usage_usd_micros,omitempty"`
	SettledResponseIDs      []string `json:"settled_response_ids,omitempty"`
}

// ChatgptThreadTurnCosts groups turn estimates by thread.
type ChatgptThreadTurnCosts struct {
	ThreadID string            `json:"thread_id"`
	Turns    []ChatgptTurnCost `json:"turns"`
}

type threadTurnIDs struct {
	ThreadID string   `json:"thread_id"`
	TurnIDs  []string `json:"turn_ids"`
}

type turnCostsRequest struct {
	Threads                   []threadTurnIDs `json:"threads"`
	IncludeSettledResponseIDs bool            `json:"include_settled_response_ids"`
}

type turnCostsResponse struct {
	Threads []ChatgptThreadTurnCosts `json:"threads"`
}

// GetTurnCosts queries per-turn estimates with the signed-in user's workspace
// permissions. The endpoint follows the client's Codex/ChatGPT path style:
// /api/codex/usage/thread-estimates/query or
// /wham/usage/thread-estimates/query.
func (c *CloudClient) GetTurnCosts(ctx context.Context, threads map[string][]string) ([]ChatgptThreadTurnCosts, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	requestThreads := make([]threadTurnIDs, 0, len(threads))
	for threadID, turnIDs := range threads {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			continue
		}
		requestThreads = append(requestThreads, threadTurnIDs{
			ThreadID: threadID,
			TurnIDs:  append([]string(nil), turnIDs...),
		})
	}
	var response turnCostsResponse
	if err := c.doJSON(ctx, http.MethodPost, c.apiPath("usage", "thread-estimates", "query"), nil, turnCostsRequest{
		Threads:                   requestThreads,
		IncludeSettledResponseIDs: true,
	}, &response); err != nil {
		return nil, err
	}
	return response.Threads, nil
}
