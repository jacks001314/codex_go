# r86 载体预检 round 4（syncl4，Linux 节点，2026-10-07）

> 派单：队长 `msg-1791370398478455500-4379`（「round 4，最后一个未穷尽的候选池」）。
> 对象：**`update/r86_worklist_2026_10_07.md` 附录 E（71 条 ≤5 文件全表）**。
> 判据（五分类，队长已采纳）：① 有无真实 Go 生产载体；② 是否已等价 / 前置齐备 / 被更晚 PR 取代；
> 标签 = `已等价型` / `无载体型` / `已取代型` / `纯测试型` / `文档型`。
> 纪律：**只读**；不写 Go 代码、不 commit/push、不改 `update/` 下其它文件。临时 worktree `preflight4`。

## 0. 固定点自检（原文）

```
$ cd /home/jacks/jacks_dev/codex_go && git fetch origin --prune && git rev-parse origin/main
4ee41b7ad414d2d21be1305465a70e0ce5642b90
$ git ls-remote origin main
4ee41b7ad414d2d21be1305465a70e0ce5642b90	refs/heads/main
$ git rev-parse origin/main   # Rust
5b0b2530354052b9194156d70d4c94a439368342
```

落地判据（`git log --grep '#PR' -F` on `4ee41b7a`），71 条实际命中 **20 条**（下 §1）。

---

## 1. 已落地（20）· 原文

| PR | Go 提交 |
|---|---|
| #49147 | `5078de6d sync610: cover cloud base URL normalization like Rust (#49147)` |
| #49852 | `84518d3f sync601: log diagnostics for skipped feedback attachments (#49852)` |
| #50454 | `7bfe8349 sync594: measure rollout persistence size reductions (#50454)` |
| #48828 | `0a1a21cc sync586: assert the archived promptless thread on the RPC path (#48828)` |
| #49097 | `04d1426a sync609: emit a usage-limit turn error when post-turn compaction fails (#49097)` |
| #50359 | `eeca361a sync605: render hook system messages with ANSI styles (#50359)` |
| #49058 | `f3da80d9 sync581: repair Windows sandbox ACLs for long runtime paths (#49058)` |
| #48982 | `937836c5 sync602: do not let message-board notifications reopen a final answer (#48982)` |
| #48983 | `b2a153b0 syncw1: keep timestamp-only thread metadata observations narrow (#48983)` |
| #49432 | `5c0e02cd sync614: revoke the application network policy when the authenticated owner changes (#49432)` |
| #49785 | `47f0e558 sync612: persist empty paginated threads when naming them (#49785)` |
| #49951 | `9620629e sync613: include preceding assistant context in Guardian sender reviews (#49951)` |
| #50200 | `eb9d894c syncw2: report the configured TUI mode in doctor (#50200)` |
| #51185 | `9c4d578f sync598: retry transient gRPC code-mode session admission (#51185)` |
| #49079 | `106ce625 sync604: centralize TUI subscription labels (#49079)` |
| #49145 | `9fafc187 syncw2: hide reasoning summaries in the /status card for server connections (#49145)` |
| #49912 | `985ccf59 sync611: respect approval policies in temporary structured threads (#49912)` |
| #50788 | `1c2ab6b0 sync561: open slash commands from empty drafts in Vim Normal mode (#50788)` |
| #50803 | `adee1b30 sync565: use the managed daemon for eligible remote-control launches (#50803)` |
| #50811 | `8cc01f75 sync507: forward launch reasoning-summary choices to remote thread starts (#50811)` |

> `#48565` 命中的是一条 **plan/triage 提交**（非落地），与本仓附录 C 的「假阳性已 reverse 为 N/A」一致 ⇒ **N/A（假阳性）**。

---

## 2. 本轮新增判定（证据 → 结论）

### 2.1 N/A（决定性证据，本轮新判）

**`#49946`** — N/A · 无载体型。Rust 是**异步 matcher**「结果标注了旧 query」的修复（新增 `snapshot.rs::for_query`，按 pattern atoms 匹配）。
```
$ grep -n '^func ' filesearch/filesearch.go
74:func DefaultOptions() Options {
78:func Run(ctx context.Context, pattern string, roots []string, options Options) (*Results, error) {
157:func Score(pattern string, candidate string) (int, []int, bool) {
```
Go 的 `filesearch.Run` 是**同步单发**函数（一次调用返回 `*Results`），**无异步 matcher、无 pending 结果、无 pattern atoms** ⇒ Rust 的「新旧 query 错配」bug 类不存在。**N/A（无载体型）。**

**`#50396`** — N/A · 已等价型。Rust 让 transcript 的 `PageUp/PageDown` 走 pager keymap 绑定（可整页/半页/一行/未绑定）。
```
$ sed -n '8077,8105p' tui/tea/model.go
// updatePagerOverlayKey applies the pager keymap to an open overlay.
func (m *Model) updatePagerOverlayKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	...
	for _, action := range actions {          // scroll_up/down, page_up/down, half_page_up/down, jump_top/bottom
		if m.keyMatches("pager", action, keySpec) {
			m.overlay.ApplyPagerAction(action)
```
```
$ grep -n 'pager' tui/keymap.go
138:	keymapAction("pager", "Pager", "page_up", "Scroll up by one page.", []string{"page-up", "shift-space", "ctrl-b"}),
139:	keymapAction("pager", "Pager", "page_down", "Scroll down by one page.", []string{"page-down", "space", "ctrl-f"}),
140:	keymapAction("pager", "Pager", "half_page_up", "Scroll up by half a page.", []string{"ctrl-u"}),
141:	keymapAction("pager", "Pager", "half_page_down", "Scroll down by half a page.", []string{"ctrl-d"}),
```
Go **已**用 `keyMatches("pager", action, …)` 动态解析（用户重绑定即时生效），并已有 half/one-row/jump 动作 ⇒ **已等价型 → N/A。**

**`#49692` / `#49694`** — N/A · 无载体型。Rust 把 rollout 扫描分到「可取消的 blocking worker」。Go 源码注释**直接说明该差异是结构性的**：
```
$ sed -n '99,104p' rollout/compressed_test.go
// Go performs the whole read/parse pass synchronously on the caller's
// goroutine, so Rust's "one blocking worker" split is structural; the
// observable equivalence asserted here is the property the Rust change added
// tests for.
```
⇒ Go 无 blocking-worker 池 ⇒ **无载体型 → N/A（两条）。**

**`#49489`** — N/A · 纯测试型。生产改动只有「删除 2 个未用 import」（`registry.rs -1`、`internal_identity.rs -1`），其余 +108 行全在 `client_tests.rs`。**纯测试型 → N/A。**

**`#50189`** — N/A · 无载体型。Rust 改 rmcp-client OAuth 的**provider 例外白名单**（Figma → Mercado Pago）。
```
$ grep -rni --include='*.go' 'mercadopago\|figma' .
(无输出)
$ grep -rn --include='*.go' 'issuer' appserver/mcp_config_refresh.go | head -2
274:		OAuthAuthorizationServerIssuer string `json:"oauth_authorization_server_issuer"`
```
Go 的 MCP OAuth 面（`appserver/mcp_config_refresh.go`）**无 provider 例外清单**（0 命中 figma/mercadopago）⇒ **无载体型 → N/A。**

**`#51140` / `#51139`** — N/A · 无载体型（Guardian-v2 异步评分器族）。Rust 侧标识符在 Go 0 命中：
```
$ grep -rn --include='*.go' 'ReviewerSelection\|FreshParentCheckpoint\|ReuseIfAvailable\|restartFromParentCheckpoint' .
(无输出)
```
Go 的 guardian 面只有 `state/guardian_retained_context.go` 与 `appserver/guardian_reviewer.go` 的 `guardianScoreProgress`，**无 reviewer-pool / session-reuse 选择器** ⇒ **无载体型 → N/A（两条）。**

**`#49416`** — N/A · 无载体型。Rust 改 `ansi-escape` crate 的 `ansi_escape_line` 告警日志（去掉 payload）。
```
$ grep -rn --include='*.go' 'ansiEscapeLine\|ansi_escape_line\|AnsiEscapeLine' .
(无输出；仅 tui/tea/unarchive_prompt_test.go 有一个测试用 regexp)
```
Go 无 ANSI-escape 告警日志模块 ⇒ **无载体型 → N/A。**

**`#50431`** — N/A · 无载体型（弱证据）。Rust 给 agents-overview 预览渲染 `HyperlinkLine::plain_hyperlink_lines` / `remap_wrapped_line`。
```
$ grep -rn --include='*.go' 'plainHyperlinkLines\|remapWrappedLine\|markBufferHyperlinks\|renderMarkdownLinesWithWidthAndCwd' app/ tui/
(无输出)
```
Go **有** `tui/history_cell/base.go:16 type HyperlinkLine` 通用基础设施，但 **agents-overview 预览**没有对应的 markdown-link 重映射/裁剪渲染点 ⇒ 该 PR 的具体落点缺失。标记 **无载体型（弱）→ N/A**，如需可深挖一次。

**`#49786`** — 见 §2.3（疑已等价）。

### 2.2 疑似可落地（载体已定位，**未做行为级比对**，不建议直接派单）

> 以下每条都**有真实 Go 载体**，但「是否已等价」我尚未逐条做值级比对。建议**先派一条 15 分钟的载体比对单**再决定，避免重蹈「3/3 全 N/A」。

| PR | 文件数 | Rust 语义 | Go 载体（file:line） | 疑似缺口 |
|---|---|---|---|---|
| `#51400` | 5 | later Guardian score 不得释放更早的 pending review（按来源 action 限定释放） | `appserver/guardian_reviewer.go:107 guardianScoreProgress{latestToolCall, latestScoredToolCall}` / `:145 markScored`（无来源 action 约束） | 中 |
| `#49280` | 4 | capability roots 限定为当前 turn 捕获的 environments（去 stale） | `appserver/environment.go:35 maxSelectedCapabilityRoots` / `:50 SelectedCapabilityRoots` | 中 |
| `#49130` | 4 | content-filter guidance 记录移到共享 Responses retry handler | `exec/exec.go:5889 execContentFilterGuidanceSessionItems`（注释「after every block」= 采样循环内记录） | 中 |
| `#49584` | 4 | Guardian 审查跳过 host skill discovery | `appserver/required_skills_test.go:134 guardian` + `router.requiredSkillsPreSamplingValidation` | 中 |
| `#49993` | 4 | Guardian async history prefix 作为 retained context 保留 | `state/guardian_retained_context.go:59 RetainedContextEntry` | 中 |
| `#50781` | 5 | TUI MCP 启动通知只发给自有线程 | `appserver/notifications.go:35 mcpServer/startupStatus/updated`（⚠ 落 `runtime_router.go`，撞 syncl1/syncw3） | 中 |
| `#50416` | 4 | 新建/分叉会话的 Git worktree 选择说明 | `tui/tea/worktree_browser_test.go:24 worktreeChoices` | 中 |
| `#49467` | 5 | login shell 启动后恢复 executor 工具路径 | `shell/snapshot_capture.go:25 SnapshotStartupInteractive` / `exec/exec.go:1302 login_shell_package_path` | 中 |
| `#51458` | 2 | 用户核验提示里的 URL 可点击（HyperlinkText） | `appserver/user_verification.go:21`（**RPC 面有**；TUI 提示视图未定位） | 中 |
| `#49800` | 2 | replay-only 侧会话 thread 缺失时仍完成 cleanup | `tui/tea/model.go:825 closedSide *activeSideConversation`（`replay-only` 概念 0 命中） | 低（疑无载体） |

### 2.3 需人审 / 证据不足（**未完成**，不得派单）

| PR | 文件数 | 状态 |
|---|---|---|
| `#49786` | 3 | **疑已等价**：Go 无 `SPAWN_AGENT_INHERITED_MODEL_GUIDANCE_V2`；该语义由 `agent/multi_agent_v2_hint.go:43 MultiAgentV2ModelOverrideUsageHint`（`:94` 无条件并入 usage hint）表达 ⇒「不显式要求就不设 model」Go **已覆盖**。载体在 `agent/tools_v2.go`（⚠ 归 syncw1）。 |
| `#48686` | 2 | 未定位：Rust 去掉 WS 响应头日志 + tool payload 预览日志；Go 侧未找到对应日志点 |
| `#48775` | 4 | 未定位：Rust 给 pinned transcript header 加 `› ` 前缀；Go 未找到对应命名 |
| `#49810` | 4 | 未定位：Go 主 composer 仅有 `pasteEnterUntil *time.Time`（**声明后未使用**）；`PasteBurst` 只接入 CustomPromptView/FeedbackView |
| `#50389` | 4 | 未定位（仅 `parity/rust_tui_snapshot_manifest_test.go:566` 快照引用） |
| `#49564` | 5 | 未定位：Go `copyPath`（execserver/fs_helper.go）是**文件复制**，非 TUI 路径复制 |
| `#49678` | 5 | 未定位（仅 `tui/tea/model.go:1643 asyncQuestionAnswerInFlight`） |
| `#49799` | 5 | 未定位：Go 有 web_search（`exec/exec.go:983 isExecWebSearchInvocation`），TUI 侧设置保留未定位 |
| `#50510` | 5 | 未定位：Go 有 bedrock provider（`plugin/curated_routing.go:14`），GovCloud 确认门未定位 |
| `#50564` | 5 | 未定位（Go 有 modal+selection，`tui/tea/modal.go`） |
| `#50756` | 4 | 未定位（仅 parity 快照注释引用） |
| `#50531` | 4 | **in-flight（他车道）**：载体 `appserver/realtime_runtime.go` 归 syncl1（你已点名 #50531 在飞）⇒ 不派 |

---

## 3. 分类汇总（71 行，互斥且完全划分）

| 分类 | 条数 | PR |
|---|---|---|
| 已落地 | **20** | §1 全表 |
| N/A（rounds 1–3 / 各车道已判） | **18** | #49171 #49261 #49300 #49411 #50445 #49082 #49118 #49144 #49708 #49714 #49798 #49043 #49308 #49444 #49811 #49019 #49041 #49624 |
| N/A（本轮新判，决定性证据） | **10** | #49489 #49946 #50396 #49692 #49694 #50189 #51140 #51139 #49416 #50431 |
| N/A（假阳性） | **1** | #48565（plan/triage 提交，非落地） |
| 疑似可落地（待载体比对） | **10** | #49800 #51458 #49130 #49280 #49584 #49993 #50416 #49467 #50781 #51400 |
| 需人审 / 未完成 | **11** | #48686 #49786 #48775 #49810 #50389 #49564 #49678 #49799 #50510 #50564 #50756 |
| in-flight（他车道） | **1** | #50531 |

> 校验：20 + 18 + 10 + 1 + 10 + 11 + 1 = **71 ✔**（与附录 E 逐条对齐）。

## 4. 可派性排序（建议顺序 + 落点 + 车道）

> ⚠ **前置条件**：§2.2 十条均**未做行为级比对**。建议先派 **1 条「载体比对」单**复核该批，再决定照搬。下列排序只反映「文件数小 × 落点窄 × 无避让面」。

| 序 | PR | f | 预计落点 | 建议车道 | 理由 |
|---|---|---|---|---|---|
| 1 | `#49800` | 2 | `tui/tea/` / `tui/chatwidget/` | syncl1 或 TUI 车道 | 最小；但疑无载体，先比对 |
| 2 | `#51458` | 2 | `tui/`（用户核验视图） | TUI 车道 | 2 文件，UI 落点 |
| 3 | `#49130` | 4 | `exec/exec.go` | syncl1 / exec 车道 | 核心 runner，零避让面 |
| 4 | `#50416` | 4 | `tui/tea/worktree_browser.go` | TUI 车道 | 说明文案，低风险 |
| 5 | `#49280` | 4 | `appserver/environment.go` | syncw3 / app-server 车道 | 环境面，需与 runtime_router 避让核对 |
| 6 | `#49584` | 4 | `appserver/` required skills | syncw3 | Guardian 面 |
| 7 | `#49993` | 4 | `state/guardian_retained_context.go` | syncl3（#51627 同域） | 与已有 retained-context 工作同域 |
| 8 | `#51400` | 5 | `appserver/guardian_reviewer.go` | syncw2 / Guardian 车道 | Guardian-v2 评分器，需与大族一起裁 |
| 9 | `#49467` | 5 | `shell/snapshot_capture.go` + `exec/exec.go` | syncl3（shell 快照域） | ⚠ 撞 executor 快照聚类 |
| 10 | `#50781` | 5 | `appserver/notifications.go`(+runtime_router) | syncw3 | ⚠ 硬撞 `runtime_router.go`（syncl1 在飞） |

---

## 5. 未决 / 建议

1. **本轮为「筛选」而非「深审」**：§2.2 十条只做了载体定位 + 决定性 grep，**未做值级行为比对**；§2.3 十一条**未完成**（明确列出，勿派）。
2. **建议**：对 §2.2 先派**一轮载体比对**（每条约 15 分钟：读 Rust diff + Go 载体 file:line 行为对照），产出真正的「可派 5–6 条」。这比直接派单更省返工。
3. **口径修正建议**：本仓附录 E 的 `#49147/#49852/#50454/#48828/#49058/#48982/#48983/#49432/#49785/#49951/#50200/#51185/#49079/#49145/#49912/#50788/#50803/#50811` 共 18 条已落地，建议在 worklist 里标注「已闭合」，避免下轮重复扫。
4. 未发现新的「台账 sha 列」错配（本轮 71 条 sha 全部自解一致）。

---

## 6. 环境与纪律

- 工作树：`/home/jacks/jacks_dev/codex_go_wt/preflight4`（detached @ `4ee41b7a`，只读；报告落盘后 `git worktree remove`）。
- 未改任何 Go 源码；未 commit/push；未动 `update/` 下其它文件；Rust 仓只读。
