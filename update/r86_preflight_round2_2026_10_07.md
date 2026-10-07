# round 86 · 载体预检第 2 轮 —— worklist 主表 12 条（syncl4 · Linux 节点 · 只读）

**派单**：`msg-1791368357986104400-4050`
**固定点**：Go `origin/main` = **`937836c5a6dc53cccbd39dfd996f8c619b90a5b9`**（`git ls-remote origin main` 实测）；Rust `/home/jacks/jacks_dev/codex` @ `5b0b253035` = `origin/main`
**判据（按队长采纳的两步筛）**：① 有无可落地载体；② **是否已有等价实现 / 前置是否齐备**
**纪律**：只读（`git log/show/grep` + `grep`）；未写 Go 代码；未 commit/push；报告只落 `update/`
**预检工作树**：`codex_go_wt/preflight2`（detached @ `937836c5`），跑完已 `git worktree remove`
**sha 解析**：全部 `git -C <rust> log origin/main --grep '#<PR>' -F` 自解，**不采信台账 sha 列**

> 说明：主表 12 条中第 2/3/9 行（`#49171` / `#49261` / `#49082`）已在第 1 轮判 N/A 并被你采纳，本轮不重复；
> 第 6 行 `#49852` 在两次派单之间**已落地 main**（见 §2）。本轮实际新分析 9 条。

---

## 0. 结论摘要

| 序 | PR | sha（自解） | 文件 | 判定 | 一句话 |
|---|---|---|---|---|---|
| 1 | #49147 | `3a16c0b707` | 1 | ⛔ N/A | Go 已是 `strings.TrimRight(…, "/")`；PR 自身是**行为不变**的重构 |
| 2 | #49171 | `c248f6d48b` | 1 | ⛔ N/A | 第 1 轮已判（Go TUI 无该路径） |
| 3 | #49261 | `f35a0fdc5d` | 1 | ⛔ N/A | 第 1 轮已判（Go 结构上无此 bug） |
| 4 | #49300 | `a6f09397aa` | 1 | ⛔ N/A | Go 的隐藏标签剥离是**无状态**函数，无跨 chunk pending 缓冲可修 |
| 5 | #49411 | `eefe0ce1a8` | 1 | ⛔ N/A | Rust **借用期** workaround（`let t = …; t`），Go 无此概念 |
| 6 | #49852 | `236be1ad9f` | 1 | ✅ **已落地** | main `84518d3f sync601`（本 PR 已在 main，无需派单） |
| 7 | #50445 | `d4eed6dca5` | 1 | ⛔ N/A | **纯测试**；Go 的等价行为已由单点发射 + 嵌套守卫实现 |
| 8 | #48686 | `41f9084b30` | 2 | ⛔ N/A | Go 未在 info 日志输出 WS headers / tool payload（只喂 telemetry） |
| 9 | #49082 | `46d2585ea4` | 2 | ⛔ N/A | 第 1 轮已判（前置缺失） |
| 10 | **#49097** | `9563713df2` | 2 | **✅ S/M 可落地** | 后置压缩 `UsageLimitExceeded` 失败时 Go 只发 Warning，**未走 turn-error lifecycle** |
| 11 | #49118 | `6c49240565` | 2 | ⛔ N/A | 该 sha 实为 **docs-only**（文档/schema 措辞）；该行语义本属 #49714（已判 N/A） |
| 12 | #49144 | `ff3c82c8a9` | 2 | ⛔ N/A | Go `launchReasoningOverrides`+`launchSettingForKey` **已实现**同一 origin 门 |

**本轮新增可派单：1 条（#49097）。** 其余 8 条新分析全部是「Go 已等价 / 无载体 / 纯测试 / 文档」。

---

## 1. ✅ 可派单项：#49097

### #49097 —— `9563713df2` · 2 文件（`core/src/session/turn.rs`、`core/src/tasks/compact.rs`）

**上游语义（commit 原文）**：*"Usage-limit failures during manual or post-turn compaction did not notify turn lifecycle extensions, leaving them unable to react to these errors. Emit the turn error lifecycle notification for `UsageLimitExceeded` in both compaction paths. Preserve the completed answer after post-turn compaction fails, and avoid duplicating the error already emitted by manual compaction."*

**Go 载体：存在，后置压缩路径有真缺口。**
```
$ sed -n '1857,1876p' appserver/turn_runtime.go          # postTurnCompact 调用点
	postTurnRan, compactErr := r.postTurnCompact(ctx, threadID, turnID, connectionID, params, runConfig, status)
	if compactErr != nil {
		r.persistCompactionFailure(threadID, compactErr)
		if errors.Is(compactErr, context.Canceled) || errors.Is(compactErr, context.DeadlineExceeded) {
			r.clearActiveRuntimeTurn(threadID, turnID)
			r.finishTurnWithErrorAnalytics(threadID, turnID, startedAtMS, compactErr, nil)   ← 只有取消/超时才走 turn-error
			return
		}
		r.notify(NotificationWarning, &WarningNotification{                                   ← 其余（含 usage-limit）只发 Warning
			Message:  "Post-turn context compaction failed: " + compactErr.Error(),
		})
	}

$ grep -n 'func (r \*RuntimeRouter) finishTurnWithErrorAnalytics' appserver/turn_runtime.go
4134:func (r *RuntimeRouter) finishTurnWithErrorAnalytics(threadID string, turnID string, startedAtMS int64, err error, analytics *turnCompletionAnalyticsContext) {

$ sed -n '4398,4405p' appserver/turn_runtime.go     # Go 侧 usage-limit 分类已存在
	case codexapi.ErrorQuotaExceeded:
		return fields("usageLimitExceeded", "quota_exceeded")
	case codexapi.ErrorUsageNotIncluded:
		return fields("usageLimitExceeded", "usage_not_included")
	case codexapi.ErrorRateLimit:
		out := fields("usageLimitExceeded", "usage_limit_reached")
```
**判据**：Go 的**后置**压缩失败路径只对「取消/超时」调 `finishTurnWithErrorAnalytics`（`:4134`，turn-error 落点），对 `UsageLimitExceeded` 仅 `notify(NotificationWarning, …)` ⇒ 与 Rust #49097「为 `UsageLimitExceeded` 发出 turn error lifecycle」**不一致**（Rust 的 *Why* 正是「扩展收不到通知」）。**手动**路径（`appserver/runtime_router.go:3289-3303`）已对任意 err 调 `finishTurnWithError`，但需按 Rust「手动压缩已发过一次、不得重复」核对去重语义。
**落地范围（估）**：`appserver/turn_runtime.go:1864-1876`（后置路径加 usage-limit 分支 → `finishTurnWithErrorAnalytics`）+ 手动路径去重核对；测试可复用 `appserver/post_turn_compact_test.go`。
**建议 RC（值级）**：让后置压缩返回一个 `codexapi.ErrorRateLimit`（→ `usageLimitExceeded`）失败，断言收到 **turn-error lifecycle**（而非仅 Warning）；撤掉新分支 ⇒ 断言 FAIL ⇒ 恢复 ⇒ ok。
**口径未决（须先探针确认，见 §3-①）**：Go 的「lifecycle extension 通知」落点是否为 `finishTurnWithErrorAnalytics`（`:4134`）还是 `TurnError` 通知（`appserver/protocol.go:913` / `notifications.go:61`）——这决定改动是 S 还是 M。**建议派单时把「确认落点」写成第一步。**

---

## 2. 与派单之间的变化：`#49852` 已落地（`#49097` 之外唯一的状态变化）

```
$ git ls-remote origin main
937836c5a6dc53cccbd39dfd996f8c619b90a5b9	refs/heads/main
$ git log origin/main --oneline -5
937836c5 sync602: do not let message-board notifications reopen a final answer (#48982)
84518d3f sync601: log diagnostics for skipped feedback attachments (#49852)      ← 主表第 6 行已落地
d5dbe694 sync600: make the cloud-config cache permission assertion platform-aware (#49269 Windows guard)
c84a80ab sync599: align cloud-config bundle atomic publish and policy revision (#49269)
9fafc187 syncw2: hide reasoning summaries in the /status card for server connections (#49145)
$ for pr in 49147 49171 49261 49300 49411 49852 50445 48686 49082 49097 49118 49144; do
    git log origin/main --grep "#$pr" -F --oneline -1 | sed "s/^/#$pr /"
  done
#49147 not-in-log   #49171 not-in-log   #49261 not-in-log   #49300 not-in-log   #49411 not-in-log
#49852 LANDED: 84518d3f sync601: log diagnostics for skipped feedback attachments (#49852)
#50445 not-in-log   #48686 not-in-log   #49082 not-in-log   #49097 not-in-log   #49118 not-in-log   #49144 not-in-log
```
⇒ 主表 12 条中 **1 条已落地（#49852）**，**1 条可派（#49097）**，其余 10 条 N/A（含第 1 轮 3 条）。**12 条全部闭合，无剩余。**

---

## 3. 附录 A：已判 N/A（逐条决定性证据）

### #49147 —— `3a16c0b707` · 1 文件 · N/A（行为不变的重构）
```
上游：Replace the trailing-slash removal loop in normalize_base_url with trim_end_matches('/'),
      preserving existing behavior.（+ 补 9 组回归用例；测试期望 ("","") 与 ("///","")）
Go：chatgptapi/cloud_tasks.go:175
func NormalizeCloudBaseURL(input string) string {
	value := strings.TrimRight(strings.TrimSpace(input), "/")     ← 已是等价形式
```
**判据**：Rust 的 `while ends_with('/') { pop() }` → `trim_end_matches('/')` 语义完全相同（commit 自述 "preserving existing behavior"），Go 早已是 `strings.TrimRight(…, "/")` ⇒ **无行为差异可对拍**，落地只能是加测试，产不出值级 FAIL。
（另注：Go 对空串回落 `DefaultCloudTasksBaseURL`，Rust 返回 `""` —— 这是**既有的** Go/Rust 分叉，不在本 PR 范围，如需对齐应单独立项。）

### #49300 —— `a6f09397aa` · 1 文件 · N/A（Go 无跨 chunk 状态）
```
上游 Why：Parsing multiple tag delimiters in a chunk repeatedly drained the pending buffer,
          shifting its remaining contents after each delimiter.（改 push_str：加 consumed 偏移、
          每 chunk 只 drain 一次；+ 逐 UTF-8 边界的单块/分块对拍测试）
Go：eventmap/eventmap.go:526
func stripInlineHiddenTag(text string, open string, close string) string {
	for {
		start := strings.Index(text, open)
		if start < 0 { return text }
		rest := text[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 { return text[:start] }
		text = text[:start] + rest[end+len(close):]
	}
}
```
**判据**：Rust 的 bug 属于**流式解析器**的 `self.pending` 反复 drain；Go 的对应物是**无状态**函数（对完整 text 循环剥离，每轮重扫，天然无 pending），调用点 `eventmap/eventmap.go:510-512` 也在渲染完整文本时执行 ⇒ **该 bug 类在 Go 不存在**。

### #49411 —— `eefe0ce1a8` · 1 文件 · N/A（借用期 workaround）
```
上游 diff（全部）：- Some(app_server_time_provider(outgoing.clone(), thread_state_manager.clone())),
                  + Some({ let time_provider = app_server_time_provider(...); time_provider }),
```
**判据**：这是为满足 Rust 借用检查器的**零语义**改写（把临时值绑定到局部变量）。Go 无借用期概念 ⇒ **无载体、无行为**。

### #50445 —— `d4eed6dca5` · 1 文件 · N/A（纯测试）
```
上游 diff：只在 core/src/tools/parallel.rs 的 `mod tests` 内 +15 行（捕获 tracing 输出，
          断言 `event.name="codex.tool_call"` 恰好 1 条）；**无生产代码改动**。
Go：telemetry/turn_metric_emit.go:154  EmitToolCallMetric(...)
    telemetry/turn_metric_emit.go:170  sink.Counter(ToolCallCountMetric, 1, tags)
    唯一生产调用点：appserver/turn_runtime.go:2756  r.emitToolCallMetrics(r.services.TurnMetrics, execution)
    turn/tool_dispatcher.go:598-616  if d.emitCodeModeNestedLifecycle || d.executedToolCalls != nil { … }   ← 直接/嵌套分流
```
**判据**：Go 的「每次直接调用只发一次 timing」由**单点发射 + 嵌套分流守卫**结构性保证（无重复发射路径）；且 Go 发的是 metrics（`telemetry/metric_names.go:7-8` `codex.tool.call` / `.duration_ms`），与 Rust 的 tracing 事件不同名，断言面无法平移 ⇒ N/A。（如你想要一条等价回归测试，可作为独立测试项，但**不是 parity 缺口**。）

### #48686 —— `41f9084b30` · 2 文件 · N/A（Go 未泄漏）
```
上游：成功 WS 连接的 info 日志去掉 `headers: {:?}`；`ToolCall: {} {}` 去掉 payload_preview。
Go（逐条对位）：
  grep -rn 'successfully connected to websocket' --include='*.go' .      → 0 命中（无该 info 日志）
  grep -rn '"ToolCall: "' --include='*.go' .                             → 0 命中
  grep -rn 'toolLogPayload\(' --include='*.go' . | grep -v _test
    ./exec/otel_metrics.go:212:      Arguments: execToolLogPayload(invocation),      ← 只喂 telemetry，
    ./appserver/session_telemetry.go:355: Arguments: toolLogPayload(invocation),     ← 不进 info 日志
  telemetry/api_request.go:81  t.LogAndTraceEvent(ctx, "codex.websocket_connect", fields, nil, nil)
  telemetry/api_request.go:70  {"auth.header_name", record.AuthHeaderName},          ← 只记 header **名**，非值
```
**判据**：Go 既没有「成功连接」带 headers 的 info 日志，也没有把 tool payload 打进 info 日志（payload 只进 telemetry `Arguments`）；WS 侧只上报一个 header **名**。**敏感面在 Go 侧不存在** ⇒ N/A。

### #49118 —— `6c49240565` · 2 文件 · N/A（**实为 docs-only**；且该行语义错挂）
```
$ git -C <rust> log origin/main --grep '#49118' -F --format=%H -1
6c49240565907ea587a4f64ac8e408333c0f743e
$ git -C <rust> log -1 --format=%s 6c49240565
Correct provider authentication storage documentation (#49118)          ← 标题即为文档修正
$ git -C <rust> show --stat 6c49240565
 codex-rs/core/config.schema.json        | 2 +-
 codex-rs/model-provider-info/src/lib.rs | 9 +++++----
（diff 内容 = `requires_openai_auth` 的 **doc comment** 与 **JSON schema description** 措辞）
```
**判据**：该 sha 的改动是注释 + schema 描述文案，**无任何行为**。主表第 11 行把它写成「API-key 的 cyber access program 与模型发现解耦」是**语义错挂**——那正是 #49714（已判 N/A，你已单独立 L 项）。⇒ 本行 **N/A**，并请从主表删除。

### #49144 —— `ff3c82c8a9` · 2 文件 · N/A（**Go 已实现同一门**）
```
上游门（diff 原文）：
    let origins = config.config_layer_stack.origins();
    for key in ["model_reasoning_summary", "model_verbosity"] {
        if origins.get(key).is_some_and(|origin| matches!(origin.name,
            ConfigLayerSource::SessionFlags | ConfigLayerSource::User { profile: Some(_), .. })) {
            overrides.insert(key.to_string(), serde_json::json!(effective[key]));
        }
    }

Go：app/interactive.go:2210  launchReasoningOverrides(root) → 对
    []string{"model_reasoning_summary", "model_verbosity"}（+ features.concurrent_reasoning_summaries）
    逐个 if !launchSettingForKey(layers, cliKeys, key) { continue } 后才写入 override。
    app/interactive.go:2256  func launchSettingForKey(...) bool {
        ...
        return source.Type == config.LayerSourceSessionFlags ||
               (source.Type == config.LayerSourceUser && source.Profile != nil)      ← 与 Rust 门逐字对应
```
**判据**：Go 的 `launchSettingForKey` 判据与 Rust #49144 的门**逐条一致**（`SessionFlags` 或带 profile 的 `User`），且作用键集合相同（含 `concurrent_reasoning_summaries`，正是 #49144 测试里出现的那一个）；Go 注释指向更晚的 Rust #50811 `config_request_overrides_from_config`，即该语义已被后续重构覆盖 ⇒ **已等价，N/A**。

---

## 4. 附录 B：>5 文件排除项

**无。** 主表 12 条最大 2 文件。

---

## 5. 附录 C：给队长的可操作建议

1. **只派 #49097（1 条）**。建议把「确认 lifecycle 落点」写成单内第一步（见 §1 口径未决），再按 §1 的 RC 方案取值级证据。
2. **主表 12 条已全部闭合**：1 条落地（#49852）、1 条可派（#49097）、10 条 N/A（含第 1 轮 3 条）。**主表可以清空**。
3. **#49118 行请删除**：sha `6c49240565` 是 docs-only，语义错挂到 #49714。建议同时复查 worklist「sha 列源自 `--grep` 自解」的做法在**多个 PR 共享同一主题**时仍会串行错挂，可加一道「标题含 `docs`/`documentation` 即降级为 N/A」的廉价闸门。
4. **两步筛的产出比很好看**：第 1 轮 6→1、第 2 轮 10→1，共筛掉 14 条无效派单。建议把本报告 §3 的「已等价型 / 无载体型 / 纯测试型 / 文档型」四分类固化为派单前的标签。

---

## 6. 复现命令（一字不改可复跑）

```zsh
R=/home/jacks/jacks_dev/codex; G=/home/jacks/jacks_dev/codex_go
git -C $G fetch origin --prune && git -C $G ls-remote origin main          # 937836c5…
git -C $G worktree add --detach /home/jacks/jacks_dev/codex_go_wt/preflight2 937836c5
for pr in 49147 49300 49411 50445 48686 49097 49118 49144 49852; do
  git -C $R log origin/main --grep "#$pr" -F --format='%h %s' -1
  git -C $R show --stat --format='' $(git -C $R log origin/main --grep "#$pr" -F --format=%H -1)
  git -C $G log origin/main --grep "#$pr" -F --oneline -1                   # 落地性复检
done
cd /home/jacks/jacks_dev/codex_go_wt/preflight2
sed -n '1857,1876p' appserver/turn_runtime.go        # #49097 缺口
grep -n 'func (r \*RuntimeRouter) finishTurnWithErrorAnalytics' appserver/turn_runtime.go
sed -n '2210p;2256,2276p' app/interactive.go          # #49144 已等价
sed -n '526,540p' eventmap/eventmap.go                # #49300 无状态
grep -rn 'toolLogPayload(' --include='*.go' . | grep -v _test    # #48686
git -C $G worktree remove /home/jacks/jacks_dev/codex_go_wt/preflight2
```
