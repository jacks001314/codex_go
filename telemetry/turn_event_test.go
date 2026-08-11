package telemetry

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCodexTurnEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID:             "thread-2",
		SessionID:            "session-thread-2",
		TurnID:               "turn-2",
		AppServerClient:      sampleAppServerClientMetadata(),
		Runtime:              sampleRuntimeMetadata(),
		ThreadSource:         stringPtrTelemetry("user"),
		InitializationMode:   "new",
		Model:                stringPtrTelemetry("gpt-5"),
		ModelProvider:        "openai",
		SandboxPolicy:        stringPtrTelemetry("read_only"),
		ReasoningEffort:      stringPtrTelemetry("high"),
		ReasoningSummary:     stringPtrTelemetry("detailed"),
		ServiceTier:          "flex",
		ApprovalPolicy:       "on-request",
		ApprovalsReviewer:    "auto_review",
		SandboxNetworkAccess: true,
		CollaborationMode:    stringPtrTelemetry("plan"),
		Personality:          stringPtrTelemetry("pragmatic"),
		WorkspaceKind:        stringPtrTelemetry("projectless"),
		NumInputImages:       2,
		IsFirstTurn:          true,
		Status:               stringPtrTelemetry("completed"),
		SteerCount:           intPtrTelemetry(0),
		RunningBackgroundProcessCount: intPtrTelemetry(3),
		TimingProfile: CodexTurnTimingProfile{
			BeforeFirstSamplingMS:     100,
			SamplingMS:                700,
			BetweenSamplingOverheadMS: 50,
			ToolBlockingMS:            250,
			AfterLastSamplingMS:       134,
			SamplingRequestCount:      2,
			SamplingRetryCount:        1,
		},
		DurationMS:  uint64PtrTelemetry(1234),
		StartedAt:   uint64PtrTelemetry(455),
		CompletedAt: uint64PtrTelemetry(456),
	})

	var got any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{
		"event_type": "codex_turn_event",
		"event_params": {
			"thread_id": "thread-2",
			"session_id": "session-thread-2",
			"turn_id": "turn-2",
			"submission_type": null,
			"app_server_client": {
				"product_client_id": "codex_cli_rs",
				"client_name": "codex-tui",
				"client_version": "1.0.0",
				"rpc_transport": "stdio",
				"experimental_api_enabled": true
			},
			"runtime": {
				"codex_rs_version": "0.1.0",
				"runtime_os": "macos",
				"runtime_os_version": "15.3.1",
				"runtime_arch": "aarch64"
			},
			"ephemeral": false,
			"thread_source": "user",
			"initialization_mode": "new",
			"subagent_source": null,
			"parent_thread_id": null,
			"model": "gpt-5",
			"model_provider": "openai",
			"sandbox_policy": "read_only",
			"reasoning_effort": "high",
			"reasoning_summary": "detailed",
			"service_tier": "flex",
			"approval_policy": "on-request",
			"approvals_reviewer": "auto_review",
			"sandbox_network_access": true,
			"collaboration_mode": "plan",
			"personality": "pragmatic",
			"workspace_kind": "projectless",
			"num_input_images": 2,
			"is_first_turn": true,
			"status": "completed",
			"turn_error": null,
			"explicit_client_interrupt_requested_at_ms": null,
			"codex_error_kind": null,
			"codex_error_http_status_code": null,
			"steer_count": 0,
			"running_background_process_count": 3,
			"total_tool_call_count": null,
			"shell_command_count": null,
			"file_change_count": null,
			"mcp_tool_call_count": null,
			"dynamic_tool_call_count": null,
			"subagent_tool_call_count": null,
			"web_search_count": null,
			"image_generation_count": null,
			"input_tokens": null,
			"cached_input_tokens": null,
			"cache_write_input_tokens": null,
			"output_tokens": null,
			"reasoning_output_tokens": null,
			"total_tokens": null,
			"before_first_sampling_ms": 100,
			"sampling_ms": 700,
			"between_sampling_overhead_ms": 50,
			"tool_blocking_ms": 250,
			"after_last_sampling_ms": 134,
			"sampling_request_count": 2,
			"sampling_retry_count": 1,
			"duration_ms": 1234,
			"started_at": 455,
			"completed_at": 456
		}
	}`), &want); err != nil {
		t.Fatalf("unmarshal expected event error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event JSON mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestCodexTurnEventThreadOriginatorOverridesProductClientID(t *testing.T) {
	event := NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID:         "thread-originator",
		SessionID:        "thread-originator",
		TurnID:           "turn-originator",
		AppServerClient:  sampleAppServerClientMetadata(),
		ThreadOriginator: "codex_work_desktop",
		Runtime:          sampleRuntimeMetadata(),
		Model:            stringPtrTelemetry("mock-model"),
		ModelProvider:    "mock_provider",
		SandboxPolicy:    stringPtrTelemetry("read_only"),
		WorkspaceKind:    stringPtrTelemetry("projectless"),
		NumInputImages:   1,
		Status:           stringPtrTelemetry("completed"),
		SteerCount:       intPtrTelemetry(0),
		ToolCounts:       &CodexTurnToolCounts{},
		TokenUsage:       &CodexTurnTokenUsage{},
		TimingProfile: CodexTurnTimingProfile{
			SamplingRequestCount: 2,
			SamplingRetryCount:   1,
		},
		DurationMS:  uint64PtrTelemetry(1234),
		StartedAt:   uint64PtrTelemetry(455),
		CompletedAt: uint64PtrTelemetry(456),
	})
	var payload map[string]any
	if err := marshalUnmarshalTelemetry(event, &payload); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := payload["event_params"].(map[string]any)
	client := params["app_server_client"].(map[string]any)
	if client["product_client_id"] != "codex_work_desktop" {
		t.Fatalf("app_server_client = %#v", client)
	}
	if _, ok := params["product_client_id"]; ok {
		t.Fatalf("product_client_id leaked at root: %#v", params)
	}
	if params["workspace_kind"] != "projectless" || params["num_input_images"] != float64(1) {
		t.Fatalf("fixture params = %#v", params)
	}
}

func TestCodexTurnSteerEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexTurnSteerEvent(CodexTurnSteerEventInput{
		ThreadID:        "thread-2",
		SessionID:       "session-thread-2",
		ExpectedTurnID:  stringPtrTelemetry("turn-2"),
		AcceptedTurnID:  stringPtrTelemetry("turn-2"),
		AppServerClient: sampleAppServerClientMetadata(),
		Runtime:         sampleRuntimeMetadata(),
		ThreadSource:    stringPtrTelemetry("user"),
		NumInputImages:  1,
		Result:          TurnSteerResultAccepted,
		CreatedAt:       455,
	})

	var got any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{
		"event_type": "codex_turn_steer_event",
		"event_params": {
			"thread_id": "thread-2",
			"session_id": "session-thread-2",
			"expected_turn_id": "turn-2",
			"accepted_turn_id": "turn-2",
			"app_server_client": {
				"product_client_id": "codex_cli_rs",
				"client_name": "codex-tui",
				"client_version": "1.0.0",
				"rpc_transport": "stdio",
				"experimental_api_enabled": true
			},
			"runtime": {
				"codex_rs_version": "0.1.0",
				"runtime_os": "macos",
				"runtime_os_version": "15.3.1",
				"runtime_arch": "aarch64"
			},
			"thread_source": "user",
			"subagent_source": null,
			"parent_thread_id": null,
			"num_input_images": 1,
			"result": "accepted",
			"rejection_reason": null,
			"created_at": 455
		}
	}`), &want); err != nil {
		t.Fatalf("unmarshal expected event error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event JSON mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestCodexTurnSteerEventThreadOriginatorOverridesProductClientID(t *testing.T) {
	event := NewCodexTurnSteerEvent(CodexTurnSteerEventInput{
		ThreadID:         "thread-originator",
		SessionID:        "thread-originator",
		ExpectedTurnID:   stringPtrTelemetry("turn-expected"),
		AppServerClient:  sampleAppServerClientMetadata(),
		ThreadOriginator: "codex_work_desktop",
		Runtime:          sampleRuntimeMetadata(),
		Result:           TurnSteerResultRejected,
		RejectionReason:  stringPtrTelemetry(TurnSteerRejectionNoActiveTurn),
	})
	var payload map[string]any
	if err := marshalUnmarshalTelemetry(event, &payload); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := payload["event_params"].(map[string]any)
	client := params["app_server_client"].(map[string]any)
	if client["product_client_id"] != "codex_work_desktop" {
		t.Fatalf("app_server_client = %#v", client)
	}
	if params["result"] != TurnSteerResultRejected || params["rejection_reason"] != TurnSteerRejectionNoActiveTurn {
		t.Fatalf("result/rejection = %#v/%#v", params["result"], params["rejection_reason"])
	}
	if _, ok := params["product_client_id"]; ok {
		t.Fatalf("product_client_id leaked at root: %#v", params)
	}
}

func TestCodexCommandExecutionEventSerializesExpectedRustShape(t *testing.T) {
	exitCode := int32(0)
	event := NewCodexCommandExecutionEvent(CodexCommandExecutionEventParams{
		CodexToolItemEventBase: CodexToolItemEventBase{
			ThreadID:                       "thread-1",
			TurnID:                         "turn-1",
			ItemID:                         "item-1",
			AppServerClient:                sampleAppServerClientMetadata(),
			Runtime:                        sampleRuntimeMetadata(),
			ThreadSource:                   stringPtrTelemetry("user"),
			ToolName:                       "shell",
			StartedAtMS:                    123000,
			CompletedAtMS:                  125000,
			DurationMS:                     uint64PtrTelemetry(2000),
			ExecutionDurationMS:            uint64PtrTelemetry(1900),
			ReviewCount:                    0,
			GuardianReviewCount:            0,
			UserReviewCount:                0,
			FinalApprovalOutcome:           FinalApprovalOutcomeNotNeeded,
			TerminalStatus:                 ToolItemTerminalStatusCompleted,
			RequestedAdditionalPermissions: false,
			RequestedNetworkAccess:         false,
		},
		CommandExecutionSource:      "agent",
		ExitCode:                    &exitCode,
		CommandTotalActionCount:     4,
		CommandReadActionCount:      1,
		CommandListFilesActionCount: 1,
		CommandSearchActionCount:    1,
		CommandUnknownActionCount:   1,
	})

	var got any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{
		"event_type": "codex_command_execution_event",
		"event_params": {
			"thread_id": "thread-1",
			"turn_id": "turn-1",
			"item_id": "item-1",
			"app_server_client": {
				"product_client_id": "codex_cli_rs",
				"client_name": "codex-tui",
				"client_version": "1.0.0",
				"rpc_transport": "stdio",
				"experimental_api_enabled": true
			},
			"runtime": {
				"codex_rs_version": "0.1.0",
				"runtime_os": "macos",
				"runtime_os_version": "15.3.1",
				"runtime_arch": "aarch64"
			},
			"thread_source": "user",
			"subagent_source": null,
			"parent_thread_id": null,
			"tool_name": "shell",
			"started_at_ms": 123000,
			"completed_at_ms": 125000,
			"duration_ms": 2000,
			"execution_duration_ms": 1900,
			"review_count": 0,
			"guardian_review_count": 0,
			"user_review_count": 0,
			"final_approval_outcome": "not_needed",
			"terminal_status": "completed",
			"failure_kind": null,
			"requested_additional_permissions": false,
			"requested_network_access": false,
			"plugin_id": null,
			"script_path": null,
			"command_execution_source": "agent",
			"exit_code": 0,
			"command_total_action_count": 4,
			"command_read_action_count": 1,
			"command_list_files_action_count": 1,
			"command_search_action_count": 1,
			"command_unknown_action_count": 1
		}
	}`), &want); err != nil {
		t.Fatalf("unmarshal expected event error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event JSON mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestCodexFileChangeEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexFileChangeEvent(CodexFileChangeEventParams{
		CodexToolItemEventBase: CodexToolItemEventBase{
			ThreadID:                       "thread-1",
			TurnID:                         "turn-1",
			ItemID:                         "item-1",
			AppServerClient:                sampleAppServerClientMetadata(),
			Runtime:                        sampleRuntimeMetadata(),
			ThreadSource:                   stringPtrTelemetry("user"),
			ToolName:                       "apply_patch",
			StartedAtMS:                    123000,
			CompletedAtMS:                  125000,
			DurationMS:                     uint64PtrTelemetry(2000),
			ReviewCount:                    0,
			GuardianReviewCount:            0,
			UserReviewCount:                0,
			FinalApprovalOutcome:           FinalApprovalOutcomeNotNeeded,
			TerminalStatus:                 ToolItemTerminalStatusCompleted,
			RequestedAdditionalPermissions: false,
			RequestedNetworkAccess:         false,
		},
		FileChangeCount: 4,
		FileAddCount:    1,
		FileUpdateCount: 1,
		FileDeleteCount: 1,
		FileMoveCount:   1,
	})

	var got any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{
		"event_type": "codex_file_change_event",
		"event_params": {
			"thread_id": "thread-1",
			"turn_id": "turn-1",
			"item_id": "item-1",
			"app_server_client": {
				"product_client_id": "codex_cli_rs",
				"client_name": "codex-tui",
				"client_version": "1.0.0",
				"rpc_transport": "stdio",
				"experimental_api_enabled": true
			},
			"runtime": {
				"codex_rs_version": "0.1.0",
				"runtime_os": "macos",
				"runtime_os_version": "15.3.1",
				"runtime_arch": "aarch64"
			},
			"thread_source": "user",
			"subagent_source": null,
			"parent_thread_id": null,
			"tool_name": "apply_patch",
			"started_at_ms": 123000,
			"completed_at_ms": 125000,
			"duration_ms": 2000,
			"execution_duration_ms": null,
			"review_count": 0,
			"guardian_review_count": 0,
			"user_review_count": 0,
			"final_approval_outcome": "not_needed",
			"terminal_status": "completed",
			"failure_kind": null,
			"requested_additional_permissions": false,
			"requested_network_access": false,
			"file_change_count": 4,
			"file_add_count": 1,
			"file_update_count": 1,
			"file_delete_count": 1,
			"file_move_count": 1
		}
	}`), &want); err != nil {
		t.Fatalf("unmarshal expected event error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event JSON mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestCodexMCPToolCallEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexMCPToolCallEvent(CodexMCPToolCallEventParams{
		CodexToolItemEventBase: CodexToolItemEventBase{
			ThreadID:             "thread-1",
			TurnID:               "turn-1",
			ItemID:               "mcp-1",
			AppServerClient:      sampleAppServerClientMetadata(),
			Runtime:              sampleRuntimeMetadata(),
			ThreadSource:         stringPtrTelemetry("user"),
			ToolName:             "search",
			StartedAtMS:          123000,
			CompletedAtMS:        125000,
			DurationMS:           uint64PtrTelemetry(2000),
			ExecutionDurationMS:  uint64PtrTelemetry(1900),
			FinalApprovalOutcome: FinalApprovalOutcomeUnknown,
			TerminalStatus:       ToolItemTerminalStatusCompleted,
		},
		MCPServerName:   "server",
		MCPToolName:     "search",
		MCPErrorPresent: false,
		PluginID:        stringPtrTelemetry("sample@test"),
	})

	var got map[string]any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := got["event_params"].(map[string]any)
	if got["event_type"] != "codex_mcp_tool_call_event" ||
		params["mcp_server_name"] != "server" ||
		params["mcp_tool_name"] != "search" ||
		params["mcp_error_present"] != false ||
		params["plugin_id"] != "sample@test" ||
		params["final_approval_outcome"] != "unknown" {
		t.Fatalf("MCP event JSON = %#v", got)
	}
}

func TestCodexDynamicToolCallEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexDynamicToolCallEvent(CodexDynamicToolCallEventParams{
		CodexToolItemEventBase: CodexToolItemEventBase{
			ThreadID:             "thread-1",
			TurnID:               "turn-1",
			ItemID:               "dynamic-1",
			AppServerClient:      sampleAppServerClientMetadata(),
			Runtime:              sampleRuntimeMetadata(),
			ThreadSource:         stringPtrTelemetry("user"),
			ToolName:             "render",
			StartedAtMS:          123000,
			CompletedAtMS:        125000,
			DurationMS:           uint64PtrTelemetry(2000),
			ExecutionDurationMS:  uint64PtrTelemetry(1900),
			FinalApprovalOutcome: FinalApprovalOutcomeUnknown,
			TerminalStatus:       ToolItemTerminalStatusCompleted,
		},
		DynamicToolName:        "render",
		Success:                boolPtrTelemetry(true),
		OutputContentItemCount: uint64PtrTelemetry(2),
		OutputTextItemCount:    uint64PtrTelemetry(1),
		OutputImageItemCount:   uint64PtrTelemetry(1),
	})

	var got map[string]any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := got["event_params"].(map[string]any)
	if got["event_type"] != "codex_dynamic_tool_call_event" ||
		params["dynamic_tool_name"] != "render" ||
		params["success"] != true ||
		params["output_content_item_count"] != float64(2) ||
		params["output_text_item_count"] != float64(1) ||
		params["output_image_item_count"] != float64(1) ||
		params["final_approval_outcome"] != "unknown" {
		t.Fatalf("dynamic event JSON = %#v", got)
	}
}

func TestCodexCollabAgentToolCallEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexCollabAgentToolCallEvent(CodexCollabAgentToolCallEventParams{
		CodexToolItemEventBase: CodexToolItemEventBase{
			ThreadID:             "thread-1",
			TurnID:               "turn-1",
			ItemID:               "collab-1",
			AppServerClient:      sampleAppServerClientMetadata(),
			Runtime:              sampleRuntimeMetadata(),
			ThreadSource:         stringPtrTelemetry("user"),
			ToolName:             "spawn_agent",
			StartedAtMS:          123000,
			CompletedAtMS:        125000,
			DurationMS:           uint64PtrTelemetry(2000),
			FinalApprovalOutcome: FinalApprovalOutcomeUnknown,
			TerminalStatus:       ToolItemTerminalStatusCompleted,
		},
		SenderThreadID:           "thread-1",
		ReceiverThreadCount:      2,
		ReceiverThreadIDs:        []string{"child-1", "child-2"},
		RequestedModel:           stringPtrTelemetry("gpt-5"),
		RequestedReasoningEffort: stringPtrTelemetry("high"),
		AgentStateCount:          uint64PtrTelemetry(2),
		CompletedAgentCount:      uint64PtrTelemetry(1),
		FailedAgentCount:         uint64PtrTelemetry(1),
	})

	var got map[string]any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := got["event_params"].(map[string]any)
	receiverIDs, _ := params["receiver_thread_ids"].([]any)
	if got["event_type"] != "codex_collab_agent_tool_call_event" ||
		params["tool_name"] != "spawn_agent" ||
		params["sender_thread_id"] != "thread-1" ||
		params["receiver_thread_count"] != float64(2) ||
		len(receiverIDs) != 2 ||
		params["requested_model"] != "gpt-5" ||
		params["requested_reasoning_effort"] != "high" ||
		params["agent_state_count"] != float64(2) ||
		params["completed_agent_count"] != float64(1) ||
		params["failed_agent_count"] != float64(1) {
		t.Fatalf("collab event JSON = %#v", got)
	}
}

func TestCodexWebSearchEventSerializesExpectedRustShape(t *testing.T) {
	action := "search"
	event := NewCodexWebSearchEvent(CodexWebSearchEventParams{
		CodexToolItemEventBase: CodexToolItemEventBase{
			ThreadID:             "thread-1",
			TurnID:               "turn-1",
			ItemID:               "web-1",
			AppServerClient:      sampleAppServerClientMetadata(),
			Runtime:              sampleRuntimeMetadata(),
			ThreadSource:         stringPtrTelemetry("user"),
			ToolName:             "web_search",
			StartedAtMS:          123000,
			CompletedAtMS:        125000,
			DurationMS:           uint64PtrTelemetry(2000),
			FinalApprovalOutcome: FinalApprovalOutcomeUnknown,
			TerminalStatus:       ToolItemTerminalStatusCompleted,
		},
		WebSearchAction: &action,
		QueryPresent:    true,
		QueryCount:      uint64PtrTelemetry(2),
	})

	var got map[string]any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := got["event_params"].(map[string]any)
	if got["event_type"] != "codex_web_search_event" ||
		params["tool_name"] != "web_search" ||
		params["web_search_action"] != "search" ||
		params["query_present"] != true ||
		params["query_count"] != float64(2) {
		t.Fatalf("web search event JSON = %#v", got)
	}
}

func TestCodexImageGenerationEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexImageGenerationEvent(CodexImageGenerationEventParams{
		CodexToolItemEventBase: CodexToolItemEventBase{
			ThreadID:             "thread-1",
			TurnID:               "turn-1",
			ItemID:               "image-1",
			AppServerClient:      sampleAppServerClientMetadata(),
			Runtime:              sampleRuntimeMetadata(),
			ThreadSource:         stringPtrTelemetry("user"),
			ToolName:             "image_generation",
			StartedAtMS:          123000,
			CompletedAtMS:        125000,
			DurationMS:           uint64PtrTelemetry(2000),
			FinalApprovalOutcome: FinalApprovalOutcomeUnknown,
			TerminalStatus:       ToolItemTerminalStatusFailed,
			FailureKind:          stringPtrTelemetry(ToolItemFailureKindToolError),
		},
		RevisedPromptPresent: true,
		SavedPathPresent:     true,
	})

	var got map[string]any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := got["event_params"].(map[string]any)
	if got["event_type"] != "codex_image_generation_event" ||
		params["tool_name"] != "image_generation" ||
		params["terminal_status"] != "failed" ||
		params["failure_kind"] != "tool_error" ||
		params["revised_prompt_present"] != true ||
		params["saved_path_present"] != true {
		t.Fatalf("image generation event JSON = %#v", got)
	}
}

func TestCodexReviewEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexReviewEvent(CodexReviewEventParams{
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		ItemID:          nil,
		ReviewID:        "review-1",
		AppServerClient: sampleAppServerClientMetadata(),
		Runtime:         sampleRuntimeMetadata(),
		ThreadSource:    stringPtrTelemetry("subagent"),
		SubagentSource:  stringPtrTelemetry("thread_spawn"),
		ParentThreadID:  stringPtrTelemetry("parent-thread-1"),
		SubjectKind:     ReviewSubjectKindNetworkAccess,
		SubjectName:     "network_access",
		Reviewer:        ReviewerUser,
		Trigger:         ReviewTriggerNetworkPolicyDenial,
		Status:          ReviewStatusApproved,
		Resolution:      ReviewResolutionNetworkPolicyAmendment,
		StartedAtMS:     123,
		CompletedAtMS:   125,
		DurationMS:      uint64PtrTelemetry(2),
	})

	var got any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{
		"event_type": "codex_review_event",
		"event_params": {
			"thread_id": "thread-1",
			"turn_id": "turn-1",
			"item_id": null,
			"review_id": "review-1",
			"app_server_client": {
				"product_client_id": "codex_cli_rs",
				"client_name": "codex-tui",
				"client_version": "1.0.0",
				"rpc_transport": "stdio",
				"experimental_api_enabled": true
			},
			"runtime": {
				"codex_rs_version": "0.1.0",
				"runtime_os": "macos",
				"runtime_os_version": "15.3.1",
				"runtime_arch": "aarch64"
			},
			"thread_source": "subagent",
			"subagent_source": "thread_spawn",
			"parent_thread_id": "parent-thread-1",
			"subject_kind": "network_access",
			"subject_name": "network_access",
			"reviewer": "user",
			"trigger": "network_policy_denial",
			"status": "approved",
			"resolution": "network_policy_amendment",
			"started_at_ms": 123,
			"completed_at_ms": 125,
			"duration_ms": 2
		}
	}`), &want); err != nil {
		t.Fatalf("unmarshal expected event error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event JSON mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestCodexThreadInitializedEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexThreadInitializedEvent(CodexThreadInitializedEventInput{
		ThreadID:           "thread-2",
		SessionID:          "session-thread-2",
		AppServerClient:    sampleAppServerClientMetadata(),
		Runtime:            sampleRuntimeMetadata(),
		Model:              "gpt-5",
		Ephemeral:          true,
		ThreadSource:       stringPtrTelemetry("user"),
		InitializationMode: "resumed",
		ParentThreadID:     stringPtrTelemetry("thread-parent"),
		ForkedFromThreadID: stringPtrTelemetry("thread-source"),
		CreatedAt:          455,
	})

	var got any
	if err := marshalUnmarshalTelemetry(event, &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{
		"event_type": "codex_thread_initialized",
		"event_params": {
			"thread_id": "thread-2",
			"session_id": "session-thread-2",
			"app_server_client": {
				"product_client_id": "codex_cli_rs",
				"client_name": "codex-tui",
				"client_version": "1.0.0",
				"rpc_transport": "stdio",
				"experimental_api_enabled": true
			},
			"runtime": {
				"codex_rs_version": "0.1.0",
				"runtime_os": "macos",
				"runtime_os_version": "15.3.1",
				"runtime_arch": "aarch64"
			},
			"model": "gpt-5",
			"ephemeral": true,
			"thread_source": "user",
			"initialization_mode": "resumed",
			"subagent_source": null,
			"parent_thread_id": "thread-parent",
			"forked_from_thread_id": "thread-source",
			"created_at": 455
		}
	}`), &want); err != nil {
		t.Fatalf("unmarshal expected event error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event JSON mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestCodexThreadInitializedEventThreadOriginatorOverridesProductClientID(t *testing.T) {
	event := NewCodexThreadInitializedEvent(CodexThreadInitializedEventInput{
		ThreadID:         "thread-originator",
		SessionID:        "thread-originator",
		AppServerClient:  sampleAppServerClientMetadata(),
		ThreadOriginator: "codex_work_desktop",
		Runtime:          sampleRuntimeMetadata(),
		Model:            "mock-model",
	})
	var payload map[string]any
	if err := marshalUnmarshalTelemetry(event, &payload); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	params := payload["event_params"].(map[string]any)
	client := params["app_server_client"].(map[string]any)
	if client["product_client_id"] != "codex_work_desktop" {
		t.Fatalf("app_server_client = %#v", client)
	}
	if params["initialization_mode"] != "new" {
		t.Fatalf("default initialization_mode = %#v", params["initialization_mode"])
	}
}

func sampleAppServerClientMetadata() CodexAppServerClientMetadata {
	return CodexAppServerClientMetadata{
		ProductClientID:       "codex_cli_rs",
		ClientName:            stringPtrTelemetry("codex-tui"),
		ClientVersion:         stringPtrTelemetry("1.0.0"),
		RPCTransport:          AppServerRPCTransportStdio,
		ExperimentalAPIEnable: boolPtrTelemetry(true),
	}
}

func sampleRuntimeMetadata() CodexRuntimeMetadata {
	return CodexRuntimeMetadata{
		CodexRSVersion:   "0.1.0",
		RuntimeOS:        "macos",
		RuntimeOSVersion: "15.3.1",
		RuntimeArch:      "aarch64",
	}
}

func marshalUnmarshalTelemetry(value any, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func stringPtrTelemetry(value string) *string {
	return &value
}

func boolPtrTelemetry(value bool) *bool {
	return &value
}

func intPtrTelemetry(value int) *int {
	return &value
}

func uint64PtrTelemetry(value uint64) *uint64 {
	return &value
}
