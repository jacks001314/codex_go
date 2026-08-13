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

## Verification

- `gofmt -l` clean for touched files
- `go build ./...` clean
- `go vet ./chatgptapi ./appserver ./mcp` clean
- `go test ./chatgptapi ./appserver ./mcp ./tui/... -count=1` passes

## Deferred (unchanged)

The remaining large/deferred items from `update/plan_2026_08_13_followup.md`
remain tracked, including:

- Plugin measurement telemetry family
- TUI/backend thread-usage surface behavior (`/status` rendering, TUI cards)
- Unified turn-input submission/routing (#38275)
- World-state persisted object representation (#38274)
- Conversation-history creation-time passthrough (#38272)
- Client-authored developer-message provenance (#38243)
- Dynamic MCP HTTP header helpers (#38245)
- gRPC code-mode reconnect (#38257)
- External auth provider unification (#38258)
- Windows managed-proxy bounded fallback ports (#38265)
