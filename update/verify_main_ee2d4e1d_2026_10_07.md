# 独立发布审计 — main @ `ee2d4e1d`（`e9849503..ee2d4e1d`，批次十五 2 笔）

- **审计员**：synct5（Windows 节点，只读审计）
- **日期**：2026-10-07
- **被审对象**：`ee2d4e1db819e1d706eaea8b5da0386e85ea1a42`
- **基线**：`e9849503`
- **总判定：GREEN** —— 2 笔全部真实（union==range=7、无夹带、无 CRLF）；tree-sha 握手一致；门禁**新增失败 0**；**两笔逐笔行为可观测**；sync640 的 managed 需求**确实接线**（端到端探针）。无「不实/夸大」。
- 全程 **0 commit / 0 push / 0 ref 移动**；重放走 `cherry-pick -n`。**未尝试任何递归删除**（尊重本机策略）。

## §1 ① 远端对齐 + tree-sha 握手

```
$ git ls-remote origin refs/heads/main refs/heads/integ86g
ee2d4e1db819e1d706eaea8b5da0386e85ea1a42  refs/heads/integ86g
ee2d4e1db819e1d706eaea8b5da0386e85ea1a42  refs/heads/main      # 两者同值 ✅
$ git merge-base --is-ancestor e9849503 ee2d4e1d ; echo $LASTEXITCODE   -> 0
$ git log --merges --oneline e9849503..ee2d4e1d                          -> (空) ✅
$ git rev-list --count e9849503..ee2d4e1d                                -> 2
$ git diff --shortstat e9849503 ee2d4e1d
 7 files changed, 361 insertions(+), 4 deletions(-)
$ git show -s --format='%T | %P' ee2d4e1d
d69b63b57f276afebebb09879ad5e16b7f86efc5 | 1a4658233801ad8e66ee4c45ad7b97ce060c2982

$ git -c core.autocrlf=false -c core.eol=lf worktree add D:\tmp\aud8_replay --detach e9849503
$ git cherry-pick -n 1a465823 ee2d4e1d ; git add -A ; git write-tree
d69b63b57f276afebebb09879ad5e16b7f86efc5      # == ee2d4e1d^{tree} ✅（暂存 7 文件 == range）
```

### §1.1 逐笔范围（`git show --stat` + `--name-status` 原文）

```
1a465823 sync639: classify queue-dispatched turns with turn_trigger=queue like Rust (#40665)
 M appserver/queued_dispatch_test.go      | 80 +++++        （注意：是**已存在文件的追加**，非新文件）
 M appserver/runtime_router.go            | 10 +-          （+8/−2）
 2 files changed, 88 insertions(+), 2 deletions(-)

ee2d4e1d sync640: honor the managed windows.allow_mxc requirement like Rust (#49642)
 M config/api.go                           |  11 +-
 M config/requirement_constraints.go       |   5 +
 M config/requirements_file.go             |   7 +
 A config/windows_mxc_requirements_test.go | 225 +++++++++   （新文件，与派单一致）
 M config/windows_sandbox_mode.go          |  27 ++++
 5 files changed, 273 insertions(+), 2 deletions(-)
```
（派单把 `queued_dispatch_test.go` 写作「+80」——准确；但它不是新增文件，基线里已存在，本笔是追加。tree 文件数 head 3827 / base 3826，Δ1 = 新增的 `windows_mxc_requirements_test.go`。）
## §2 ② LF 树门禁

```
$ git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o D:\tmp\head8.tar ee2d4e1d ; tar -xf -C D:\tmp\aud8_head
$ git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o D:\tmp\base8.tar e9849503 ; tar -xf -C D:\tmp\aud8_base
head files=3827   base files=3826      # Δ1 = 本批唯一新增文件
```

**head @ `ee2d4e1d`**：
```
(1) gofmt -l <本批 7 个改动文件>                       -> 输出为空, exit=0
(2) gofmt -l .                                        -> 32 行（base 同 32，纯既有基线）
(3) go build ./...                                    -> 输出为空, exit=0
(4) go vet ./appserver/ ./config/                     -> 输出为空, exit=0
(5) go test ./appserver/ ./config/ -count=1
        FAIL codex_go/appserver  158.861s   （6 条，见 §2.1）
        ok   codex_go/config       0.782s
(6) $env:CODEX_RUST_ROOT="C:\rw\codex-rs" ; go test ./parity/ -count=1 -> ok codex_go/parity 41.167s
```

**base @ `e9849503`（同命令对拍）**：
```
    gofmt -l . -> 32 行（同）;  vet ./appserver/ ./config/ -> 输出为空, exit=0
    FAIL codex_go/appserver 158.256s   （6 条已知 + 1 条偶发，见下）
    ok   codex_go/config 1.090s
```

### §2.1 appserver 对拍与一处**偶发**判定

两侧共有 6 条已知稳定红（逐条同名同子项）：
`TestExecutorSkillPathEqualsMatchesWindowsIdentityLikeRust` / `TestMultiAgentWaitDurationRecordsOutcomeLikeRust` / `TestRequiredSkillsPreSamplingValidationReleasesSatisfiedTurnsLikeRust` / `TestShellSnapshotCommandMetricsLikeRust` / `TestShellSnapshotCommandMetricsReportProtectedAndUnavailableLikeRust`(+2 子项) / `TestShellSnapshotCommandMetricsSkipNonDirectLaunchesLikeRust`。

**头侧 6 条；base 整包侧多出 1 条 `TestRuntimeRouterTurnStartRestoresThreadDynamicTools`（0.16s）**。方向是「head 绿、base 红」，不构成 head 新增失败，但仍按纪律隔离验证：
```
$ （base）go test ./appserver/ -run '^TestRuntimeRouterTurnStartRestoresThreadDynamicTools$' -count=1   ×3
run1 : ok 0.177s   run2 : ok 0.396s   run3 : ok 0.445s
$ （head）同命令 ×3
run1 : ok 0.169s   run2 : ok 0.401s   run3 : ok 0.455s
```
⇒ **整包顺序性 flaky，非回归**（两侧隔离 3/3 PASS）。建议并入已知 flaky 名单 —— 它与既有的 `TestRuntimeRouterTurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust`、`TestRuntimeRouterAppMention…` 同族（`RuntimeRouterTurnStart*`）。
**对拍结论：新增失败 0。**

## §3 ③ 逐笔真实性

```
$ git show --pretty=format: --name-only <2 笔>  -> 2 + 5 = 7（raw）
$ git diff --name-only e9849503 ee2d4e1d       -> 7
   => union == range ✅（无夹带、无遗漏；无跨笔重复文件）
$ git grep -n -P '\r' ee2d4e1d -- <7 文件>      -> (无输出，exit=1)  => 无 CRLF 污染 ✅
```
## §3.1 逐笔行为可观测性探针（head 测试文件放 base 实现上）

```
# sync639 —— 编译通过，行为性失败（head 侧同命令 2/2 PASS）
$ （base + head 的 appserver/queued_dispatch_test.go）
--- RUN   TestRuntimeRouterQueuedDispatchCarriesQueueTurnTriggerLikeRust
    queued_dispatch_test.go:168: idle queued dispatch turn trigger = "", want queue
--- FAIL: TestRuntimeRouterQueuedDispatchCarriesQueueTurnTriggerLikeRust (0.37s)
--- RUN   TestRuntimeRouterThreadQueueStartCarriesQueueTurnTriggerLikeRust
    queued_dispatch_test.go:209: thread/queue/start turn trigger = "", want queue
--- FAIL: TestRuntimeRouterThreadQueueStartCarriesQueueTurnTriggerLikeRust (0.28s)
FAIL codex_go/appserver 1.067s

# sync640 —— 符号级证据
$ （base + head 的 config/windows_mxc_requirements_test.go）
config\windows_mxc_requirements_test.go:140:17: requirements.AllowMXC undefined
        (type ConfigRequirements has no field or method AllowMXC)
FAIL codex_go/config [build failed]
```
⇒ 两笔均**行为可观测**，无「纯注释/改名」。

## §3.2 队长重点 A —— sync640 的两种写法 + 自动选择门

**独立探针**（`go test -overlay`，探针文件在 `D:\tmp\probe8\`，**不落仓**）：
```
$ go test -overlay=D:\tmp\probe8\overlay_head_config.json ./config/ -run TestSynct5ProbeManagedMXC -count=1 -v
  PROBE parse snake=false camel=false both(snake=false,camel=true)=false true=true
  PROBE explicit-mxc + req(false) err=windows.sandbox = "mxc" is not allowed when the managed requirement windows.allow_mxc = false
  PROBE explicit-mxc + req(true)  err=<nil>
  PROBE explicit-mxc + req(nil)   err=<nil>
  PROBE explicit-unelevated + req(false) err=<nil>
  PROBE automatic req(false)=false req(true)=true req(nil)=true localoptout=false
  PROBE ResolveWindowsSandboxMode(mxc, req(false)) = "mxc" fellBack=false ok=true
PASS ok codex_go/config 0.038s
```

**端到端探针（真实 load 路径，最强证据）**：
```
$ go test -overlay=D:\tmp\probe8\overlay_head_e2e.json ./config/ -run TestSynct5ProbeEndToEndManagedMXC -count=1 -v
  PROBE e2e mxc + req(allow_mxc=false)  : cfg==nil=true  err=windows.sandbox = "mxc" is not allowed when the managed requirement windows.allow_mxc = false
  PROBE e2e mxc + req(allow_mxc=true)   : cfg==nil=false err=<nil>
  PROBE e2e mxc + no requirements.toml  : cfg==nil=false err=<nil>
  PROBE e2e elevated + req(allow_mxc=false): cfg==nil=false err=<nil>
PASS ok codex_go/config 0.034s
$ （同一探针跑 base = 「改动前」）         -> 四例全部 cfg==nil=false err=<nil>
```
**结论**：
1. **两种写法都生效**：`allow_mxc` 与 `allowMxc` 均被解析（`config/requirements_file.go:328` `boolAnyKey(nested, "allow_mxc", "allowMxc")`）；两者同时出现时 **snake_case 优先**（`anyKey` 按参数顺序取首个命中，实测 `both(snake=false,camel=true)=false`）。
   - 补充：Rust `WindowsRequirementsToml`（`config/src/config_requirements.rs:873` 起）**没有 serde alias**，即 Rust 只认 `allow_mxc`，`allowMxc` 会被 serde 当未知字段忽略 ⇒ Go 的 camelCase 接受是**本仓既有惯例的扩展**（所有 requirements 键都走 AnyKey 双拼写），且**两者并存时的结果与 Rust 恰好一致**（Rust 取 `allow_mxc`，忽略 `allowMxc`）。属良性偏差，建议在偏差台账记一笔。
2. **显式 `windows.sandbox = "mxc"` 的拒绝是真接线**：`ValidateManagedWindowsMXCOptOut` 被 `applyManagedConstrainedOverrides`（`config/requirement_constraints.go:70`）调用，而后者在 `config/config.go:375` 的 load 路径上、于 `values` 合并完用户配置之后执行 ⇒ 端到端探针已证明整机 load 失败，且错误文案与 Rust `ConstraintError::InvalidValue{allowed: "windows.allow_mxc = false"}` 对齐。非 mxc 模式不受影响。
3. **自动选择门 `WindowsAutomaticMXCAllowedWithRequirements`：判定 = 「语义冻结」，不是漏接线**。依据：
   - 全仓 `rg` 该符号：只有**测试**调用方（`config/windows_mxc_requirements_test.go`）+ 定义本身；兄弟函数 `WindowsAutomaticMXCAllowed`（#51547 引入）同样只有测试调用方 —— 这是**既有形态**，不是本批新引入的孤岛。
   - **Go 根本不存在「自动选中 MXC」的路径**：`WindowsSandboxModeFromValues`（`config/windows_sandbox_mode.go:29`）只读显式配置（`windows.sandbox` → 旧键 → 旧 feature 开关，后两者只映射 elevated/unelevated），从不返回 mxc；`ResolveWindowsSandboxMode` 只在「显式 mxc」时返回 mxc。`features.prefer_mxc` 在 Go 里仅作为**配置元数据**传递（`app/app.go:926` → `execserver.SetPreferMXC` → `execserver/hostconfig/read.go:60` 回注 session-flags 层），没有任何消费点把它转成 MXC 后端。
   - Rust 侧的对照：`core/src/config/mod.rs:3541` `prefer_mxc = features.enabled(Feature::PreferMxc) && config_allows_mxc(...) && windows_mxc_available()` → `resolve_windows_sandbox_type(configured, prefer_mxc)`（`windows_sandbox_config.rs:33`：`if prefer_mxc { SandboxType::WindowsMxc }`）。Go 缺的正是这条链路，故新函数的门**无物可门**。
   - 该「Go 从不隐式选 MXC」的语义由 `config/windows_mxc_optout_test.go:81-105` **测试冻结**（sync389 时已记录），`features/features.go:296` 亦标记 `prefer_mxc` 为 `StageUnderDevelopment / DefaultEnabled=false`。
   ⇒ 建议措辞：**「#49642 的显式拒绝已接线；自动选择门随 Go 尚无 prefer_mxc→MXC 链路而语义冻结」**，并作为**已知 Go↔Rust 功能缺口**入台账（若日后实现 prefer_mxc 自动选择，必须改用 `WindowsAutomaticMXCAllowedWithRequirements`）。
4. **一处潜在不对称（建议，不阻塞）**：`ResolveWindowsSandboxMode(mxc, req(false))` 仍返回 `"mxc"`（探针实测）。当前安全，因为 load 期校验先失败；但任何绕过 load 直接构造 `Config{Values, Requirements}` 的调用方都能拿到 mxc。可考虑在 `ResolveWindowsSandboxMode` 内也加同一条守卫（defence-in-depth）。
## §3.3 队长重点 B —— sync639 是否还有第三条队列派发路径漏了 trigger

**结论：没有第三条。**队列派发在 Go 里是**单一收口**，两条入口都已带 trigger：

```
$ git grep -n 'DequeueFirstSubmission' ee2d4e1d -- '*.go'
appserver/runtime_router.go:3738:   submission, err := r.services.ThreadRouter.store.DequeueFirstSubmission(...)   # 唯一「出队」生产调用点
session/store.go:833 / store.go:855 / store_test.go                                        # 定义与自递归

$ git grep -n 'handleTurnStart(' ee2d4e1d -- 'appserver/*.go'
runtime_router.go:3000  MethodTurnStart RPC（客户端直发，非队列）
runtime_router.go:3697  thread/queue/start（已带 TurnTrigger:"queue"）      ← 路径①
runtime_router.go:3749  FIFO wakeup（maybeDispatchNextQueuedSubmission，已带）← 路径②
agent_controller.go:280 / 488 / 746  子代理（agent controller）turn，非线程队列
memory_startup.go:264 / realtime_runtime.go:613  各自触发（memory_consolidation / realtime）
```
- 出队只发生在 `maybeDispatchNextQueuedSubmission`（`runtime_router.go:3738`）这一处；它的调用方为 `turn_runtime.go:1950`、`turn_runtime.go:4188`（turn 完成/失败后）、以及 `maybeDispatchQueuedSubmissionIfIdle`（`runtime_router.go:3829`，其唯一生产调用方是 `runtime_router.go:2985` 的 resume/load 后空闲唤醒）。**全部收敛到同一函数**，故新增的 `TurnTrigger:"queue"` 一处即覆盖全部 FIFO 派发。
- 显式 RPC 只有 `thread/queue/start`（`handleThreadQueueStartRuntime`）。
- 边界说明（非缺陷）：`agent_controller.go:745` 会把「子代理排队项」`append(queued, item)` 塞进一次 turn 启动，但它**不是线程队列派发**——它沿用父 turn 的 `c.turnTrigger`，属 Rust 的另一子系统（agent/多代理输入队列），#40665 未涉及。

## §4 告警 / 提示汇总

1. **无告警、无不实**。（§2.1 的 `TestRuntimeRouterTurnStartRestoresThreadDynamicTools` 方向为 base-only，已证明是整包 flaky。）
2. 新增已知 flaky 建议：`TestRuntimeRouterTurnStartRestoresThreadDynamicTools`（`TestRuntimeRouterTurnStart*` 同族，两侧隔离 3/3 PASS）。
3. `queue` trigger 的语义提示：`thread/queue/start` 与 FIFO 唤醒产生的 turn 在 Responses turn metadata 里标记为 `turn_trigger = "queue"`（`codexapi/client_context.go:218`），即**队列派发的 turn 归因于队列而非原始提交客户端** —— 属可观测的元数据变更，建议发布说明点名。
4. sync640 的 `ConfigRequirements.AllowMXC` 是 `json:"-"` 内部字段（不上 app-server wire schema），`cloneRequirements` 已同步深拷贝（`config/api.go:3766`）。
5. 偏差记录建议（见 §3.2 第 1、3、4 点）：camelCase 接受（良性）、自动选择门语义冻结（功能缺口）、`ResolveWindowsSandboxMode` 无第二条守卫（defence-in-depth）。

## §5 只读声明 / 资源

- 0 commit / 0 push / 0 ref 移动；重放走 `cherry-pick -n`；未触碰他人 worktree。
- 临时目录（按规程，**未尝试递归删除**）：
  `D:\tmp\aud8_replay`（git worktree；清理：`git worktree remove --force D:\tmp\aud8_replay`）
  `D:\tmp\aud8_head` / `D:\tmp\aud8_base`（LF 解包树；**base 侧已拷入 head 的 2 个测试文件用于探针、且 `config/windows_mxc_requirements_test.go` 已被置空为 `package config` 以便继续跑 e2e 探针 ⇒ base 侧非纯净基线**）
  `D:\tmp\head8.tar` / `D:\tmp\base8.tar` · `D:\tmp\probe8\`（overlay 探针与 JSON）
- 产物：本文件（未跟踪，留队长提交）。

## §6 复跑命令

```
git ls-remote origin refs/heads/main refs/heads/integ86g
git show -s --format=%T ee2d4e1d          # d69b63b57f276afebebb09879ad5e16b7f86efc5
git rev-list --count e9849503..ee2d4e1d   # 2
git diff --name-status e9849503 1a465823 ; git diff --name-status 1a465823 ee2d4e1d
git grep -n -P '\r' ee2d4e1d -- <7 文件>    # 应无输出

git -c core.autocrlf=false -c core.eol=lf worktree add D:\tmp\aud8_replay --detach e9849503
cd D:\tmp\aud8_replay ; git cherry-pick -n 1a465823 ee2d4e1d ; git add -A ; git write-tree

git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o D:\tmp\head8.tar ee2d4e1d
git -c core.autocrlf=false -c core.eol=lf archive --format=tar -o D:\tmp\base8.tar e9849503
mkdir D:\tmp\aud8_head D:\tmp\aud8_base ; tar -xf D:\tmp\head8.tar -C D:\tmp\aud8_head ; tar -xf D:\tmp\base8.tar -C D:\tmp\aud8_base

cd D:\tmp\aud8_head
gofmt -l <7 个改动文件> ; gofmt -l . ; go build ./... ; go vet ./appserver/ ./config/
go test ./appserver/ ./config/ -count=1                    # 与 D:\tmp\aud8_base 同命令对拍
go test ./appserver/ -run '^TestRuntimeRouterTurnStartRestoresThreadDynamicTools$' -count=1   # flaky 隔离复核
$env:CODEX_RUST_ROOT="C:\rw\codex-rs"; go test ./parity/ -count=1

# 探针（overlay，不入仓）
go test -overlay=D:\tmp\probe8\overlay_head_config.json ./config/ -run TestSynct5ProbeManagedMXC -count=1 -v
go test -overlay=D:\tmp\probe8\overlay_head_e2e.json   ./config/ -run TestSynct5ProbeEndToEndManagedMXC -count=1 -v
```

## §7 备注：审计期间远端前移（不影响本判定）

```
$ git ls-remote origin refs/heads/main refs/heads/integ86g   （审计后段）
fb24b81e238f196ac7259a2edba7c84adf3ff18c  refs/heads/integ86g
fb24b81e238f196ac7259a2edba7c84adf3ff18c  refs/heads/main
$ git merge-base --is-ancestor ee2d4e1d origin/main ; echo $LASTEXITCODE   -> 0
$ git log --oneline ee2d4e1d..origin/main
fb24b81e sync642: keep terminal hyperlinks in the command-center preview like Rust (#50431)
8b2453a9 sync641: launch embedded with a warning from an elevated Windows terminal like Rust (#49855)
```
即 `main` 在本次作业期间由 `ee2d4e1d` 前移到 **`fb24b81e`**（+2 笔纯 ff）。**本审计对象 `ee2d4e1d` 仍是 main 的祖先**，§1–§3 结论对其**依然成立**；新增 2 笔**不在本单范围**，本报告未作任何判定。