# r86 · 后冻结点工作清单 + 补丁镜像枯竭裁定（队长 syntropy · 2026-10-07）

> 基线：Go `origin/main = origin/integ86g = 9a62462bc8e9e8153670aab0e776d5caeb5d2d3a`（实测 `git ls-remote`）
> Rust freeze pin：`5b0b2530354052b9194156d70d4c94a439368342`；Rust `origin/main = 37eaae6eeb71c3bd3d165f52515086db8f5e7dee`（`git fetch origin main` 实测，`5b0b253035..37eaae6eeb` 前向快进）
> 本轮无人 push 到 Go 侧（远端未前进，只有我在落主）。

---

## 1. 补丁镜像已枯竭（可重放性审计结论）

审计来源：`update/r86_patch_ledger_2026_10_07.md`（syncl5，30268 B，含 30 包逐一 bytes/sha256 + 前向 `git apply --check` + 反向 `-R` + 逐文件 landed 判定）。

| 结论分类 | 数量 | 说明 |
|---|---|---|
| 已落地（冗余，禁重放） | 24 | 反向 `-R` exit=0 或逐文件 reverse-check 全过 |
| 已落地（冗余，作废） | 2 | 上下文漂移/部分文件被其它已落补丁取代 |
| 作废（superseded，禁重放） | 3 | `syncl1_49912` / `syncw1_49076`(+`syncl3_49076` 同族) / `syncw4_49147`(旧) |
| **可落地（待批）** | **1** | `syncw4_49147.patch`（1969 B） |

**唯一「前向可应用」项的裁定：不收录。**
- 内容 = 新增文件 `chatgptapi/cloud_tasks_normalize_like_rust_test.go`（**纯测试**，`TestNormalizeCloudBaseURLLikeRust` / `TestNormalizeCloudBaseURLEmptyFallsBackLikeGo`）。
- `#49147` 的**生产面已对齐**（`chatgptapi/cloud_tasks.go:175 NormalizeCloudBaseURL`），**回归覆盖已落地**：`5078de6d sync610: cover cloud base URL normalization like Rust (#49147)`（`chatgptapi/cloud_tasks_normalize_test.go`）。
- ⇒ 收录 = 同一行为第二份不同命名的测试，只增加维护重复，无新增覆盖 ⇒ **裁定：不落主、标记冗余**。

**结论：`update/r86_patches/` 内除下述 4 个「在飞单」外，无任何待落主补丁；全部标记 `DO NOT REPLAY`（重放会重复定义同名测试/函数 ⇒ 编译失败）。**

在飞（尚未交付，不在镜像内）：
1. `#49584(b)` → syncw3（**必须补仓内正式回归测试**）
2. `#50756` → syncl5（已批准）
3. `#49714` → syncw4（已授权补能力，`model/`）
4. `#49624` cwd 残留 → syncw2（先行为判定；若需 `cli/cli.go` 只报撞面）

---

## 2. 冻结点之后的上游新增（唯一新料）

```
$ git -C C:\rw rev-list --count 5b0b253035..origin/main
2
$ git -C C:\rw log --oneline --no-merges 5b0b253035..origin/main
37eaae6eeb Require hostname authorization before proxy DNS lookups (#51650)
a513012869 Fix retained context handling for typed section content (#51642)
```

| # | sha | 判定 | 依据 |
|---|---|---|---|
| 1 | `a513012869` | **N/A（已判）** | `#51642` 为类型层修正（Go 无 `SectionContent`/`TranscriptRecord`）；`state/guardian_retained_context.go:227-240` 已按冻结后语义匹配，`state/guardian_retained_context_test.go:543 TestRetainedInstructionSectionsSplitLikeRust` 已覆盖（syncl3 结论，已采信） |
| 2 | `37eaae6eeb` | **真缺口（唯一可落地）** | 见 §3 |

⇒ 上游「新功能」管线目前**极薄**：冻结点之后仅 2 笔，其中 1 笔已 N/A。**剩余真正的功能缺口是 5 组「冻结子系统聚类」**（见 §5），它们不是单 PR，需产品/架构裁定。

---

## 3. `#51650` 判定：Go 侧真缺口（安全相关）

Rust `--stat`：9 文件 / +357 −35（`network-proxy/src/{host_policy_tests,mitm,mitm_tests,network_policy,proxy,runtime}.rs`、`sandboxing/src/{seatbelt,seatbelt_network_tests,seatbelt_tests}.rs`）。
上游 commit message 的四条语义 ↔ Go 逐条对照：

### (1) 未获批主机不得触发 DNS（**Go 有同病**）
Go `network/proxy_server.go:1625-1634`（`evaluateProxyPolicy` 内）：
```go
if !settings.AllowLocalBinding {
    ...
    } else if (allowed || s.policyDecider != nil) && proxyHostResolvesToNonPublicIP(normalizedHost, request.Port) {
        return deny(ProxyReasonNotAllowedLocal, ProxyDecisionSourceBaselinePolicy)
    }
}
```
- `s.policyDecider != nil` 时（正常交互路径：审批要问用户），**在调用 decider 之前**就已解析 DNS。
- decider 在 `:1647` 才被调用。⇒ 与 Rust 修前完全同病：**最终被拒的主机名已泄露给 DNS**。
- Rust 修后语义 = 只有「allowlist 命中」或「策略批准」之后才允许做地址解析。

### (2) 审批后必须重查 baseline 限制（**Go 缺失**）
Go 的 local/private baseline 检查只在 decider **之前**、且仅在 `!settings.AllowLocalBinding` 分支内；decider 返回 Allow 后（`:1649-1653`）**没有任何重查**。
Rust 修后：local 与 remote 两条审批路径在 decider 之后都要重查，避免审批绕过显式 deny 或 local/private 限制（并修「controller 显式 `allow_local_binding=false` 被 executor `allow_local_binding=true` 覆盖」）。

### (3) CONNECT 获批后，内层 HTTPS 仍要保 DNS 检查（待定，需查 mitm 路径）
Rust 明确：「Preserve DNS checks for inner HTTPS requests after an approved CONNECT, including hosts without a persisted allowlist entry」。Go 侧 `network/proxy_server.go:1755` 另有一处 `net.DefaultResolver.LookupIPAddr`（需判定它是否属 CONNECT 内层路径、是否对「无持久 allowlist 条目」的主机被跳过）。

### (4) Seatbelt 外部 DNS 许可（Go = macOS-only 面，本节点不可验证）
Rust 删掉了 Seatbelt 对外部 DNS 的放行（保留 local binding 与 loopback proxy 访问）。Go 的对应面在 `sandbox/` 的 darwin 文件；**Windows 节点无法实跑验证** ⇒ 只能做「结构对齐 + 标注不可验证」，或转 Linux/macOS 车道。

### 可测试性前提（关键设计点）
Go 的 DNS 调用是包级 `net.DefaultResolver.LookupIPAddr`（`:1538`、`:1755`），**不可注入** ⇒ Rust 用来断言「零 DNS」的注入式 lookup 闭包在 Go 无 seam。
**要让「未获批主机零 DNS」成为可回归的值级断言，必须先引入 seam**（`ProxyServer` 上的 `lookupHost` 字段或包级可覆盖 var），否则无法写出该测试。

### 落点预估（Go）
- `network/proxy_server.go`（`:1579 evaluateProxyPolicy` 的授权/DNS 次序 + 审批后重查；`:1538`/`:1755` 的解析点改走 seam）—— 主改动
- `network/proxy_responses.go` / `proxy_policy.go`（若需新 reason 文案对齐 Rust）
- `network/proxy_server_test.go`（新增：零 DNS / 审批后私有地址拒绝 / 审批次序 / CONNECT 后 DNS）
- （可选，darwin 面）`sandbox/` 的 seatbelt 网络放行删除 —— **标注「本节点不可验证」**
预计 **3–5 文件**（不含 darwin 面）。

---

## 4. 派单口径（已下发）
- **`#51650` → syncl4**：先给 §3 的 (1)(2)(3) 值级对照齐备版 + `(4)` 的不可验证标注；实现 seam + 次序修复 + 回归测试；交付 `update/r86_patches/syncl4_51650.patch`（bytes + sha256 + `git apply --check` 打 `9a62462b`）+ ≥1 条**值级** RC（撤「授权前置」⇒「零 DNS」断言 FAIL；撤「审批后重查」⇒ 私有地址断言 FAIL）+ `go test ./network/ ./appserver/` 对拍 + `parity`。
- 避让面不变：`appserver/runtime_router.go`、`tui/state.go`、`sandbox/windowssandbox/`。

---

## 5. 仍需你（用户）裁定的结构性缺口（多轮未决）
冻结点之后已无「单 PR 级」新料；**剩余功能缺口全部是聚类**，且已被多个车道反复证明「Go 无载体」：
1. Guardian v2 异步评分器（`async_scorer`：`CachedScore`/`WrapperLag`/`cached_evidence`）—— Go 为简化同步 reviewer，恒 fail-closed（#51400 判无载体）
2. executor shell 快照（`TestShellSnapshotCommandMetrics*` 一族为既有红）
3. TUI 核验视图（`transcript_view` / `owned_transcript`）—— Go 整体缺失（#48775/#50389 判无载体）
4. windows-sandbox-service
5. TUI 快照 prune

以及 3 个已登记的新项：
- `#49786` 的 V2 spawn 描述**基底组合器** + `model_catalog_in_context` 接线（需破 ⛔ `runtime_router.go`）
- GPT-6 家族进 bundled fallback 目录（Go 不跟踪 `models-manager/models.json`；波及 `tui/`、`app/`）
- `#49993` 的 sync 拆分残余面（**只在** Go 真存在「stateful 同步 reviewer」路径时成立，需与 `appserver/` 车道对表）
