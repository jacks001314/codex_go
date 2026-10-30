package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/state"
)

// TestAppServerCodexErrorInfoAcceptsUnknownVariantsLikeRust pins Rust #49806:
// app-server clients must keep decoding a payload when a newer server adds a
// CodexErrorInfo variant. Go carries the field as untyped JSON, so unknown
// strings and objects pass through instead of failing the enclosing TurnError
// or ErrorNotification, while known variants keep their wire shape.
func TestAppServerCodexErrorInfoAcceptsUnknownVariantsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
	}{
		{name: "unknown string", raw: `"futurePolicyVariant"`},
		{name: "unknown object", raw: `{"futureStructuredVariant":{"detail":"x"}}`},
		{name: "other", raw: `"other"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var turnErr TurnError
			payload := `{"message":"blocked","codexErrorInfo":` + testCase.raw + `}`
			if err := json.Unmarshal([]byte(payload), &turnErr); err != nil {
				t.Fatalf("TurnError decode with %s failed: %v", testCase.raw, err)
			}
			if turnErr.Message != "blocked" || turnErr.CodexErrorInfo == nil {
				t.Fatalf("TurnError = %#v, want message and codexErrorInfo preserved", turnErr)
			}

			var notification ErrorNotification
			notificationPayload := `{"threadId":"thread-1","turnId":"turn-1","willRetry":false,"error":` + payload + `}`
			if err := json.Unmarshal([]byte(notificationPayload), &notification); err != nil {
				t.Fatalf("ErrorNotification decode with %s failed: %v", testCase.raw, err)
			}
			if notification.Error.Message != "blocked" || notification.Error.CodexErrorInfo == nil {
				t.Fatalf("ErrorNotification = %#v, want the turn error preserved", notification)
			}

			stored := state.ThreadHistoryTurn{TurnID: "turn-1", Status: "failed", ErrorJSON: []byte(payload)}
			turn, err := stateThreadTurnToAPI(stored, TurnItemsFull)
			if err != nil {
				t.Fatalf("stateThreadTurnToAPI failed with %s: %v", testCase.raw, err)
			}
			if turn.Error == nil || turn.Error.Message != "blocked" || turn.Error.CodexErrorInfo == nil {
				t.Fatalf("stored turn error = %#v, want the classification preserved", turn.Error)
			}
		})
	}

	var structured TurnError
	if err := json.Unmarshal([]byte(`{"message":"retry","codexErrorInfo":{"responseStreamDisconnected":{"httpStatusCode":503}}}`), &structured); err != nil {
		t.Fatalf("known structured variant decode failed: %v", err)
	}
	info, ok := structured.CodexErrorInfo.(map[string]any)
	if !ok || info["responseStreamDisconnected"] == nil {
		t.Fatalf("known structured variant = %#v, want its object payload preserved", structured.CodexErrorInfo)
	}
}
