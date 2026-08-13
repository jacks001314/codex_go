# Codex Go update alignment plan - 2026-08-13 continued batch

## Baseline

- Rust upstream checkout: `D:\qax\reagent\dev\git\codex`
- Rust `origin/main`: `e766f75989`
- Go baseline before this batch: `df884bb` (clean `main`, pushed)
- Scope: continue deferred work from `update/plan_2026_08_13_followup.md`

## Implemented in this batch

1. **Per-thread usage backend query and app-server routing**
   (`842fae26c9`, #38270, with `f1a1fce26a` protocol routing from #38281)
   - `chatgptapi.CloudClient.GetThreadUsage(ctx, threadID)` posts
     `{"thread_ids":["<threadId>"]}` to the path-style-aware
     `/usage/thread_usage/query` endpoint and rejects a response missing the
     requested thread.
   - `auth.ThreadUsage` / `auth.ThreadUsageBreakdownGroup` are reused as the
     app-server response shape; backend-owned `chatgptapi.ThreadUsage` types
     keep the lower-level client independent of the app-server protocol.
   - `RuntimeRouter.handleGetAccountTokenUsage` now accepts the optional
     `threadId` parameter, validates it as non-empty, uses a 60s fetch timeout,
     maps 403/404 to an empty usage response with no thread usage, and returns
     an empty account summary plus the converted thread usage on success.
   - Tests: backend request/parse/mismatch and app-server routing/auth.

2. **Lazy required cached MCP servers for subagents** (`7093e8c480`, #38217)
   - `MCPService.populateStatusInventories` now leaves a required server
     dormant when the lazy (non-blocking optional) startup policy is in use,
     the server state is already `ready`, and cached tools are available.
   - Cached tools continue to satisfy the subagent catalog capture; calling a
     tool later uses the normal per-call client path and starts the connection.
   - Test covers required cached-server dormancy without running the configured
     command.

3. **Stable transcript render-cache invalidation for TUI**
   (`3d7bb2dd2e`, #38242 analog)
   - Adds a message revision counter to `codextui.State` and bumps it wherever
     the transcript message slice is replaced or edited.
   - Caches transcript rendering by message revision, width, raw-output mode,
     and active theme; unchanged transcripts skip re-render while activity
     follow still scrolls to the bottom on height changes.
   - This is the Go TUI equivalent of Rust's stable active-cell layout cache;
     the terminal framework and viewport stack differ, so the cache is applied
     at the transcript render boundary rather than individual active cells.

4. **Conversation-history creation times** (`361fe2d202`, #38272)
   - `rollout.LineFromItem` now stamps locally authored user/developer
     messages and tool/agent output items with fractional Unix `create_time`
     inside `internal_chat_message_metadata_passthrough`.
   - `SessionItemFromRolloutItem` prefers the persisted `create_time` when
     replaying a response item, preserving it across resume/history reads.
   - Tests cover stamping, fractional second preservation, and client-supplied
     timestamp preservation.

5. **Client-authored developer-message provenance** (`0e0ef5d818`, #38243)
   - `Router` gained a feature predicate installed by `RuntimeRouter`; when
     `retain_client_developer_messages` is enabled, injected developer messages
     are marked with `harness_metadata.client_authored` before session/rollout
     persistence.
   - Active-turn provider input remains unannotated, matching Rust's
     serialization boundary.

6. **Windows managed-proxy bounded fallback ports** (`631bbb33cc`, #38265)
   - `startProxyServer` now reserves HTTP and SOCKS5 listeners independently
     with the Rust-compatible preferred ranges (`3128-3159` and `8081-8112`)
     and falls back to an ephemeral loopback port only after the preferred
     range is exhausted.

7. **Persisted world-state JSON object representation** (`4b07886d59`, #38274)
   - Go already persists world state as a typed JSON object and reconstructs
     it without arbitrary JSON values; no production change was required.
   - Added a regression test asserting `EncodeWorldState` emits an object and
     `DecodeWorldState` rejects a non-object array.

8. **Root-turn propagation across delegated requests** (`1f4ea79853`, #38232)
   - Added `root_turn_id` to Responses client/turn metadata and reserved
     metadata keys.
   - Propagated `RootTurnID` through turn-start params, agent controller
     child turns, and runtime client-metadata construction.
   - Root turns default to the current turn; nested subagents inherit the
     parent root or, when unavailable, fall back to the immediate parent turn.

9. **Unix socket gRPC code-mode endpoints** (`bde723ae7d`, #38257 partial)
   - `UsesGrpcCodeModeEndpoint` and endpoint validation now accept `unix://`
     and `unix:` endpoints without path/query/fragment restrictions.
   - The existing gRPC transport continues to use insecure transport
     credentials for Unix sockets.
   - Added host-generation cell-ID scoping for reconnected sessions: public
     cells are `gN:` prefixed after generation one, stale cell requests are
     rejected, and delegate callbacks receive generation-scoped cell IDs.

10. **Unified turn-input submission/routing** (`cbb7e82a8b`, #38275)
    - Go already routes user input, response items, and inter-agent
      communication through `SteerMailbox`/`AgentLoopRequest.InputItems`;
      no new input enum is required. Existing `turn` tests cover the
      submission/drain boundary, so this item is classified equivalent.

11. **Plugin measurement telemetry call-site wiring**
    - `RuntimeRouter.toolRouterForTurnContext` now installs the trusted-plugin
      metrics resolver and analytics tracker into `turn.ToolRegistryOptions`.
    - Shell execution can therefore publish validated
      `codex_plugin_measurement_event` analytics events from the app-server
      turn path.

11. **Trusted plugin analytics manifest resolution** (`dc8562d672`, #38238)
    - Added `analytics.yaml` v1 parsing for trusted remote plugin roots:
      operation path validation, measurement name/dimension validation,
      duplicate-path rejection, and exact command-to-operation resolution.

12. **Plugin measurement sidecar and analytics events** (`9ca0337dbf`,
    `d6eefb26a6`, #38252/#38239 partial)
    - Added `CODEX_PLUGIN_METRICS_OUTPUT` sidecar with temp-file lifecycle,
      64 KiB / 100-row bounds, enum dimension validation, and cleanup.
    - Added `codex_plugin_measurement_event` telemetry types, batch row
      validation, and an analytics-client tracking method.
    - Added `ShellExecutorOptions` resolver/tracker hooks and passed them
      through the turn tool registry, so runtime callers can install the
      sidecar environment and publish batches. App-server call-site wiring and
      remote/unified-exec sandbox permission merging remain the next step.

## Verification

- `gofmt -l` clean for touched files
- `go build ./...` clean
- `go vet ./chatgptapi ./appserver ./mcp` clean
- `go test ./rollout ./network ./appserver ./chatgptapi ./mcp ./tui/... -count=1` passes

## Deferred (unchanged)

The remaining items from `update/plan_2026_08_13_followup.md` remain tracked,
including:

- Plugin measurement telemetry family
- TUI/backend thread-usage surface behavior (`/status` rendering, TUI cards)
- Unified turn-input submission/routing (#38275)
- Dynamic MCP HTTP header helpers (#38245)
- gRPC code-mode reconnect (#38257)
- Root-turn tracking across delegated requests (#38232)

Architecture/N/A classifications are also retained in that plan:

- #38244 rollout-ID resolution is N/A because Go has no `thread/revert`
  rollout-ID model; it stores one session record per thread.
- #38258 external-auth provider unification is N/A for the current Go auth
  manager, which has no Rust-style `ExternalAuth` provider interface yet.
