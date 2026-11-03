# Protocol field matrix — Rust app-server-protocol v2 vs Go codex_go

- Date: 2026-11-02
- Rust source (read-only): `D:\qax\reagent\dev\git\codex\codex-rs\app-server-protocol\src\protocol\v2\`
- Go source: worktree `D:\qax\reagent\dev\codex_go_lane_protofield` (branch `align/protofield`, base `b6db8edc`)
- Every Rust field of every in-scope struct is listed; no sampling, no "rest is identical" rows.

Legend — `wire`: 发 = serialized by the Go client, 解 = decoded by the Go server, 不发 = never on the wire
(`json:"-"` or only in-process). `结论`: 对齐 / Go 缺失 / Go 多发 / 语义差异 / 不适用.

Note on marshaling: the remote client sends `turn.TurnStartParams` **by value**, so
`encoding/json` uses the struct tags (the pointer `MarshalJSON` is not used on that path).
Rows that differ between the two encodings are called out explicitly.

## 1. TurnStartParams (turn.rs:174-322, 28 fields)

| Rust 字段 | Rust 类型 | serde 属性 | Go 字段 | Go 类型 / tag | wire | 结论 |
|---|---|---|---|---|---|---|
| thread_id | String | rename_all=camelCase | ThreadID | string `json:"threadId"` | 发+解 | 对齐 |
| disabled_plugin_ids | Option<Vec<String>> | — | DisabledPluginIDs | *[]string `omitempty` | 发 | 对齐 |
| client_user_message_id | Option<String> | — | ClientUserMessageID | string `omitempty` | 发 | 对齐 |
| input | Vec<UserInput> | — | Input | []TurnUserInput `json:"input"` | 发+解 | 对齐 |
| turn_trigger | Option<String> | — | TurnTrigger | string `omitempty` | 发 | 对齐 |
| parent_turn_id | Option<String> | — | ParentTurnID | string `omitempty` | 发 | 对齐（空串≈None） |
| root_turn_id | Option<String> | — | RootTurnID | string `omitempty` | 发 | 对齐 |
| tool_output | Option<Box<TurnToolOutput>> | — | ToolOutput | *TurnToolOutput `omitempty` | 发 | 对齐 |
| responsesapi_client_metadata | Option<HashMap<String,String>> | experimental | ResponsesAPIMetadata | map[string]string `omitempty` | 发(值)/不发(指针) | 语义差异 |
| responsesapi_client_metadata_patch | Option<HashMap<String,String>> | experimental | ResponsesAPIMetadataPatch | map[string]string `omitempty` | 发(值)/不发(指针) | 语义差异 |
| additional_context | Option<HashMap<String, AdditionalContextEntry>> | experimental | AdditionalContext | map `omitempty` | 发 | 对齐 |
| environments | Option<Vec<TurnEnvironmentParams>> | experimental | Environments | []map[string]any `omitempty` | 发 | 对齐（Go 宽松） |
| cwd | Option<PathBuf> | — | CWD | string `omitempty` | 发 | 对齐 |
| runtime_workspace_roots | Option<Vec<AbsolutePathBuf>> | experimental | RuntimeWorkspaceRoots | []string `omitempty` | 发(值)/不发(指针) | 语义差异 |
| approval_policy | Option<AskForApproval> | experimental(nested) | ApprovalPolicy | any `omitempty` | 发 | 对齐（Go 宽松） |
| approvals_reviewer | Option<ApprovalsReviewer> | — | ApprovalsReviewer | *string `omitempty` | 发 | 对齐 |
| sandbox_policy | Option<SandboxPolicy> | — | SandboxPolicy | any `omitempty` | 发（Client 且无命名 profile） | 语义差异 → 已修 aeb8f824 |
| permissions | Option<String> | experimental | Permissions | *string `omitempty` | 发（本次修复） | Go 缺失 → 已修 aeb8f824 |
| model | Option<String> | — | Model | string `omitempty` | 发 | 对齐 |
| service_tier | Option<Option<String>> | double option, skip if None | ServiceTier + ServiceTierSet | *string `omitempty`; Set `json:"-"` | 发（非空）；无法发 null | 语义差异 |
| service_tier_for_turn | Option<String> | — | ServiceTierForTurn | *string `omitempty` | 发 | 对齐 |
| effort | Option<ReasoningEffort> | — | Effort | *string `omitempty` | 发 | 对齐 |
| summary | Option<ReasoningSummary> | — | Summary | *string `omitempty` | 发 | 对齐 |
| personality | Option<Personality> | deprecated（无 skip） | Personality + PersonalitySet | *string `omitempty`; Set `json:"-"` | 发 | 对齐 |
| output_schema | Option<JsonValue> | — | OutputSchema | any `omitempty` | 发 | 对齐 |
| collaboration_mode | Option<CollaborationMode> | experimental | CollaborationMode | map[string]any `omitempty` | 发 | 对齐（Go 宽松） |
| multi_agent_mode | Option<MultiAgentMode> | experimental, deprecated | MultiAgentMode | *string `omitempty` | 发(值)/不发(指针) | 语义差异 |
| cyber_access_program | Option<CyberAccessProgram> | experimental | CyberAccessProgram | *CyberAccessProgram `omitempty` | 发 | 对齐 |

Go-only fields that the value encoder also writes (Rust has no such field):

| Go 字段 | Go 类型 / tag | wire | 结论 |
|---|---|---|---|
| Prompt | string `json:"prompt,omitempty"` | 发（值）；指针 marshal 不发 | Go 多发 |
| Originator | string `prompt,omitempty` | 发（值） | Go 多发 |
| Config | map[string]any `omitempty` | 发（值）；Rust turn/start 无该字段 | Go 多发 |
| BaseInstructions | *string `omitempty` | 发（值） | Go 多发 |
| DeveloperInstructions | *string `omitempty` | 发（值） | Go 多发 |
| DynamicTools | []DynamicToolSpec `omitempty` | 发（值，若设置） | Go 多发 |
| UserItemUUID / PersistedPermissionProfile / CoreCyberAccessProgram / InitiatingAgentPath / AdditionalInputItems / AutomaticWake / Trace / ServiceTierSet / PersonalitySet | — | 不发（`json:"-"`） | 不适用（进程内） |

## 2. TurnStartResponse (turn.rs:324-330, 1 field)

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| turn | Turn | — | Turn | TurnRecord `json:"turn"` | 解 | 语义差异（见下） |

Nested `Turn` (thread_data.rs:387-411) vs Go `turn.TurnRecord` (turn/api.go:760-771):

| Rust 字段 | Go 字段 | wire | 结论 |
|---|---|---|---|
| id | ID string | 解 | 对齐 |
| root_turn_id Option<String> | RootTurnID string `omitempty` | 解 | 对齐 |
| items Vec<ThreadItem> | —（Go MarshalJSON 停机输出 `items: []`） | 解 | Go 缺失 |
| items_view TurnItemsView | —（固定 `notLoaded`） | 解 | Go 缺失 |
| status TurnStatus | Status string | 解 | 对齐 |
| error Option<TurnError> | —（固定 null） | 解 | Go 缺失 |
| started_at Option<i64> | StartedAt int64 | 解 | 语义差异（Go 空值为 0） |
| completed_at Option<i64> | CompletedAt *int64 | 解 | 对齐 |
| duration_ms Option<i64> | —（固定省略） | 解 | Go 缺失 |
| — | ThreadID / Prompt / Inputs | 发 | Go 多发（Rust Turn 无这些字段） |

## 3. ThreadStartParams (thread.rs:62-..., 27 fields)

| Rust 字段 | Rust 类型 | serde 属性 | Go 字段 | Go 类型 / tag | wire | 结论 |
|---|---|---|---|---|---|---|
| model | Option<String> | — | Model | string `omitempty` | 发 | 对齐 |
| model_provider | Option<String> | — | ModelProvider | string `omitempty` | 发 | 对齐 |
| allow_provider_model_fallback | bool | skip if false | AllowProviderModelFallback | bool `omitempty` | 发 | 对齐 |
| service_tier | Option<Option<String>> | double option | ServiceTier + ServiceTierSet | *string `omitempty`; Set `-` | 发（非空） | 语义差异 |
| cwd | Option<String> | — | CWD | string `omitempty` | 发 | 对齐 |
| runtime_workspace_roots | Option<Vec<AbsolutePathBuf>> | experimental | RuntimeWorkspaceRoots | []string `omitempty` | 发 | 对齐 |
| approval_policy | Option<AskForApproval> | experimental(nested) | ApprovalPolicy | any `omitempty` | 发 | 对齐（宽松） |
| approvals_reviewer | Option<ApprovalsReviewer> | — | ApprovalsReviewer | *string `omitempty` | 发 | 对齐 |
| sandbox | Option<SandboxMode> | — | Sandbox | any `omitempty` | 发 | 对齐（宽松） |
| permissions | Option<String> | experimental | Permissions | *string `omitempty` | 发（Go 不设） | 对齐（Rust 仅非 Remote 设置，Go 走 Remote 故不发） |
| config | Option<HashMap<String, JsonValue>> | — | Config | map[string]any `omitempty` | 发 | 对齐 |
| service_name | Option<String> | — | ServiceName | *string `omitempty` | 发 | 对齐 |
| base_instructions | Option<String> | — | BaseInstructions | *string `omitempty` | 发 | 对齐 |
| developer_instructions | Option<String> | — | DeveloperInstructions | *string `omitempty` | 发 | 对齐 |
| personality | Option<Personality> | deprecated | Personality | *string `omitempty` | 发 | 对齐 |
| multi_agent_mode | Option<MultiAgentMode> | experimental, deprecated | MultiAgentMode | MultiAgentMode `omitempty` | 发 | 对齐 |
| ephemeral | Option<bool> | — | Ephemeral | bool `omitempty` | 发 | 对齐 |
| history_mode | Option<ThreadHistoryMode> | experimental | HistoryMode | ThreadHistoryMode `omitempty` | 发 | 对齐 |
| session_start_source | Option<ThreadStartSource> | — | SessionStartSource | *string `omitempty` | 发 | 对齐 |
| thread_source | Option<ThreadSource> | — | ThreadSource | *ThreadSource `omitempty` | 发 | 对齐 |
| project_id | Option<String> | experimental | ProjectID | *string `omitempty` | 发 | 对齐 |
| daybreak_enabled | Option<bool> | experimental | DaybreakEnabled | *bool `omitempty` | 发 | 对齐 |
| environments | Option<Vec<TurnEnvironmentParams>> | experimental | Environments | []map[string]any `omitempty` | 发 | 对齐（宽松） |
| dynamic_tools | Option<Vec<DynamicToolSpec>> | experimental + custom deser | DynamicTools | []json.RawMessage `omitempty` | 发 | 对齐 |
| selected_capability_roots | Option<Vec<SelectedCapabilityRoot>> | experimental | SelectedCapabilityRoots | []SelectedCapabilityRoot `omitempty` | 发 | 对齐 |
| mock_experimental_field | Option<String> | experimental(test-only) | — | — | 不发 | 不适用 |
| experimental_raw_events | bool | skip if false | ExperimentalRawEvents | bool `omitempty` | 发 | 对齐 |

Go-only: `Prompt json:"prompt,omitempty"`, `AllowedTools *[]string` → Go 多发.

## 4. ThreadResumeParams (thread.rs:354-..., 18 fields)

| Rust 字段 | Rust 类型 | serde 属性 | Go 字段 | Go 类型 / tag | wire | 结论 |
|---|---|---|---|---|---|---|
| thread_id | String | — | ThreadID | string `json:"threadId"` | 发+解 | 对齐 |
| history | Option<Vec<ResponseItem>> | experimental | History | []ThreadResumeHistoryItem `omitempty` + HistorySet `-` | 发 | 语义差异（Go 自定义条目类型） |
| path | Option<PathBuf> | experimental, empty→None | Path | *string `omitempty` | 发 | 对齐 |
| model | Option<String> | — | Model | *string `omitempty` | 发 | 对齐 |
| model_provider | Option<String> | — | ModelProvider | *string `omitempty` | 发 | 对齐 |
| service_tier | Option<Option<String>> | double option | ServiceTier + ServiceTierSet | *string `omitempty`; Set `-` | 发（非空） | 语义差异 |
| cwd | Option<String> | — | CWD | *string `omitempty` | 发 | 对齐 |
| runtime_workspace_roots | Option<Vec<AbsolutePathBuf>> | experimental | RuntimeWorkspaceRoots | []string `omitempty` | 发 | 对齐 |
| approval_policy | Option<AskForApproval> | experimental(nested) | ApprovalPolicy | any `omitempty` | 发 | 对齐（宽松） |
| approvals_reviewer | Option<ApprovalsReviewer> | — | ApprovalsReviewer | *string `omitempty` | 发 | 对齐 |
| sandbox | Option<SandboxMode> | — | Sandbox | any `omitempty` | 发 | 对齐（宽松） |
| permissions | Option<String> | experimental | Permissions | *string `omitempty` | 发 | 对齐 |
| config | Option<HashMap<String, Value>> | — | Config | map[string]any `omitempty` | 发 | 对齐 |
| base_instructions | Option<String> | — | BaseInstructions | *string `omitempty` | 发 | 对齐 |
| developer_instructions | Option<String> | — | DeveloperInstructions | *string `omitempty` | 发 | 对齐 |
| personality | Option<Personality> | deprecated | Personality | *string `omitempty` | 发 | 对齐 |
| exclude_turns | bool | skip if false | ExcludeTurns | bool `omitempty` | 发 | 对齐 |
| initial_turns_page | Option<ThreadResumeInitialTurnsPageParams> | experimental | InitialTurnsPage | *ThreadInitialPageParams `omitempty` | 发 | 对齐 |

Go-only: `ClientName`, `AllowedTools`, `HistorySet(-)` → ClientName/AllowedTools 为 Go 多发.

## 5. ThreadForkParams (thread.rs:544-..., 23 fields)

| Rust 字段 | Rust 类型 | serde 属性 | Go 字段 | Go 类型 / tag | wire | 结论 |
|---|---|---|---|---|---|---|
| thread_id | String | — | ThreadID | string `json:"threadId"` | 发 | 对齐 |
| experimental_prediction_mode | bool | skip if false | ExperimentalPredictionMode | bool `omitempty` | 发 | 对齐 |
| experimental_disable_prediction_mcp | bool | skip if false | ExperimentalDisablePredictionMcp | bool `omitempty` | 发 | 对齐 |
| last_turn_id | Option<String> | — | LastTurnID | string `omitempty` | 发 | 对齐（空串≈None） |
| before_turn_id | Option<String> | experimental | BeforeTurnID | string `omitempty` | 发 | 对齐 |
| path | Option<PathBuf> | experimental, empty→None | Path | *string `omitempty` | 发 | 对齐 |
| model | Option<String> | — | Model | *string `omitempty` | 发 | 对齐 |
| model_provider | Option<String> | — | ModelProvider | *string `omitempty` | 发 | 对齐 |
| service_tier | Option<Option<String>> | double option | ServiceTier + ServiceTierSet | *string `omitempty`; Set `-` | 发（非空） | 语义差异 |
| cwd | Option<String> | — | CWD | *string `omitempty` | 发 | 对齐 |
| runtime_workspace_roots | Option<Vec<AbsolutePathBuf>> | experimental | RuntimeWorkspaceRoots | []string `omitempty` | 发 | 对齐 |
| approval_policy | Option<AskForApproval> | experimental(nested) | ApprovalPolicy | any `omitempty` | 发 | 对齐（宽松） |
| approvals_reviewer | Option<ApprovalsReviewer> | — | ApprovalsReviewer | *string `omitempty` | 发 | 对齐 |
| sandbox | Option<SandboxMode> | — | Sandbox | any `omitempty` | 发 | 对齐（宽松） |
| permissions | Option<String> | experimental | Permissions | *string `omitempty` | 发 | 对齐 |
| config | Option<HashMap<String, Value>> | — | Config | map[string]any `omitempty` | 发 | 对齐 |
| base_instructions | Option<String> | — | BaseInstructions | *string `omitempty` | 发 | 对齐 |
| developer_instructions | Option<String> | — | DeveloperInstructions | *string `omitempty` | 发 | 对齐 |
| ephemeral | bool | skip if false | Ephemeral | bool `omitempty` | 发 | 对齐 |
| attach_to_source | bool | skip if false | AttachToSource | bool `omitempty` | 发 | 对齐 |
| thread_source | Option<ThreadSource> | — | ThreadSource | *ThreadSource `omitempty` | 发 | 对齐 |
| exclude_turns | bool | skip if false | ExcludeTurns | bool `omitempty` | 发 | 对齐 |
| defer_goal_continuation | bool | skip if false | DeferGoalContinuation | bool `omitempty` | 发 | 对齐 |

Go-only: `ParentItemID`, `LastN`, `HistoryMode`, `ServiceTierSet(-)` → 前三个为 Go 多发（Rust 用 lastTurnId/beforeTurnId 表达剪裁）.

## 6. ThreadReadParams (thread.rs:1737-..., 2 fields)

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| thread_id | String | — | ThreadID | string `json:"threadId"` | 发+解 | 对齐 |
| include_turns | bool | skip if false | IncludeTurns | bool `omitempty` | 发 | 对齐 |

## 7. Config read / requirements read

ConfigReadParams (config.rs:390-401, 2 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| include_layers | bool | skip if false | IncludeLayers | bool `omitempty` | 发 | 对齐 |
| cwd | Option<String> | — | CWD | *string `omitempty` | 发 | 对齐 |

ConfigReadResponse (config.rs:403-..., 3 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| config | Config | experimental(nested) | Config | map[string]any `json:"config"` | 解 | 对齐（Go 宽松） |
| origins | HashMap<String, ConfigLayerMetadata> | — | Origins | map[string]LayerMetadata `json:"origins"` | 解 | 对齐 |
| layers | Option<Vec<ConfigLayer>> | skip if none | Layers | []Layer `omitempty` | 解 | 对齐 |

ConfigRequirementsReadResponse (config.rs:838-..., 2 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| supports_independent_speed_modes | Option<bool> | default | SupportsIndependentSpeedModes | *bool `json:"supportsIndependentSpeedModes"` | 解 | 对齐（两侧都会发 null） |
| requirements | Option<ConfigRequirements> | experimental(nested) | Requirements | *ConfigRequirements `json:"requirements"` | 解 | 对齐 |

## 8. Permission profile requests and responses

PermissionProfileListParams (permissions.rs:391-404, 3 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| cursor | Option<String> | — | Cursor | *string `omitempty` | 发 | 对齐 |
| limit | Option<u32> | — | Limit | *int `omitempty` | 发 | 语义差异（Go 可发负数，Rust 仅 u32） |
| cwd | Option<String> | — | CWD | *string `omitempty` | 发 | 对齐 |

PermissionProfileSummary (permissions.rs:406-416, 3 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| id | String | — | ID | string `json:"id"` | 解/发 | 对齐 |
| description | Option<String> | — | Description | string `omitempty` | 解/发 | 对齐 |
| allowed | bool | — | Allowed | bool `json:"allowed"` | 解/发 | 对齐 |
| — | — | — | SandboxMode | string `omitempty` | 发 | Go 多发 |
| — | — | — | Network | string `omitempty` | 发 | Go 多发 |

PermissionProfileListResponse (permissions.rs:418-426, 2 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| data | Vec<PermissionProfileSummary> | — | Data | []PermissionProfileSummary `json:"data"` | 解/发 | 对齐 |
| next_cursor | Option<String> | — | NextCursor | *string `json:"nextCursor"` | 解/发 | 对齐 |

ActivePermissionProfile (permissions.rs:428-..., 2 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| id | String | — | ID | string（自定义 MarshalJSON → `id`） | 解/发 | 对齐 |
| extends | Option<String> | default | Extends | string（自定义 MarshalJSON → `extends`） | 解/发 | 对齐 |

AdditionalPermissionProfile (permissions.rs:472-..., 2 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| network | Option<AdditionalNetworkPermissions> | — | Network | *bool | 不发 | 语义差异（Go 内部布尔简写） |
| file_system | Option<AdditionalFileSystemPermissions> | — | FileSystem + ReadFileSystem | []string | 不发 | 语义差异（Go 内部扁平化） |

GrantedPermissionProfile (permissions.rs:505-..., 2 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| network | Option<AdditionalNetworkPermissions> | skip if none | Network | *AdditionalNetworkPermissions `omitempty` | 发/解 | 对齐 |
| file_system | Option<AdditionalFileSystemPermissions> | skip if none | FileSystem | *AdditionalFileSystemPermissions `omitempty` | 发/解 | 对齐 |

PermissionsRequestApprovalParams (permissions.rs:773-..., 8 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| thread_id | String | — | ThreadID | string `json:"threadId"` | 解 | 对齐 |
| turn_id | String | — | TurnID | string `json:"turnId"` | 解 | 对齐 |
| item_id | String | — | ItemID | string `json:"itemId"` | 解 | 对齐 |
| environment_id | Option<String> | default | EnvironmentID | *string `json:"environmentId"` | 解 | 对齐 |
| started_at_ms | i64 | — | StartedAtMS | uint64 `json:"startedAtMs"` | 解 | 语义差异（Go 无符号） |
| cwd | LegacyAppPathString | — | CWD | string `json:"cwd"` | 解 | 语义差异（Rust 支持 legacy 路径联合体） |
| reason | Option<String> | — | Reason | *string `json:"reason"` | 解 | 对齐 |
| permissions | RequestPermissionProfile | — | Permissions | map[string]any `json:"permissions"` | 解 | 语义差异（Go 解析为 map 后再转换） |

PermissionsRequestApprovalResponse (permissions.rs:799-..., 3 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| permissions | GrantedPermissionProfile | — | Permissions | *GrantedPermissionProfile `json:"permissions"` | 发 | 对齐 |
| scope | PermissionGrantScope | default | Scope | PermissionGrantScope `json:"scope"` | 发 | 对齐 |
| strict_auto_review | Option<bool> | default, skip if none | StrictAutoReview | *bool `omitempty` | 发 | 对齐 |

RequestPermissionProfile (permissions.rs:217-226, 2 fields):

| Rust 字段 | Rust 类型 | serde | Go 字段 | Go 类型 | wire | 结论 |
|---|---|---|---|---|---|---|
| network | Option<AdditionalNetworkPermissions> | deny_unknown_fields | Network | *AdditionalNetworkPermissions `json:"network"` | 解 | 对齐（严格性差异见 proto_alignment） |
| file_system | Option<AdditionalFileSystemPermissions> | deny_unknown_fields | FileSystem | *AdditionalFileSystemPermissions `json:"fileSystem"` | 解 | 对齐（同上） |

## 9. NewConversationParams / SendUserTurnParams

Not present anywhere in `codex-rs/app-server-protocol` or `codex-rs/app-server`
(`rg "NewConversationParams|SendUserTurnParams"` → 0 hits): those are pre-v2 names. Their
modern equivalents are `ThreadStartParams` (section 3) and `TurnStartParams` (section 1),
which are covered field by field above. 结论：不适用.