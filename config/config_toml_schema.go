package config

// configValueKind classifies how ConfigToml deserializes one config.toml field.
type configValueKind int

const (
	configKindAny configValueKind = iota
	configKindString
	configKindPathString
	configKindBool
	configKindInteger
	configKindFloat
	configKindSequence
	configKindMap
	configKindStruct
	configKindTaggedEnum
	configKindEnum
	configKindUntaggedEnum
)

// configFieldKind is the deserializer shape of one config.toml field: the kind
// serde expects, the type name it prints when the value has the wrong type, and
// the accepted variants of an enum.
type configFieldKind struct {
	kind     configValueKind
	expected string
	variants []string
	// positional marks the two fields whose serde errors quote a TOML position
	// instead of the dotted path; dualPositional selects the two-cause form.
	positional     bool
	dualPositional bool
}

// configTomlFields lists every field whose serde shape the Rust 0.166 binary
// reports, keyed by its config.toml name and captured from the binary itself.
// Fields it accepts with any value are absent (the untagged
// `forced_chatgpt_workspace_id` repr and the two fields whose errors carry TOML
// positions, `projects` and `project_root_markers` aside), so the validator
// never rejects a document Rust accepts.
var configTomlFields = map[string]configFieldKind{
	"agents":                           {kind: configKindStruct, expected: "AgentsToml"},
	"allow_login_shell":                {kind: configKindBool},
	"allow_symlinked_codex_home":       {kind: configKindBool},
	"analytics":                        {kind: configKindStruct, expected: "AnalyticsConfigToml"},
	"approval_policy":                  {kind: configKindEnum, variants: []string{"untrusted", "on-failure", "on-request", "granular", "never"}},
	"approvals_reviewer":               {kind: configKindEnum, variants: []string{"user", "auto_review", "guardian_subagent"}},
	"apps":                             {kind: configKindStruct, expected: "AppsConfigToml"},
	"apps_mcp_product_sku":             {kind: configKindString},
	"audio":                            {kind: configKindStruct, expected: "RealtimeAudioToml"},
	"auto_review":                      {kind: configKindStruct, expected: "AutoReviewToml"},
	"background_terminal_max_timeout":  {kind: configKindInteger, expected: "u64"},
	"browser_use":                      {kind: configKindStruct, expected: "BrowserUseConfigToml"},
	"chatgpt_base_url":                 {kind: configKindString},
	"check_for_update_on_startup":      {kind: configKindBool},
	"cli_auth_credentials_store":       {kind: configKindEnum, variants: []string{"file", "keyring", "auto", "ephemeral"}},
	"cloud":                            {kind: configKindStruct, expected: "CloudToml"},
	"compact_prompt":                   {kind: configKindString},
	"computer_use":                     {kind: configKindStruct, expected: "ComputerUseConfigToml"},
	"daybreak":                         {kind: configKindBool},
	"default_permissions":              {kind: configKindString},
	"desktop":                          {kind: configKindMap},
	"developer_instructions":           {kind: configKindString},
	"disable_paste_burst":              {kind: configKindBool},
	"experimental_compact_prompt_file": {kind: configKindPathString},
	"experimental_realtime_start_instructions":   {kind: configKindString},
	"experimental_realtime_webrtc_call_base_url": {kind: configKindString},
	"experimental_realtime_ws_backend_prompt":    {kind: configKindString},
	"experimental_realtime_ws_base_url":          {kind: configKindString},
	"experimental_realtime_ws_model":             {kind: configKindString},
	"experimental_realtime_ws_startup_context":   {kind: configKindString},
	"experimental_thread_store":                  {kind: configKindTaggedEnum, expected: "ThreadStoreToml"},
	"experimental_thread_store_endpoint":         {kind: configKindString},
	"experimental_use_unified_exec_tool":         {kind: configKindBool},
	"features":                                   {kind: configKindStruct, expected: "FeaturesToml"},
	"feedback":                                   {kind: configKindStruct, expected: "FeedbackConfigToml"},
	"file_opener":                                {kind: configKindEnum, variants: []string{"vscode", "vscode-insiders", "windsurf", "cursor", "none"}},
	"forced_login_method":                        {kind: configKindEnum, variants: []string{"chatgpt", "api"}},
	"forced_chatgpt_workspace_id":                {kind: configKindUntaggedEnum, expected: "Repr"},
	"ghost_snapshot":                             {kind: configKindStruct, expected: "GhostSnapshotToml"},
	"goals":                                      {kind: configKindStruct, expected: "GoalsToml"},
	"hide_agent_reasoning":                       {kind: configKindBool},
	"history":                                    {kind: configKindStruct, expected: "History"},
	"hooks":                                      {kind: configKindStruct, expected: "HooksToml"},
	"include_apps_instructions":                  {kind: configKindBool},
	"include_collaboration_mode_instructions":    {kind: configKindBool},
	"include_environment_context":                {kind: configKindBool},
	"include_environment_context_time":           {kind: configKindBool},
	"include_permissions_instructions":           {kind: configKindBool},
	"instructions":                               {kind: configKindString},
	"js_repl_node_module_dirs":                   {kind: configKindSequence},
	"js_repl_node_path":                          {kind: configKindPathString},
	"log_dir":                                    {kind: configKindPathString},
	"marketplaces":                               {kind: configKindMap},
	"mcp_enterprise_managed_auth":                {kind: configKindStruct, expected: "McpEnterpriseManagedAuthConfig"},
	"mcp_oauth_callback_port":                    {kind: configKindInteger, expected: "u16"},
	"mcp_oauth_callback_url":                     {kind: configKindString},
	"mcp_oauth_credentials_store":                {kind: configKindEnum, variants: []string{"auto", "file", "keyring"}},
	"mcp_optional_startup_grace_ms":              {kind: configKindInteger, expected: "u64"},
	"mcp_servers":                                {kind: configKindMap},
	"memories":                                   {kind: configKindStruct, expected: "MemoriesToml"},
	"model":                                      {kind: configKindString},
	"model_auto_compact_token_limit":             {kind: configKindInteger, expected: "i64"},
	"model_auto_compact_token_limit_scope":       {kind: configKindEnum, variants: []string{"total", "body_after_prefix"}},
	"model_catalog_json":                         {kind: configKindPathString},
	"model_context_window":                       {kind: configKindInteger, expected: "i64"},
	"model_instructions_file":                    {kind: configKindPathString},
	"model_post_turn_compact_threshold_percent":  {kind: configKindInteger, expected: "u8"},
	"model_provider":                             {kind: configKindString},
	"model_providers":                            {kind: configKindMap},
	"model_reasoning_effort":                     {kind: configKindString},
	"model_reasoning_summary":                    {kind: configKindEnum, variants: []string{"auto", "concise", "detailed", "none"}},
	"model_verbosity":                            {kind: configKindEnum, variants: []string{"low", "medium", "high"}},
	"notice":                                     {kind: configKindStruct, expected: "Notice"},
	"notify":                                     {kind: configKindSequence},
	"openai_base_url":                            {kind: configKindString},
	"orchestrator":                               {kind: configKindStruct, expected: "OrchestratorToml"},
	"oss_provider":                               {kind: configKindString},
	"otel":                                       {kind: configKindStruct, expected: "OtelConfigToml"},
	"permissions":                                {kind: configKindStruct, expected: "PermissionsToml"},
	"personality":                                {kind: configKindEnum, variants: []string{"none", "friendly", "pragmatic"}},
	"plan_mode_reasoning_effort":                 {kind: configKindString},
	"plugins":                                    {kind: configKindStruct, expected: "PluginsConfigToml"},
	"profile":                                    {kind: configKindString},
	"profiles":                                   {kind: configKindMap},
	"project_root_markers":                       {kind: configKindSequence, positional: true},
	"projects":                                   {kind: configKindMap, positional: true, dualPositional: true},
	"project_doc_fallback_filenames":             {kind: configKindSequence},
	"project_doc_max_bytes":                      {kind: configKindInteger, expected: "usize"},
	"realtime":                                   {kind: configKindStruct, expected: "RealtimeToml"},
	"responses_api_metadata":                     {kind: configKindMap},
	"review_model":                               {kind: configKindString},
	"sandbox_mode":                               {kind: configKindEnum, variants: []string{"read-only", "workspace-write", "danger-full-access"}},
	"sandbox_workspace_write":                    {kind: configKindStruct, expected: "SandboxWorkspaceWrite"},
	"service_tier":                               {kind: configKindString},
	"shell_environment_policy":                   {kind: configKindStruct, expected: "ShellEnvironmentPolicyTomlRaw"},
	"show_raw_agent_reasoning":                   {kind: configKindBool},
	"skills":                                     {kind: configKindStruct, expected: "SkillsConfig"},
	"sqlite_home":                                {kind: configKindPathString},
	"suppress_unstable_features_warning":         {kind: configKindBool},
	"thread_unload_delay_secs":                   {kind: configKindInteger, expected: "u64"},
	"tool_output_token_limit":                    {kind: configKindInteger, expected: "usize"},
	"tool_suggest":                               {kind: configKindStruct, expected: "ToolSuggestConfig"},
	"tools":                                      {kind: configKindStruct, expected: "ToolsToml"},
	"tui":                                        {kind: configKindStruct, expected: "Tui"},
	"web_search":                                 {kind: configKindEnum, variants: []string{"disabled", "cached", "indexed", "live"}},
	"windows":                                    {kind: configKindStruct, expected: "WindowsToml"},
}
