# r87 · syncl4 域扫描（`network/` + 相邻 `sandbox/`，非 `windowssandbox/`）

- 车道域：Go `network/`（proxy / MITM / host policy / 环境策略）+ 相邻 `sandbox/`（**排除** `sandbox/windowssandbox/`、`appserver/runtime_router.go`、`tui/state.go` 避让面）
- Rust 上游新 pin：**`b17c74cfd5ebb39fe70ffaff78de198120278636`**（= `origin/main`）
- Go 最新 main：**`185d03c933086ddd2b0a1f498fdcf7f9d2683d70`**
- 纪律：**只读** —— 0 补丁 / 0 commit / 0 push / 0 ref 移动；探针只放 `/tmp` 临时 worktree 并已移除。

---

## 0) 环境自检（原文）

```
$ cd /home/jacks/jacks_dev/codex_go && git fetch origin --prune
$ git rev-parse origin/main && git ls-remote origin main
185d03c933086ddd2b0a1f498fdcf7f9d2683d70
185d03c933086ddd2b0a1f498fdcf7f9d2683d70	refs/heads/main

$ cd /home/jacks/jacks_dev/codex && git fetch origin --prune
$ git rev-parse origin/main b17c74cfd5
b17c74cfd5ebb39fe70ffaff78de198120278636
b17c74cfd5ebb39fe70ffaff78de198120278636
$ git rev-parse HEAD          # rust 工作树（LF 树）冻结点
5b0b2530354052b9194156d70d4c94a439368342
```

---

## 1) 严格新窗口：`37eaae6eeb..b17c74cfd5`（上一已扫前沿 → 新 pin）

```
$ git log --oneline --no-decorate 37eaae6eeb..b17c74cfd5
b17c74cfd5 Record telemetry for AGENTS.md changes made by apply_patch (#51652)
30bdfec59d Persist Guardian review failures for reports across restarts (#51651)
$ git rev-list --count 5b0b253035..b17c74cfd5
4
$ git log --oneline --no-decorate 5b0b253035..b17c74cfd5 -- codex-rs/network-proxy codex-rs/sandboxing
37eaae6eeb Require hostname authorization before proxy DNS lookups (#51650)
```

**结论：新窗口 2 笔均不在本域**（#51652 = `core/src/tools/runtimes/apply_patch.rs` 遥测；#51651 = guardian/feedback/state 持久化）。
整个 `5b0b253035..b17c74cfd5` 只有 **1 笔**触及本域 = `37eaae6eeb #51650`，**已入主 sync630 `89832492`**。
⇒ **新增可派单候选 = 0**（严格窗口口径）。

---

## 2) 主表：本域近期 ≤5 文件 · 非避让面条目（sha 均 `git log --grep '#<PR>' -F` 自解）

| # | PR | sha（自解） | 域内文件数 | 上游语义（一句话） | Go 载体（file:line）| 五分类 | 级别 | 避让面 |
|---|---|---|---|---|---|---|---|---|
| 1 | **#47898** | `2457571e4cd109e602d3ac278d93ec24f1f2e483` | 5 | 环境策略中 `allow_local_binding` 省略时**应继承**（Option 化 + 组合后再 resolve） | `network/network.go:155`（`AllowLocalBinding bool`）、`:169`、`:222` | **真缺口候选** | **S** | 否 |
| 2 | #46027 | `47c27cbffa6e17bce3548d87440484e36e1cbfc3` | 2 | 文档化并测试域名模式 `?` 单字符通配（行为本已存在） | `network/proxy_policy.go:149`（`glob.Compile`） | 已等价型（缺测试） | S(测试) | 否 |
| 3 | #46578 | `29a57861e31aecb97c7d2d8420bc3ed234d58e8c` | 1 | standalone 代理初始化传 `Platform::native()` 校验 socket 路径 | `network/proxy_config.go:260`/`:375`（默认 `utils.NativePlatform()`） | 已等价型 | — | 否 |
| 4 | #45463 | `99b3ab2131a8672089fd7d78da62483187ba1122` | 3 | managed proxy `DedicatedListeners` 路由模式 | 无（Go 无 `ManagedProxyRouting`/`DedicatedListeners`） | 无载体型 | N/A | 含 Windows 面 |
| 5 | #48565 | `228ae3da8dda6edcff6a2078992e239729723a7d` | 2 | macOS Seatbelt 允许 TLS 信任评估 | `sandbox/seatbelt.go`（darwin） | darwin-only | N/A | 不可验证 |
| 6 | #47920 | `c19dcd975d8cf428e3c04cc9bd3a2b9b3bb37f64` | 2 | Seatbelt basename deny 下允许目录移动 | `sandbox/seatbelt.go` | darwin-only | N/A | 不可验证 |
| 7 | #46583 | `78245b47af2a7aafcabe025828ceecca69db4df1` | 1 | Seatbelt 拒绝 XPC 服务查询 | `sandbox/seatbelt.go` | darwin-only | N/A | 不可验证 |
| 8 | #46571 | `d84ebff59295a5fc897bd7b703ade933c5312198` | 4 | Seatbelt scratch 目录保留排除项 | `sandbox/seatbelt.go` | darwin-only | N/A | 不可验证 |
| 9 | #46532 | `01f92c57802a743b5d3893abc9df7670eef8cfe9` | 1 | 移除 Seatbelt 平台默认 `com.apple.runningboard` | `sandbox/seatbelt.go` | darwin-only | N/A | 不可验证 |
| 10 | #46500 | `04e4d2b40ffdeb7768319bec80bad6b2951c92e4` | 2 | Seatbelt 阻断可变 fcntl | `sandbox/seatbelt.go` | darwin-only | N/A | 不可验证 |
| 11 | #45548 | `c18db9ba69b2b29d2773eec45f2f57c328ad1ecb` | 3 | Seatbelt 尊重预置 unix socket 权限 | `sandbox/seatbelt.go` | darwin-only | N/A | 不可验证 |
| 12 | #47974 | `a92ccbde5328697894d09935a656f7c5f07a7d3f` | 3 | 跨可写根保留 Git 目录保护（Seatbelt 测试 + protocol 权限） | — | darwin-only/不可验证 | N/A | 不可验证 |
| 13 | #49478 | `15fd656ddb55bd82a208fb9f00681880523f5260` | 11（域内仅 2 行导出） | MCP 授权服务器发现与 ID-JAG 校验 | — | 非本域（仅 `lib.rs`/`policy.rs` 导出） | — | — |

---

## 3) 深度候选

### 3.A `#47898` = **S 级真缺口**（可做值级 RC；**按纪律先报不动手**）

Rust 变更（`2457571e4c`）：`EnvironmentNetworkPolicy.allow_local_binding: bool → Option<bool>`；
`apply_to` 由 `config.allow_local_binding() && self.allow_local_binding` 改为「任一侧显式拒绝即拒，否则留给 executor」：
```rust
config.allow_local_binding = match (config.allow_local_binding, self.allow_local_binding) {
    (Some(controller), Some(owner)) => Some(controller && owner),
    (controller, owner) => controller.or(owner),
};
```
并把 `local_binding_policy.resolve(&config)` 从 `apply_to` **之前**移到**之后**（`network_proxy_spec.rs` / `proxy.rs`）。

Go 现状 = **修复前语义**：
- `network/network.go:155` `AllowLocalBinding bool`（无 Option ⇒ 「省略」在采集期就塌成 `false`：`:169`
  `AllowLocalBinding: config.AllowLocalBinding`）。
- `network/network.go:222` `config.AllowLocalBinding = config.AllowLocalBinding && p.AllowLocalBinding`
  ⇒ 附件省略该字段时，controller 的 `true` 被 `&& false` 抹掉（**继承丢失**）。
- `NewSpecForEnvironment`（`:227`）先 `ApplyTo` 之后**没有**任何 executor 侧 local-binding 解析
  （Rust 的 `LocalBindingPolicy::resolve` 在 Go 无对位）——该半分属「无载体/前置缺失」。
- 现有测试 `network/network_environment_policy_test.go` 只覆盖 domain 与 unix socket，**无** local-binding 继承用例。

**机器证据**（临时 worktree `/tmp/wt-scan-r87` @ `185d03c9`，探针只落 `/tmp`，已移除）：
```
=== RUN   TestZZScanLocalBindingInheritance
    zz_scan_localbinding_test.go:12: controller.AllowLocalBinding after omitted owner policy = false
    zz_scan_localbinding_test.go:14: inheritance lost: controller grant (true) not inherited by an owner policy that omitted the setting -> false
--- FAIL: TestZZScanLocalBindingInheritance (0.00s)
```
（镜像 Rust 新测试 `environment_local_binding_preserves_explicit_denials_and_inherits_omitted_settings` 的 `managed_omitted` 用例。）

**预计落点（3 文件，均不触避让面）**：`network/network.go`（Option 化 + 组合规则）
+ `network/network_environment_policy_test.go`（表驱动继承/显式拒绝用例）
+ 视需要 `network/proxy_env.go` / `standalone_config.go`（wire 侧 `*bool` 传递）。
**值级 RC**：controller `AllowLocalBinding=true` + 附件省略 ⇒ 断言 `true`（修后）；附件显式 `false` ⇒ 断言 `false`（拒绝保留）。

### 3.B `#46027` = 已等价型（仅缺同款表驱动测试）

Rust 只加了注释 + 表驱动测试（行为早已存在）。Go 的 `CompileProxyDomainMatcher` 走
`github.com/gobwas/glob`（`network/proxy_policy.go:9/:149`），把 Rust 表 19 例逐条跑过：
```
$ go test ./network/ -run TestZZScanQMarkContract -count=1 -v    # 临时探针
--- PASS: TestZZScanQMarkContract (0.00s)
ok  	codex_go/network	0.004s      # 19/19 契约用例全中
```
⇒ 行为已等价；**可选**低成本项 = 把该契约表落成仓内测试（1 文件）。

### 3.C `#46578` = 已等价型
Go 的运行时解析默认就用 native platform：`ResolveProxyRuntime → ResolveProxyRuntimeForPlatform(cfg, utils.NativePlatform())`
（`network/proxy_config.go:260`），socket 路径校验 `:375/:387` 同源。Rust 的 1 行修复在 Go 无缺口。

### 3.D `#45463` = 无载体型
Go 无 `ManagedProxyRouting` / `DedicatedListeners` / `SharedIngress` 抽象（grep 0 命中），
且主体面向 SID-attributed ingress（Windows 面）。不派单。

---

## 4) 附录 · 已判 N/A / L（>5 文件）排除项（同窗口域内，抽样；sha 自解）

| PR | sha（自解，完整） | 文件数 | 排除理由 |
|---|---|---|---|
| #48198 | `8f5387d104534d280a3c2b15ef9f5dbb3aa3a758` | 6 | **已落地**：Go `network/network.go:150` 注释直接引用 `#48198`；`RequiresProxy` 已实现 |
| #44931 | `4d205c7a4dc36b719679a0356a45b23133732265` | 1 | **已落地**：`network/proxy_env.go:66` 注释引用 Rust #44931，`NoProxyEnvKeys` 已剔除 `YARN_NO_PROXY` |
| #45982 | `53401a28086afa11cefcbe058022cc7a31c26153` | 6 | L + darwin（macOS native DNS） |
| #48568 | `b8d5e3f12e58fff942093457bf1350f9ceb695e9` | 65 | L（域内仅 5 文件；exec-server 代理私有 IP 上游） |
| #50058 | `b06b7d2f77…` | 99 | L（Windows bindings `windows-sys` 升级） |
| #50018 | `595534314f…` | 76 | L（测试夹具 descriptor-safe helper） |
| #43884 | `5a65fd87d84215b24ea79100f8d47238f578a5f2` | 10 | L（proxy 连接生命周期；Go 有 `network/proxy_conn_tracking.go` 部分对位） |
| #42746 | `8e85265c39176b6bd498242a33d7b0f9b4b98303` | 9 | L（pending network reviews；域内 3 文件） |
| #46554 / #46271 / #45757 / #45730 / #45550 / #44658 … | — | 30–94 | L + `sandbox/windowssandbox/` 避让面 |
| #49478 | `15fd656ddb55bd82a208fb9f00681880523f5260` | 11 | 非本域（MCP 授权） |

（上一批已入主/已判的 #51650/#51512/#51211/#47132/#46573/#46523/#46334/#46302/#46004/#45984 等不再重复列。）

---

## 5) 未决 + 建议

1. **建议派单（唯一 S 真缺口）**：`#47898` local-binding 继承 —— 3 文件、非避让面、可值级 RC。
   若队长批准，我下一轮出 `update/r86_patches/syncl4_47898.patch`（含表驱动回归测试）。
2. `#46027` 属可选「纯测试型」低成本项（1 文件），是否需要请队长定。
3. `#46578`/`#45463` 判 N/A 依据如上；darwin-only 一族（#48565/#47920/#46583/#46571/#46532/#46500/#45548/#47974）
   在 Linux/Windows 节点均**不可验证**，如要覆盖需 macOS 车道。
4. 严格新窗口（`37eaae6eeb..b17c74cfd5`）**0 新候选**；如需继续挖，需放宽到「L 级」（>5 文件）或推进上游 pin。
