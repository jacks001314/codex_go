# r86 过滤后可派单清单（syncl6 独立跑，2026-10-07）

> 任务来源：队长 `msg-1791366162856151600-3523`（「先产出过滤后的可派单清单」）。
> 纪律：**只读** —— 未 commit / 未 push / 未改任何 Go 源码；产物只写 `update/` 与本机 `/tmp`。
> 上游固定点：`/home/jacks/jacks_dev/codex` @ `origin/main` = `5b0b2530354052b9194156d70d4c94a439368342`；Go 侧参考点 `origin/main` = `origin/integ86` = `ed6f52b7406055444a6ac3023788b8a0f588e6b2`。

## ⚠️ 0. 文件名撞车披露（请队长裁定）

派单要求产出 `update/r86_worklist_2026_10_07.md`，但**该文件已被 syncl4 占用**（`27739 B`，etag `a445a6b5bbda7172`，见 blackboard `r86_main_advanced.worklist_landed`；本机镜像 `update/r86_worklist_2026_10_07.md` 亦为 syncl4 版本，mtime 17:45）。
按「撞文件就停下问我」的纪律，我**未覆盖它**，本清单另存为 `update/syncl6_worklist_filtered_2026_10_07.md`（**独立一遍，非复制**）。
若队长要合并进 syncl4 那版，请给指令（我会带 syncl4 的 etag 做追加式合并，而不是整体覆写）。

## 1. 口径与复跑命令（全部本次实跑）

```bash
# ① 候选语料（派单点名的 5 类输入）
cd /home/jacks/jacks_dev/codex_go
grep -ohE '#[0-9]{4,6}' update/remaining_ledger_2026_10_07.md update/queue_r86_2026_10_07.md \
  update/triage_2026_10_07.md update/triage2_2026_10_07.md update/triage3_2026_10_07.md \
  update/triage4_2026_10_07.md | tr -d '#' | sort -un | wc -l          # 555

# ② 自解 sha（台账 sha 列不采信）
git -C /home/jacks/jacks_dev/codex log --grep '#<PR>' -F --format=%H -1 origin/main

# ③ 改动文件数
git -C /home/jacks/jacks_dev/codex show --name-only --format= <sha> | grep -c .

# ④ 已落地判据（Go 侧）：提交标题含 (#PR) 或 .go 源码含 #PR
git -C /home/jacks/jacks_dev/codex_go log --grep '#<PR>' -F --format=%h -3 origin/main
git -C /home/jacks/jacks_dev/codex_go grep -l '#<PR>' origin/main -- '*.go'

# ⑤ Go 载体：取 Rust 新增行里最长的标识符（snake/camel/Camel 三写法）回 grep
git -C /home/jacks/jacks_dev/codex show <sha> -- '*.rs' | grep '^+' | grep -oE '\b[A-Za-z_][A-Za-z0-9_]{11,}\b' | sort -u
```

### 收敛漏斗（本次实跑计数）

| 步骤 | 条数 |
|---|---|
| 语料 PR token（去重） | **555** |
| 在 Rust `origin/main` 上解到 sha | 555（100%） |
| `≤5 文件` | **274** |
| ├ 其中「有生产 `.rs` 改动 **且** Go 无提交 **且** Go 源码无 `#PR` 引用」 | **100** |
| └ 再剔除已进 r86 补丁 19 个 PR + 5 组聚类成员 + 本队已判 N/A | **78 条候选** |
| ├ 去重后仍有生产 `.rs` 改动 | 70 |
| └ 自动定位到 Go 侧疑似载体（1 条以上 grep 命中） | 69 |
| **本清单主表** | **15 条**（12 条有载体 + 3 条标「需人审」） |

**已剔除的 19 个「已落地/已出补丁」PR**（`grep -ohE '#[0-9]{5}' update/r86_patches/*.patch` 原文）：
`#41050 #46279 #46319 #46328 #47074 #47411 #48100 #48158 #48611 #48772 #48819 #49127 #49269 #49675 #49702 #50177 #51556 #51595 #51627`

**已剔除的 5 组聚类**（队长口径 + `update/queue_delta_2026_10_07.md` §7）：
① Guardian v2 异步评分器（`#51396 #51400 #51378 #49257 #49792 #50066 #50273 #51065 #51070 #51133 #48725 #49294 #49793 #49812 #51139`）
② executor 侧 shell 快照（`#51350 #51347 #49360`）
③ TUI 用户核验提示视图（`#51458`）
④ windows-sandbox-service 守护进程（`#49067 #50480 #50507 #51256 #48829 #50058 #49389`）
⑤ TUI 快照 prune 体系（`#50808 #50219 #51192 #49818`）+ Git-discovery（`#49082`）

---

## 2. 主表（15 条，文件数升序；同数按 PR 号升序）

避让面口径 = 会撞 `appserver/runtime_router.go`(syncw3) / `sandbox/windowssandbox/`(syncw4) / `model/`(#49675 在飞) / `prompt/`(#49127 在飞) / `config/`(#49269 在飞)；`⚠` = 撞避让清单里的**宽面**（`appserver/`、`tui/`、`app/`、`cli/`、`sandbox/`）；`⛔` = 硬撞在飞文件。

| 序 | PR# | sha（自解） | 文件数 | 上游一句话语义 | Go 侧疑似载体（1 条 grep 证据） | 避让面 |
|---|---|---|---|---|---|---|
| 1 | `#49147` | `3a16c0b707` | 1 | `cloud-tasks/src/util.rs`：`normalize_base_url` 尾斜杠循环改 `trim_end_matches('/')`（行为不变）+ 回归测试 | **有**：`git grep -In NormalizeCloudBaseURL` → `chatgptapi/cloud_tasks.go` | 无 |
| 2 | `#49300` | `a6f09397aa` | 1 | `utils/stream-parser` inline hidden tag 缓冲每个 chunk 只压紧一次（性能） | **有**：`git grep -In stripInlineHiddenTag` → `eventmap/eventmap.go` | 无 |
| 3 | `#49714` | `f151a0f5c2` | 2 | API-key 的 cyber access program 与模型发现解耦（`core/src/cyber_access_program.rs`） | **有**：`git grep -In CyberAccessProgram` → `appserver/access_program_test.go`、`appserver/agent_controller.go` | ⚠ `appserver/` |
| 4 | `#49708` | `4f699cd642` | 2 | session index 的 I/O 移出 async runtime 线程 | **有**：`git grep -In SessionIndexEntry` → `rollout/session_index.go` | ⚠ `rollout/` |
| 5 | `#49097` | `9563713df2` | 2 | compaction 两条路径在 `UsageLimitExceeded` 时补发 turn error lifecycle、保留已完成回答 | **有**：`TurnRuntimeCodexErrorUsageLimitExceeded` → `tui/chatwidget/turn_runtime.go:69` | ⚠ `tui/` |
| 6 | `#50105` | `e58b493329` | 2 | TUI chat composer footer 逻辑集中到 `footer_state` | **有**：`QuitShortcutReminder` → `tui/bottom_pane/chat_composer/footer_state.go:18` | ⚠ `tui/` |
| 7 | `#50359` | `91168365a5` | 2 | TUI hook 系统消息渲染 ANSI 样式 | **有**：`HookEventName` → `appserver/bundled_hooks.go` | ⚠ `appserver/` |
| 8 | `#49076` | `011f803f3c` | 3 | skill analytics 不再收集无用 Git metadata | **有**：`SanitizedGitUrl` → `gitutil/gitutil.go` | ⚠ `app/`（analytics 面） |
| 9 | `#49811` | `c51f5bfb82` | 3 | exec-server 处理不支持的 `fs/writeBlock` 请求 | **有**：`git grep -In writeBlock` → `execserver/client.go`、`execserver/server.go` | ⚠ `execserver/` |
| 10 | `#49444` | `8c3612fb63` | 3 | 反向 JSONL 扫描改用 `memrchr` 找换行（性能） | **等价物有**：`bytes.LastIndexByte` → `rollout/projection.go:51` | ⚠ `rollout/` |
| 11 | `#49308` | `50d9c5deac` | 3 | 管道式 legacy Windows sandbox 进程不带 console 运行 | **有（弱）**：`CREATE_NO_WINDOW` → `sandbox/windowssandbox/process_windows.go:74` | ⛔ `sandbox/`（syncw4 在改）+ windows-only |
| 12 | `#48799` | `4c8cf3964d` | 3 | 修复 Windows 终端捕获的 SGR mouse reporting | **需人审**（`tui/` 有 style/overlay 面，未定位到 SGR 报告点） | ⚠ `tui/` |
| 13 | `#49043` | `4f63088cce` | 3 | TUI 更新 Pro plan 显示名 | **需人审**（`tui/chatwidget/transcript.go:12 PlanProgress` 是邻近但非同名面） | ⚠ `tui/` |
| 14 | `#49041` | `3074be908a` | 5 | TUI 行内代码选区按纯文本复制 | **需人审** | ⚠ `tui/` |
| 15 | `#49624` | `7219fd735b` | 5 | 显式远程会话命令使用服务端认证 | **疑似无**：`git grep -In 'RemoteAppServerConnect' app/ cli/` → 0（需先定位会话入口） | ⚠ `cli/`+`app/` |

### 3. 建议最先派的 3 条（我的排序理由）

1. **`#49147`**（1 file，`chatgptapi/`，**零避让面**）：Rust 侧是纯语义等价重写 + 一个回归测试，Go 侧 `NormalizeCloudBaseURL` 就在 `chatgptapi/cloud_tasks.go` —— 最小、最干净、RC 好做（尾斜杠/空串边界的行为级断言）。
2. **`#49300`**（1 file，`eventmap/`，**零避让面**）：性能类但可观测（per-chunk 压紧次数可用计数器/探针断言），Go 侧 `stripInlineHiddenTag` 已有同构函数，属「改一处 + 补测试」。
3. **`#49097`**（2 files，`tui/`）：**唯一**一条有明确 `file:line` 载体（`tui/chatwidget/turn_runtime.go:69`）的 TUI 项，且语义清楚（usage-limit 时补发 turn error lifecycle）；⚠ 需先与 TUI 车道对表（`tui/` 在避让清单）。

> 备选：若队长想优先「零避让面」，第 3 位可换成 **`#49076`**（3 files，载体 `gitutil/gitutil.go`，analytics 面）或 **`#49444`**（3 files，`bytes.LastIndexByte` 等价物已存在）。

## 4. 判 N/A / 待裁定（含决定性命令，供剔除）

| PR# | sha | 文件数 | 结论 | 复跑命令 + 实际输出 |
|---|---|---|---|---|
| `#49411` | `eefe0ce1a8` | 1 | **机制性 N/A**（app-server 把 time provider 绑到局部变量，是 Rust 借用期问题；Go 无该概念） | `git grep -In 'timeProvider' -- '*.go'` → 仅测试桩，无生产绑定点 |
| `#49171` | `c248f6d48b` | 1 | **机制性 N/A**（TUI history 取 model provider 路径在 Go 不存在） | `git grep -In 'HistoryModelProvider\|history_model_provider'` → **0** |
| `#50445` | `d4eed6dca5` | 1 | **待裁定**（Rust 是「只有直接工具调用发 timing 事件」的断言测试；Go 侧无同名 timing 事件名） | `git grep -In 'toolCallTiming' -- '*.go'` → 0 |
| `#49019` | `4fd574e848` | 5 | **机制性 N/A**（Windows MXC 沙箱的 PowerShell 回退；Go 有 `execserver/windows_powershell_fallback.go` 但与 `POWERSHELL_FALLBACK_PATHS` 无语义锚点） | `git grep -In 'POWERSHELL_FALLBACK_PATHS'` → 0 |
| `#49798` | `c538fbabe5` | 2 | **已判 N/A（Go 已覆盖）**（exec-server `EnvironmentInfo` OnceCell 语义，台账/§85 裁过） | `git grep -In 'EnvironmentInfo' -- execserver/` → 命中（面已在） |

## 5. 口径发现（重要 · 台账 sha 列的**语义**也需复核）

台账 §5 有一行的**语义列**与 **sha 列**不匹配，属「台账不可信」的新实例：

```
$ git -C /home/jacks/jacks_dev/codex log --grep='#49118' -F --format='%H %s' origin/main
6c49240565907ea587a4f64ac8e408333c0f743e Correct provider authentication storage documentation (#49118)
$ git -C /home/jacks/jacks_dev/codex log --grep='#49714' -F --format='%H %s' origin/main
f151a0f5c20e50055957b79d50e16834e00e32e1 Decouple API-key cyber access programs from model discovery (#49714)
```

`update/remaining_ledger_2026_10_07.md` §5 的 `#49118` 行语义写作「API-key 的 cyber access program 与模型发现解耦」，但该语义实属 **`#49714`**；`#49118` 本体是文档/schema（`core/config.schema.json` + `model-provider-info/src/lib.rs`，+6/−5）。
**影响**：syncl4 的 worklist 主表第 11 行把该语义与载体 `appserver/access_program_test.go:19` 挂在 `#49118` 上 → 应改挂 `#49714`。本清单已按正确归属列在第 3 行。
（推论：`≤5 文件` 的筛选基于 sha，不受影响；受影响的只有「PR# ↔ 语义」的对应关系。）

## 6. 计数结论 / 情报

- 输入语料 **555** 个 PR token，其中 **274** 条 ≤5 文件（可派单粒度），**100** 条同时满足「有生产 `.rs` 改动 + Go 无提交 + Go 源码无引用」，剔除已落地/聚类后剩 **78 条候选**。
- 78 条里我自动定位到 Go 侧疑似载体的有 **69** 条 → **可派单池远不止 12 条**，但其中 **大量落在 `tui/`（宽避让面）**；真正「零避让面」的高质量小项是少数（主表前 2 条 + `#49444`/`#49076`）。
- 本清单是**机械定位 + 单点 grep**，未逐条做行为级语义对比；主表第 12–15 行标「需人审」即未达证据级，派单前需一次人工比对。

## 7. 未决 / 需队长裁定

1. 本文件与 syncl4 的 `update/r86_worklist_2026_10_07.md` 的**合并或双轨**（见 §0）。
2. 主表 12–15 行是否继续投入人工比对，还是直接从候选池剔除。
3. `tui/` 面条目（`#49097 #50105 #48799 #49043 #49041`）是否派给 TUI 专责车道，而非本车道。
