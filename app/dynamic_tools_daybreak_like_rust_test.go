package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
)

func daybreakDynamicToolServer(featureEnabled bool, account string, daybreakDefault any) *dynamicToolTestServer {
	return &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		switch method {
		case appserver.MethodThreadRead:
			return map[string]any{"thread": map[string]any{
				"id": "thread-caller", "status": map[string]any{"type": "idle"},
				"cwd": "/work", "modelProvider": "openai", "daybreakEnabled": true,
			}}, nil
		case appserver.MethodThreadResume:
			return map[string]any{
				"thread":        map[string]any{"id": "thread-caller", "daybreakEnabled": true},
				"model":         "gpt-5",
				"modelProvider": "openai",
			}, nil
		case appserver.MethodThreadStart:
			return map[string]any{
				"thread":        map[string]any{"id": "thread-bg", "daybreakEnabled": true},
				"model":         "gpt-5",
				"modelProvider": "openai",
			}, nil
		case appserver.MethodGetAccount:
			return map[string]any{"account": map[string]any{"type": account}}, nil
		case appserver.MethodExperimentalFeatureList:
			return map[string]any{"data": []any{map[string]any{
				"name": "api_key_cyber_access_programs", "enabled": featureEnabled, "defaultEnabled": true,
			}}}, nil
		case appserver.MethodModelList:
			return map[string]any{"data": []any{map[string]any{
				"id": "gpt-5", "model": "gpt-5",
				"availableAccessPrograms": map[string]any{"cyber": []any{"standard", "daybreakBlue"}},
			}}}, nil
		case appserver.MethodConfigRead:
			return map[string]any{"config": map[string]any{"daybreak": daybreakDefault}, "origins": map[string]any{}}, nil
		case appserver.MethodTurnStart:
			return map[string]any{"turn": map[string]any{"id": "turn-new", "items": []any{}, "status": "inProgress"}}, nil
		default:
			return nil, errors.New("unexpected method")
		}
	}}
}

func (s *dynamicToolTestServer) cyberAccessProgramForTurn(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.turns) != 1 {
		t.Fatalf("turns = %#v", s.turns)
	}
	if s.turns[0].CyberAccessProgram == nil {
		return ""
	}
	return s.turns[0].CyberAccessProgram.CoreValue()
}

// Rust #53701/#53717: a background turn keeps the target thread's Daybreak
// choice for an API-key account only when the target thread reports
// api_key_cyber_access_programs, so a project opt-out omits the program.
func TestDynamicToolBackgroundTurnProgramHonorsTargetThreadSettingsLikeRust(t *testing.T) {
	server := daybreakDynamicToolServer(true, "apiKey", nil)
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "send_message_to_thread",
		Arguments: map[string]any{"threadId": "thread-caller", "prompt": "continue"},
	}, DynamicToolOptions{
		FeatureSettings: map[string]bool{"cli_daybreak": true},
	})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	if got := server.cyberAccessProgramForTurn(t); got != "daybreak_blue" {
		t.Fatalf("program = %q, want daybreak_blue", got)
	}

	optedOut := daybreakDynamicToolServer(false, "apiKey", nil)
	response = runDynamicTool(t, optedOut, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "send_message_to_thread",
		Arguments: map[string]any{"threadId": "thread-caller", "prompt": "continue"},
	}, DynamicToolOptions{
		FeatureSettings: map[string]bool{"cli_daybreak": true},
	})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	if got := optedOut.cyberAccessProgramForTurn(t); got != "" {
		t.Fatalf("opted-out program = %q, want omission", got)
	}
}

// Rust #53701: create_thread inherits the source thread's effective `daybreak`
// configuration, and a ChatGPT account resolves the background program from
// the target thread without the API-key feature lookup.
func TestDynamicToolCreateThreadInheritsDaybreakDefaultLikeRust(t *testing.T) {
	server := daybreakDynamicToolServer(false, "chatgpt", true)
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "create_thread",
		Arguments: map[string]any{"prompt": "start a task"},
	}, DynamicToolOptions{
		FeatureSettings: map[string]bool{"cli_daybreak": true},
	})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.starts) != 1 || server.starts[0].DaybreakEnabled == nil || !*server.starts[0].DaybreakEnabled {
		t.Fatalf("thread start = %#v", server.starts)
	}
	if len(server.turns) != 1 || server.turns[0].CyberAccessProgram == nil || server.turns[0].CyberAccessProgram.CoreValue() != "daybreak_blue" {
		t.Fatalf("turns = %#v", server.turns)
	}
}

// Rust #53701: a failed account lookup surfaces when Daybreak is enabled for
// the OpenAI provider instead of silently dropping the saved preference.
func TestDynamicToolBackgroundTurnReportsAccountLookupFailureLikeRust(t *testing.T) {
	server := &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		switch method {
		case appserver.MethodThreadRead:
			return map[string]any{"thread": map[string]any{
				"id": "thread-caller", "status": map[string]any{"type": "idle"}, "daybreakEnabled": true,
			}}, nil
		case appserver.MethodThreadResume:
			return map[string]any{
				"thread":        map[string]any{"id": "thread-caller", "daybreakEnabled": true},
				"model":         "gpt-5",
				"modelProvider": "openai",
			}, nil
		case appserver.MethodGetAccount:
			return nil, errors.New("account unavailable")
		default:
			return nil, errors.New("unexpected method")
		}
	}}
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "send_message_to_thread",
		Arguments: map[string]any{"threadId": "thread-caller", "prompt": "continue"},
	}, DynamicToolOptions{
		FeatureSettings: map[string]bool{"cli_daybreak": true},
	})
	if response.Success || !strings.Contains(dynamicToolText(t, response), "Could not determine Daybreak account eligibility") {
		t.Fatalf("response = %#v", response)
	}
}
