# 上游覆盖率审计（2026-10-07）

## 1. 审计对象与方法

静态 parity 层 pin（Go vendored 协议导出的上游基线）= `5f3180c793`（2026-09-26，见 `update/plan_2026_09_26.md` sync234）；上游 head = `18e28fe1b9`（#51556）。

```bash
cd /home/jacks/jacks_dev/codex
git log --pretty='%h\t%s' 5f3180c793..18e28fe1b9 > /tmp/window.txt
wc -l /tmp/window.txt          # 498
grep -c '(#[0-9]\+)$' /tmp/window.txt   # 498（窗口内每个提交都带 PR 号）

cd /home/jacks/jacks_dev/codex_go
# 从全部 update/*.md 提取被引用过的 PR 号
python3 - <<'PY'
import re, pathlib
txt = "".join(p.read_text(errors="ignore") for p in pathlib.Path("update").glob("*.md"))
print(len(set(re.findall(r"#(\d{4,6})", txt))))
PY
```

差集（窗口内 498 个 PR 中，从未在任何 `update/*.md` 里出现过 PR 号的）= **6 个**：

| PR | 上游 SHA | 标题 | 处置 |
|---|---|---|---|
| #50442 | `3629508849` | Preserve native USD amounts in thread usage responses | **已落地** sync408（`0269d580`）|
| #50446 | `9b0a676d5d` | Bundle rollout attachments into a gzip tar archive | **已落地** sync409（`a9284006`）|
| #50462 | `09bced5ad9` | Populate thread previews from delegated task inputs | 已派 sync51480（队列 A）|
| #50454 | `3c3a990da0` | Measure rollout persistence size reductions | 已派 sync51480（队列 B）|
| #50443 | `a4bfd07d51` | Stabilize paused-time code-mode service tests | 记录为 N/A（Rust 测试专用）|
| #50445 | `d4eed6dca5` | Assert that only direct tool calls emit timing events | 记录为 N/A（Rust 测试专用）|

即窗口覆盖率 = **492/498 已记录**，其余 6 个差值本轮全部给出处置（4 落地 / 2 N/A）。

## 2. 两个 N/A 的决定性证据

### #50443 `a4bfd07d51`
```bash
git show --stat a4bfd07d51
#  codex-rs/code-mode-runtime/src/service_tests.rs | 123 +++++++++++++-------
#  1 file changed, 88 insertions(+), 35 deletions(-)
```
改动全部落在 Rust 的 `service_tests.rs`：用 blocking-task guard 显式控制 tokio 虚拟时钟、把有界轮询换成 tool-start 通知。无产品行为变化；Go 的 `codemode/` 测试框架不复刻 tokio paused-clock 语义（`ls codemode/*_test.go` 有 `observation_yield_test.go` 等，但无 paused-time 调度器等价物）。

### #50445 `d4eed6dca5`
```bash
git show d4eed6dca5 -- codex-rs/core/src/tools/parallel.rs | head -40
git show d4eed6dca5:codex-rs/core/src/tools/parallel.rs | grep -n "cfg(test)|mod tests"
# 455:#[cfg(test)]
# 456:mod tests {
```
新增的 15 行全部位于 `#[cfg(test)] mod tests`（第 455 行起）内的 `tool_call_timing_guard_ignores_code_mode_source`：捕获 tracing 输出并断言 direct + nested code-mode 调用合计只发出一个 `codex.tool_call` 事件。Rust 测试专用。
Go 侧对应物：`grep -rn "ToolCallTiming\|tool_call_timing" --include='*.go' .` → 无命中，即 Go 没有该 tracing 断言，属测试框架差异，不是功能缺口。

## 3. #50442 的结构性差异（已核实）

- Rust 的 `native_usage_usd_micros` 加在 **backend-client**（`codex-rs/backend-client/src/client/thread_usage.rs`）与 **tui analytics 测试 fixture**；**app-server 协议 v2 未改动**——
  `git show 18e28fe1b9:codex-rs/app-server-protocol/schema/typescript/v2/ThreadUsage.ts` 仍为
  `{ threadId, estimatedUsageCreditsMicros, estimatedUsageUsdMicros, groups }`（无 native 字段）。
- 因此 Go 的落点**只在** `chatgptapi`（backend client 解码面，对应 backend-client），**故意没有**给 `auth.ThreadUsage`（= app-server 协议 v2 的 Go 镜像，由 `appserver/schema/precomputed/*.zst` 与 `parity/` 校验）加字段，避免制造协议漂移。
- Go 无 TUI chats analytics 消费者（`grep -rn "GetThreadUsage" --include='*.go' .` → 仅 `appserver/runtime_router.go` 一处）；Rust 侧该 PR 的 tui 改动本身也只是 fixture 补字段。

## 4. #50446 的结构性差异（已核实）

- Rust 在 `FeedbackSnapshot::feedback_attachments` 里惰性产出 `rollouts.tar.gz`（sentry envelope 写入时才物化），并额外按 `sentry::protocol::Envelope` 的**整包**字节数再判一次上限。
- Go 的 feedback 管道**止于** `PreparedFeedbackUpload`（无 sentry envelope 组装、无网络上传：`grep -rln "sentry" --include='*.go' .` 仅命中一个测试文件；`grep -rn "LastPrepared" --include='*.go' .` 仅 `appserver/feedback.go` 自身与测试）。因此 Go 在**准备上传时**就把归档物化成 `rollouts.tar.gz` 附件（可观测产物一致：单个 gzip tar 附件 + 条目名 = 各 rollout 文件名 + 失败回退成单个附件），但跳过了"整包 envelope 字节数"这一层判定（Go 无 envelope）。
