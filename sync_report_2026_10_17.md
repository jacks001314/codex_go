# codex_go 同步 Rust codex CLI 报告（2026-10-17，round 91）

## 结论

- Rust 上游（`D:\qax\reagent\dev\git\codex`）最新提交 = **`e16b631d08`**（Reduce memory overhead in tool JSON schemas, #54754）；`git pull origin main` → `Already up to date`。
- Go 仓（`D:\qax\reagent\dev\codex_go`）本轮基线 = `634daa4e`。
- **parity 控制面从 6 项失败修到全绿**：`go test ./parity -count=1` → `ok codex_go/parity`。
- **落地 2 个此前 implementable 的上游功能**：#53784（voice session id 转发）、#54379（TUI 输入预设配置）。
- 协议 oracle 重新对齐：`appserver/schema/precomputed/*.zst` 与 Rust 树逐字节一致，且 Go 重新生成的 TS/JSON/internal-JSON 导出树与之一致。

## 本轮改动

### A. parity 漂移修复（6 项）

1. `tui/chatwidget/prompt_for_plan_mode.md` 按 `.gitattributes` 重新 checkout 为 LF（此前工作树 CRLF 导致模板比对失败）。
2. `parity/rust_fixture_manifest_test.go`：`core/tests/suite` 352 → 355（#54654 新增 1 个套件 + 2 个快照）。
3. `parity/rust_snapshot_test.go`：workspace 成员加入 `ext/multi-env-copy-file`（#54674）。
4. `parity/rust_snapshot_test.go`：关键文件哈希改为 CRLF→LF 归一再比对（与注释声明的 blob 语义一致），并重算 `Cargo.toml` / `exec/src/lib.rs` 两个 pin。
5. `appserver/schema/precomputed/*.zst`：按 Rust 重新 vendored（#54686 / #53784），`appserver/schema_test.go` 的 SHA 断言同步更新。
6. `parity/rust_tui_snapshot_manifest_test.go`：composer 快照 30 → 31，总数 1432 → 1433（#54742）。

### B. 功能落地

- **#53784 转发客户端 voice session id**（Rust `6786dccbe7`）
  - `realtime/realtime.go`：`StartParams.VoiceSessionID`（wire `voiceSessionId`，可空）、`SessionConfig.VoiceSessionID`。
  - `realtime/wire.go`：`applyRealtimeSessionHeaders` 同时写 `x-session-id` 与 `voice-session-id`。
  - `realtime/transport.go`：WebRTC call 创建请求复用同一头集合。
  - 测试：`realtime/voice_session_id_like_rust_test.go`。
- **#54379 TUI 输入预设配置**（Rust `1544ffc79d`）
  - `config/tui_presets.go`：`TUIPreset` + `Validate` + `ParseTUIPresets` + 装载期校验，错误文案与 Rust 一致。
  - `config/config.go`：`tui.presets` 加入已知字段并在 `LoadEffectiveWithOptions` 校验。
  - `tui/tea/model.go` + `app/interactive.go`：`QuickDials` 进入 TUI 本地设置。
  - 测试：`config/tui_presets_test.go`。

## 门禁

- `go build ./...`：通过（本文件产出时已覆盖受影响包）。
- `go test ./parity`：ok。
- `go test ./realtime ./config`：ok。
- `./appserver`、`./app`、`./tui/tea` 仍各有个别失败，已在 pristine worktree（`084ec624`）逐一复现，属既有环境相关失败（TERM=dumb、本机 project trust/git 状态、剪贴板路径），非本轮引入。

## 剩余 backlog

见 `update/alignment_audit_2026_10_17.md` §5：12 个 implementable、11 个 partial、20 个 blocked_precondition、1 个 unverified。
