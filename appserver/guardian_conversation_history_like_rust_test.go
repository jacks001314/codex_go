package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/state"
	"codex_go/tool"
)

type guardianHistoryAgent struct {
	responses []*model.AgentResponse
	requests  []*model.AgentRequest
}

func (a *guardianHistoryAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	clone := *request
	clone.InputItems = append([]any(nil), request.InputItems...)
	clone.Tools = append([]any(nil), request.Tools...)
	a.requests = append(a.requests, &clone)
	index := len(a.requests) - 1
	if index >= len(a.responses) {
		index = len(a.responses) - 1
	}
	return a.responses[index], nil
}

func guardianHistoryTestRuntime(call func(*mcp.MCPToolCallParams) (*mcp.MCPToolCallResponse, error)) *guardianConversationHistoryRuntime {
	annotations := &mcp.RuntimeToolAnnotations{ReadOnlyHint: boolPointerAppserverTest(true)}
	return &guardianConversationHistoryRuntime{
		ThreadID:        "parent-thread",
		TurnID:          "parent-turn",
		MaxOutputTokens: 50,
		AppPolicy:       apps.NewAppToolPolicyEvaluator(nil),
		Call:            call,
		Tools: []mcp.RuntimeToolInfo{
			{
				ServerName:        mcp.RuntimeCodexAppsMCPServerName,
				CallableNamespace: guardianHistoryNamespace,
				CallableName:      guardianHistorySearchTool,
				ConnectorID:       "history",
				ConnectorName:     "user_message",
				HostOwnedApps:     true,
				Tool: mcp.RuntimeTool{
					Name:        "user_message.search_messages",
					Description: "Search conversation history.",
					InputSchema: map[string]any{
						"type":       "object",
						"properties": map[string]any{"query": map[string]any{"type": "string"}},
					},
					Annotations: annotations,
				},
			},
			{
				ServerName:        mcp.RuntimeCodexAppsMCPServerName,
				CallableNamespace: guardianHistoryNamespace,
				CallableName:      guardianHistoryReadTool,
				ConnectorID:       "history",
				ConnectorName:     "user_message",
				HostOwnedApps:     true,
				Tool: mcp.RuntimeTool{
					Name:        "user_message.read_messages",
					Description: "Read conversation history.",
					InputSchema: map[string]any{
						"type":       "object",
						"properties": map[string]any{"message_id": map[string]any{"type": "string"}},
					},
					Annotations: annotations,
				},
			},
		},
	}
}

func boolPointerAppserverTest(value bool) *bool {
	return &value
}

func TestGuardianReviewExecutesParentHistoryToolsLikeRust(t *testing.T) {
	var calls []*mcp.MCPToolCallParams
	history := guardianHistoryTestRuntime(func(params *mcp.MCPToolCallParams) (*mcp.MCPToolCallResponse, error) {
		clone := *params
		calls = append(calls, &clone)
		text := "history-search-evidence"
		if params.ToolName == "user_message.read_messages" {
			text = strings.Repeat("Earlier user instructions. ", 1_000)
		}
		return &mcp.MCPToolCallResponse{
			Content: []mcp.MCPToolCallContent{{Type: "text", Text: text}},
		}, nil
	})
	agent := &guardianHistoryAgent{responses: []*model.AgentResponse{
		{
			ResponseID: "history-calls",
			Items: []model.AgentItem{
				{Type: "function_call", Namespace: "user_message", Name: guardianHistorySearchTool, CallID: "search-1", Arguments: `{"query":"workspace"}`},
				{Type: "function_call", Namespace: "user_message", Name: guardianHistoryReadTool, CallID: "read-1", Arguments: `{"message_id":"scope"}`},
			},
			Usage: model.AgentUsage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18},
		},
		{
			ResponseID: "assessment",
			Message:    `{"risk_level":"low","user_authorization":"low","outcome":"deny","rationale":"Fixture review complete."}`,
			Usage:      model.AgentUsage{InputTokens: 13, OutputTokens: 5, TotalTokens: 18},
		},
	}}
	request := &model.AgentRequest{
		Prompt:     "review the action",
		Model:      "review-model",
		Endpoint:   model.ResponsesEndpointGuardian,
		TaskKind:   model.AgentTaskReview,
		ThreadID:   "parent-thread",
		TurnID:     "parent-turn",
		Originator: "guardian",
	}
	response, err := runGuardianReviewWithHistory(context.Background(), agent, request, history)
	if err != nil {
		t.Fatalf("runGuardianReviewWithHistory() error = %v", err)
	}
	if response == nil || response.ResponseID != "assessment" {
		t.Fatalf("final response = %#v", response)
	}
	if response.Usage.TotalTokens != 36 {
		t.Fatalf("aggregate usage = %#v, want 36 total tokens", response.Usage)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("agent requests = %d, want 2", len(agent.requests))
	}
	toolJSON, err := json.Marshal(agent.requests[0].Tools)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(toolJSON), "user_message") ||
		!strings.Contains(string(toolJSON), guardianHistorySearchTool) ||
		!strings.Contains(string(toolJSON), guardianHistoryReadTool) {
		t.Fatalf("reviewer tools = %s", toolJSON)
	}
	if len(calls) != 2 {
		t.Fatalf("history calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		if call.ThreadID != "parent-thread" || call.SessionID != "parent-thread" {
			t.Fatalf("history call identity = thread %q session %q", call.ThreadID, call.SessionID)
		}
	}
	secondInput, err := json.Marshal(agent.requests[1].InputItems)
	if err != nil {
		t.Fatal(err)
	}
	text := string(secondInput)
	if !strings.Contains(text, "history-search-evidence") || !strings.Contains(text, "truncated output") {
		t.Fatalf("history outputs missing or untruncated: %s", text)
	}
	if agent.requests[1].Prompt != "" || agent.requests[1].PreviousResponseID != "history-calls" {
		t.Fatalf("continued request = prompt %q previous %q", agent.requests[1].Prompt, agent.requests[1].PreviousResponseID)
	}
}

func TestGuardianHistoryToolFailsClosedForAppPolicyLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{
			name: "disabled",
			values: map[string]any{"apps": map[string]any{
				"history": map[string]any{"enabled": false},
			}},
			want: mcp.MCPToolCallBlockedByAppConfigurationMessage,
		},
		{
			name: "approval required",
			values: map[string]any{"apps": map[string]any{
				"history": map[string]any{
					"tools": map[string]any{
						"user_message.read_messages": map[string]any{"approval_mode": "prompt"},
					},
				},
			}},
			want: guardianHistoryApprovalUnavailable,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			history := guardianHistoryTestRuntime(func(*mcp.MCPToolCallParams) (*mcp.MCPToolCallResponse, error) {
				t.Fatal("blocked history tool reached the MCP service")
				return nil, nil
			})
			history.AppPolicy = apps.NewAppToolPolicyEvaluator(apps.AppsConfigFromValues(testCase.values))
			executor := newGuardianHistoryToolExecutor(history, history.Tools[1])
			output, err := executor.Execute(context.Background(), &tool.Invocation{
				CallID:   "read-blocked",
				ToolName: tool.NamespacedName(guardianHistoryNamespace, guardianHistoryReadTool),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message_id":"scope"}`},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if output == nil || output.Success || !strings.Contains(output.Body, testCase.want) {
				t.Fatalf("blocked output = %#v, want %q", output, testCase.want)
			}
		})
	}
}

func TestGuardianConversationHistoryInstructionsLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"features": map[string]any{
			"apps":                                true,
			"guardian_conversation_history_tools": true,
		},
		"auto_review": map[string]any{
			"experimental_conversation_history_prompt": "Use the owner's history.",
		},
	}}
	instructions := guardianReviewInstructions(cfg, nil)
	if !strings.Contains(instructions, "Use the owner's history.") ||
		strings.Contains(instructions, "## Conversation history retrieval") {
		t.Fatalf("custom history instructions = %q", instructions)
	}
	cfg.Values["auto_review"] = map[string]any{"experimental_conversation_history_prompt": ""}
	instructions = guardianReviewInstructions(cfg, nil)
	if strings.Contains(instructions, "## Conversation history retrieval") {
		t.Fatalf("blank override retained bundled history instructions: %q", instructions)
	}
	cfg.Values["features"] = map[string]any{
		"apps":                                true,
		"guardian_conversation_history_tools": false,
	}
	instructions = guardianReviewInstructions(cfg, nil)
	if strings.Contains(instructions, "Use the owner's history.") {
		t.Fatalf("disabled feature retained history instructions: %q", instructions)
	}
}

func TestGuardianReviewUsesConversationHistoryToolsLikeRust(t *testing.T) {
	historyCalls := 0
	history := guardianHistoryTestRuntime(func(params *mcp.MCPToolCallParams) (*mcp.MCPToolCallResponse, error) {
		historyCalls++
		return &mcp.MCPToolCallResponse{
			Content: []mcp.MCPToolCallContent{{Type: "text", Text: "Earlier authorization: workspace only."}},
		}, nil
	})
	agentResponses := []*model.AgentResponse{
		{
			ResponseID: "history-turn-1",
			Items: []model.AgentItem{{
				Type:      "function_call",
				Namespace: guardianHistoryNamespace,
				Name:      guardianHistorySearchTool,
				CallID:    "history-search-1",
				Arguments: `{"query":"workspace authorization"}`,
			}},
		},
		{
			ResponseID: "history-turn-2",
			Message:    `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"Authorized for the workspace."}`,
		},
	}
	agentCalls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			response := agentResponses[agentCalls]
			agentCalls++
			return response, nil
		}),
		store: state.NewReviewStore(),
		conversationHistory: func(threadID, turnID string) *guardianConversationHistoryRuntime {
			return history
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "parent-thread", "parent-turn", "action-call", state.Action{
		Type:    "command",
		Command: "touch generated.txt",
		CWD:     "/workspace",
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("Review() decision = %v, want approved", decision)
	}
	if historyCalls != 1 || agentCalls != 2 {
		t.Fatalf("history calls = %d, agent calls = %d, want 1 and 2", historyCalls, agentCalls)
	}
}
