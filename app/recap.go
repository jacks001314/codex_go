package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/config"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

const (
	temporaryStructuredTurnTimeout  = 30 * time.Second
	temporaryStructuredMaxBytes     = 8 * 1024
	temporaryStructuredTextKind     = "text"
	temporaryStructuredAgentMessage = "agentmessage"
)

// interactiveRemoteRecapGenerateHandler runs the temporary structured recap turn
// over the app-server websocket (Rust app/recap.rs +
// temporary_structured_request.rs): start an isolated ephemeral thread, submit a
// schema-constrained turn, collect the assistant response, and detach.
func interactiveRemoteRecapGenerateHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.RecapGenerateFunc {
	return func(threadID string, options codextea.RecapThreadOptions, prompt string, schema map[string]any) (string, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return "", err
		}
		defer client.close()
		runCtx, cancel := context.WithTimeout(ctx, temporaryStructuredTurnTimeout)
		defer cancel()
		temporaryThreadID, err := startRemoteTemporaryStructuredThread(runCtx, client, options)
		if err != nil {
			return "", err
		}
		defer unsubscribeRemoteTemporaryThread(client, temporaryThreadID)
		return runRemoteTemporaryStructuredTurn(runCtx, client, temporaryThreadID, prompt, schema)
	}
}

// startRemoteTemporaryStructuredThread reads the effective configuration and
// starts an isolated ephemeral thread that disables every tool, MCP server, and
// feature the recap does not need (Rust start_temporary_thread).
func startRemoteTemporaryStructuredThread(ctx context.Context, client *remoteAppServerTUIClient, options codextea.RecapThreadOptions) (string, error) {
	cwd := strings.TrimSpace(options.CWD)
	readParams := config.ConfigReadParams{IncludeLayers: false}
	if cwd != "" {
		readParams.CWD = &cwd
	}
	var effective config.ConfigReadResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, readParams, &effective); err != nil {
		return "", fmt.Errorf("could not read the effective configuration: %w", err)
	}
	overrides := temporaryStructuredConfigOverrides()
	if names := mcpServerNamesFromEffectiveConfig(effective.Config); len(names) > 0 {
		servers := make(map[string]any, len(names))
		for _, name := range names {
			servers[name] = map[string]any{"enabled": false}
		}
		overrides["mcp_servers"] = servers
	}
	params := appserver.ThreadStartParams{
		Model:          strings.TrimSpace(options.Model),
		ModelProvider:  strings.TrimSpace(options.ModelProvider),
		CWD:            cwd,
		ApprovalPolicy: "never",
		Ephemeral:      true,
		Config:         overrides,
	}
	if profile := strings.TrimSpace(options.PermissionProfile); profile != "" && !strings.HasPrefix(profile, ":") {
		params.Permissions = &profile
	} else {
		params.Sandbox = "read-only"
	}
	var started appserver.ThreadStartResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodThreadStart, params, &started); err != nil {
		return "", err
	}
	if started.Thread == nil || strings.TrimSpace(started.Thread.ID) == "" {
		return "", errors.New("temporary structured thread start returned no thread")
	}
	return strings.TrimSpace(started.Thread.ID), nil
}

// runRemoteTemporaryStructuredTurn submits the schema-constrained prompt and
// waits for the turn's assistant message (Rust start_structured_turn +
// collect_structured_response).
func runRemoteTemporaryStructuredTurn(ctx context.Context, client *remoteAppServerTUIClient, threadID string, prompt string, schema map[string]any) (string, error) {
	params := turn.TurnStartParams{
		ThreadID:     threadID,
		Input:        []turn.TurnUserInput{{Type: temporaryStructuredTextKind, Text: prompt}},
		OutputSchema: schema,
	}
	var started turn.TurnStartResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodTurnStart, params, &started); err != nil {
		return "", err
	}
	if strings.TrimSpace(started.Turn.ID) == "" {
		return "", errors.New("temporary structured turn start returned no turn")
	}
	return collectRemoteStructuredResponse(ctx, client, started.Turn.ID)
}

// collectRemoteStructuredResponse consumes notifications until the requested
// turn completes, returning its last assistant message.
func collectRemoteStructuredResponse(ctx context.Context, client *remoteAppServerTUIClient, turnID string) (string, error) {
	response := ""
	for {
		message, err := client.readRemoteMessage(ctx)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(message.Method) == "" {
			continue
		}
		if len(message.ID) > 0 {
			// Server-initiated request (approvals, elicitations); answer with the
			// client's default so the turn cannot stall.
			_ = client.respondServerRequest(ctx, message)
			continue
		}
		switch appserver.NotificationMethod(strings.TrimSpace(message.Method)) {
		case appserver.NotificationItemCompleted:
			var payload appserver.ItemCompletedNotification
			if err := json.Unmarshal(message.Params, &payload); err != nil {
				return "", err
			}
			if strings.TrimSpace(payload.TurnID) != strings.TrimSpace(turnID) {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(remoteTUIAnyString(payload.Item["type"])), temporaryStructuredAgentMessage) {
				continue
			}
			text := strings.TrimSpace(remoteTUIAnyString(payload.Item["text"]))
			if len(text) > temporaryStructuredMaxBytes {
				return "", fmt.Errorf("temporary structured response exceeds %d bytes", temporaryStructuredMaxBytes)
			}
			response = text
		case appserver.NotificationTurnCompleted:
			var payload appserver.TurnCompletedNotification
			if err := json.Unmarshal(message.Params, &payload); err != nil {
				return "", err
			}
			if strings.TrimSpace(payload.Turn.ID) != strings.TrimSpace(turnID) {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(string(payload.Turn.Status)), string(appserver.TurnStatusCompleted)) {
				return "", fmt.Errorf("temporary structured turn ended with status %s", payload.Turn.Status)
			}
			if response == "" {
				return "", errors.New("temporary structured turn completed without a response")
			}
			return response, nil
		}
	}
}

// unsubscribeRemoteTemporaryThread makes a bounded best-effort attempt to detach
// the ephemeral thread (Rust unsubscribe_temporary_thread).
func unsubscribeRemoteTemporaryThread(client *remoteAppServerTUIClient, threadID string) {
	if client == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), temporaryStructuredTurnTimeout)
	defer cancel()
	var response appserver.ThreadUnsubscribeResponse
	_ = remoteSessionRequest(ctx, client, appserver.MethodThreadUnsubscribe, appserver.ThreadUnsubscribeParams{ThreadID: threadID}, &response)
}

// mcpServerNamesFromEffectiveConfig lists the MCP servers the effective config
// would enable, so the temporary thread can disable each by name.
func mcpServerNamesFromEffectiveConfig(effective map[string]any) []string {
	servers, ok := effective["mcp_servers"].(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	return names
}

// temporaryStructuredConfigOverrides disables every feature, tool, and extension
// the isolated recap turn must not reach (Rust start_temporary_thread).
func temporaryStructuredConfigOverrides() map[string]any {
	overrides := map[string]any{
		"web_search": "disabled",
	}
	for _, key := range temporaryStructuredDisabledFeatures {
		overrides[key] = false
	}
	return overrides
}

var temporaryStructuredDisabledFeatures = []string{
	"features.apps",
	"features.code_mode",
	"features.code_mode_only",
	"features.context_management",
	"features.current_time_reminder",
	"features.deferred_executor",
	"features.enable_fanout",
	"features.goals",
	"features.hooks",
	"features.image_generation",
	"features.memories",
	"features.multi_agent",
	"features.multi_agent_v2",
	"features.plugins",
	"features.request_permissions_tool",
	"features.shell_snapshot",
	"features.shell_tool",
	"features.standalone_web_search",
	"features.token_budget",
	"features.tool_suggest",
	"features.unified_exec",
	"features.view_image",
	"orchestrator.skills.enabled",
	"skills.include_instructions",
	"tools.experimental_request_user_input.enabled",
	"tools.update_plan.enabled",
}
