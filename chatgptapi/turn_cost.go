package chatgptapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
)

// ChatgptTurnCost is the workspace-visible estimate for one ChatGPT turn.
// Missing amounts and settlement IDs stay nil so callers never interpret an
// absent value as a zero-cost or fully settled turn (Rust #41944).
type ChatgptTurnCost struct {
	TurnID                  string  `json:"turn_id"`
	Model                   *string `json:"model,omitempty"`
	EstimatedUsageUSDMicros *int64  `json:"estimated_usage_usd_micros,omitempty"`
	// NativeUsageUSDMicros is the native-dollar component of the turn's cost.
	// Rust #62810 defaults an omitted field to zero for legacy responses, while
	// an explicit null means the backend cannot report native USD. nil therefore
	// means "unavailable", not "zero".
	NativeUsageUSDMicros *int64   `json:"native_usage_usd_micros,omitempty"`
	SettledResponseIDs   []string `json:"settled_response_ids,omitempty"`
}

// UnmarshalJSON distinguishes an omitted native USD field (legacy backend,
// treated as zero) from an explicit null (native USD unavailable).
func (c *ChatgptTurnCost) UnmarshalJSON(data []byte) error {
	if c == nil {
		return errors.New("cannot unmarshal turn cost into nil pointer")
	}
	var raw struct {
		TurnID                  string          `json:"turn_id"`
		Model                   *string         `json:"model"`
		EstimatedUsageUSDMicros *int64          `json:"estimated_usage_usd_micros"`
		NativeUsageUSDMicros    json.RawMessage `json:"native_usage_usd_micros"`
		SettledResponseIDs      []string        `json:"settled_response_ids"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.TurnID = raw.TurnID
	c.Model = raw.Model
	c.EstimatedUsageUSDMicros = raw.EstimatedUsageUSDMicros
	c.SettledResponseIDs = raw.SettledResponseIDs
	c.NativeUsageUSDMicros = nil
	if len(raw.NativeUsageUSDMicros) == 0 {
		zero := int64(0)
		c.NativeUsageUSDMicros = &zero
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw.NativeUsageUSDMicros), []byte("null")) {
		return nil
	}
	var native int64
	if err := json.Unmarshal(raw.NativeUsageUSDMicros, &native); err != nil {
		return err
	}
	c.NativeUsageUSDMicros = &native
	return nil
}

// VisibleTotalUSDMicros returns the complete visible estimate: credit-converted
// USD plus native USD. The total is unavailable when either component is
// missing, negative, or their sum overflows, matching Rust #62810.
func (c *ChatgptTurnCost) VisibleTotalUSDMicros() (int64, bool) {
	if c == nil || c.EstimatedUsageUSDMicros == nil || c.NativeUsageUSDMicros == nil {
		return 0, false
	}
	converted := *c.EstimatedUsageUSDMicros
	native := *c.NativeUsageUSDMicros
	if converted < 0 || native < 0 || converted > math.MaxInt64-native {
		return 0, false
	}
	return converted + native, true
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
