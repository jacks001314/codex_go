package config

// This file holds the deserializer shape of the config.toml subtables that
// serde walks at load time. Each table mirrors the Rust struct of the same
// name; fields that accept any shape are left out so the validator never
// rejects a document Rust accepts.

var configAgentRoleFields = map[string]configFieldKind{
	"description":         {kind: configKindString},
	"config_file":         {kind: configKindPathString},
	"nickname_candidates": {kind: configKindStringSequence},
}

var configAgentsFields = map[string]configFieldKind{
	"enabled":                            {kind: configKindBool},
	"max_threads":                        {kind: configKindUsize, expected: "usize", pathName: "max_concurrent_threads_per_session"},
	"max_concurrent_threads_per_session": {kind: configKindUsize, expected: "usize"},
	"max_depth":                          {kind: configKindI32},
	"default_subagent_model":             {kind: configKindString},
	// ReasoningEffort accepts any string at load time.
	"default_subagent_reasoning_effort": {kind: configKindString},
	"job_max_runtime_seconds":           {kind: configKindU64},
	"interrupt_message":                 {kind: configKindBool},
}

var configAnalyticsFields = map[string]configFieldKind{
	"enabled": {kind: configKindBool},
}

var configAppToolFields = map[string]configFieldKind{
	"enabled":       {kind: configKindBool},
	"approval_mode": {kind: configKindEnum, variants: []string{"auto", "prompt", "writes", "approve"}},
}

var configAppLinkFields = map[string]configFieldKind{
	"approvals_reviewer":          {kind: configKindEnum, variants: []string{"user", "auto_review", "guardian_subagent"}},
	"default_tools_approval_mode": {kind: configKindEnum, variants: []string{"auto", "prompt", "writes", "approve"}},
}

var configAppToolsFields = map[string]configFieldKind{}

var configAppLinksFields = map[string]configFieldKind{}

var configAppFields = map[string]configFieldKind{
	"enabled":                     {kind: configKindBool},
	"omit_tools_from":             {kind: configKindSequence},
	"approvals_reviewer":          {kind: configKindEnum, variants: []string{"user", "auto_review", "guardian_subagent"}},
	"destructive_enabled":         {kind: configKindBool},
	"open_world_enabled":          {kind: configKindBool},
	"default_tools_approval_mode": {kind: configKindEnum, variants: []string{"auto", "prompt", "writes", "approve"}},
	"default_tools_enabled":       {kind: configKindBool},
	"tools": {
		kind:           configKindStruct,
		expected:       "AppToolsConfig",
		fields:         configAppToolsFields,
		flattenEntries: true,
		entry:          &configFieldKind{kind: configKindStruct, expected: "AppToolConfig", fields: configAppToolFields},
	},
	"links": {
		kind:           configKindStruct,
		expected:       "AppLinksConfig",
		fields:         configAppLinksFields,
		flattenEntries: true,
		entry:          &configFieldKind{kind: configKindStruct, expected: "AppLinkConfig", fields: configAppLinkFields},
	},
}

var configAppsDefaultFields = map[string]configFieldKind{
	"enabled":                     {kind: configKindBool},
	"approvals_reviewer":          {kind: configKindEnum, variants: []string{"user", "auto_review", "guardian_subagent"}},
	"destructive_enabled":         {kind: configKindBool},
	"open_world_enabled":          {kind: configKindBool},
	"default_tools_approval_mode": {kind: configKindEnum, variants: []string{"auto", "prompt", "writes", "approve"}},
}

var configAppsFields = map[string]configFieldKind{
	"_default": {kind: configKindStruct, expected: "AppsDefaultConfig", fields: configAppsDefaultFields},
}

var configAudioFields = map[string]configFieldKind{
	"microphone_channel": {kind: configKindUntaggedEnum, expected: "MicrophoneChannels", nonzeroU16: true},
	"microphone":         {kind: configKindString},
	"speaker":            {kind: configKindString},
}

var configAutoReviewFields = map[string]configFieldKind{
	"timeout_ms":           {kind: configKindU32},
	"circuit_break_action": {kind: configKindEnum, variants: []string{"default", "strict"}},
	"policy":               {kind: configKindString},
	"experimental_conversation_history_prompt":   {kind: configKindString},
	"experimental_denial_reconsideration_prompt": {kind: configKindString},
	"conversation_history_max_output_tokens":     {kind: configKindNonZeroUsize},
}

var configBrowserUseOriginPolicyFields = map[string]configFieldKind{
	"access":          {kind: configKindEnum, variants: []string{"allow", "deny"}},
	"downloads":       {kind: configKindEnum, variants: []string{"allow", "deny"}},
	"uploads":         {kind: configKindEnum, variants: []string{"allow", "deny"}},
	"full_cdp_access": {kind: configKindEnum, variants: []string{"allow", "deny"}},
}

var configBrowserUseFields = map[string]configFieldKind{
	"allow_history_access":  {kind: configKindBool},
	"default_origin_policy": {kind: configKindStruct, expected: "BrowserUseOriginPolicyConfigToml", fields: configBrowserUseOriginPolicyFields},
	"origins":               {kind: configKindStructMap, expected: "BrowserUseOriginPolicyConfigToml", fields: configBrowserUseOriginPolicyFields},
}

var configFeatureToggleFields = map[string]configFieldKind{
	"enabled": {kind: configKindBool},
}

var configCloudFields = map[string]configFieldKind{
	"skills": {kind: configKindStruct, expected: "FeatureToggleToml", fields: configFeatureToggleFields},
}

var configOrchestratorFields = map[string]configFieldKind{
	"skills": {kind: configKindStruct, expected: "FeatureToggleToml", fields: configFeatureToggleFields},
	"mcp":    {kind: configKindStruct, expected: "FeatureToggleToml", fields: configFeatureToggleFields},
}

var configComputerUseMacosFields = map[string]configFieldKind{
	"bundle_ids": {kind: configKindEnumMap, variants: []string{"allow", "deny"}},
}

var configComputerUseWindowsExeFields = map[string]configFieldKind{
	"publisher_name": {kind: configKindString},
	"product_name":   {kind: configKindString},
	"binary_name":    {kind: configKindString},
	"access":         {kind: configKindEnum, variants: []string{"allow", "deny"}},
}

var configComputerUseWindowsFields = map[string]configFieldKind{
	"aumids": {kind: configKindEnumMap, variants: []string{"allow", "deny"}},
	"exes":   {kind: configKindStructSequence, expected: "ComputerUseWindowsExeConfigToml", fields: configComputerUseWindowsExeFields, required: []string{"publisher_name", "product_name", "access"}},
}

var configComputerUseFields = map[string]configFieldKind{
	"default_app_access": {kind: configKindEnum, variants: []string{"allow", "deny"}},
	"macos":              {kind: configKindStruct, expected: "ComputerUseMacosConfigToml", fields: configComputerUseMacosFields},
	"windows":            {kind: configKindStruct, expected: "ComputerUseWindowsConfigToml", fields: configComputerUseWindowsFields},
}

var configFeedbackFields = map[string]configFieldKind{
	"enabled": {kind: configKindBool},
}

var configGhostSnapshotFields = map[string]configFieldKind{
	"ignore_large_untracked_files": {kind: configKindInteger, expected: "i64"},
	"ignore_large_untracked_dirs":  {kind: configKindInteger, expected: "i64"},
	"disable_warnings":             {kind: configKindBool},
}

var configGoalsFields = map[string]configFieldKind{
	"max_goal_token_budget": {kind: configKindNonZeroU64},
}

var configHooksHandlerFields = map[string]configFieldKind{
	"type": {kind: configKindString},
}

var configHooksMatcherFields = map[string]configFieldKind{
	"matcher": {kind: configKindString},
	"hooks":   {kind: configKindStructSequence, expected: "HookHandlerConfig", fields: configHooksHandlerFields, required: []string{"type"}},
}

var configHooksStateFields = map[string]configFieldKind{
	"enabled":              {kind: configKindBool},
	"trusted_hash":         {kind: configKindString},
	"trusted_content_hash": {kind: configKindString},
}

// configHooksEventFields are HookEventsToml's `#[serde(flatten)]` events; each
// one is a `Vec<MatcherGroup>` reported against the enclosing `hooks` table.
var configHooksEventFields = map[string]configFieldKind{
	"PreModelRequest":   {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"PostModelRequest":  {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"PreToolUse":        {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"PermissionRequest": {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"PostToolUse":       {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"PreCompact":        {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"PostCompact":       {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"SessionStart":      {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"SessionEnd":        {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"UserPromptSubmit":  {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
	"SubagentStart":     {kind: configKindStructSequence, expected: "MatcherGroup", fields: configHooksMatcherFields, flattenPath: true},
}

var configHooksFields = map[string]configFieldKind{
	"state":             {kind: configKindStructMap, expected: "HookStateToml", fields: configHooksStateFields},
	"PreModelRequest":   configHooksEventFields["PreModelRequest"],
	"PostModelRequest":  configHooksEventFields["PostModelRequest"],
	"PreToolUse":        configHooksEventFields["PreToolUse"],
	"PermissionRequest": configHooksEventFields["PermissionRequest"],
	"PostToolUse":       configHooksEventFields["PostToolUse"],
	"PreCompact":        configHooksEventFields["PreCompact"],
	"PostCompact":       configHooksEventFields["PostCompact"],
	"SessionStart":      configHooksEventFields["SessionStart"],
	"SessionEnd":        configHooksEventFields["SessionEnd"],
	"UserPromptSubmit":  configHooksEventFields["UserPromptSubmit"],
	"SubagentStart":     configHooksEventFields["SubagentStart"],
}

var configMcpEnterpriseIdpFields = map[string]configFieldKind{
	"issuer":                     {kind: configKindString},
	"client_id":                  {kind: configKindString},
	"client_secret":              {kind: configKindString},
	"token_endpoint_auth_method": {kind: configKindEnum, variants: []string{"client_secret_basic", "client_secret_post"}},
}

var configMcpEnterpriseManagedAuthFields = map[string]configFieldKind{
	"idp": {kind: configKindStruct, expected: "McpServerIdpOAuthConfig", fields: configMcpEnterpriseIdpFields, required: []string{"issuer", "client_id"}},
}

var configMemoriesFields = map[string]configFieldKind{
	"version":                            {kind: configKindEnum, variants: []string{"v1", "v2"}},
	"dual_write":                         {kind: configKindBool},
	"disable_on_external_context":        {kind: configKindBool},
	"generate_memories":                  {kind: configKindBool},
	"use_memories":                       {kind: configKindBool},
	"dedicated_tools":                    {kind: configKindBool},
	"max_raw_memories_for_consolidation": {kind: configKindUsize, expected: "usize"},
	"max_unused_days":                    {kind: configKindInteger, expected: "i64"},
	"max_rollout_age_days":               {kind: configKindInteger, expected: "i64"},
	"max_rollouts_per_startup":           {kind: configKindUsize, expected: "usize"},
	"min_rollout_idle_hours":             {kind: configKindInteger, expected: "i64"},
	"min_rate_limit_remaining_percent":   {kind: configKindInteger, expected: "i64"},
	"extract_model":                      {kind: configKindString},
	"consolidation_model":                {kind: configKindString},
	"consolidation_service_tier":         {kind: configKindString},
}

var configNoticeMigrationPromptsFields = map[string]configFieldKind{
	"home":                     {kind: configKindBool},
	"home_last_prompted_at":    {kind: configKindInteger, expected: "i64"},
	"projects":                 {kind: configKindValueMap, entry: &configFieldKind{kind: configKindBool}},
	"project_last_prompted_at": {kind: configKindValueMap, entry: &configFieldKind{kind: configKindInteger, expected: "i64"}},
}

var configNoticeFields = map[string]configFieldKind{
	"hide_gov_cloud_guidance":                 {kind: configKindBool},
	"hide_full_access_warning":                {kind: configKindBool},
	"hide_world_writable_warning":             {kind: configKindBool},
	"fast_default_opt_out":                    {kind: configKindBool},
	"hide_rate_limit_model_nudge":             {kind: configKindBool},
	"hide_gpt5_1_migration_prompt":            {kind: configKindBool},
	"hide_gpt-5.1-codex-max_migration_prompt": {kind: configKindBool},
	"model_migrations":                        {kind: configKindStringMap},
	"external_config_migration_prompts":       {kind: configKindStruct, expected: "ExternalConfigMigrationPrompts", fields: configNoticeMigrationPromptsFields},
}

var configOtelToolResultFields = map[string]configFieldKind{
	"max_bytes": {kind: configKindUsize, expected: "usize"},
}

var configOtelFields = map[string]configFieldKind{
	"tool_result":              {kind: configKindStruct, expected: "ToolResultLogConfig", fields: configOtelToolResultFields, required: []string{"max_bytes"}},
	"log_user_prompt":          {kind: configKindBool},
	"log_agent_responses":      {kind: configKindBool},
	"log_guardian_assessments": {kind: configKindBool},
	"environment":              {kind: configKindString},
	"exporter": {
		kind:                  configKindEnum,
		variants:              []string{"none", "statsig", "otlp-http", "otlp-grpc"},
		structVariants:        map[string]string{"otlp-http": "OtelExporterKind::OtlpHttp", "otlp-grpc": "OtelExporterKind::OtlpGrpc"},
		structVariantRequired: []string{"endpoint"},
	},
	"trace_exporter": {
		kind:                  configKindEnum,
		variants:              []string{"none", "statsig", "otlp-http", "otlp-grpc"},
		structVariants:        map[string]string{"otlp-http": "OtelExporterKind::OtlpHttp", "otlp-grpc": "OtelExporterKind::OtlpGrpc"},
		structVariantRequired: []string{"endpoint"},
	},
	"metrics_exporter": {
		kind:                  configKindEnum,
		variants:              []string{"none", "statsig", "otlp-http", "otlp-grpc"},
		structVariants:        map[string]string{"otlp-http": "OtelExporterKind::OtlpHttp", "otlp-grpc": "OtelExporterKind::OtlpGrpc"},
		structVariantRequired: []string{"endpoint"},
	},
	"span_attributes": {kind: configKindStringMap},
	"tracestate":      {kind: configKindStringMapOfMaps},
}

var configPermissionsNetworkFields = map[string]configFieldKind{
	"enabled":                              {kind: configKindBool},
	"proxy_url":                            {kind: configKindString},
	"enable_socks5":                        {kind: configKindBool},
	"socks_url":                            {kind: configKindString},
	"enable_socks5_udp":                    {kind: configKindBool},
	"allow_upstream_proxy":                 {kind: configKindBool},
	"dangerously_allow_non_loopback_proxy": {kind: configKindBool},
	"dangerously_allow_all_unix_sockets":   {kind: configKindBool},
	"mode":                                 {kind: configKindEnum, variants: []string{"limited", "full"}},
	"domains":                              {kind: configKindStruct, expected: "NetworkDomainPermissionsToml"},
	"unix_sockets":                         {kind: configKindStruct, expected: "NetworkUnixSocketPermissionsToml"},
	"allow_local_binding":                  {kind: configKindBool},
	"mitm":                                 {kind: configKindStruct, expected: "NetworkMitmToml"},
}

var configPermissionsFilesystemFields = map[string]configFieldKind{
	"glob_scan_max_depth": {kind: configKindUsize, expected: "usize"},
}

var configPermissionsProfileFields = map[string]configFieldKind{
	"description":     {kind: configKindString},
	"extends":         {kind: configKindString},
	"workspace_roots": {kind: configKindStruct, expected: "WorkspaceRootsToml", flattenEntries: true, entry: &configFieldKind{kind: configKindBool}},
	"filesystem": {
		kind:           configKindStruct,
		expected:       "FilesystemPermissionsToml",
		fields:         configPermissionsFilesystemFields,
		flattenEntries: true,
		entry:          &configFieldKind{kind: configKindUntaggedEnum, expected: "FilesystemPermissionToml", allowBool: true, allowString: true},
	},
	"network": {kind: configKindStruct, expected: "NetworkToml", fields: configPermissionsNetworkFields},
}

var configPermissionsFields = map[string]configFieldKind{}

var configPluginMcpServerFields = map[string]configFieldKind{
	"enabled":                     {kind: configKindBool},
	"has_unsupported_ema_auth":    {kind: configKindBool},
	"default_tools_approval_mode": {kind: configKindEnum, variants: []string{"auto", "prompt", "writes", "approve"}},
	"enabled_tools":               {kind: configKindStringSequence},
	"disabled_tools":              {kind: configKindStringSequence},
	"tools":                       {kind: configKindMap},
}

var configPluginFields = map[string]configFieldKind{
	"enabled":          {kind: configKindBool},
	"remote_plugin_id": {kind: configKindString},
	"release_id":       {kind: configKindString},
	"auto_provision":   {kind: configKindBool},
	"mcp_servers": {
		kind:     configKindStructMap,
		expected: "PluginMcpServerConfig",
		fields:   configPluginMcpServerFields,
	},
}

var configPluginsDefaultFields = map[string]configFieldKind{
	"enabled": {kind: configKindBool},
}

var configPluginsFields = map[string]configFieldKind{
	"_default": {kind: configKindStruct, expected: "PluginsDefaultConfig", fields: configPluginsDefaultFields},
}

var configRealtimeFields = map[string]configFieldKind{
	"version":   {kind: configKindEnum, variants: []string{"v1", "v2", "v3"}},
	"type":      {kind: configKindEnum, variants: []string{"conversational", "transcription"}},
	"transport": {kind: configKindEnum, variants: []string{"webrtc", "websocket"}},
	"voice": {
		kind: configKindEnum,
		variants: []string{
			"alloy", "arbor", "ash", "aube", "ballad", "breeze", "bubbie", "cedar",
			"coral", "cove", "echo", "ember", "haetsal", "hanul", "himari", "juniper",
			"leher", "maple", "marin", "miko", "neer", "porto", "rindo", "rio",
			"rivage", "sage", "selva", "shimmer", "sol", "sonna", "spruce", "tinta",
			"vale", "verse", "viola",
		},
	},
}

var configSandboxWorkspaceWriteFields = map[string]configFieldKind{
	"writable_roots":         {kind: configKindPathSequence},
	"network_access":         {kind: configKindBool},
	"exclude_tmpdir_env_var": {kind: configKindBool},
	"exclude_slash_tmp":      {kind: configKindBool},
}

var configSkillFields = map[string]configFieldKind{
	"path":    {kind: configKindPathString},
	"name":    {kind: configKindString},
	"enabled": {kind: configKindBool},
}

var configSkillsBundledFields = map[string]configFieldKind{
	"enabled": {kind: configKindBool},
}

var configSkillsFields = map[string]configFieldKind{
	"bundled":              {kind: configKindStruct, expected: "BundledSkillsConfig", fields: configSkillsBundledFields},
	"include_instructions": {kind: configKindBool},
	"max_context_tokens":   {kind: configKindNonZeroUsize},
	"config":               {kind: configKindStructSequence, expected: "SkillConfig", fields: configSkillFields, required: []string{"enabled"}},
}

var configToolSuggestEntryFields = map[string]configFieldKind{
	"type": {kind: configKindEnum, variants: []string{"connector", "plugin"}},
	"id":   {kind: configKindString},
}

var configToolSuggestFields = map[string]configFieldKind{
	"discoverables":  {kind: configKindStructSequence, expected: "ToolSuggestDiscoverable", fields: configToolSuggestEntryFields, required: []string{"type", "id"}},
	"disabled_tools": {kind: configKindStructSequence, expected: "ToolSuggestDisabledTool", fields: configToolSuggestEntryFields, required: []string{"type", "id"}},
}

var configWebSearchLocationFields = map[string]configFieldKind{
	"country":  {kind: configKindString},
	"region":   {kind: configKindString},
	"city":     {kind: configKindString},
	"timezone": {kind: configKindString},
}

var configWebSearchFields = map[string]configFieldKind{
	"context_size":    {kind: configKindEnum, variants: []string{"low", "medium", "high"}},
	"allowed_domains": {kind: configKindStringSequence},
	"location":        {kind: configKindStruct, expected: "WebSearchLocation", fields: configWebSearchLocationFields},
}

var configUpdatePlanFields = map[string]configFieldKind{
	"enabled": {kind: configKindBool},
}

var configRequestUserInputFields = map[string]configFieldKind{
	"enabled": {kind: configKindBool},
}

var configToolsFields = map[string]configFieldKind{
	"client_search_provider":          {kind: configKindEnum, variants: []string{"standalone", "search_mcp"}},
	"web_search":                      {kind: configKindUntaggedStruct, expected: "WebSearchToolConfigInput", allowBool: true, fields: configWebSearchFields},
	"experimental_request_user_input": {kind: configKindStruct, expected: "ExperimentalRequestUserInput", fields: configRequestUserInputFields},
	"update_plan":                     {kind: configKindStruct, expected: "UpdatePlanToolConfig", fields: configUpdatePlanFields},
}
