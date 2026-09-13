package telemetry

// Metric names emitted by Codex, mirroring codex-rs/otel/src/metrics/names.rs.
// Keep the values byte-for-byte identical to Rust: they are the OTLP instrument
// names and the Statsig-disabled list matches on exact strings.
const (
	ToolCallCountMetric                        = "codex.tool.call"
	ToolCallDurationMetric                     = "codex.tool.call.duration_ms"
	ToolCallUnifiedExecMetric                  = "codex.tool.unified_exec"
	ArtifactOperationStartedMetric             = "codex.artifact.operation.started"
	ArtifactOperationExpectedOutputCountMetric = "codex.artifact.operation.expected_output_count"
	ProcessStartMetric                         = "codex.process.start"
	// ExecServerClientRequestCountMetric counts caller-side exec-server RPC
	// attempts, including local admission and transport failures.
	ExecServerClientRequestCountMetric          = "exec_server_client_requests_total"
	APICallCountMetric                          = "codex.api_request"
	APICallDurationMetric                       = "codex.api_request.duration_ms"
	SSEEventCountMetric                         = "codex.sse_event"
	SSEEventDurationMetric                      = "codex.sse_event.duration_ms"
	WebSocketRequestCountMetric                 = "codex.websocket.request"
	WebSocketRequestDurationMetric              = "codex.websocket.request.duration_ms"
	WebSocketEventCountMetric                   = "codex.websocket.event"
	WebSocketEventDurationMetric                = "codex.websocket.event.duration_ms"
	ResponsesAPIOverheadDurationMetric          = "codex.responses_api_overhead.duration_ms"
	ResponsesAPIInferenceTimeDurationMetric     = "codex.responses_api_inference_time.duration_ms"
	ResponsesAPIEngineIAPITTFTDurationMetric    = "codex.responses_api_engine_iapi_ttft.duration_ms"
	ResponsesAPIEngineServiceTTFTDurationMetric = "codex.responses_api_engine_service_ttft.duration_ms"
	ResponsesAPIEngineIAPITBTDurationMetric     = "codex.responses_api_engine_iapi_tbt.duration_ms"
	ResponsesAPIEngineServiceTBTDurationMetric  = "codex.responses_api_engine_service_tbt.duration_ms"
	TurnE2EDurationMetric                       = "codex.turn.e2e_duration_ms"
	TurnTTFTDurationMetric                      = "codex.turn.ttft.duration_ms"
	TurnTTFMDurationMetric                      = "codex.turn.ttfm.duration_ms"
	TurnNetworkProxyMetric                      = "codex.turn.network_proxy"
	TurnMemoryMetric                            = "codex.turn.memory"
	TurnToolCallMetric                          = "codex.turn.tool.call"
	TurnTokenUsageMetric                        = "codex.turn.token_usage"
	TurnCostMicroUSDMetric                      = "codex.turn.cost_microusd"
	TurnUnifiedExecRunningProcessesMetric       = "codex.turn.unified_exec.running_processes"
	GuardianReviewCountMetric                   = "codex.guardian.review"
	GuardianReviewDurationMetric                = "codex.guardian.review.duration_ms"
	GuardianReviewTTFTDurationMetric            = "codex.guardian.review.ttft.duration_ms"
	GuardianReviewTokenUsageMetric              = "codex.guardian.review.token_usage"
	GoalCreatedMetric                           = "codex.goal.created"
	GoalResumedMetric                           = "codex.goal.resumed"
	GoalCompletedMetric                         = "codex.goal.completed"
	GoalBudgetLimitedMetric                     = "codex.goal.budget_limited"
	GoalUsageLimitedMetric                      = "codex.goal.usage_limited"
	GoalBlockedMetric                           = "codex.goal.blocked"
	GoalTokenCountMetric                        = "codex.goal.token_count"
	GoalDurationSecondsMetric                   = "codex.goal.duration_s"
	PluginInstallElicitationSentMetric          = "codex.plugins.install_elicitation.sent"
	PluginInstallSuggestionMetric               = "codex.plugins.install_suggestion"
	CuratedPluginsStartupSyncMetric             = "codex.plugins.startup_sync"
	CuratedPluginsStartupSyncFinalMetric        = "codex.plugins.startup_sync.final"
	HookRunMetric                               = "codex.hooks.run"
	HookRunDurationMetric                       = "codex.hooks.run.duration_ms"
	// StartupPhaseDurationMetric measures coarse startup phases, tagged by
	// low-cardinality phase and status.
	StartupPhaseDurationMetric = "codex.startup.phase.duration_ms"
	// StartupPrewarmDurationMetric measures the total runtime of a startup
	// prewarm attempt until it completes, tagged by final status.
	StartupPrewarmDurationMetric = "codex.startup_prewarm.duration_ms"
	// StartupPrewarmAgeAtFirstTurnMetric measures the age of the startup prewarm
	// attempt when the first real turn resolves it, tagged by outcome.
	StartupPrewarmAgeAtFirstTurnMetric          = "codex.startup_prewarm.age_at_first_turn_ms"
	ThreadStartedMetric                         = "codex.thread.started"
	ThreadSkillsEnabledTotalMetric              = "codex.thread.skills.enabled_total"
	ThreadSkillsKeptTotalMetric                 = "codex.thread.skills.kept_total"
	ThreadSkillsDescriptionTruncatedCharsMetric = "codex.thread.skills.description_truncated_chars"
	ThreadSkillsTruncatedMetric                 = "codex.thread.skills.truncated"
)

// ConversationTurnCountMetric mirrors config.rs's local
// CONVERSATION_TURN_COUNT_METRIC.
const ConversationTurnCountMetric = "codex.conversation.turn.count"
