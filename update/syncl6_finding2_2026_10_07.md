# syncl6 · r88c · 发现②「A 组」7 行字段对齐交付报告

- 派单：`msg-1791377999413421700-5484`（r88c，队长批准 A 组 7 行，A3 不排除）
- 车道：syncl6（Linux 节点 `de1bb1e71f8f7ad6969025798b555057`）
- 日期：2026-10-07
- 产物：`update/r86_patches/syncl6_finding2.patch`（2 文件）+ 本报告
- 结论：**7 行全部落地**，仓内值级回归测试钉住 → 基线失败集新增 **0**；3 条值级 RC 全过；`parity` ok。
- **用户可见变更**：有（见 §6 登记，含 gpt-5.5 / 5.6-* picker 文案、`codex-auto-review` 上下文预算 1000000→872000、两处 bytes→tokens 截断口径）。

---

## 0. 环境自检原文

```
$ cd /home/jacks/jacks_dev/codex_go && git rev-parse HEAD
a32e1c35facdbfec20887cce46da126d6ac0e68b
$ git rev-parse origin/main
c9c6a200de74ab129493641d1ea0cfa1797099b5      # sync644
$ git status --porcelain
?? scripts/loc_report.sh                        # 既存 untracked，非本单
```

Rust 上游（只读对照仓）：

```
$ cd /home/jacks/jacks_dev/codex && git rev-parse origin/main
d83bb540ec64bf6b009bca0283b0be91ea33f26a        # 枚举 pin
$ git log -1 --format='%H %s' origin/main
d83bb540ec64bf6b009bca0283b0be91ea33f26a Move Windows sandbox tests into a dedicated integration binary (#51678)
```

工作树：`git worktree add --detach /tmp/wt-syncl6-finding2 c9c6a200`（detached，**未建/未移任何 ref**）。

---

## 1. 逐行落点与 Rust 对照依据（`model/catalog.go`）

对照真源 = `b17c74cfd5:codex-rs/models-manager/models.json`（Rust 上游该 pin 的 bundled 目录；
Go `fallbackBundledModelsResponse()` 是该文件的离线镜像，文件内注释即如此声明）。
取料命令：`git -C /home/jacks/jacks_dev/codex show b17c74cfd5:codex-rs/models-manager/models.json`。

| # | Go 落点 | 改前 | 改后 | Rust 依据（models.json @ b17c74cfd5） |
|---|---------|------|------|----------------------------------------|
| 1 | `model/catalog.go:961` | `gpt-5.6-sol` desc = `Latest frontier agentic coding model.` | `Older generation workhorse model.` | L727 `"description": "Older generation workhorse model."` |
| 2 | `model/catalog.go:973` | `gpt-5.6-terra` desc = `Balanced agentic coding model for everyday work.` | `Older balanced model for straightforward work.` | L868 `"description": "Older balanced model for straightforward work."` |
| 3 | `model/catalog.go:985` | `gpt-5.6-luna` desc = `Fast and affordable agentic coding model.` | `Older fast and efficient model.` | L1009 `"description": "Older fast and efficient model."` |
| 4 | `model/catalog.go:999` | `gpt-5.5` desc = `Frontier model for complex coding, research, and real-world work.` | `Legacy coding model.` | L1376 `"description": "Legacy coding model."` |
| 5 | `model/catalog.go:1008` | `gpt-5.5` truncation = `{Mode: bytes, Limit: 10000}` | `{Mode: tokens, Limit: 10000}` | L1351-1354 `"truncation_policy": { "mode": "tokens", "limit": 10000 }` |
| 6 | `model/catalog.go:1050` | `codex-auto-review` truncation = `{Mode: bytes, Limit: 10000}` | `{Mode: tokens, Limit: 10000}` | L1476-1479 `"truncation_policy": { "mode": "tokens", "limit": 10000 }` |
| 7 | `model/catalog.go:1052` | `codex-auto-review` `MaxContextWindow = 1000000` | `872000` | L1496 `"max_context_window": 872000` |

Rust 原文摘录（gpt-5.5 段）：

```
1340:      "slug": "gpt-5.5",
1351:      "truncation_policy": {
1352:        "mode": "tokens",
1353:        "limit": 10000
1354:      },
1370:      "context_window": 272000,
1371:      "max_context_window": 272000,
1376:      "description": "Legacy coding model.",
```

Rust 原文摘录（codex-auto-review 段）：

```
1465:      "slug": "codex-auto-review",
1476:      "truncation_policy": {
1477:        "mode": "tokens",
1478:        "limit": 10000
1479:      },
1495:      "context_window": 272000,
1496:      "max_context_window": 872000,
1501:      "description": "Automatic approval review model for Codex.",
```

> 说明：Rust 中 `gpt-5.6-*` 三条的 `truncation_policy` 已是 `tokens`、`max_context_window` 已是 872000，
> Go 该三行本已对齐（无改动）；A 组只覆盖上表 7 处仍漂移的字段。
> D 组（缺 `gpt-daybreak-blue/red-latest`、多 `gpt-5.2` / `gpt-5.4-mini`）**本单不改**，按队长裁定入 backlog。

---

## 2. 改动集（仅 2 文件，均在批准写集内）

```
$ git -C /tmp/wt-syncl6-finding2 diff --stat
 model/catalog.go      | 14 ++++++-------
 model/catalog_test.go | 58 +++++++++++++++++++++++++++++++++++++++++++++++++++
 2 files changed, 65 insertions(+), 7 deletions(-)
```

`model/catalog.go`：7 行值替换（4 条 description + 2 条 truncation mode + 1 条 max_context_window）。
`model/catalog_test.go`：新增 `TestFallbackCatalogCarriesModelsJSONFieldValues`（追加于文件末，有序断言，
失败信息确定），**值级钉住这 7 行**（不是只改数字不钉）：4 条 description 逐条比对、`gpt-5.5` 与
`codex-auto-review` 的 `TruncationPolicy`=`{tokens,10000}`、`codex-auto-review.MaxContextWindow=872000`
并顺带钉 `ContextWindow=272000`（防误改）。

---

## 3. 门禁 6 步原文

```
$ gofmt -l model/catalog.go model/catalog_test.go      # 空输出
（rc=0）

$ go build ./...
（空输出，rc=0）

$ go vet ./model/
model/responses_agent.go:1462:11: assignment copies lock value to clone: codex_go/model.ResponsesAgentRunner contains sync.Mutex
（rc=0；仅既存 lock-copy，非本补丁）
```

基线对拍（受影响 3 包，整包 `-count=1`，与改动前**逐字同失败集**）：

```
$ # 基线（c9c6a200 干净树）
$ go test ./model/ -count=1      → FAIL（2 条，均 env 型）
      --- FAIL: TestRefreshBedrockAWSCredentialsRunsCommandOnceAndReloads
      --- FAIL: TestRefreshBedrockAWSCredentialsBoundsProviderRecoveryPerRequest
$ go test ./app/ -count=1        → ok  codex_go/app    13.043s
$ go test ./appserver/ -count=1  → FAIL（4 条，均 env 型）

$ # 改动后（/tmp/wt-syncl6-finding2）
$ env -u TERM go test ./model/ -count=1      → FAIL（同上 2 条）
$ env -u TERM go test ./app/ -count=1        → ok   codex_go/app
$ env -u TERM go test ./appserver/ -count=1  → FAIL（同上 4 条）

$ # 失败集 diff（按 `--- FAIL:` 行排序）
$ diff <(grep -oE '^--- FAIL: [A-Za-z0-9_]+' base_model.txt|sort) <(... after_model.txt|sort)
（空）
$ diff <(grep -oE '^--- FAIL: [A-Za-z0-9_]+' base_appserver.txt|sort) <(... after_appserver.txt|sort)
（空，两侧各 4 条同名）
$ app 两侧均为空集
```

结论：**新增失败 0**。基线 env 型红（model 2 / appserver 4）与本次改动无关，两侧逐字一致。

```
$ CODEX_RUST_ROOT=/home/jacks/jacks_dev/codex/codex-rs go test ./parity/ -count=1
ok  	codex_go/parity	0.791s
```

---

## 4. 值级 RC 原文（撤接线 ⇒ FAIL ⇒ 还原 ⇒ ok ⇒ sha256 一致）

复跑脚本 `/tmp/syncl6/r88c/rc.sh`（撤一行 → 跑目标测试 → 从固定副本逐文件还原 → 复跑 → 校验 sha256）。
测试命令：`env -u TERM go test ./model/ -run TestFallbackCatalogCarriesModelsJSONFieldValues -count=1`。

### RC-1（`:1052` codex-auto-review `max_context_window` 872000 → 1000000）

```
### RC-1: codex-auto-review max_context_window 872000 -> 1000000
    catalog_test.go:2199: codex-auto-review max_context_window = 1000000, want 872000 (models.json @ b17c74cfd5)
FAIL
FAIL	codex_go/model	0.005s
FAIL
restored; run again:
ok  	codex_go/model	0.005s
2e0a08a638c0f43a043a770d2161184c7c82d946a11eb90867a473f655c9be98  model/catalog.go
```

### RC-2（`:1008` gpt-5.5 `truncation_policy` tokens → bytes）

```
### RC-2: gpt-5.5 truncation tokens -> bytes (line 1008)
    catalog_test.go:2187: gpt-5.5 truncation_policy = {Mode:bytes Limit:10000}, want {mode:tokens limit:10000}
FAIL
FAIL	codex_go/model	0.006s
FAIL
restored; run again:
ok  	codex_go/model	0.005s
```

### RC-3（`:961` gpt-5.6-sol description 还原为旧文案）

```
### RC-3: gpt-5.6-sol description revert
    catalog_test.go:2176: gpt-5.6-sol description = "Latest frontier agentic coding model.", want "Older generation workhorse model." (models.json @ b17c74cfd5)
FAIL
FAIL	codex_go/model	0.006s
FAIL
restored; run again:
ok  	codex_go/model	0.006s
2e0a08a638c0f43a043a770d2161184c7c82d946a11eb90867a473f655c9be98  model/catalog.go
9481c881455550e321cad49cf3a23d58f416c2fb0e89563d4370faaa8c1aa34d  model/catalog_test.go
```

还原后两文件 sha256 与补丁产出的定稿逐字节一致：

```
$ sha256sum model/catalog.go model/catalog_test.go
2e0a08a638c0f43a043a770d2161184c7c82d946a11eb90867a473f655c9be98  model/catalog.go
9481c881455550e321cad49cf3a23d58f416c2fb0e89563d4370faaa8c1aa34d  model/catalog_test.go
```

补丁在 RC 前后自比对一致（证明 RC 过程未污染定稿）：

```
$ sha256sum syncl6_finding2.patch verify.patch（RC 后重生成）
b656a9f767406e87bc1f14839a5463872689e091bc63fd5b37d23c0ce45f2c7d  syncl6_finding2.patch
b656a9f767406e87bc1f14839a5463872689e091bc63fd5b37d23c0ce45f2c7d  verify.patch
```

---

## 5. 补丁交付契约

```
$ git -C /tmp/wt-syncl6-finding2 add -A -N && git -C /tmp/wt-syncl6-finding2 diff --no-color --binary > syncl6_finding2.patch
$ wc -c syncl6_finding2.patch
6645
$ sha256sum syncl6_finding2.patch
b656a9f767406e87bc1f14839a5463872689e091bc63fd5b37d23c0ce45f2c7d  syncl6_finding2.patch
$ file syncl6_finding2.patch
unified diff output text, 1st line "diff --git a/model/catalog.go b/model/catalog.go" ... ASCII text
$ head -3 | cat -A
diff --git a/model/catalog.go b/model/catalog.go$
index ef94c5ff..7404b48b 100644$
--- a/model/catalog.go$
```

LF、无 BOM、无 CR（`cat -A` 行尾仅 `$`），仅含 2 个目标文件。

`git apply --check` 打在基线 `c9c6a200`（`git worktree add --detach /tmp/wt-syncl6-finding2-base c9c6a200`，
`core.autocrlf` 未设置 ⇒ 干净 LF 树）：

```
$ cd /tmp/wt-syncl6-finding2-base && git rev-parse HEAD
c9c6a200de74ab129493641d1ea0cfa1797099b5
$ git apply --check -v /tmp/syncl6/r88c/syncl6_finding2.patch
Checking patch model/catalog.go...
Checking patch model/catalog_test.go...
（rc=0）
```

---

## 6. 用户可见行为 / 口径变更登记（供发布说明）

1. **离线默认模型不变**（`GPT-6.1-Sol`，sync634 起）——本单不改 priority，不影响默认选择。
2. **picker 文案变更**（用户可见）：`gpt-5.6-sol` / `gpt-5.6-terra` / `gpt-5.6-luna` 的模型描述由
   「Latest frontier… / Balanced… / Fast and affordable…」改为 Rust 现行文案
   「Older generation workhorse model. / Older balanced model for straightforward work. /
   Older fast and efficient model.」；`gpt-5.5` 由「Frontier model for complex coding, research,
   and real-world work.」改为「Legacy coding model.」。
3. **`codex-auto-review` 上下文预算变更**（行为级，涉及审查模型可读输入量）：
   `max_context_window` `1000000 → 872000`（对齐 Rust / models.json）。该值是该审查模型的上下文预算，
   属用户可见口径变更。
4. **`A3`（不排除）——`gpt-5.5` 截断口径变更**：`truncation_policy` `bytes/10000 → tokens/10000`。
   `gpt-5.5` 仍是 `VisibilityList` 的活跃可选模型，其截断单位由「字节」改为「token」，**这正是本次修复本身**
   （此前 Go 与 Rust/models.json 口径相反）；`codex-auto-review` 同步由 bytes 改为 tokens。
5. **回滚路径**：
   - 整体回滚：`git checkout c9c6a200 -- model/catalog.go model/catalog_test.go`（或 revert 本单 2 文件的补丁）。
   - 仅回滚「用户可见面」：把上述 7 行改回原值即可（原值见 §1 表「改前」列）；`gpt-5.5` / `5.6-*`
     文案若只想回滚 picker 而不动截断口径，仅还原 `:961/:973/:985/:999` 四行 description。

> 说明（残余组合漂移，未静默）：Go fallback 仍缺 Rust 已有的 `gpt-daybreak-blue/red-latest`，且多出
> Rust 已移除的 `gpt-5.2` / `gpt-5.4-mini`；**本次只做 A 组 7 行对齐，不增删条目**（队长已裁定 D 组入 backlog）。

---

## 7. 未落地项的正向陈述（可复跑）

- **D 组（增删条目）**：不含在本单。可复跑核查：
  ```
  $ git show b17c74cfd5:codex-rs/models-manager/models.json | grep -c '"slug"'
  （Rust slug 数）
  $ grep -c 'Slug:' /tmp/wt-syncl6-finding2/model/catalog.go   # Go fallback slug 数
  ```
  差异即 D 组；删 `gpt-5.4-mini` 会破既有 `TestFallbackBundledCatalogDropsGPT54LikeRust`，故不动。
- **路线 (c)（`//go:embed` 跟踪 `models.json`）**：未开闸，本轮 0 改动（成本估计见
  `update/syncl6_gpt6_bundled_scope_2026_10_07.md` / `update/syncl6_gpt6_landed_audit_2026_10_07.md`）。
- **WS 采样残余**（`model/responses_agent.go:972 runWebSocket` content-filter 不记 guidance / 无 stream 重试环）：
  已登记，暂不派工；如需开工，先只读给「有无重试环」两方案对比 ≤1 页再裁。

---

## 8. 未决问题 / 待裁

1. 无阻塞项。本单 7 行均为「值对齐 + 值级回归钉住」，无架构决策需求。
2. 队列：本单交付后**队列空**，请队长派新单（候选人已在队长侧 worklist：`#50396`/`#48775` 属 `tui/`＝syncl3 域；
   `execserver/` 等 Linux 面待排）。
