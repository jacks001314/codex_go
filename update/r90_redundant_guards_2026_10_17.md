# 冗余实现 / 无效守卫登记表（第 90 轮，leader 维护）

用途：记录**已经存在于 Go、但实际不起作用**的实现，和**守不住任何东西的守卫**。
登记 ≠ 要修。目的是防止后续车道把空转代码当缺口去"补"，或把无效守卫当护栏去信任。

新增条目时请给：位置（`file:line`）、为什么无效、依据（谁实测、哪个证据文件）、处置（不改 / 已派修 / 待立项）。

---

## RG-1 · plugin merge 层激活策略（空转）+ 守合成输入的用例

- 位置：`plugin/api.go:3224`、`plugin/curated_routing.go:171` 在 merge 路径调用 `applyPluginActivationPolicy`；
  对应用例 `plugin/marketplace_config_test.go:104-107` 喂的是 `discovered{Enabled:true}`。
- 为什么无效：`plugin/marketplace_loader.go:164 marketplacePluginDetailFromManifest` 构造 `PluginSummary`（`:207-223`）时
  **从不设置 `Enabled`**，而 `plugin/api.go:1912` 的 `!Enabled && !Installed ⇒ Enabled=true` 归一化只在 `addPluginLocked`（`:1890`）
  里对**存入服务**的详情生效，不作用于 `loadMarketplacePlugins` 的产出。
  ⇒ discover-only 详情在**父提交上就已经是 `false`**，merge 层只能 clear、不能 set ⇒ 调用**空转**；
  且该用例的输入在**真实 loader 路径上不可能出现**，守的不是真实现象。
- 依据：V-b10（`update/r90_verify_v12_2026_10_17.md`）。实测 A/B 同探针：
  `tool@acme` 在父/新两侧**恒为 false**；真正可判别的是**已安装/配置**路径
  （`_default=false` + installed 未写 `enabled`：父 `true` → 新 `false`）。
- 处置：**只登记，不改码**。产品行为无错（无 over-/under-enable）。
  若日后有人让 loader 开始设置 `Enabled`，本条需重开复核。

## RG-2 · appserver Go-only 别名门控守卫（有洞）

- 位置：`appserver/schema_test.go:975-984`（别名循环只遍历 `goOnlyExperimentalAliases` 注册表）、
  `:993-1007`（over-gated 循环遍历 Rust `ClientRequest.json` 的 OneOf，只有 snake_case），
  且 `:998-1000` 对 `goOnlyAliases` 显式 `continue`。
- 为什么无效：**同时**撤 runtime 门控 + 撤注册 ⇒ 整个 `appserver` 包仍
  `ok codex_go/appserver 121.865s`，**无任何断言发现**；而该字面量在 Go 真实可达
  （`protocol.go:33`、`router.go:1104`、`runtime_router.go:2655` 门、`:3915`/`:17233`）⇒ 真解除门控。
- 依据：V-b9（`update/r90_verify_v11_2026_10_17.md` §RC-D，判定 `rejected`）。
- 处置：**已派修**（P-b11，pin 车道）：删 `:998-1000` 的 `continue`，改为对别名也断言
  `experimentalAPIMethod(alias) == true`；验收要求「修后同时撤必须变红」。修完请把本条改为「已闭环」。

## RG-3 · 设计类：Go 从不启外部扫描器做 deny-glob 展开（系列永不落地）

- 位置：`sandbox/linuxsandbox/linux.go:591 expandLinuxDenyGlob` / `:596 doublestar.FilepathGlob`；
  `sandbox/windowssandbox/deny_read_resolver.go:136 collectExistingGlobMatches`；
  由测试注释冻结：`sandbox/linuxsandbox/linux_test.go:142-148`「**Go never invokes ripgrep for this walk**」。
- 为什么是"设计类"而非缺口：上游 `expand_unreadable_globs_in_environment` / `GLOB_SCAN_PROGRAM` /
  `resolve_windows_deny_read_paths_in_environment` 这一族是**显式环境扫描器入口**；只要 Go 维持
  「进程内走查、不启 `rg`」的设计，它们**永远不会有对应物**。
- 依据：B-b10（`update/r90b_b10_2026_10_17.md`）——符号级 0 命中 + 逐 hunk 行为等价核对。
- 处置：**不要再派工评估**；如需引入外部扫描器，那是**设计变更**，不是同步。

## RG-4（方法类，已进 runbook，不重复展开）

- 「0 命中 ⇒ N/A」必须带**时点 sha**；资产类改动按**路径**判（`git ls-files`）而非符号 grep。
  详见 `update/leader_runbook_2026_10_07.md` §R5-sexies / §R5-septies。