package config

import "testing"

// The remaining ConfigToml subtables carry their own serde shapes; the want
// strings are the bodies Rust 0.166 prints under "failed to load bootstrap
// configuration", captured by running the real binary against an empty home.
func TestValidateRemainingConfigTables(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "auto review timeout is u32",
			toml: "[auto_review]\ntimeout_ms = -1\n",
			want: "invalid value: integer `-1`, expected u32\nin `auto_review.timeout_ms`",
		},
		{
			name: "auto review timeout overflow",
			toml: "[auto_review]\ntimeout_ms = 4294967296\n",
			want: "invalid value: integer `4294967296`, expected u32\nin `auto_review.timeout_ms`",
		},
		{
			name: "auto review circuit break action",
			toml: "[auto_review]\ncircuit_break_action = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `default` or `strict`\nin `auto_review.circuit_break_action`",
		},
		{
			name: "auto review history tokens are nonzero",
			toml: "[auto_review]\nconversation_history_max_output_tokens = 0\n",
			want: "invalid value: integer `0`, expected a nonzero usize\nin `auto_review.conversation_history_max_output_tokens`",
		},
		{
			name: "memories version is an enum",
			toml: "[memories]\nversion = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `v1` or `v2`\nin `memories.version`",
		},
		{
			name: "memories booleans",
			toml: "[memories]\ndual_write = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `memories.dual_write`",
		},
		{
			name: "memories consolidation count is unsigned",
			toml: "[memories]\nmax_raw_memories_for_consolidation = -1\n",
			want: "invalid value: integer `-1`, expected usize\nin `memories.max_raw_memories_for_consolidation`",
		},
		{
			name: "memories day window is i64",
			toml: "[memories]\nmax_unused_days = \"x\"\n",
			want: "invalid type: string \"x\", expected i64\nin `memories.max_unused_days`",
		},
		{
			name: "skills context tokens are nonzero",
			toml: "[skills]\nmax_context_tokens = -1\n",
			want: "invalid value: integer `-1`, expected a nonzero usize\nin `skills.max_context_tokens`",
		},
		{
			name: "skills config must be a sequence of structs",
			toml: "[skills]\nconfig = [5]\n",
			want: "invalid type: integer `5`, expected struct SkillConfig\nin `skills.config`",
		},
		{
			name: "skills entry requires enabled",
			toml: "[[skills.config]]\n",
			want: "missing field `enabled`\nin `skills.config`",
		},
		{
			name: "skills entry path is a path string",
			toml: "[[skills.config]]\npath = 1\n",
			want: "invalid type: integer `1`, expected path string\nin `skills.config.path`",
		},
		{
			name: "skills bundled is a struct",
			toml: "[skills]\nbundled = 5\n",
			want: "invalid type: integer `5`, expected struct BundledSkillsConfig\nin `skills.bundled`",
		},
		{
			name: "agents booleans",
			toml: "[agents]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `agents.enabled`",
		},
		{
			name: "agents depth is i32",
			toml: "[agents]\nmax_depth = 1.5\n",
			want: "invalid type: floating point `1.5`, expected i32\nin `agents.max_depth`",
		},
		{
			name: "agents depth overflow",
			toml: "[agents]\nmax_depth = 2147483648\n",
			want: "invalid value: integer `2147483648`, expected i32\nin `agents.max_depth`",
		},
		{
			name: "agents max threads reports the canonical field",
			toml: "[agents]\nmax_threads = \"x\"\n",
			want: "invalid type: string \"x\", expected usize\nin `agents.max_concurrent_threads_per_session`",
		},
		{
			name: "agents job runtime is u64",
			toml: "[agents]\njob_max_runtime_seconds = -1\n",
			want: "invalid value: integer `-1`, expected u64\nin `agents.job_max_runtime_seconds`",
		},
		{
			name: "agents flattened role reports the table",
			toml: "[agents.foo]\ndescription = 5\n",
			want: "invalid type: integer `5`, expected a string\nin `agents`",
		},
		{
			name: "agents flattened role must be a struct",
			toml: "[agents]\nroles = 5\n",
			want: "invalid type: integer `5`, expected struct AgentRoleToml\nin `agents`",
		},
		{
			name: "apps default entry keeps its own path",
			toml: "[apps._default]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `apps._default.enabled`",
		},
		{
			name: "apps flattened entry reports the table",
			toml: "[apps.foo]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `apps`",
		},
		{
			name: "apps tool map reports the table",
			toml: "[apps.foo]\ntools = { x = 5 }\n",
			want: "invalid type: integer `5`, expected struct AppToolConfig\nin `apps`",
		},
		{
			name: "apps approval mode is an enum",
			toml: "[apps.foo]\ndefault_tools_approval_mode = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `auto`, `prompt`, `writes`, `approve`\nin `apps`",
		},
		{
			name: "apps omitted surfaces are a sequence",
			toml: "[apps.foo]\nomit_tools_from = 5\n",
			want: "invalid type: integer `5`, expected a sequence\nin `apps`",
		},
		{
			name: "feedback booleans",
			toml: "[feedback]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `feedback.enabled`",
		},
		{
			name: "goals token budget is nonzero",
			toml: "[goals]\nmax_goal_token_budget = 0\n",
			want: "invalid value: integer `0`, expected a nonzero u64\nin `goals.max_goal_token_budget`",
		},
		{
			name: "cloud skills toggle is a struct",
			toml: "[cloud]\nskills = 5\n",
			want: "invalid type: integer `5`, expected struct FeatureToggleToml\nin `cloud.skills`",
		},
		{
			name: "cloud ignores an mcp toggle",
			toml: "[cloud]\nmcp = 5\n",
			want: "",
		},
		{
			name: "orchestrator mcp toggle is a struct",
			toml: "[orchestrator]\nmcp = 5\n",
			want: "invalid type: integer `5`, expected struct FeatureToggleToml\nin `orchestrator.mcp`",
		},
		{
			name: "orchestrator toggle field type",
			toml: "[orchestrator.skills]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `orchestrator.skills.enabled`",
		},
		{
			name: "browser use history access",
			toml: "[browser_use]\nallow_history_access = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `browser_use.allow_history_access`",
		},
		{
			name: "browser use origin policy enum",
			toml: "[browser_use.default_origin_policy]\naccess = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `allow` or `deny`\nin `browser_use.default_origin_policy.access`",
		},
		{
			name: "browser use origin map entries",
			toml: "[browser_use.origins.x]\naccess = 5\n",
			want: "invalid type: unit variant, expected string only\nin `browser_use.origins.x.access`",
		},
		{
			name: "computer use default access needs a string",
			toml: "[computer_use]\ndefault_app_access = 5\n",
			want: "invalid type: unit variant, expected string only\nin `computer_use.default_app_access`",
		},
		{
			name: "computer use macos bundle ids",
			toml: "[computer_use.macos]\nbundle_ids = { a = 5 }\n",
			want: "invalid type: unit variant, expected string only\nin `computer_use.macos.bundle_ids.a`",
		},
		{
			name: "computer use windows exe requires publisher",
			toml: "[computer_use.windows]\nexes = [{}]\n",
			want: "missing field `publisher_name`\nin `computer_use.windows.exes`",
		},
		{
			name: "audio microphone channels are numbers",
			toml: "[audio]\nmicrophone_channel = \"both\"\n",
			want: "data did not match any variant of untagged enum MicrophoneChannels\nin `audio.microphone_channel`",
		},
		{
			name: "audio microphone channel must be nonzero",
			toml: "[audio]\nmicrophone_channel = [0]\n",
			want: "data did not match any variant of untagged enum MicrophoneChannels\nin `audio.microphone_channel`",
		},
		{
			name: "audio speaker is a string",
			toml: "[audio]\nspeaker = 5\n",
			want: "invalid type: integer `5`, expected a string\nin `audio.speaker`",
		},
		{
			name: "sandbox writable roots are paths",
			toml: "[sandbox_workspace_write]\nwritable_roots = [1]\n",
			want: "invalid type: integer `1`, expected path string\nin `sandbox_workspace_write.writable_roots`",
		},
		{
			name: "analytics booleans",
			toml: "[analytics]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `analytics.enabled`",
		},
		{
			name: "notice model migrations is a string map",
			toml: "[notice]\nmodel_migrations = { a = 5 }\n",
			want: "invalid type: integer `5`, expected a string\nin `notice.model_migrations.a`",
		},
		{
			name: "notice migration prompts descend",
			toml: "[notice.external_config_migration_prompts]\nprojects = { a = 5 }\n",
			want: "invalid type: integer `5`, expected a boolean\nin `notice.external_config_migration_prompts.projects.a`",
		},
		{
			name: "otel struct variant needs its table",
			toml: "[otel]\nexporter = \"otlp-http\"\n",
			want: "invalid type: unit variant, expected struct variant\nin `otel.exporter`",
		},
		{
			name: "otel struct variant requires endpoint",
			toml: "[otel]\nexporter = { \"otlp-http\" = {} }\n",
			want: "missing field `endpoint`\nin `otel.exporter`",
		},
		{
			name: "otel struct variant payload type",
			toml: "[otel]\nexporter = { \"otlp-http\" = 5 }\n",
			want: "invalid type: integer `5`, expected struct variant OtelExporterKind::OtlpHttp\nin `otel.exporter`",
		},
		{
			name: "otel unknown exporter lists variants",
			toml: "[otel]\nexporter = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `none`, `statsig`, `otlp-http`, `otlp-grpc`\nin `otel.exporter`",
		},
		{
			name: "otel tracestate is a map of maps",
			toml: "[otel]\ntracestate = { a = 5 }\n",
			want: "invalid type: integer `5`, expected a map\nin `otel.tracestate.a`",
		},
		{
			name: "otel ignores an unknown field",
			toml: "[otel]\nlog_sse_deltas = 5\n",
			want: "",
		},
		{
			name: "realtime version is an enum",
			toml: "[realtime]\nversion = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `v1`, `v2`, `v3`\nin `realtime.version`",
		},
		{
			name: "realtime transport is an enum",
			toml: "[realtime]\ntransport = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `webrtc` or `websocket`\nin `realtime.transport`",
		},
		{
			name: "tools web search is bool or struct",
			toml: "[tools]\nweb_search = \"disabled\"\n",
			want: "data did not match any variant of untagged enum WebSearchToolConfigInput\nin `tools.web_search`",
		},
		{
			name: "tools web search context size",
			toml: "[tools]\nweb_search = { context_size = \"bogus\" }\n",
			want: "data did not match any variant of untagged enum WebSearchToolConfigInput\nin `tools.web_search`",
		},
		{
			name: "tools update plan descends",
			toml: "[tools.update_plan]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `tools.update_plan.enabled`",
		},
		{
			name: "tool suggest discoverables are structs",
			toml: "[tool_suggest]\ndiscoverables = [5]\n",
			want: "invalid type: integer `5`, expected struct ToolSuggestDiscoverable\nin `tool_suggest.discoverables`",
		},
		{
			name: "tool suggest entries require an id",
			toml: "[[tool_suggest.discoverables]]\ntype = \"connector\"\n",
			want: "missing field `id`\nin `tool_suggest.discoverables`",
		},
		{
			name: "tool suggest entry type is an enum",
			toml: "[[tool_suggest.discoverables]]\ntype = \"app\"\nid = \"x\"\n",
			want: "unknown variant `app`, expected `connector` or `plugin`\nin `tool_suggest.discoverables.type`",
		},
		{
			name: "ghost snapshot thresholds are i64",
			toml: "[ghost_snapshot]\nignore_large_untracked_files = \"x\"\n",
			want: "invalid type: string \"x\", expected i64\nin `ghost_snapshot.ignore_large_untracked_files`",
		},
		{
			name: "permissions entries report the table",
			toml: "[permissions.x]\ndescription = 5\n",
			want: "invalid type: integer `5`, expected a string\nin `permissions`",
		},
		{
			name: "permissions network mode is an enum",
			toml: "[permissions.x.network]\nmode = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `limited` or `full`\nin `permissions`",
		},
		{
			name: "permissions filesystem entries are untagged",
			toml: "[permissions.x.filesystem]\nfoo = 5\n",
			want: "data did not match any variant of untagged enum FilesystemPermissionToml\nin `permissions`",
		},
		{
			name: "permissions network is a struct",
			toml: "[permissions.x]\nnetwork = 5\n",
			want: "invalid type: integer `5`, expected struct NetworkToml\nin `permissions`",
		},
		{
			name: "hooks events are sequences",
			toml: "[hooks]\nPreToolUse = 5\n",
			want: "invalid type: integer `5`, expected a sequence\nin `hooks`",
		},
		{
			name: "hooks matcher reports the table",
			toml: "[hooks]\nPreToolUse = [{ matcher = 5 }]\n",
			want: "invalid type: integer `5`, expected a string\nin `hooks`",
		},
		{
			name: "hooks handlers require a type",
			toml: "[hooks]\nPreToolUse = [{ hooks = [{}] }]\n",
			want: "missing field `type`\nin `hooks`",
		},
		{
			name: "hooks state descends",
			toml: "[hooks.state.x]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `hooks.state.x.enabled`",
		},
		{
			name: "plugins default entry keeps its own path",
			toml: "[plugins._default]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `plugins._default.enabled`",
		},
		{
			name: "plugins entries report the table",
			toml: "[plugins.foo]\nenabled = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `plugins`",
		},
		{
			name: "plugins mcp servers are structs",
			toml: "[plugins.foo]\nmcp_servers = { x = 5 }\n",
			want: "invalid type: integer `5`, expected struct PluginMcpServerConfig\nin `plugins`",
		},
		{
			name: "managed auth requires idp",
			toml: "[mcp_enterprise_managed_auth]\n",
			want: "missing field `idp`\nin `mcp_enterprise_managed_auth`",
		},
		{
			name: "managed auth idp is a struct",
			toml: "[mcp_enterprise_managed_auth]\nidp = 5\n",
			want: "invalid type: integer `5`, expected struct McpServerIdpOAuthConfig\nin `mcp_enterprise_managed_auth.idp`",
		},
		{
			name: "managed auth idp requires a client id",
			toml: "[mcp_enterprise_managed_auth.idp]\nissuer = \"x\"\n",
			want: "missing field `client_id`\nin `mcp_enterprise_managed_auth.idp`",
		},
		{
			name: "managed auth token method is an enum",
			toml: "[mcp_enterprise_managed_auth.idp]\nissuer = \"x\"\nclient_id = \"y\"\ntoken_endpoint_auth_method = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `client_secret_basic` or `client_secret_post`\nin `mcp_enterprise_managed_auth.idp.token_endpoint_auth_method`",
		},
		{
			name: "microphone channels accept a number",
			toml: "[audio]\nmicrophone_channel = 1\n",
			want: "",
		},
		{
			name: "web search accepts a boolean",
			toml: "[tools]\nweb_search = true\n",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ParseConfigDocumentValues([]byte(tc.toml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = ValidateConfigDocumentValues(values, &ConfigDocumentLocator{Path: `C:\home\config.toml`, Text: tc.toml})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
