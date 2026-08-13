# Codex Go update alignment plan - 2026-08-13 follow-up batch

## Baseline

- Rust upstream checkout: `D:\qax\reagent\dev\git\codex`
- Previous aligned Rust commit: `91d6f48992` (Go commit `af8a86c`, 2026-08-13;
  sync batch 2 already committed #38227, #38256, and thread-usage protocol
  fixtures for #38270/#38281)
- Audited Rust commit: `e766f75989` (`origin/main` after the latest
  `git pull origin main`)
- Range: 31 commits (`91d6f48992..e766f75989`)
- Go baseline: `af8a86c` (clean worktree on `main`, pushed to `origin/main`)

## Upstream audit (31 commits)

### Implement in this batch

1. **Detect implicit skill invocations from PowerShell reads**
   (`9df9ff6ad9`, #38228)
   - Rust extends `detect_implicit_skill_invocation_for_command` to recognize
     `Get-Content [-Raw] <SKILL.md path>` with quoted paths, spaces, and
     Windows backslashes.
   - Go: extend
     `prompt.DetectImplicitSkillInvocationForCommand` with a
     `Get-Content` parser and lookup against the same skill-doc map.

2. **Include Node REPL policy in turn metadata** (`74004b5397`, #38241)
   - Rust parses `node_repl_auto_review_required` and `node_repl_disabled`
     from model-catalog `ModelInfo`, adds them to Responses turn metadata,
     and reserves both keys from client overrides.
   - Go: add the two model-catalog fields, thread them through
     `ResponsesClientMetadataOptions`, emit them in `ClientMetadata`
     turn-metadata, and add them to `ClientReservedMetadataKeys`.

3. **Read model ETags from WebSocket metadata events** (`8bb8d60234`, #38251)
   - Rust stops reading/reporting `X-Models-Etag` from WebSocket upgrade
     headers and instead emits `ModelsEtag` from `codex.response.metadata`
     event headers.
   - Go: parse `x-models-etag` from `codex.response.metadata` events and emit
     `ResponsesStreamEventModelsETag`; do not emit it from WebSocket upgrade
     headers (current Go WebSocket path already does not).

4. **Resolve skill package aliases in `skills.read`** (`130c7c93a9`, #38261)
   - Rust resolves catalog aliases automatically when looking up model-visible
     executor/orchestrator skill packages and updates prompts/docs to tell
     models to pass listed short locators directly.
   - Go: update prompt wording and resolve `eN`/`oN` package aliases in
     `turn/skills_tools.go` for executor and orchestrator reads.

5. **Expose executor skill roots from `skills.read`** (`5664a5c07c`, #38268)
   - Rust adds `skill_root` to executor `skills.read` responses, derived from
     the skill's main resource directory.
   - Go: add `skill_root` to `skillsReadResponse` and populate it for executor
     authority reads.

### Deferred for dedicated batches

| Commit | PR | Reason for deferral |
|---|---|---|
| `e766f75989` | #38285 | Rust `Cargo.toml` dev-dependency move only -> N/A |
| `27a98dde4d` | #38280 | Rust Bazel proto rule -> N/A |
| `c38d59fca0` | #38278 | Rust app-server test-only -> N/A |
| `9579479d28` | #38283 | Remote-executor plugin metrics; requires exec-server/plugin sidecar port |
| `1e71e35df6` | #38282 | TUI thread-usage status surfaces; large TUI batch |
| `f1a1fce26a` | #38281 | `/status` thread usage; protocol fixtures already in `af8a86c`, UI/backend behavior remains deferred |
| `96e8afbfb8` | #38276 | Background unified-exec plugin metrics |
| `cbb7e82a8b` | #38275 | Unify turn input submission/routing; large session/core refactor |
| `4b07886d59` | #38274 | Go uses typed `session.WorldState` object representation; architecture delta / audit |
| `361fe2d202` | #38272 | History-item creation timestamps; protocol/session/rollout batch |
| `842fae26c9` | #38270 | Backend per-thread usage queries; backend-client/API batch |
| `631bbb33cc` | #38265 | Windows managed-proxy fallback ports; Go proxy architecture differs |
| `18dcc7646f` | #38258 | External auth provider unification; Go auth architecture differs |
| `bde723ae7d` | #38257 | gRPC code-mode reconnect; Go gRPC client reconnect batch |
| `6e7daed1e9` | #38253 | Unified-exec plugin metrics |
| `9ca0337dbf` | #38252 | Plugin shell-command metrics |
| `379cb68444` | #38245 | MCP dynamic HTTP header helpers; rmcp/http-client batch |
| `8d4d57387a` | #38244 | Paginated history resolution by rollout ID; thread-store batch |
| `0e0ef5d818` | #38243 | Client-authored developer messages in rollout history |
| `3d7bb2dd2e` | #38242 | TUI active-cell layout cache; TUI rendering batch |
| `d6eefb26a6` | #38239 | Bounded plugin measurement analytics |
| `dc8562d672` | #38238 | Manifest-defined plugin script metrics |
| `1f4ea79853` | #38232 | Root-turn tracking across delegated requests |
| `7093e8c480` | #38217 | Lazy required cached MCP servers for subagents |

## Implementation plan

1. `prompt/instructions.go` + tests: PowerShell `Get-Content` implicit skill
   detection.
2. `model/catalog.go`: add `NodeReplAutoReviewRequired` and
   `NodeReplDisabled` fields to `ModelInfo` (raw + unmarshal). Thread them
   through `codexapi.ClientMetadata` and `turn.ResponsesClientMetadataOptions`,
   then set them from `modelInfo` in appserver/exec/web-search paths.
3. `model/responses_stream.go` + tests: parse model ETags from
   `codex.response.metadata` events.
4. `prompt/skills_render.go` + `turn/skills_tools.go` + tests: alias-aware
   `skills.read` package lookup and executor `skill_root` output.
5. Verification: `gofmt -l` empty on touched files; `go build ./...` clean;
   `go vet ./prompt ./model ./codexapi ./turn ./appserver ./exec ./doctor`
   clean; targeted `go test` passes for changed packages.
6. Commit and push to `origin/main`.

## Deliverable

- Commit aligned to Rust `e766f75989` covering #38228, #38241, #38251,
  #38261, and #38268 (Go code + tests), pushed to `origin/main`.
- This plan document (`update/plan_2026_08_13_followup.md`).
