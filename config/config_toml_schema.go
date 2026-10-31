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
	// configKindStructMap is a map whose entries are structs, like
	// `profiles.<name>`.
	configKindStructMap
	// configKindStringMap is a map of strings, like `http_headers`.
	configKindStringMap
	// configKindEnumMap is a map of enums, like shell_environment_policy.filters.
	configKindEnumMap
	// configKindStringSequence is a sequence of strings.
	configKindStringSequence
	// configKindUsize is an unsigned integer; serde reports a negative literal
	// as `invalid value` instead of `invalid type`.
	configKindUsize
	// configKindWireAPI is WireApi: `responses` is the only variant and `chat`
	// carries its own removal message.
	configKindWireAPI
	configKindTaggedEnum
	configKindEnum
	configKindUntaggedEnum
	// configKindU32, configKindU64 and configKindI32 are integer widths whose
	// serde rejection for an out-of-range literal is `invalid value`.
	configKindU32
	configKindU64
	configKindI32
	// configKindNonZeroUsize and configKindNonZeroU64 are NonZeroUsize and
	// NonZeroU64, which reject zero as `invalid value`.
	configKindNonZeroUsize
	configKindNonZeroU64
	// configKindStructSequence is a `Vec<Struct>`; a non-table element is
	// reported against the field and its fields descend from there.
	configKindStructSequence
	// configKindPathSequence is a `Vec<PathBuf>`.
	configKindPathSequence
	// configKindStringMapOfMaps is a map of maps of strings, like
	// `otel.tracestate`.
	configKindStringMapOfMaps
	// configKindValueMap is a map whose entries are checked with `entry`.
	configKindValueMap
	// configKindUntaggedStruct is an untagged enum of a boolean and a struct,
	// like `tools.web_search`.
	configKindUntaggedStruct
)

// configFieldKind is the deserializer shape of one config.toml field: the kind
// serde expects, the type name it prints when the value has the wrong type, and
// the accepted variants of an enum.
type configFieldKind struct {
	kind     configValueKind
	expected string
	variants []string
	// fields lists the struct's own fields when serde descends into it
	// (`profiles.<name>`, `..tui`).
	fields map[string]configFieldKind
	// boolValues marks a FeaturesToml-style map whose entries must be booleans;
	// serde reports that failure against the table itself.
	boolValues bool
	// positional marks the two fields whose serde errors quote a TOML position
	// instead of the dotted path; dualPositional selects the two-cause form.
	positional     bool
	dualPositional bool
	// tablePositional marks a nested field whose custom deserializer reports
	// the position of its enclosing table header.
	tablePositional bool
	// flattenPath marks a `#[serde(flatten)]` field: serde reports its failures
	// against the enclosing table, and only after the named fields.
	flattenPath bool
	// required lists the struct's mandatory fields; a table without them
	// reports `missing field`.
	required []string
	// stringOrMap selects serde's `expected string or map` wording for an
	// externally tagged enum that is not a bare unit variant.
	stringOrMap bool
	// allowBool lets an untagged enum accept a boolean variant; allowString does
	// the same for a bare string.
	allowBool   bool
	allowString bool
	// nonzeroU16 lets an untagged enum accept 1..=65535 and sequences of it,
	// the shape of MicrophoneChannels.
	nonzeroU16 bool
	// pathName overrides the last segment of the field's reported path, the way
	// a `#[serde(alias)]` still reports the canonical field name.
	pathName string
	// entry is the element shape of a map- or sequence-shaped field; it is a
	// pointer so the shape can nest inside itself.
	entry *configFieldKind
	// flattenEntries marks a `#[serde(flatten)]` map: every key the named
	// fields do not claim is an entry, and serde reports its failures against
	// the enclosing table instead of the entry's own path.
	flattenEntries bool
	// structVariants are the externally tagged variants that carry fields;
	// variantStructNames spells the type serde prints for a non-table payload.
	structVariants        map[string]string
	structVariantRequired []string
}

// configProfileFields is ConfigProfile (config/src/profile_toml.rs). Fields that
// accept any string (`model_reasoning_effort`, `plan_mode_reasoning_effort`) or
// any nested value (`tools`) are left out so the validator never rejects a
// document Rust accepts.
var configProfileFields = map[string]configFieldKind{
	"model":                                   {kind: configKindString},
	"service_tier":                            {kind: configKindString},
	"model_provider":                          {kind: configKindString},
	"approval_policy":                         {kind: configKindEnum, variants: []string{"untrusted", "on-failure", "on-request", "granular", "never"}},
	"approvals_reviewer":                      {kind: configKindEnum, variants: []string{"user", "auto_review", "guardian_subagent"}},
	"sandbox_mode":                            {kind: configKindEnum, variants: []string{"read-only", "workspace-write", "danger-full-access"}},
	"model_reasoning_summary":                 {kind: configKindEnum, variants: []string{"auto", "concise", "detailed", "none"}},
	"model_verbosity":                         {kind: configKindEnum, variants: []string{"low", "medium", "high"}},
	"model_catalog_json":                      {kind: configKindPathString},
	"personality":                             {kind: configKindEnum, variants: []string{"none", "friendly", "pragmatic"}},
	"chatgpt_base_url":                        {kind: configKindString},
	"model_instructions_file":                 {kind: configKindPathString},
	"js_repl_node_path":                       {kind: configKindPathString},
	"js_repl_node_module_dirs":                {kind: configKindSequence},
	"experimental_compact_prompt_file":        {kind: configKindPathString},
	"include_permissions_instructions":        {kind: configKindBool},
	"include_apps_instructions":               {kind: configKindBool},
	"include_collaboration_mode_instructions": {kind: configKindBool},
	"include_environment_context":             {kind: configKindBool},
	"include_environment_context_time":        {kind: configKindBool},
	"experimental_use_unified_exec_tool":      {kind: configKindBool},
	"tools":                                   {kind: configKindStruct, expected: "ToolsToml"},
	"web_search":                              {kind: configKindEnum, variants: []string{"disabled", "cached", "indexed", "live"}},
	"analytics":                               {kind: configKindStruct, expected: "AnalyticsConfigToml"},
	"tui":                                     {kind: configKindStruct, expected: "ProfileTui", fields: configProfileTuiFields},
	"windows":                                 {kind: configKindStruct, expected: "WindowsToml"},
	"features":                                {kind: configKindStruct, expected: "FeaturesToml", boolValues: true},
	"oss_provider":                            {kind: configKindString},
}

// configModelProviderFields is ModelProviderInfo (model-provider-info/src/lib.rs).
// Nested structs are only checked for being a table, which is what serde
// reports before it descends into them.
var configModelProviderFields = map[string]configFieldKind{
	"name":                           {kind: configKindString},
	"base_url":                       {kind: configKindString},
	"model_catalog_url":              {kind: configKindString},
	"env_key":                        {kind: configKindString},
	"env_key_instructions":           {kind: configKindString},
	"experimental_bearer_token":      {kind: configKindString},
	"auth":                           {kind: configKindStruct, expected: "ModelProviderAuthInfo"},
	"gateway_oauth":                  {kind: configKindStruct, expected: "GatewayOAuthConfig"},
	"aws":                            {kind: configKindStruct, expected: "ModelProviderAwsAuthInfo"},
	"project_id":                     {kind: configKindString},
	"location":                       {kind: configKindString},
	"credential_file":                {kind: configKindPathString},
	"wire_api":                       {kind: configKindWireAPI},
	"capabilities":                   {kind: configKindStruct, expected: "ModelProviderCapabilities"},
	"query_params":                   {kind: configKindStringMap},
	"http_headers":                   {kind: configKindStringMap},
	"env_http_headers":               {kind: configKindStringMap},
	"request_max_retries":            {kind: configKindInteger, expected: "u64"},
	"stream_max_retries":             {kind: configKindInteger, expected: "u64"},
	"stream_idle_timeout_ms":         {kind: configKindInteger, expected: "u64"},
	"websocket_connect_timeout_ms":   {kind: configKindInteger, expected: "u64"},
	"requires_openai_auth":           {kind: configKindBool},
	"supports_websockets":            {kind: configKindBool},
	"supports_standalone_web_search": {kind: configKindBool},
}

// configHistoryFields is History (config/src/types.rs).
var configHistoryFields = map[string]configFieldKind{
	"persistence": {kind: configKindEnum, variants: []string{"save-all", "none"}},
	"max_bytes":   {kind: configKindUsize, expected: "usize"},
}

// configWindowsFields is WindowsToml.
var configWindowsFields = map[string]configFieldKind{
	"sandbox":   {kind: configKindEnum, variants: []string{"elevated", "unelevated", "mxc"}},
	"allow_mxc": {kind: configKindBool},
}

// configShellEnvironmentPolicyFields is ShellEnvironmentPolicyTomlRaw. Its
// filters carry a custom deserializer, so serde reports those three fields at
// the table header instead of at a dotted path.
var configShellEnvironmentPolicyFields = map[string]configFieldKind{
	"inherit":                  {kind: configKindEnum, variants: []string{"core", "all", "none"}},
	"ignore_default_excludes":  {kind: configKindBool},
	"exclude":                  {kind: configKindStringSequence, tablePositional: true},
	"set":                      {kind: configKindStringMap},
	"include_only":             {kind: configKindStringSequence, tablePositional: true},
	"filters":                  {kind: configKindEnumMap, expected: "ShellEnvironmentPolicyFilter", variants: []string{"include", "exclude"}, tablePositional: true},
	"experimental_use_profile": {kind: configKindBool},
}

// configTuiFields is Tui (config/src/types.rs). The notification settings come
// in through `#[serde(flatten)]`, which is why their failures are reported
// against `tui` itself.
var configTuiFields = map[string]configFieldKind{
	"animations":                      {kind: configKindBool},
	"screen_reader_detection_done":    {kind: configKindBool},
	"effects":                         {kind: configKindStruct, expected: "TuiEffects", fields: configTuiEffectsFields},
	"rendering":                       {kind: configKindStruct, expected: "TuiRendering", fields: configTuiRenderingFields},
	"show_tooltips":                   {kind: configKindBool},
	"show_server_version_notice":      {kind: configKindBool},
	"auto_recap":                      {kind: configKindBool},
	"disable_paste_burst":             {kind: configKindBool},
	"vim_mode_default":                {kind: configKindBool},
	"question_esc_back":               {kind: configKindBool},
	"raw_output_mode":                 {kind: configKindBool},
	"fullscreen_transcript":           {kind: configKindBool},
	"mouse_scroll_speed":              {kind: configKindFloat, expected: "f64"},
	"copy_on_select":                  {kind: configKindEnum, variants: []string{"auto", "always", "never"}},
	"copy_on_select_clear_selection":  {kind: configKindBool},
	"right_click_paste":               {kind: configKindEnum, variants: []string{"auto", "on", "off"}},
	"alternate_screen":                {kind: configKindEnum, variants: []string{"auto", "always", "never"}},
	"status_line":                     {kind: configKindStringSequence},
	"status_line_use_colors":          {kind: configKindBool},
	"terminal_title":                  {kind: configKindStringSequence},
	"theme":                           {kind: configKindString},
	"pet":                             {kind: configKindString},
	"pet_anchor":                      {kind: configKindEnum, variants: []string{"composer", "screen-bottom"}},
	"session_picker_view":             {kind: configKindEnum, variants: []string{"comfortable", "dense"}},
	"agents_overview_grouping":        {kind: configKindEnum, variants: []string{"project", "status", "model"}},
	"resume_cwd":                      {kind: configKindEnum, variants: []string{"current", "session"}},
	"keymap":                          {kind: configKindStruct, expected: "TuiKeymap", fields: configTuiKeymapFields},
	"presets":                         {kind: configKindStructMap, expected: "QuickDial", fields: configQuickDialFields, required: []string{"input"}},
	"model_availability_nux":          {kind: configKindStruct, expected: "ModelAvailabilityNuxConfig"},
	"terminal_resize_reflow_max_rows": {kind: configKindUsize, expected: "usize"},
	"notifications":                   {kind: configKindUntaggedEnum, expected: "Notifications", allowBool: true, flattenPath: true},
	"notification_method":             {kind: configKindEnum, variants: []string{"auto", "osc9", "bel"}, stringOrMap: true, flattenPath: true},
	"notification_condition":          {kind: configKindEnum, variants: []string{"unfocused", "always"}, stringOrMap: true, flattenPath: true},
}

// configTuiEffectsFields is TuiEffects: six boolean toggles.
var configTuiEffectsFields = map[string]configFieldKind{
	"starfield": {kind: configKindBool},
	"shimmer":   {kind: configKindBool},
	"welcome":   {kind: configKindBool},
	"effort":    {kind: configKindBool},
	"progress":  {kind: configKindBool},
	"title":     {kind: configKindBool},
}

// configTuiRenderingFields is TuiRendering.
var configTuiRenderingFields = map[string]configFieldKind{
	"mermaid": {kind: configKindBool},
	"math":    {kind: configKindBool},
	"tables":  {kind: configKindBool},
	"lists":   {kind: configKindBool},
}

// configTuiKeymapFields is the subset of TuiKeymap whose shape the 0.166 binary
// reports; the per-context tables are left to the TUI itself.
var configTuiKeymapFields = map[string]configFieldKind{
	"global": {kind: configKindStruct, expected: "TuiGlobalKeymap"},
}

// configQuickDialFields is QuickDial (serialized as `Preset`).
var configQuickDialFields = map[string]configFieldKind{
	"input": {kind: configKindString},
	"label": {kind: configKindString},
}

// configProfileTuiFields is ProfileTui, the profile-scoped subset of the TUI
// settings.
var configProfileTuiFields = map[string]configFieldKind{
	"session_picker_view": {kind: configKindEnum, variants: []string{"comfortable", "dense"}},
}

// configTomlFields lists every field whose serde shape the Rust 0.166 binary
// reports, keyed by its config.toml name and captured from the binary itself.
// Fields it accepts with any value are absent (the untagged
// `forced_chatgpt_workspace_id` repr and the two fields whose errors carry TOML
// positions, `projects` and `project_root_markers` aside), so the validator
// never rejects a document Rust accepts.
var configTomlFields = map[string]configFieldKind{
	"agents":                           {kind: configKindStruct, expected: "AgentsToml", fields: configAgentsFields, flattenEntries: true, entry: &configFieldKind{kind: configKindStruct, expected: "AgentRoleToml", fields: configAgentRoleFields}},
	"allow_login_shell":                {kind: configKindBool},
	"allow_symlinked_codex_home":       {kind: configKindBool},
	"analytics":                        {kind: configKindStruct, expected: "AnalyticsConfigToml", fields: configAnalyticsFields},
	"approval_policy":                  {kind: configKindEnum, variants: []string{"untrusted", "on-failure", "on-request", "granular", "never"}},
	"approvals_reviewer":               {kind: configKindEnum, variants: []string{"user", "auto_review", "guardian_subagent"}},
	"apps":                             {kind: configKindStruct, expected: "AppsConfigToml", fields: configAppsFields, flattenEntries: true, entry: &configFieldKind{kind: configKindStruct, expected: "AppConfig", fields: configAppFields}},
	"apps_mcp_product_sku":             {kind: configKindString},
	"audio":                            {kind: configKindStruct, expected: "RealtimeAudioToml", fields: configAudioFields},
	"auto_review":                      {kind: configKindStruct, expected: "AutoReviewToml", fields: configAutoReviewFields},
	"background_terminal_max_timeout":  {kind: configKindInteger, expected: "u64"},
	"browser_use":                      {kind: configKindStruct, expected: "BrowserUseConfigToml", fields: configBrowserUseFields},
	"chatgpt_base_url":                 {kind: configKindString},
	"check_for_update_on_startup":      {kind: configKindBool},
	"cli_auth_credentials_store":       {kind: configKindEnum, variants: []string{"file", "keyring", "auto", "ephemeral"}},
	"cloud":                            {kind: configKindStruct, expected: "CloudToml", fields: configCloudFields},
	"compact_prompt":                   {kind: configKindString},
	"computer_use":                     {kind: configKindStruct, expected: "ComputerUseConfigToml", fields: configComputerUseFields},
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
	"feedback":                                   {kind: configKindStruct, expected: "FeedbackConfigToml", fields: configFeedbackFields},
	"file_opener":                                {kind: configKindEnum, variants: []string{"vscode", "vscode-insiders", "windsurf", "cursor", "none"}},
	"forced_login_method":                        {kind: configKindEnum, variants: []string{"chatgpt", "api"}},
	"forced_chatgpt_workspace_id":                {kind: configKindUntaggedEnum, expected: "Repr", allowString: true},
	"ghost_snapshot":                             {kind: configKindStruct, expected: "GhostSnapshotToml", fields: configGhostSnapshotFields},
	"goals":                                      {kind: configKindStruct, expected: "GoalsToml", fields: configGoalsFields},
	"hide_agent_reasoning":                       {kind: configKindBool},
	"history":                                    {kind: configKindStruct, expected: "History", fields: configHistoryFields},
	"hooks":                                      {kind: configKindStruct, expected: "HooksToml", fields: configHooksFields},
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
	"mcp_enterprise_managed_auth":                {kind: configKindStruct, expected: "McpEnterpriseManagedAuthConfig", fields: configMcpEnterpriseManagedAuthFields, required: []string{"idp"}},
	"mcp_oauth_callback_port":                    {kind: configKindInteger, expected: "u16"},
	"mcp_oauth_callback_url":                     {kind: configKindString},
	"mcp_oauth_credentials_store":                {kind: configKindEnum, variants: []string{"auto", "file", "keyring"}},
	"mcp_optional_startup_grace_ms":              {kind: configKindInteger, expected: "u64"},
	"mcp_servers":                                {kind: configKindMap},
	"memories":                                   {kind: configKindStruct, expected: "MemoriesToml", fields: configMemoriesFields},
	"model":                                      {kind: configKindString},
	"model_auto_compact_token_limit":             {kind: configKindInteger, expected: "i64"},
	"model_auto_compact_token_limit_scope":       {kind: configKindEnum, variants: []string{"total", "body_after_prefix"}},
	"model_catalog_json":                         {kind: configKindPathString},
	"model_context_window":                       {kind: configKindInteger, expected: "i64"},
	"model_instructions_file":                    {kind: configKindPathString},
	"model_post_turn_compact_threshold_percent":  {kind: configKindInteger, expected: "u8"},
	"model_provider":                             {kind: configKindString},
	"model_providers":                            {kind: configKindStructMap, expected: "ModelProviderInfo", fields: configModelProviderFields},
	"model_reasoning_effort":                     {kind: configKindString},
	"model_reasoning_summary":                    {kind: configKindEnum, variants: []string{"auto", "concise", "detailed", "none"}},
	"model_verbosity":                            {kind: configKindEnum, variants: []string{"low", "medium", "high"}},
	"notice":                                     {kind: configKindStruct, expected: "Notice", fields: configNoticeFields},
	"notify":                                     {kind: configKindSequence},
	"openai_base_url":                            {kind: configKindString},
	"orchestrator":                               {kind: configKindStruct, expected: "OrchestratorToml", fields: configOrchestratorFields},
	"oss_provider":                               {kind: configKindString},
	"otel":                                       {kind: configKindStruct, expected: "OtelConfigToml", fields: configOtelFields},
	"permissions":                                {kind: configKindStruct, expected: "PermissionsToml", fields: configPermissionsFields, flattenEntries: true, entry: &configFieldKind{kind: configKindStruct, expected: "PermissionProfileToml", fields: configPermissionsProfileFields}},
	"personality":                                {kind: configKindEnum, variants: []string{"none", "friendly", "pragmatic"}},
	"plan_mode_reasoning_effort":                 {kind: configKindString},
	"plugins":                                    {kind: configKindStruct, expected: "PluginsConfigToml", fields: configPluginsFields, flattenEntries: true, entry: &configFieldKind{kind: configKindStruct, expected: "PluginConfig", fields: configPluginFields}},
	"profile":                                    {kind: configKindString},
	"profiles":                                   {kind: configKindStructMap, expected: "ConfigProfile", fields: configProfileFields},
	"project_root_markers":                       {kind: configKindSequence, positional: true},
	"projects":                                   {kind: configKindMap, positional: true, dualPositional: true},
	"project_doc_fallback_filenames":             {kind: configKindSequence},
	"project_doc_max_bytes":                      {kind: configKindInteger, expected: "usize"},
	"realtime":                                   {kind: configKindStruct, expected: "RealtimeToml", fields: configRealtimeFields},
	"responses_api_metadata":                     {kind: configKindMap},
	"review_model":                               {kind: configKindString},
	"sandbox_mode":                               {kind: configKindEnum, variants: []string{"read-only", "workspace-write", "danger-full-access"}},
	"sandbox_workspace_write":                    {kind: configKindStruct, expected: "SandboxWorkspaceWrite", fields: configSandboxWorkspaceWriteFields},
	"service_tier":                               {kind: configKindString},
	"shell_environment_policy":                   {kind: configKindStruct, expected: "ShellEnvironmentPolicyTomlRaw", fields: configShellEnvironmentPolicyFields},
	"show_raw_agent_reasoning":                   {kind: configKindBool},
	"skills":                                     {kind: configKindStruct, expected: "SkillsConfig", fields: configSkillsFields},
	"sqlite_home":                                {kind: configKindPathString},
	"suppress_unstable_features_warning":         {kind: configKindBool},
	"thread_unload_delay_secs":                   {kind: configKindInteger, expected: "u64"},
	"tool_output_token_limit":                    {kind: configKindInteger, expected: "usize"},
	"tool_suggest":                               {kind: configKindStruct, expected: "ToolSuggestConfig", fields: configToolSuggestFields},
	"tools":                                      {kind: configKindStruct, expected: "ToolsToml", fields: configToolsFields},
	"tui":                                        {kind: configKindStruct, expected: "Tui", fields: configTuiFields},
	"web_search":                                 {kind: configKindEnum, variants: []string{"disabled", "cached", "indexed", "live"}},
	"windows":                                    {kind: configKindStruct, expected: "WindowsToml", fields: configWindowsFields},
}
