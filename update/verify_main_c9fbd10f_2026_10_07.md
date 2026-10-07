# 独立发布审计 · main `c9fbd10f`（范围 `d12d00dd..c9fbd10f`，11 笔）

- 审计员：`synct5`（Windows 节点，只读发布审计车道）
- 日期：2026-10-07
- 权限：**0 commit / 0 push / 0 ref 移动**；重放一律 `git cherry-pick -n` / `git checkout <C>^ -- <files>`；产物未跟踪留 `update/`
- 范围：`d12d00dd..c9fbd10f`（sync649 `60b97880` → sync659 `c9fbd10f`，11 笔）
- 基线：`d12d00dd`（上一份审计对象，见 `update/verify_main_d12d00dd_2026_10_07.md`）
- 工作树：head `D:\qax\reagent\dev\codex_go_wt\synct5v13head`(@c9fbd10f)、base `D:\qax\reagent\dev\codex_go_wt\synct5v13base`(@d12d00dd)，均 LF（`-c core.autocrlf=false -c core.eol=lf worktree add`）；重放/RC 树 `D:\tmp\rp13`；overlay 探针 `D:\tmp\probe13`

---

## §0 远端对齐

```
$ git fetch origin --prune
$ git ls-remote origin refs/heads/main refs/heads/integ86g
c9fbd10f56d75e011b193f2bc3ad6e195bde05a9	refs/heads/integ86g
c9fbd10f56d75e011b193f2bc3ad6e195bde05a9	refs/heads/main

$ git merge-base --is-ancestor d12d00dd origin/main   -> exit=0     # 可 ff，无 merge
$ git rev-list --count d12d00dd..origin/main          -> 11
$ git rev-list --count --merges d12d00dd..origin/main -> 0
$ git diff --shortstat d12d00dd c9fbd10f
 39 files changed, 1479 insertions(+), 122 deletions(-)

$ git show -s --format='%H %T' c9fbd10f
c9fbd10f56d75e011b193f2bc3ad6e195bde05a9 23bb2c2b45848e707a71453a91e3ee48ea9b2ccb
```

`main == integ86g`，11 笔线性、无 merge，`d12d00dd` 仍是 main 的祖先 ⇒ 本段为**纯增量**。

⚠️ 本审计只对 `c9fbd10f` 负责。本轮已多次出现「作业期间 main 被推进」；若你读到时 main 已前进，新提交属**未审计**（见 §9）。

---

## §1 tree-sha 握手（逐笔，在各自 parent 上重放）

```
$ git worktree add D:\tmp\rp13 --detach <parent>
# 每笔：git reset --hard <C^> ; git cherry-pick -n <C> ; git write-tree
```

| # | commit | parent | `cherry-pick -n` exit | 重放 `write-tree` | main 上 `<C>^{tree}` | 一致 |
|---|---|---|---|---|---|---|
| 1 | `60b97880` sync649 #49861 | `d12d00dd` | 0 | `cb6a9757182f` | `cb6a9757` | ✅ |
| 2 | `668150fb` sync650 #49861 | `60b97880` | 0 | `b496f40a8b83` | `b496f40a` | ✅ |
| 3 | `b041cbfd` sync651 #49861 | `668150fb` | 0 | `09d6fb097225` | `09d6fb09` | ✅ |
| 4 | `9762262c` sync652 #49861 | `b041cbfd` | 0 | `be15b74a4a66` | `be15b74a` | ✅ |
| 5 | `b52105cf` sync653 #39935 | `9762262c` | 0 | `17b0ad3cfee0` | `17b0ad3c` | ✅ |
| 6 | `e72df5b5` sync654 #47590 | `b52105cf` | 0 | `93984f708af1` | `93984f70` | ✅ |
| 7 | `4df8a518` sync655 #47657 | `e72df5b5` | 0 | `79325b01eb35` | `79325b01` | ✅ |
| 8 | `240913ad` sync656（本地 side-close） | `4df8a518` | 0 | `ab4855622742` | `ab485562` | ✅ |
| 9 | `8fe41572` sync657 #50786 | `240913ad` | 0 | `a1f477bc9575` | `a1f477bc` | ✅ |
| 10 | `999e655d` sync658 #50786 | `8fe41572` | 0 | `9af1c2a4c335` | `9af1c2a4` | ✅ |
| 11 | `c9fbd10f` sync659 #47974 | `999e655d` | 0 | `23bb2c2b4584` | `23bb2c2b` | ✅ |

**`MATCH=11/11`**：每一笔在其 parent 上重放得到的 tree 与 main 上的 tree 逐字节一致，父链即 main 的线性历史（无 merge、无改写）。

---

## §2 LF 门禁

> 全部门禁在 **LF 树**内跑；抽查 `sandbox/sandboxpath/sandboxpath.go` → `bytes=12898 cr=0`。

① 改动文件数 与 `gofmt -l`：

```
$ git -C <head> diff --name-only d12d00dd c9fbd10f | wc -l   -> 39
$ gofmt -l <39 files>
<empty>
gofmt-exit=0
```

② `go build ./...` → `build-exit=0`

③ `go vet`（改动包）：

```
$ go vet ./app/ ./doctor/ ./mcp/ ./model/ ./sandbox/sandboxpath/ ./tui/ ./tui/bottom_pane/ ./tui/chatwidget/ ./tui/tea/ ./tui/agents_overview/
head @c9fbd10f: model\responses_agent.go:1500:11: assignment copies lock value to clone: codex_go/model.ResponsesAgentRunner contains sync.Mutex
base @d12d00dd: model\responses_agent.go:1462:11: assignment copies lock value to clone: codex_go/model.ResponsesAgentRunner contains sync.Mutex
```

⇒ **两侧同为 1 条既有告警**；行号 1462→1500 的漂移由 sync654 在同文件插入 38 行造成（§4），**无新增 vet 发现**。

④ 受影响包整包对拍（两侧同命令，`go test <pkgs> -count=1`）：

| 包 | head @c9fbd10f | base @d12d00dd | 判定 |
|---|---|---|---|
| `./app/` | ok 16.504s | ok 15.801s | ✅ |
| `./doctor/` | ok 20.512s | ok 20.154s | ✅ |
| `./mcp/` | ok 12.698s | ok 12.731s | ✅ |
| `./model/` | ok 25.448s | ok 25.729s | ✅ |
| `./tui/` | ok 2.357s | ok 2.660s | ✅ |
| `./tui/bottom_pane/` | ok 0.200s | ok 0.200s | ✅ |
| `./tui/chatwidget/` | ok 0.225s | ok 0.209s | ✅ |
| `./tui/tea/` | **FAIL 24.311s（1 条）** | **FAIL 24.317s（同 1 条）** | 同态 ✅ |
| `./tui/agents_overview/` | ok 0.825s | ok 0.925s | ✅ |

唯一 FAIL = `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge`（`slash_copy_hard_break_like_rust_test.go:71: copied = "", want "Intro  \nlast line  "`），两侧逐字相同 ⇒ **新增失败 0**。

`./sandbox/sandboxpath/` 在本机 = `?   codex_go/sandbox/sandboxpath  [no test files]`（唯一测试文件 `gitdir_pointer_test.go` 带 `//go:build linux`）⇒ Windows 无覆盖，见 §5④ 的 overlay 行为探针。

⑤ `parity`（LF 检出）：

```
$env:CODEX_RUST_ROOT='C:\rw\codex-rs'; go test ./parity/ -count=1
ok  	codex_go/parity	31.058s        # exit=0
```

本批 39 个改动文件**无一落在 `parity/`**；`C:\rw\codex-rs` = `5b0b253035`（LF 检出）。
（对照：`D:\qax\reagent\dev\git\codex` 为 CRLF 检出，会让含 `go:embed` 的 parity 用例假红，勿用于 parity。）

---

## §3 逐笔真实性（无夹带 / 无 CRLF / 无 BOM）

| commit | PR | files | +/- | 生产文件（非测试） |
|---|---|---|---|---|
| `60b97880` sync649 | #49861 | 8 | +206 −8 | `tui/bottom_pane/{status_line_setup,status_line_style,status_surface_preview,title_setup}.go`、`tui/chatwidget/{status_controls,status_surfaces}.go` |
| `668150fb` sync650 | #49861 | 7 | +137 −43 | `tui/{resume_picker,session_source}.go`、`tui/tea/{model,session_picker,status_controls,thread_reset}.go` |
| `b041cbfd` sync651 | #49861 | 2 | +22 −0 | `doctor/doctor.go` |
| `9762262c` sync652 | #49861 | 4 | +129 −15 | `app/interactive.go`、`tui/tea/model.go` |
| `b52105cf` sync653 | #39935 | 5 | +58 −43 | `mcp/oauth_discovery.go` |
| `e72df5b5` sync654 | #47590 | 2 | +102 −0 | `model/responses_agent.go` |
| `4df8a518` sync655 | #47657 | 3 | +124 −0 | `model/{provider,catalog}.go` |
| `240913ad` sync656 | — | 2 | +226 −1 | `app/interactive.go` |
| `8fe41572` sync657 | #50786 | 5 | +229 −10 | `tui/agents_overview/overview.go`、`tui/tea/{agents_overview,model,settings_commands}.go` |
| `999e655d` sync658 | #50786 | 3 | +40 −2 | `app/interactive.go`、`app/remote_tui.go` |
| `c9fbd10f` sync659 | #47974 | 2 | +206 −0 | `sandbox/sandboxpath/sandboxpath.go` |
| **合计** | | **43** | **+1479 −122** | |

- `union(逐笔文件)` = **39**，`git diff --name-only d12d00dd c9fbd10f` = **39**，`Compare-Object` 结果为空 ⇒ **union == range，无夹带**（重叠 4 = `tui/tea/model.go`×3、`app/interactive.go`×3）。
- 逐笔 numstat 加总 `+1479 −122` **完全等于** range `--shortstat`。
- `ls-files`：head **3838** / base **3830** ⇒ Δ8 = `name-status` 里 8 个 `A`（全部为新增测试文件：`app/agents_overview_grouping_settings_like_rust_test.go`、`app/interactive_sideclose_test.go`、`sandbox/sandboxpath/gitdir_pointer_test.go`、`tui/bottom_pane/status_line_daybreak_like_rust_test.go`、`tui/chatwidget/daybreak_status_surfaces_like_rust_test.go`、`tui/tea/agents_overview_grouping_persist_like_rust_test.go`、`tui/tea/daybreak_default_test.go`、`tui/tea/daybreak_status_surfaces_test.go`）。
- CR/BOM（head 树逐文件读字节）：`checked=39 bad=0` ⇒ **39/39 `cr=0`、无 BOM**。

---

## §4 逐笔独立 RC（**值级**复跑）

控制组 = 该笔原样；回退组 = 把该笔**生产文件**回退到 parent；若回退导致测试引用新符号而 `[build failed]`，再补一次**「只静音行为、保留符号」**的外科手术版，以拿到运行时断言。

| commit | 控制组 | 回退组（prod→parent） | 外科静音（行为级） |
|---|---|---|---|
| `60b97880` sync649 | `ok 0.123s / ok 0.080s` | `[build failed]`：`undefined: StatusLineDaybreak / StatusPreviewDaybreak / TerminalTitleDaybreak` + `unknown field DaybreakEnabled`（bottom_pane + chatwidget） | 把 `ParseStatusLineItem` 的 `case "daybreak"` 改成返回 `StatusLineFastMode` → **`--- FAIL: TestDaybreakItemMetadataLikeRust`**；`status_line_daybreak_like_rust_test.go:11: ParseStatusLineItem(daybreak) = 20 ok=true` |
| `668150fb` sync650 | `ok 0.094s` | `[build failed]`：`daybreak_status_surfaces_test.go:19:59: unknown field DaybreakEnabled in … SessionSummary`、`:21:12 model.daybreakEnabled undefined` | 把 `session_picker.go` 的 `if response.Summary != nil && response.Summary.DaybreakEnabled` 置 false → **`--- FAIL: TestResumeRestoresDaybreakPreferenceLikeRust`**；`daybreak_status_surfaces_test.go:22: resumed thread with Daybreak enabled kept daybreakEnabled=false` |
| `b041cbfd` sync651 | `ok 0.054s` | **`--- FAIL: TestTerminalTitleAcceptsDaybreakItemLikeRust`**；`doctor_test.go:1140: … terminal title invalid items: "daybreak" … Status:warning`（daybreak 被当成非法标题项） | — |
| `9762262c` sync652 | `ok 0.100s / ok 0.094s` | `[build failed]`：`daybreak_default_test.go:31:5: unknown field DaybreakDefault in … Options`、`app_test.go:6246:15: settings.DaybreakDefault undefined` | ①`DaybreakDefault: settings.DaybreakDefault`→`false` ②`if interactiveTUISettings(root).DaybreakDefault`→false ③`model.go` 去掉 `&& options.DaybreakDefault` → **`--- FAIL: TestLocalThreadStartStagesDaybreakDefaultLikeRust`**（`app_test.go:6223: thread/start daybreakEnabled = <nil>, want true`）+ **`--- FAIL: TestNewThreadDaybreakDefaultLikeRust/no_configured_default`**（`daybreak_default_test.go:34: daybreakEnabled = true, want false`） |
| `b52105cf` sync653 | `ok 0.032s` | **`--- FAIL: TestMCPOAuthDiscoveryBindsIssuerLessTokenEndpointLikeRust`**；`oauth_issuer_binding_test.go:234: DiscoverStreamableHTTPOAuth() error = <nil>, want the cross-origin token endpoint rejected` | — |
| `e72df5b5` sync654 | `ok 0.024s` | **`--- FAIL: TestResponsesReasoningEffortWireTypeLikeRust`**；`responses_agent_test.go:4254: custom number: reasoning body = {"effort":"64"}, want {"effort":64}` | — |
| `4df8a518` sync655 | `ok 0.032s` | `[build failed]`：`undefined: isAmazonBedrockGovCloudMantleEndpoint / AmazonBedrockGovCloudModelCatalog` | 把 `if isAmazonBedrockGovCloudMantleEndpoint(p.info.BaseURL)` 置 false → **`--- FAIL: TestAmazonBedrockGovCloudMantleCatalogLikeRust`**；`provider_test.go:343: govcloud manager slugs = [openai.gpt-6.1-sol … openai.gpt-5.5], want [openai.gpt-5.6-terra openai.gpt-5.6-luna]` |
| `240913ad` sync656 | `ok 0.424s` | `[build failed]`：`undefined: interactiveLocalSideClose`、`interrupts.interruptThread undefined` | 让 `interruptThread` 提前返回 false → **`--- FAIL: TestInteractiveLocalSideCloseInterruptsRunningTurn`**（`interactive_sideclose_test.go:104: local side close left the running turn alive: turn context was never cancelled`，5.08s）+ **`--- FAIL: TestInteractiveInterruptControllerInterruptThreadScopesToThread`**（`:158: interruptThread(tracked thread) = false, want true`）；负例 `KeepsOtherRunningTurn` 仍 PASS |
| `8fe41572` sync657 | `ok 0.092s` | `[build failed]`：`model.agentsOverviewGrouping undefined`、`undefined: agentsoverview.ParseGroupingConfig`、`grouping.ConfigValue undefined`、`unknown field AgentsOverviewGrouping in … Options` | 让 `updateAgentsOverviewKey` 不再发出持久化命令（`return nil`） → **`--- FAIL: TestAgentsOverviewGroupingPersistsLikeRust`**（`:45: grouping toggle to "status" returned no persistence command`）+ **`--- FAIL: TestAgentsOverviewGroupingSaveFailureLikeRust`**（`:90: grouping toggle returned no persistence command`） |
| `999e655d` sync658 | `ok 0.162s` | **`--- FAIL: TestSettingsCarryAgentsOverviewGroupingLikeRust`**；`agents_overview_grouping_settings_like_rust_test.go:23: settings AgentsOverviewGrouping = "", want "model"` | — |
| `c9fbd10f` sync659 | overlay 探针 **`ok`** | 测试为 `//go:build linux`（Windows 上 `[no test files]`） | 静音 `addResolvedGitDirCarveouts(out)` → **`--- FAIL: TestProbeGitDirCarveoutLikeRust`**；`probe13_gitdir_test.go:37: expected …\realgit to be a read-only carveout of the enclosing writable root; roots=[… ReadOnlySubpaths:[…\.git …]]` |

**11/11 全部拿到可复现的值级反例**（5 条直接运行时断言 + 3 条「先编译失败、再外科静音得运行时断言」+ 3 条外科静音运行时断言）。overlay 探针命令见 §8。---

## §5 特别核（队长点名四项）

### ① sync653 `#39935`：issuer-less 臂与 Rust `issuer_binding.rs:70-74` 是否逐字同态 → **是（条件与文案均逐字一致）**

Rust（`C:\rw\codex-rs\rmcp-client\src\oauth\issuer_binding.rs`，`use url::Url;`）：

```rust
  70:     if token_endpoint.origin() != authorization_endpoint.origin() {
  71:         bail!(
  72:             "OAuth token endpoint origin does not match the authorization server origin without issuer-bound callbacks"
  73:         );
  74:     }
```

Go（`mcp/oauth_discovery.go:668-672`）：

```go
	// Issuer-less metadata: Rust still requires the token endpoint to share the
	// authorization endpoint's origin, so the browser hand-off and the token
	// exchange cannot straddle two different authorization servers.
	if !sameHTTPOrigin(authorizationEndpoint, tokenEndpoint) {
		return errors.New("OAuth token endpoint origin does not match the authorization server origin without issuer-bound callbacks")
	}
```

- 文案**逐字节相同**；旧的 `DELIBERATE GO DIFFERENCE` 注释已删除。
- 同态依据：Rust 的 `token_endpoint.origin()` 是 `url::Url::origin()`（special scheme 返回 `(scheme, host, 规范化默认端口)`）；Go `sameHTTPOrigin`（`mcp/oauth_discovery.go:122-140`）= scheme（`EqualFold`）+ hostname（`EqualFold`）+ 有效端口（显式端口，否则 `http→80` / `https→443`）⇒ 值域一致。
- 测试 `TestMCPOAuthDiscoveryBindsIssuerLessTokenEndpointLikeRust` **双向钉死**：反例（跨 origin 被拒）见 §4（回退即 FAIL）；正例（同 origin 的 issuer-less 被接受）随三处 fixture 调整（`mcp/http_client_test.go`、`mcp/oauth_login_id_test.go`、`mcp/oauth_test.go`）保留为正向覆盖，**断言未减少**。

### ② sync654 `#47590`：`reasoningEffortJSONValue` 对 `+007` / `-1` / 超 u64 / 非数字的处置是否与 Rust `serialize_reasoning_effort` 一致 → **是（逐值一致）**

Rust（`C:\rw\codex-rs\codex-api\src\common.rs`，`Reasoning.effort` 挂 `#[serde(serialize_with = "serialize_reasoning_effort")]`）：

```rust
fn serialize_reasoning_effort<S>(effort: &Option<ReasoningEffortConfig>, serializer: S) -> Result<S::Ok, S::Error> {
    if let Some(ReasoningEffortConfig::Custom(value)) = effort
        && let Ok(value) = value.parse::<u64>()
    {
        return serializer.serialize_u64(value);
    }
    effort.serialize(serializer)
}
```

枚举 `ReasoningEffort`（`codex-rs/protocol/src/openai_models.rs`）= `None | Minimal | Low | Medium | High | XHigh | Max | Ultra | Persistent | Custom(String)`（snake_case 序列化）⇒ **只有 `Custom` 会被上面那段拦截**。

Go：`reasoningEffortJSONValue`（`model/responses_agent.go`）= 空串→省略；`!IsKnownReasoningEffort(e)` 时 `strconv.ParseUint(strings.TrimPrefix(e, "+"), 10, 64)` 成功 → 输出十进制 JSON **数字**；否则 `json.Marshal(string)`。Rust 的 `u64::from_str`（`from_str_radix`）接受**一个可选前导 `+` 与前导零**，与 Go 的 `ParseUint(TrimPrefix("+"))` **接受集完全相同**。

逐值对拍（测试 `TestResponsesReasoningEffortWireTypeLikeRust`，13 例 + 生产路径断言）：

| 输入 | Go 线上形态 | Rust 等价路径 | 一致 |
|---|---|---|---|
| `high` / `none` / `disabled` / `ultra` | `"high"` / `"none"` / `"disabled"` / `"ultra"` | 命名变体 → 字符串 | ✅ |
| `64` / `0` / `+7` / `007` / `+007` | `64` / `0` / `7` / `7` / `7` | `parse::<u64>()` → `serialize_u64` | ✅ |
| `18446744073709551615`（u64::MAX） | `18446744073709551615` | 不溢出，数字 | ✅ |
| `-1` / `+` / `experimental` / `18446744073709551616` | 字符串 | `u64` 解析失败 → 原样字符串 | ✅ |
| `""` | 字段省略（`omitempty`） | `skip_serializing_if = "Option::is_none"` | ✅ |

命名集合：Go `knownReasoningEfforts`（`model/reasoning_effort.go`）= `none/minimal/low/medium/high/xhigh/max/ultra/persistent/disabled`；Rust 命名集没有 `disabled`（Go 把 Rust `Persistent` 解析为 `disabled`）⇒ Go 是**超集**，且这两条在两侧都仍是**字符串**，线上类型不变。
值级反例：回退 `model/responses_agent.go` → `responses_agent_test.go:4254: custom number: reasoning body = {"effort":"64"}, want {"effort":64}`（§4）。

### ③ sync656：`interruptThread` 是否只在 threadID 匹配时 cancel、不误伤其它线程 → **是**

Go `app/interactive.go`：`interruptThread(threadID)` 的早退阶梯 —— `c == nil` → false；`TrimSpace(threadID) == ""` → false；`c.threadID != threadID` → **false，且完全不触碰 `cancel`**。匹配时才：取出 `cancel/turnID/steerMailbox` → 清空 `cancel/threadID/turnID` → drain 该 turn 的 steer mailbox → `cancel()`。接线：`OnCloseSide: interactiveLocalSideClose(interrupts, sideCoordinator)` → 先 `interrupts.interruptThread(params.SideThreadID)`，再 `coordinator.Close(params)`。

Rust 对应物（`C:\rw\codex-rs\tui\src\app\side.rs`）：`discard_side_thread`（:428）→ `interrupt_side_thread`（:549-559）先 `active_turn_id_for_thread(thread_id)` 取**该线程自己的** turn，再 `turn_interrupt` / `startup_interrupt`，**之后**才 `thread_unsubscribe`（:438）；`discard_side_thread_in_background`（:450-461）同样先按线程取 turn id。

测试三连（`app/interactive_sideclose_test.go`）：
- 正例 `TestInteractiveLocalSideCloseInterruptsRunningTurn`：关闭本地 side → turn ctx 被 cancel。静音 `interruptThread` 即 **FAIL**：`:104: local side close left the running turn alive: turn context was never cancelled`。
- 反例 `TestInteractiveLocalSideCloseKeepsOtherRunningTurn`：关闭 side **不影响**其它线程的 running turn（静音后仍 PASS ⇒ 该断言专测「不误伤」）。
- 定向 `TestInteractiveInterruptControllerInterruptThreadScopesToThread`：turn 属 `thread-side` 时对 `thread-parent` 调用 → 返回 **false** 且 ctx **未**被 cancel。静音后该用例失败于正向臂（`:158: interruptThread(tracked thread) = false, want true`）。

⇒ **只在 threadID 匹配时 cancel；不匹配（含空/未知线程）为静默 no-op，其它线程不受影响。**

### ④ sync659 `#47974`：在**非 Linux** 上是否只多一条 carveout、无副作用 → **不是空操作；但改动严格单调收窄，无越权/外溢副作用（与 Rust 跨平台语义一致）**

- 测试文件 `sandbox/sandboxpath/gitdir_pointer_test.go` 带 `//go:build linux`，本机（Windows）`go test ./sandbox/sandboxpath/` = `[no test files]` ⇒ **Windows 侧无回归覆盖**。我用 overlay 行为探针补齐（命令见 §8）：真实现 `ok`，静音 carveout 步骤即 FAIL（§4）。
- ⚠️ **修正一条容易得出的误判**：Windows **并非**「不消费该字段」。`ReadOnlySubpaths` 在 Windows 侧被真实消费：
  - `sandbox/windowssandbox/allow.go:25-28`：每个 `ReadOnlySubpaths` 条目 → `paths.Deny[canonical]`
  - `sandbox/windowssandbox/setup.go:382-392`：每个 `ReadOnlySubpaths` 条目 → deny 路径表

  因此在 Windows 上，只要解析出的 gitdir 落在某个 Windows 可写根内，它就是**新增的一条 deny（只读）条目**——这**正是** `#47974` 想要的保护，而非意外副作用。（macOS `sandbox/seatbelt.go:80/127/146` 同理消费。）
- **单调收窄、无外溢**：
  - `addResolvedGitDirCarveouts` **只** `append` 到 `ReadOnlySubpaths`（`containsSubpath` 去重），**从不**修改 `Root` / `ProtectedMetadataNames`，从不删除条目；
  - `isExplicitWritableRoot` 让**显式可写根优先**（不会因 carveout 被降级）；
  - 仅当 `PathWithin(resolved, other.Root)` 成立才追加 ⇒ 影响面限于被解析 gitdir 本身；
  - 解析失败（指针非 `gitdir:`、目标是目录、目标不存在、`EvalSymlinks` 失败）一律 `continue`（不产生条目）。

  ⇒ 只可能**缩小**可写面，绝不放宽。**结论口径**：非 Linux 上「无副作用」应表述为**不越权、不外溢、不改 `Root`/受保护元数据名、不影响被解析 gitdir 之外的路径**；**但不要把它说成「Windows 空操作」**（它会实际生效，与 Rust `codex-rs/protocol/src/permissions.rs` 的跨平台 carve-out 一致，Rust 侧 `resolve_gitdir_from_file` 的语义——`trim` + `split_once(':')` + `prefix=="gitdir"` + 空值拒绝 + 相对目标按指针所在目录解析 + 目标必须存在——与 Go `GitDirPointerTarget` 逐条对应）。

---

## §6 告警与非阻塞发现

1. **覆盖缺口（本批引入，非阻塞）**：`tui/session_source.go` 的两处 `DaybreakEnabled: …Metadata.DaybreakEnabled…,` / `…thread.DaybreakEnabled…,` 映射**没有任何测试覆盖** —— 把这两行静音成 `false` 后 `TestResumeRestoresDaybreakPreferenceLikeRust` 仍然 `ok`（该测试直接构造 `SessionSummary`，走的是 `session_picker.go:407-412`）。即「持久化记录 → `SessionSummary`」这一环无断言。建议后续补一条 `sessionSummaryFromRecord` / `sessionSummaryFromAppServerThread` 层面的用例。
2. **`sandbox/sandboxpath` 在 Windows 无测试**（`//go:build linux`），而本批新增的 `#47974` 逻辑在 Windows **会实际生效**（§5④）却没有平台覆盖。建议把平台中立内核（`GitDirPointerTarget` + `addResolvedGitDirCarveouts`）的用例移出 linux 门限，或加 Windows 专用用例。
3. **`app/interactive.go` 被本批 3 笔共同修改**：`9762262c`（新线程 Daybreak 种子）、`240913ad`（side-close interrupt）、`999e655d`（startup settings 回灌）。三处**符号/字段互不相交**，且各有定向测试钉住（§4）；但**整体回退 `app/interactive.go` 会把三件事一起回退**。台账请勿把该文件归给单一 sync 号。
4. **`mcp/oauth_discovery.go` 的 fixture 语义变更**（`#39935`）：三处 fixture 把「issuer-less 跨 origin 的 token endpoint」改为同 origin。**断言未减少**（转为正向覆盖），但这意味着**旧形状的 fixture 现在会被新校验拒绝**，属预期行为；将来按旧形状新增用例需注意。
5. **已知 flaky / 基线（沿用，本批未新增）**：`./tui/tea/` `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge`（Windows 基线，两侧逐字相同）；`./appserver/` 6 条 shell-snapshot / executor-skill 族 + `TestOtelProviderReloadsAfterAccountChange`、`TestRuntimeRouterTurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust`（时序 flaky）。本批**未触达 `appserver/`**（§3 union 里没有该目录）。
6. **环境事件（非代码，供台账记录）**：作业中途本机 `D:\tmp` 被**外部**清理，我上一轮的 scratch 树（`rc13`、`aud13_head`、`aud13_base` 等）被删。我已在新位置 `D:\qax\reagent\dev\codex_go_wt\synct5v13{head,base}` 重建 LF 树并**全套重跑**；本报告 §2/§4 的全部结论均来自**重建后的复跑**，§1 的握手在 `D:\tmp\rp13` 内复跑得 `MATCH=11/11`。

---

## §7 只读声明与资源

- **0 commit / 0 push / 0 ref 移动**；所有重放以 `git cherry-pick -n` + `git reset --hard` 收尾；未执行任何 `Remove-Item` / `cmd /c del`（本机策略拦截删除命令，`D:\tmp` 的历史残留不在本次范围）。
- 本次新建、**未提交、无 ref** 的临时 worktree（可随时 `git worktree remove`）：
  - `D:\qax\reagent\dev\codex_go_wt\synct5v13head`（detached @`c9fbd10f`）
  - `D:\qax\reagent\dev\codex_go_wt\synct5v13base`（detached @`d12d00dd`）
- 沿用：`D:\tmp\rp13`（重放树，现 detached @`c9fbd10f`）；overlay 探针在 `D:\tmp\probe13\`（`probe_gitdir_test.go` + `overlay.json`）。
- 本产物：`update/verify_main_c9fbd10f_2026_10_07.md`（**未跟踪**，由队长提交）。

---

## §8 复跑命令

```powershell
# 0) 远端对齐
git fetch origin --prune
git ls-remote origin refs/heads/main refs/heads/integ86g
git merge-base --is-ancestor d12d00dd origin/main; echo $LASTEXITCODE
git rev-list --count d12d00dd..origin/main ; git rev-list --count --merges d12d00dd..origin/main
git diff --shortstat d12d00dd c9fbd10f

# 1) tree-sha 握手（逐笔）
git worktree add D:\tmp\rp13 --detach d12d00dd
# 对每笔 C: git reset --hard C^ ; git -c user.name=syntropy -c user.email=syntropy@local cherry-pick -n C ; git write-tree ; git rev-parse C^{tree}

# 2) LF 树门禁
git -c core.autocrlf=false -c core.eol=lf worktree add --detach D:\qax\reagent\dev\codex_go_wt\synct5v13head c9fbd10f
git -c core.autocrlf=false -c core.eol=lf worktree add --detach D:\qax\reagent\dev\codex_go_wt\synct5v13base d12d00dd
cd D:\qax\reagent\dev\codex_go_wt\synct5v13head
$files = git diff --name-only d12d00dd c9fbd10f ; gofmt -l @files ; go build ./...
go vet ./app/ ./doctor/ ./mcp/ ./model/ ./sandbox/sandboxpath/ ./tui/ ./tui/bottom_pane/ ./tui/chatwidget/ ./tui/tea/ ./tui/agents_overview/
go test ./app/ ./doctor/ ./mcp/ ./model/ ./tui/ ./tui/bottom_pane/ ./tui/chatwidget/ ./tui/tea/ ./tui/agents_overview/ -count=1   # 两侧同命令，base 在 synct5v13base
$env:CODEX_RUST_ROOT='C:\rw\codex-rs' ; go test ./parity/ -count=1

# 4) 逐笔 RC（回退组）
cd D:\tmp\rp13 ; git reset --hard C ; git checkout C^ -- <prod files> ; go test <pkgs> -run <regex> -count=1 ; git reset --hard C

# 4b) overlay 行为探针（sync659，Windows）
#   D:\tmp\probe13\overlay.json = {"Replace":{"D:\\tmp\\rp13\\sandbox\\sandboxpath\\probe13_gitdir_test.go":"D:\\tmp\\probe13\\probe_gitdir_test.go"}}
#   （把 rp13 reset --hard c9fbd10f 后：）
go test ./sandbox/sandboxpath/ -run TestProbeGitDirCarveoutLikeRust -count=1 -overlay=D:\tmp\probe13\overlay.json
```

---

## §9 main 前进登记

- 审计开始时：`origin/main == origin/integ86g == c9fbd10f56d75e011b193f2bc3ad6e195bde05a9`（本报告对象）。
- 报告落盘前复查：`origin/main == origin/integ86g == 059c4de9d2e4ca0090616c8ec26cd995023af82d`。
  - 新增 **1 笔未审计**：`059c4de9 sync660: fail MCP OAuth login when the issuer binding rejects the metadata like Rust (#39935)`（3 files, +131/−22）。
  - `git merge-base --is-ancestor c9fbd10f origin/main` → exit=0 ⇒ 仍是线性快进，`c9fbd10f` 的内容未被改写，**本报告的 §1–§5 结论对 `c9fbd10f` 持续有效**。
  - `c9fbd10f..059c4de9` 这 1 笔**不在本次审计范围**，需另派审计。

---

## 结论

**GREEN / 可发布（对 `c9fbd10f`）。**

- 11 笔逐笔 tree-sha 握手 **11/11 一致**；线性、无 merge、无改写；`d12d00dd` 为祖先（纯增量）。
- LF 门禁：`gofmt -l` 空、`go build ./...` rc=0、`vet` 两侧同为 1 条既有告警（无新增）、受影响包整包对拍**新增失败 0**（唯一 FAIL 为已知 Windows 基线 `tui/tea` 1 条，两侧逐字相同）、`parity` `ok`。
- 逐笔真实性：`union == range`（39/39，无夹带）、逐笔 numstat 加总 = range shortstat（+1479/−122）、**39/39 无 CRLF / 无 BOM**、`ls-files` Δ8 = 8 个新增测试文件。
- 逐笔 RC：**11/11 均拿到可复现值级反例**。
- §5 四项点名：①**逐字同态**；②**逐值一致**（`status`）；③**只在匹配线程 cancel**；④ **非 Linux 并非空操作**，但改动**单调收窄、无越权/外溢**（修正一处易犯误判，见 §5④）。
- 告警：**无阻塞项**。两条非阻塞建议见 §6（`session_source.go` 的 Daybreak 映射无覆盖；`sandboxpath` 的 `#47974` 逻辑在 Windows 生效却只有 linux 门限测试）。
- 唯一需队长注意的流转项：main 已前进到 `059c4de9`（+1 笔 `#39935` 续作），**该笔未审计**。