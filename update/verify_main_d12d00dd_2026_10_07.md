# 独立发布审计 · main `d12d00dd`（范围 `8b2453a9..d12d00dd`，7 笔）

- 审计员：`synct5`（Windows 节点，只读发布审计车道）
- 日期：2026-10-07
- 权限：**0 commit / 0 push / 0 ref 移动**；重放一律 `git cherry-pick -n`；产物未跟踪留 `update/`
- 范围：`8b2453a9..d12d00dd`（sync642 `fb24b81e` → sync648 `d12d00dd`）
- 基线：`8b2453a9`（= 上一份审计对象，见 `update/verify_main_8b2453a9_2026_10_07.md`）
- 工作树：head `D:\tmp\aud12_head`(@d12d00dd)、base `D:\tmp\aud12_base`(@8b2453a9)（均 LF）；重放树 `D:\tmp\rp12`；RC 树 `D:\tmp\rc12`

---

## §0 远端对齐

```
$ git fetch origin --prune
$ git ls-remote origin refs/heads/main refs/heads/integ86g
d12d00dde5732a8f910db636b5452949a4594785	refs/heads/integ86g
d12d00dde5732a8f910db636b5452949a4594785	refs/heads/main

$ git rev-list --count 8b2453a9..origin/main   -> 7
$ git rev-list --merges --count 8b2453a9..origin/main -> 0     # 无 merge
$ git merge-base --is-ancestor 8b2453a9 origin/main -> exit=0

$ git diff --shortstat 8b2453a9 origin/main
 13 files changed, 1411 insertions(+), 28 deletions(-)
```

⚠️ 审计期间 main 再次前进（本轮第 2 次观察到「作业中推进」）：定稿前实测 `main == integ86g == b52105cf`，比 `d12d00dd` 多 **5 笔**（sync649–653）。`d12d00dd` 本身未被改写，故本次审计有效；新增 5 笔**未审计**，仅登记（§9）。

---

## §1 tree-sha 握手（**逐笔，在各自 parent 上重放**）

```
$ git worktree add D:\tmp\rp12 --detach 8b2453a9
# 每笔：git reset --hard <C^> ; git cherry-pick -n <C> ; git write-tree
```

| # | commit | parent | `cherry-pick -n` exit | `write-tree` | `<C>^{tree}` | 一致 |
|---|---|---|---|---|---|---|
| 1 | `fb24b81e` sync642 | `8b2453a9` | 0 | `0b5d9129c96f611d9786d26c521dee947af4d468` | 同 | ✅ |
| 2 | `6315c3a7` sync643 | `fb24b81e` | 0 | `b9eba0ae0615047e5b0ac1041974a2f689c441aa` | 同 | ✅ |
| 3 | `c9c6a200` sync644 | `6315c3a7` | 0 | `e2f6af5cab3893ba0a3df73ae08746a615e5fb18` | 同 | ✅ |
| 4 | `5bddd813` sync645 | `c9c6a200` | 0 | `e37f73fb2d4b7d3109da50dc7a6a4e6e64c533b0` | 同 | ✅ |
| 5 | `97107c7b` sync646 | `5bddd813` | 0 | `07229d5c1b51a83f00b3c6aa73fe9b639a6e09d2` | 同 | ✅ |
| 6 | `3140c46a` sync647 | `97107c7b` | 0 | `4d37e5009eaa7e6ef0f5555f2628ac57756e31ce` | 同 | ✅ |
| 7 | `d12d00dd` sync648 | `3140c46a` | 0 | `3fd6fe5620c903606e38ee4ea5ed6f1b38741644` | 同 | ✅ |

**7/7 逐笔重放与 main 上的 tree 逐字节一致。** 链式父提交亦与 main 的线性历史一致（无 merge、无改写）。

---

## §2 LF 门禁

> 全部在 LF 树内跑（`git -c core.autocrlf=false -c core.eol=lf worktree add`）；head/base 文件字节数 == blob 字节数、`\r` 计数 = 0。

① `gofmt -l` 13 个改动文件 → **空**（exit 0）
② `go build ./...` → **exit 0**
③ `go vet ./app/ ./mcp/ ./model/ ./rollout/ ./tool/ ./tui/agents_overview/` → 两侧**同为 1 条既有告警**：

```
model\responses_agent.go:1462:11: assignment copies lock value to clone: codex_go/model.ResponsesAgentRunner contains sync.Mutex
```

（head/base 同输出；行号由早先的 1448 漂到 1462 —— 行号漂移，非新增。此告警计入既有基线。）

④ **受影响包整包对拍**（`go test ... -count=1`，两侧同命令）：

| 包 | head @d12d00dd | base @8b2453a9 | 判定 |
|---|---|---|---|
| `./app/` | ok 49.460s | ok 37.051s | ✅ |
| `./mcp/` | ok 16.469s | ok 22.390s | ✅ |
| `./model/` | ok 30.583s | ok 39.871s | ✅（本机 model 全绿，未见 env 型基线红） |
| `./rollout/` | ok 9.242s | ok 10.925s | ✅ |
| `./tui/agents_overview/` | ok 1.752s | ok 3.580s | ✅ |
| `./doctor/` | ok 25.775s | ok 29.806s | ✅ |
| `./tool/` | FAIL 110.788s（3 条 env-FS） | FAIL 112.679s（**同 3 条**） | 同态 ✅ |
| `./tui/tea/` | FAIL 36.009s（1 条） | FAIL 33.999s（**同 1 条**） | 同态 ✅ |

- `./tool/` 3 条：`TestApplyPatchPreflightReadsSelectedEnvironmentFileSystemLikeRust`、`TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust`、`TestLocalShellLaunchRestoresExecutorPathDirsLikeRust`
- `./tui/tea/` 1 条：`TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge`

⑤ `./appserver/`（本批**未触碰**该包，仍按基线口径对拍）：

| 运行 | 失败数 | 失败集合 |
|---|---|---|
| head run1（与 base 并发） | 11 | 6 基线 + `CurrentTimeReadAddsDeveloperInput`、`TurnStartRestoresSessionHistory`、`TurnStartInjectsAdditionalContext`、`CurrentTimeRemindersFollowInterval`、`ZeroCurrentTimeReminderInterval` |
| head run2（独占、无并发） | 7 | 6 基线 + `PropagatesOwnerEnvironmentFailureToDescendants` |
| base（与 head run1 并发） | 7 | 6 基线 + `CurrentTimeReadAddsDeveloperInput` |

6 条稳定基线 = `ExecutorSkillPathEquals…` / `MultiAgentWaitDuration…` / `RequiredSkillsPreSampling…` / `ShellSnapshotCommandMetrics{L,R,S}…`。
对其余「多余项」在两侧**隔离重跑**：

```
$ go test ./appserver/ -run 'TestRuntimeRouterTurnStartRestoresSessionHistory|TestRuntimeRouterTurnStartInjectsAdditionalContext|TestRuntimeRouterCurrentTimeRemindersFollowIntervalAndPersistInHistoryLikeRust|TestRuntimeRouterZeroCurrentTimeReminderIntervalDeliversWhenTimeMovesBackwardLikeRust|TestRuntimeRouterCurrentTimeReadAddsDeveloperInputLikeRust' -count=1
head: ok  codex_go/appserver  7.898s
base: ok  codex_go/appserver  6.670s
```

⇒ 这些是 **时间/环境敏感 flaky**，两侧同族、隔离即 PASS；且本批 7 笔**一个 appserver 文件都没碰**（§3）⇒ **新增失败 0**。建议把上列 6 个名字加入已知 flaky 名单。

⑥ `parity`（LF 检出，`CODEX_RUST_ROOT='C:\rw\codex-rs'`）：

```
ok  	codex_go/parity	46.604s        # exit=0
```

---

## §3 逐笔真实性（无夹带 / 无 CRLF / 无 BOM）

```
fb24b81e sync642  2 files  +103 −2   tui/agents_overview/{style.go, hyperlink_preview_like_rust_test.go}
6315c3a7 sync643  2 files   +91 −1   model/{catalog.go, catalog_test.go}
c9c6a200 sync644  2 files  +130 −5   tool/{apply_patch_agents_md_metrics.go, ..._like_rust_test.go}
5bddd813 sync645  2 files  +200 −2   app/{remote_tui.go, app_test.go}
97107c7b sync646  2 files  +450 −0   mcp/{oauth_discovery.go, oauth_issuer_binding_test.go}
3140c46a sync647  3 files  +372 −11  rollout/{line_reader.go, compression_metrics.go, line_reader_47565_test.go}
d12d00dd sync648  2 files   +65 −7   model/{catalog.go, catalog_test.go}
                 ---------
                 逐笔加总 = +1411 / −28 = 与 range `--shortstat` 完全一致
```

`union(逐笔文件)` = 13 个，与 `git diff --name-status 8b2453a9 d12d00dd` 的 13 个 **一一对应且无重叠**（`model/{catalog.go,catalog_test.go}` 被 sync643/sync648 各改一次，仍是同一文件）⇒ **union == range，无夹带**。

CR/BOM（LF 树内逐文件读字节）：

```
app/app_test.go                               size=240315 cr=0 bom=False
app/remote_tui.go                             size=195696 cr=0 bom=False
mcp/oauth_discovery.go                        size=28045  cr=0 bom=False
mcp/oauth_issuer_binding_test.go              size=12246  cr=0 bom=False
model/catalog.go                              size=77240  cr=0 bom=False
model/catalog_test.go                         size=93713  cr=0 bom=False
rollout/compression_metrics.go                size=10210  cr=0 bom=False
rollout/line_reader.go                        size=8547   cr=0 bom=False
rollout/line_reader_47565_test.go             size=8629   cr=0 bom=False
tool/apply_patch_agents_md_metrics.go         size=2231   cr=0 bom=False
tool/apply_patch_agents_md_metrics_like_rust_test.go size=15199 cr=0 bom=False
tui/agents_overview/hyperlink_preview_like_rust_test.go size=2768 cr=0 bom=False
tui/agents_overview/style.go                  size=7044   cr=0 bom=False
```

`base ls-files` = 3827、`head ls-files` = 3830 ⇒ Δ3 = 3 个新增测试文件，与 `name-status` 的 3 个 `A` 一致。

---

## §4 独立 RC（**逐笔值级**，我本人复跑）

控制组 = 该提交原样；实验组 = 把该笔的**生产文件**回退到其 parent（符号仍在则直接跑，符号被测试引用则再补一次「只静音行为」的外科版）。

| 提交 | 手法 | 实验组结果 |
|---|---|---|
| `fb24b81e` sync642 | 回退 `tui/agents_overview/style.go` | `FAIL codex_go/tui/agents_overview [build failed]`（新测试引用 `stripTerminalEscapes` 等新符号）；控制组 `ok 0.039s` |
| `6315c3a7` sync643 | 回退 `model/catalog.go` | `--- FAIL: TestBundledFallbackCarriesCatalogJSONBooleans`；`catalog_test.go:2138: gpt-6.1-sol: supports_reasoning_summary_parameter = false on the fallback path, true on the models.json path`；控制组 `ok 0.026s` |
| `c9c6a200` sync644 | 回退 `tool/apply_patch_agents_md_metrics.go` | 2 条子用例 FAIL：`apply_patch_agents_md_metrics_like_rust_test.go:372: filenames = "", want "agents.md"`（native_separator）、`:398` 同（absolute_path）；控制组 `ok 0.041s` |
| `5bddd813` sync645 | ①回退 `app/remote_tui.go` ②**仅静音** interrupt 调用点 | ①`[build failed]` ②`--- FAIL: TestInteractiveRemoteCloseSideInterruptsRunningSideTurnLikeRust`；`app_test.go:6135: second request = "thread/unsubscribe", want turn/interrupt before thread/unsubscribe`；控制组 `ok 0.152s` |
| `97107c7b` sync646 | ①回退 `mcp/oauth_discovery.go` ②**仅静音**两处 validate 调用 | ①`[build failed]` ②`oauth_issuer_binding_test.go:64/:88/:169/:186` 多子用例 FAIL（cross-origin / lookalike / http 降级 / issuer-bound 无 issuer）；控制组 `ok 0.048s` |
| `3140c46a` sync647 | ①回退 `rollout/{line_reader.go,compression_metrics.go}` ②**仅静音** reason/progress 计算 | ①`[build failed]` ②`line_reader_47565_test.go:50: read counters = [... "reason":"none" ...], want {... "reason":"zstd_invalid_frame" ...}`；控制组 `ok 0.182s` |
| `d12d00dd` sync648 | 回退 `model/catalog.go` | `--- FAIL: TestFallbackCatalogCarriesModelsJSONFieldValues`；`catalog_test.go:2176: gpt-5.6-sol description = "Latest frontier agentic coding model.", want "Older generation workhorse model." (models.json @ b17c74cfd5)`；控制组 `ok 0.032s` |

**7/7 均有可复现的值级反例**（4 条运行时断言 + 3 条先编译失败、再以外科静音拿到运行时断言）。

---

## §5 特别核（队长点名三项）

### ① sync644 的 `filepath.Base` 是否与 Rust `PathUri` Windows convention 值级同态 → **是**

Rust（`C:\rw\codex-rs\utils\path-uri\src\lib.rs:514-518`）：

```rust
let path = match convention {
    PathConvention::Posix => path.to_string(),
    PathConvention::Windows => path.replace('\\', "/"),
};
for component in path.split('/') { ... }
```

即 **Windows convention 先把 `\` 归一成 `/`，再按 `/` 切段**；`basename()` 取最后一个非空段。Go 的 `filepath.Base`：Windows 上 `\` 与 `/` 都是分隔符，POSIX 上**只有 `/`** —— 与 Rust 的 Windows / Posix 两条 convention 逐一对上。

探针（`D:\tmp\rc12`，Windows 节点）：回退生产文件后

```
--- FAIL: .../native_separator    :372: codex.agents_md.edit filenames = "", want "agents.md"
--- FAIL: .../absolute_path       :398: codex.agents_md.edit filenames = "", want "agents.md"
```

新实现下两条均 PASS ⇒ 原生分隔符与绝对 Windows 路径都命中 `agents.md`。**同态成立。**
附加：新增的 `change.Kind == applypatch.ChangeUpdate` 守卫也与 Rust `AppliedPatchFileChange::Update { move_path }` 一致（move 目标只在 update 上携带），并由 `TestRecordAgentsMdEditMetricsMovePathOnlyForUpdatesLikeRust` 钉住。

### ② sync648 的 `gpt-5.5` truncation 与 `codex-auto-review.max_context_window` 是否逐值一致 → **是**

从 Rust 仓取 `b17c74cfd5:codex-rs/models-manager/models.json` 解析后比对：

| slug | Rust @ b17c74cfd5 | Go @ d12d00dd | 一致 |
|---|---|---|---|
| `gpt-5.5` | `truncation_policy = {mode:"tokens", limit:10000}` | `{Mode: TruncationModeTokens, Limit: 10000}` | ✅ |
| `gpt-5.5` | `description = "Legacy coding model."`, `max_context_window = 272000` | 同 | ✅ |
| `codex-auto-review` | `max_context_window = 872000`, `context_window = 272000` | 同 | ✅ |
| `gpt-5.6-sol/terra/luna` | `"Older generation workhorse model."` / `"Older balanced model for straightforward work."` / `"Older fast and efficient model."` | 同 | ✅ |

（`codex-auto-review` 的 `truncation_policy` 也由 bytes→tokens，同样与 Rust 一致；这是 **用户可见口径变更**：评审模型的上下文预算由 1,000,000 → 872,000，截断单位由字节改 token 数。）
新测试 `TestFallbackCatalogCarriesModelsJSONFieldValues` 把这 7 处值逐条钉死（回退即 FAIL，见 §4）。

**附带发现（非阻塞、既有，非本批引入）**：Go fallback 对 `codex-auto-review` **未设置** `ToolMode` / `MultiAgentVersion` / `UseResponsesLite`，而 Rust models.json @ b17c74cfd5 明确给了 `tool_mode="code_mode_only"` / `multi_agent_version="v1"` / `use_responses_lite=true`。对照：同一 fallback 对**全部** GPT-6 / 5.6 条目都设了这三个字段；对 `gpt-5.5` Rust 本身是 `null/false`，Go 零值一致（无偏差）。故**唯一缺口是 `codex-auto-review` 这一条**。现有测试（sync643 只比 2 个 serde-default 布尔、sync648 只比 7 处值）覆盖不到它。建议下一轮「fallback 镜像对齐」顺手补测/补值。

### ③ sync646 的 `DELIBERATE GO DIFFERENCE`（issuer-less 臂未移植）是否被测试钉死 → **是**，且**该缺口已在 range 之外被关闭**

Rust `rmcp-client/src/oauth/issuer_binding.rs::validate_authorization_server_endpoints` 在 issuer 存在分支之后还有一段：

```rust
if token_endpoint.origin() != authorization_endpoint.origin() {
    bail!("OAuth token endpoint origin does not match the authorization server origin without issuer-bound callbacks");
}
```

Go 版把这一段**有意不移植**（源码中 `DELIBERATE GO DIFFERENCE (flagged for the lane leader)` 注释），并由 `TestMCPOAuthDiscoveryKeepsIssuerLessMetadataAccepted` **钉死该行为**：issuer-less 元数据 + 跨 origin 的 token endpoint 仍被判为可用（授权端点 `https://issuer.example/…`、token 端点 `http://127.0.0.1:1/token`，断言 `DiscoverStreamableHTTPOAuth() error == nil`）⇒ 宣称与实现一致，**非「不实」**。
其余部分逐值对齐已核：兼容例外表（mercadopago / robinhood 三元组）逐字一致；六条错误文案（valid URL ×3 / HTTP or HTTPS / issuer-bound 需 issuer / origin 不匹配）与 Rust `bail!` 文本一致。

**重要状态更新**：审计期间远端 main 前进到 `b52105cf`，其 **`sync653` 的标题正是 "require issuer-less token endpoints to share the authorization origin like Rust (#39935)"** —— 即上条有意偏差**已被后续提交关闭**。故该项对本批（`d12d00dd`）仍是「已声明的有意偏差」，但**当前 main 上已不复存在**；建议台账据此销项（sync653 未在本次范围内，未审计）。

---

## §6 告警

1. **appserver 时间敏感 flaky 扩大**：head 两次整包跑给出 11 / 7 两个不同失败集（base 为 7）。多余项为 `TestRuntimeRouter{TurnStartRestoresSessionHistory, TurnStartInjectsAdditionalContext, CurrentTimeRemindersFollowIntervalAndPersistInHistoryLikeRust, ZeroCurrentTimeReminderIntervalDeliversWhenTimeMovesBackwardLikeRust, CurrentTimeReadAddsDeveloperInputLikeRust, PropagatesOwnerEnvironmentFailureToDescendantsLikeRust}`，两侧隔离重跑全 PASS。**非本批回归**（本批 0 个 appserver 文件），建议入已知 flaky 名单。
2. **`codex-auto-review` 的 3 字段镜像缺口**（见 §5② 附带发现）：既有、非本批引入，建议随下一轮对齐处理。
3. `model/responses_agent.go` lock-copy vet 告警行号漂移（1448 → 1462），两侧同态，非新增。
4. 队长口径提示：本机 `./model/` 实测**全绿**（两侧），未出现你所记的「2 条 env 型」红；`./doctor/` 亦全绿。以本机两次实测为准。

---

## §7 只读与资源

- 0 commit / 0 push / 0 ref 移动；重放 `cherry-pick -n`；RC 全部在 `D:\tmp\rc12` 内做、每次 `reset --hard` 还原（结束时工作区干净）。
- 本次新增临时物（本机策略拦截 `Remove-Item`，未做任何删除，供统一收口）：
  - worktree：`D:\tmp\rp12`(@8b2453a9 重放)、`D:\tmp\rc12`(@d12d00dd 隔离)、`D:\tmp\aud12_head`(@d12d00dd)、`D:\tmp\aud12_base`(@8b2453a9)
  - 其余无：无 tar、无 probe 目录（RC 用 `checkout <C^> -- <file>` + 文本静音，不落新文件）

---

## §8 复跑命令

```powershell
$env:PYTHONIOENCODING='utf-8'; [Console]::OutputEncoding=[System.Text.Encoding]::UTF8
cd D:\qax\reagent\dev\codex_go
git fetch origin --prune; git ls-remote origin refs/heads/main refs/heads/integ86g
git rev-list --count 8b2453a9..d12d00dd          # 7 ; --merges -> 0
$cs=@('fb24b81e','6315c3a7','c9c6a200','5bddd813','97107c7b','3140c46a','d12d00dd')
git worktree add D:\tmp\rp12 --detach 8b2453a9
foreach($c in $cs){ git -C D:\tmp\rp12 reset --hard "$($c)^" ; git -C D:\tmp\rp12 cherry-pick -n $c ; git -C D:\tmp\rp12 write-tree ; git rev-parse "$($c)^{tree}" }
git -c core.autocrlf=false -c core.eol=lf worktree add D:\tmp\lfh12 --detach d12d00dd
cd D:\tmp\lfh12; gofmt -l <13 改动文件>        # 空
go build ./... ; go vet ./app/ ./mcp/ ./model/ ./rollout/ ./tool/ ./tui/agents_overview/
$env:TERM=$null; go test ./app/ ./mcp/ ./model/ ./rollout/ ./tool/ ./tui/agents_overview/ ./doctor/ ./tui/tea/ -count=1
$env:CODEX_RUST_ROOT='C:\rw\codex-rs'; go test ./parity/ -count=1
# RC 见 §4；models.json 比对：
git -C C:\rw\codex-rs show 'b17c74cfd5:codex-rs/models-manager/models.json' | Select-String 'Legacy coding model|872000'
```

---

## §9 main 前进登记（**未审计**）

定稿前实测 `origin/main == origin/integ86g == b52105cf74e3fb00bf8e324f29f84c61065ea706`，比审计对象多 5 笔：

```
b52105cf sync653: require issuer-less token endpoints to share the authorization origin like Rust (#39935)
9762262c sync652: seed a new thread's Daybreak preference like Rust (#49861)
b041cbfd sync651: accept the daybreak terminal-title item in doctor like Rust (#49861)
668150fb sync650: wire the Daybreak thread preference into the live status controls like Rust (#49861)
60b97880 sync649: add the Daybreak status-line surfaces like Rust (#49861)
```

`d12d00dd` tree = `3fd6fe5620c903606e38ee4ea5ed6f1b38741644`；`b52105cf` 未取 tree（未审计）。

---

## 结论

**GREEN**：`8b2453a9..d12d00dd` 7 笔，**逐笔 tree-sha 握手 7/7 一致**；union==range（13 文件）无夹带；13 文件零 CRLF / 零 BOM；LF 门禁 gofmt 空 / build 0 / vet 仅 1 条既有告警 / parity ok；受影响包对拍**新增失败 0**（tool 3 + tui/tea 1 两侧同态，appserver 6 条基线 + 时间敏感 flaky，隔离双侧全 PASS）；**7/7 值级 RC 可复现**。三项特别核全部通过：sync644 `filepath.Base` 与 Rust `PathUri` Windows convention 值级同态、sync648 七处值（含 gpt-5.5 tokens 与 `codex-auto-review` 872000）与 Rust `models.json @ b17c74cfd5` 逐值一致、sync646 的有意偏差确被测试钉死（且已被 range 外的 sync653 关闭）。
告警 2 条非阻塞：appserver 时间敏感 flaky 名单扩充、`codex-auto-review` fallback 3 字段镜像缺口（既有）。**无「不实/夸大」项。**