# Codex Go update alignment plan - 2026-08-26 (sync27 follow-up: Guardian endpoints + risk scores)

## Baseline

- Rust upstream checkout: `D:\qax\reagent\dev\git\codex`
- Rust `origin/main` frozen target (sync26, static baseline): `f5420174da` (#40866)
- Rust new HEAD (sync27): `bde9db1375`
- Go baseline commit: `0079f62` (`main`)
- Go baseline state: `go build ./...` clean.

Rust `git pull origin main` advanced `f5420174da..bde9db1375` (4 upstream commits on top of the sync26 static freeze). All four are in the Guardian V2 / Responses-endpoint theme.

## New upstream commits

### 1. `10d5a603ae` #40884 - Persist Guardian V2 risk scores without restoring them
- Rust `core/src/session/session.rs`: removed the resume/fork path that re-inserted a persisted
  `SecurityRiskScore` from rollout history into the active Guardian extension state.
- Rust `ext/guardian-v2/.../async_scorer/extension.rs`: append accepted
  `RolloutItem::SecurityRiskScore(score)` to rollout history for non-ephemeral threads.
- Go impact: `rollout.AppendSecurityRiskScore` already persists durable rollout-only
  `security_risk_score` records. The Go codebase does not yet read a persisted score back into
  Guardian extension state on resume, so the "without restoring" behavior is satisfied by
  construction. No restore path to remove.

### 2. `d4998d611a` #40901 - Record reviewed actions with security risk scores
- Rust `protocol/src/security_risk.rs`: added optional `call_id: Option<String>` and
  `action: Option<serde_json::Value>` to `SecurityRiskScore`.
- Rust `ext/guardian-v2/.../async_scorer/extension.rs`: populate `call_id` and `action` when
  classifying a planned action; leave fail-closed/legacy scores without provenance.
- Go impact: extend the `security_risk_score` rollout payload to carry `call_id`, `action`, and
  `sampled_at` alongside `scores`.

### 3. `62fb56ee56` #40892 - Route Guardian inference through dedicated endpoints
- Rust `codex-api/src/endpoint/responses.rs` + `responses_websocket.rs`: new
  `ResponsesEndpoint` enum (`Responses` / `Guardian` / `GuardianClassifier`) with `.path()`
  returning `/responses`, `/guardian`, `/guardian-classifier`; `with_endpoint()` on HTTP and
  WebSocket clients.
- Rust `core/src/client.rs`: `responses_endpoint()` selects a Guardian route when
  `free_guardian_enabled` AND the session is a basic Guardian session AND codex backend auth
  AND `supports_codex_backend_routes()` AND the model is the approval-review-preferred model.
  `uses_codex_backend()` extracted. Omit routing hints + `service_tier` on Guardian routes.
  Endpoint-aware websocket connection reuse.
- Rust `core/src/config/mod.rs`: `free_guardian_enabled()` reads
  `[features.guardianv2].free_guardian`.
- Rust `features/src/feature_configs.rs`: `GuardianV2ConfigToml.free_guardian`.
- Rust `model-provider-info/src/lib.rs`: `supports_codex_backend_routes()`.
- Rust `ext/guardian-v2/.../async_scorer/sampler.rs`: route classifier sampling to
  `GuardianClassifier` when eligible; omit routing hints/service_tier.
- Go impact: add `ResponsesEndpoint` + `SupportsCodexBackendRoutes()` + `Config.FreeGuardianEnabled()`
  + `ResponsesAgentRunner.FreeGuardianEnabled` + endpoint selection in `newResponsesHTTPRequest`.

### 4. `bde9db1375` #40906 - Record actual Responses endpoints in tracing spans
- Rust `responses.rs` / `responses_websocket.rs` / `client.rs`: report `api.path` from the
  selected `ResponsesEndpoint` instead of hard-coded `responses`.
- Go impact: record `api.path` from the selected endpoint in request tracing.

## Implementation scope (Go)

- `rollout/rollout.go`: extend `security_risk_score` payload with `call_id`, `action`, `sampled_at`.
- `model/provider_info.go`: add `SupportsCodexBackendRoutes()`.
- `model/responses_agent.go`: add `ResponsesEndpoint` + `Path()`, `FreeGuardianEnabled` field,
  `responsesEndpoint()` selection, endpoint-aware `responsesURL()`, request path tracing.
- `config/config.go`: add `FreeGuardianEnabled()`.
- `appserver/agent_runtime.go`: wire `agent.FreeGuardianEnabled = cfg.FreeGuardianEnabled()`.

## Verification

- `go build ./...` clean.
- `go test ./rollout -count=1` (SecurityRiskScore payload).
- `go test ./model -count=1`, `go test ./config -count=1`, `go test ./features -count=1`.
- `go test ./parity -count=1` (static/surface gates remain green).
- `go test ./appserver -count=1` pass (static-freeze precomputed export hashes reconciled to
  Rust `bde9db1375`).

## Status summary

Static layer (L0) unchanged from sync26 (`f5420174da`). This sync27 follow-up lands the Guardian
inference endpoint routing building blocks and the reviewed-action SecurityRiskScore provenance
fields in the Go port.
