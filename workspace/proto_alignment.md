# Proto-field alignment — align/protofield

- Date: 2026-11-02
- Worktree: `D:\qax\reagent\dev\codex_go_lane_protofield`, branch `align/protofield`, base `b6db8edc`
- Rust reference (read-only): `D:\qax\reagent\dev\git\codex\codex-rs\app-server-protocol\src\protocol\v2\`
- Matrix: `workspace/proto_field_matrix.md` (every field of every in-scope struct, no sampling)

## 1. Matrix statistics

152 field-level rows were checked, covering **133 Rust fields** in 18 structs plus 19 rows for
nested/Go-only fields (`Turn` payload, Go-only wire fields, `PermissionProfileSummary` extras).

| 结论 | 行数 | 说明 |
|---|---|---|
| 对齐 | 118 | same wire name/type semantics (Go 宽松类型：`any`/`map` 视为对齐) |
| 语义差异 | 18 | 见 §3，多数是 `Option<Option<_>>`、互斥字段、指针/值两种编码不一致 |
| Go 多发 | 9 | Go 发送 Rust v2 不认识的字段（`config`/`dynamicTools`/`prompt`/`allowedTools`/`parentItemId`/`lastN`/`historyMode`/`sandboxMode`/`network`） |
| Go 缺失 | 5 | `turn/start.permissions`（S，已修）+ `Turn.items`/`itemsView`/`error`/`durationMs`（M） |
| 不适用 | 2 | `mockExperimentalField`（Rust 测试专用）、Go 的进程内字段 |

Section coverage: TurnStartParams 28, TurnStartResponse 1 (+10 nested `Turn` rows),
ThreadStartParams 27, ThreadResumeParams 18, ThreadForkParams 23, ThreadReadParams 2,
ConfigReadParams 2, ConfigReadResponse 3, ConfigRequirementsReadResponse 2,
PermissionProfileListParams 3, PermissionProfileSummary 3 (+2 Go-only), PermissionProfileListResponse 2,
ActivePermissionProfile 2, AdditionalPermissionProfile 2, GrantedPermissionProfile 2,
PermissionsRequestApprovalParams 8, PermissionsRequestApprovalResponse 3, RequestPermissionProfile 2.
`NewConversationParams` / `SendUserTurnParams` do not exist in current Rust (`rg` → 0 hits); their
equivalents are ThreadStartParams + TurnStartParams, both covered.

## 2. S-level fix landed

### S1 — client turn/start never sent the active permission profile

- Rust: `app_server_session.rs:1620-1634` sets `params.permissions` only in the
  `ThreadSettingsOwner::Client` branch, from `turn_permissions_overrides(...)`; the
  `ActiveProfile` arm returns `(sandbox_policy: None, permissions: Some(id))`
  (`app_server_session.rs:2294-2300`), i.e. **the two fields are mutually exclusive**.
  The choice comes from `app/turn_permissions_override_from_config` (`app/thread_routing.rs:938-941`).
- Go before: `app/remote_tui.go` copied `state.Sandbox` (which `tui/tea/thread_settings.go` had
  already overwritten with the *profile id*) into `sandboxPolicy` and never set `permissions`.
  A thread using a named profile therefore sent `sandboxPolicy: "trusted"` — an unknown sandbox
  mode — and the named profile never reached the server.
- Fix (`aeb8f824`):
  - `tui/state.go` gains `ActivePermissionProfile` (kept separate from `Sandbox`);
  - `tui/tea/thread_settings.go` populates it from `settings.ActivePermissionProfile` and clears it
    when the server reports a legacy sandbox policy;
  - `app/remote_tui.go` prefers `permissions` and omits `sandboxPolicy` when a profile is active;
    server-owned (cloud) turns still send neither.
- Evidence (real wire, not struct fields):
  - `TestClientTurnStartSendsActivePermissionProfileLikeRust` — intercepts the turn/start JSON,
    asserts `permissions == "trusted"` and **no** `sandboxPolicy`.
  - `TestClientTurnStartKeepsSandboxPolicyWithoutProfileLikeRust` — without a profile the legacy
    `sandboxPolicy` still travels and `permissions` is absent.
  - `TestCloudTurnStartIgnoresActivePermissionProfileLikeRust` — cloud sends neither field.
  - `TestTurnStartParamsSerializePermissionAndSandboxLikeRust` — positive control: the params type
    really does serialize both keys, so the absence assertions cannot pass vacuously.

No second S-level gap was found: every other "Go 缺失" row is either test-only or a response payload
that Go never reads (`Turn.items`/`itemsView`/`error`/`durationMs`), and every other request field
Rust sends for the corresponding mode has a Go counterpart (see §3 and §5).

## 3. Semantic differences (why they are not all S)

1. `service_tier: Option<Option<String>>` in Turn/ThreadStart/Resume/Fork. Rust can send
   `"serviceTier": null` to clear a sticky tier; Go's `*string` + `omitempty` can only omit. Go
   decodes the distinction into `ServiceTierSet` but never emits it. M-level: needs a nullable
   wrapper (or custom marshal) across four params types plus the TUI's "default tier" path.
2. `sandbox_policy` vs `permissions` exclusivity — fixed (S1).
3. `responsesapiClientMetadata`/`Patch`, `runtimeWorkspaceRoots`, `multiAgentMode` are emitted by
   the value encoder (the real wire) but dropped by the pointer `MarshalJSON`
   (`turn/api.go:393-435`), and `turn/api_test.go:508` pins that older shape. Two encodings of one
   type disagree — M-level, must be fixed together with that test.
4. `PermissionsRequestApprovalParams.started_at_ms: i64` vs Go `uint64`; `cwd: LegacyAppPathString`
   vs Go `string`; `permissions: RequestPermissionProfile` vs Go `map[string]any`. Go parses by
   shape and converts, so behaviour matches for current payloads; L-level typing drift.
5. `PermissionProfileListParams.limit: Option<u32>` vs Go `*int`: Go can send a negative limit
   that Rust would reject at deserialization. L.
6. `AdditionalPermissionProfile` is a flat Go struct (internal), not the Rust overlay type. Not on
   the wire in Go. 不适用/语义差异.
7. `ThreadResumeParams.history` uses Go's own `ThreadResumeHistoryItem` instead of `ResponseItem`.
   Structurally compatible for the fields Go reads; M-level if full re-anchoring is wanted.

## 4. Unknown-field and tolerance semantics (task 3)

- **Rust/serde default: unknown fields are ignored.** `deny_unknown_fields` appears in only five
  files under `app-server-protocol/src`; of the in-scope structs only
  `RequestPermissionProfile` (permissions.rs:217-226) opts out of the default.
- **Go: unknown fields are ignored** (`encoding/json` default). 23 call sites in the repo call
  `DisallowUnknownFields`, but none of them are on the app-server RPC path —
  the remote client decodes with plain `json.Unmarshal` (`app/remote_tui.go` `waitResponse`), and the
  Go server decodes request params the same way.
- **Verdict: the two sides agree for every in-scope struct except `RequestPermissionProfile`.**
  Concretely: a newer server that adds a key inside `permissions` (or inside `network`/`fileSystem`
  inside it) makes a **Rust client fail the whole approval request**, while a Go client accepts it
  and proceeds. The reverse (Go sending `config`/`dynamicTools` on turn/start) is harmless today
  because Rust ignores unknown fields, but it would break the moment Rust adds
  `deny_unknown_fields` to `TurnStartParams`.
- **Recommendation: do not add global strict decoding.** Old-client/new-server compatibility
  depends on leniency in both directions. If exact parity is wanted, add `DisallowUnknownFields`
  **only** for `RequestPermissionProfile` in the Go client, and add a test that a payload with an
  extra key fails. Risk either way is low; the current leniency is the safer default.

## 5. Backlog

| # | Item | Size | Where |
|---|---|---|---|
| M1 | `serviceTier: null` (double option) cannot be sent by the Go client | M | turn/api.go, appserver/protocol.go (ThreadStart/Resume/Fork) |
| M2 | Pointer `MarshalJSON` of `TurnStartParams` drops 9 Rust fields while the value encoder sends them; `api_test.go:508` pins the old shape | M | turn/api.go:393, turn/api_test.go |
| M3 | `Turn` payload fields `items`, `itemsView`, `error`, `durationMs` are not parsed; Go emits placeholders | M | turn/api.go:760-795 |
| M4 | Go sends `config`/`dynamicTools` on turn/start although Rust `TurnStartParams` has no such fields | M | app/remote_tui.go (turn builder) |
| M5 | Go-only wire fields `prompt`, `originator`, `allowedTools`, `parentItemId`, `lastN`, `historyMode` | M | appserver/protocol.go |
| M6 | `ThreadResumeParams.history` uses Go's own item type rather than `ResponseItem` | M | appserver/protocol.go |
| L1 | `startedAtMs` i64 vs uint64; `cwd` LegacyAppPathString vs string; `RequestPermissionProfile` strict-decoding parity; `limit` u32 vs int | L | appserver/server_request.go, sandbox/*, app/remote_tui.go |

## 6. Verification

- `go build ./...` → 0
- `GOOS=windows GOARCH=amd64 go build ./...` → 0
- `go test ./app/ ./turn/ ./tui/ ./tui/tea/ -count=1` → pass except the two known
  worktree-path snapshots in `tui/tea` (`TestModelTerminalSnapshots`,
  `TestModelVT100TerminalSnapshotMainView`), which embed the absolute checkout path.
- `go test ./tui/ -run TestDetectStdoutColorLevel -count=1` fails **only** with the ambient
  `NO_COLOR=1` / `WT_SESSION` of this host; with both unset it passes. Pre-existing at `b6db8edc`
  (introduced by `e6b886ca` earlier), unrelated to this branch.