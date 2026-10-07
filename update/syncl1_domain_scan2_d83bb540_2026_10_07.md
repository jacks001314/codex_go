# syncl1 · round 88d · 域扫描 round 2 —— `sandbox/` + `config/`

- 车道：syncl1（Linux 节点，跨节点车道）
- 任务：只读域扫描（0 补丁 / 0 commit / 0 push / 0 ref 移动）
- 上游枚举 pin：`d83bb540ec64bf6b009bca0283b0be91ea33f26a`（Rust `Move Windows sandbox tests into a dedicated integration binary (#51678)`）
- Go 参照：`origin/main = d12d00dde5732a8f910db636b5452949a4594785`
- parity 检出冻结 pin `5b0b253035`（未动）

## 0. 环境自检
| 项 | 值 |
|---|---|
| Go 仓真实路径 | `/home/jacks/jacks_dev/codex_go` |
| Go origin/main | `d12d00dde5732a8f910db636b5452949a4594785`（已 `git fetch origin`） |
| Go 本地 main | `a32e1c35facdbfec20887cce46da126d6ac0e68b`（队长合并区，**未触碰**） |
| Rust 仓 | `/home/jacks/jacks_dev/codex`（origin = openai/codex，已 fetch） |
| Rust pin 存在性 | `d83bb540ec…` = `Move Windows sandbox tests into a dedicated integration binary (#51678)` ✔ |
| 交付方式 | 本单**只读**，无 commit/push；产物经 `project_file_sync` 入镜像 |

## 1. 方法与覆盖
- 命令（在 Rust 仓）：`git log -600 --format='@@%H|%s' --name-only d83bb540ec` → 600 笔。
  （队长原命令为 `--numstat -300`；本单把窗口**加宽到 600** 以覆盖更早的 47xxx 未处置笔，见 §1.1。`--numstat` 会把 commit message 混排到块尾，改用 `--name-only` + `@@` 分隔符解析，语义等价且更稳。）
- 域映射：`sandbox/` ⟵ `codex-rs/sandboxing/`、`codex-rs/linux-sandbox/`、`codex-rs/mxc-sandbox/`、`codex-rs/bwrap/`；`config/` ⟵ `codex-rs/config/`、`codex-rs/config-schema/`、`codex-rs/core/src/config`。
- 排除面（按派单）：`sandbox/windowssandbox/`、`appserver/runtime_router.go`、`appserver/turn_runtime.go`、`tui/state.go`。
- 统计（`/tmp/r88d_stats.py`）：
  - 扫描 commit：600
  - 域内 commit：61（其中 `≤5 文件` = **21**，`>5 文件` = 40）
  - 逐条五分类对象 = 这 21 条（>5 文件按规则不逐条判，仅登记）

### 1.1 加宽窗口的增量（证明 -300 之外确实有新面）
`/tmp/r88d_stats2.py`：21 条候选里，idx<300 有 8 条，idx≥300 有 13 条：
`#51547 #51527 #51407 #50525 #50354 #50059 #49910 #49784`（≤300）；
`#49295 #49246 #48565 #48469 #47974 #47968 #47943 #47924 #47920 #47908 #47904 #47903 #47902`（>300）。
⇒ 若只按 -300 会漏掉 13 条（含本单唯一的真缺口候选 `#47974`）。

## 2. 五分类总表（21 条，全量）
| #PR | Rust sha | Rust 文件数 | 判定 | Go 载体 / 0 命中证据 |
|---|---|---|---|---|
| #51547 | `e95abcdf49` | 5 | **已落地** | `config/config.go:583`（引 `Rust #51547`）、`config/windows_sandbox_mode.go:123 ValidateWindowsMXCOptOut`、`config/windows_mxc_optout_test.go:9`；落主 `4481ffd8 sync389` |
| #51527 | `ac9b5b8380` | 2 | 已覆盖（已知） | `sandbox/linuxsandbox/linux_test.go:143`（引 `Rust #51527`）；`efac0a9b sync385` |
| #51407 | `ccde2fc8b7` | 2 | 已覆盖（已知） | `sandbox/linuxsandbox/linux_test.go:127 expandLinuxDenyGlob`；`c1e03539 sync346` |
| #50525 | `b65ab465ce` | 4 | **已落地** | `config/config.go:875 validateKnownTuiConfigFields` + `:910`（引 `Rust #50525`）；`42fbf7f3 sync533` |
| #50354 | `f4e18a95bb` | 1 | **已落地** | `config/config.go:3316 configPathIsAliasPrefix`；`368beeb2 sync319`（同一笔覆盖 #47908/#47904/#47903） |
| #50059 | `ae7aad586e` | 3 | **已落地** | `sandbox/linuxsandbox/linux.go:866 appendUnreadableRootBwrapArgs` + `linux_test.go:215`（引 `Rust #50059`）；`e9b6e85a sync338` |
| #49910 | `819efd7273` | 4 | 已等价（非本域：`tui/`） | `tui/keymap_config.go:356/388`（Go 手写解析器，无 serde untagged-enum 屏蔽错误的问题） |
| #49784 | `3bbf8ec3a1` | 3 | **已落地** | `features/features.go:329`（引 `3bbf8ec3a1 #49784`） |
| #49295 | `cf12c86dc5` | 2 | **已落地** | `config/fingerprint.go:13`（引 `#49295 / cf12c86dc5`）；`0525ac4d sync573` |
| #49246 | `0462dcc062` | 3 | **纯测试** | Rust 仅改 `Cargo.lock`+`Cargo.toml`(dev-dep)+`bundled_bwrap.rs` 测试夹具 ⇒ 无生产载体 |
| #48565 | `228ae3da8d` | 2 | 无载体（macOS Seatbelt） | `git grep 'TrustEvaluationAgent\|mach-lookup' origin/main -- '*.go'` → 仅 `sandbox/seatbelt.go:90`（全局 deny，无 TLS-trust 放行） |
| #48469 | `b334d5b3f2` | 4 | **已落地** | `tui/app/copy_on_select.go:10`（引 `#48469`）；`ad588229 sync268` |
| #47974 | `a92ccbde53` | 3 | **真缺口候选（S）** | 见 §3 |
| #47968 | `a708fc6839` | 2 | 无载体（子系统未移植） | `git grep 'mountinfo\|daemon_socket_mask\|SocketFilesystem' origin/main -- 'sandbox/'` → **0 命中**（rc=1） |
| #47943 | `549455f3ec` | 3 | 无载体（Windows-only 死代码删除，撞避让面） | Go 侧同源模块 `sandbox/windowssandbox/audit.go`（**避让面**，仍被 `app/`·`doctor/`·`execserver/` 引用）；`config/` 侧 0 命中 |
| #47924 | `1866e51677` | 5 | 已等价 | `config/project_trust.go:32/56/75`（`ProjectTrustPath`/`FromNativePath`/`ForTarget`：cwd 优先于 repo root） |
| #47920 | `c19dcd975d` | 2 | 无载体（macOS Seatbelt） | `git grep 'seatbeltRegex\|unreadableGlob\|DeniedGlob' origin/main -- 'sandbox/'` → **0 命中**（rc=1） |
| #47908 | `5c8fc15cc9` | 2 | **已落地** | `config/config.go:3267 {legacy:[tui,whimsy] → canonical:[tui,effects,starfield]}`；`368beeb2 sync319` |
| #47904 | `f0f38b68f7` | 1 | **已落地** | `config/config.go:3284 normalizeConfigKeyAliases` + `insertAtCanonicalPath`（嵌套 canonical 路径）；`368beeb2 sync319` |
| #47903 | `0b78ddb035` | 4 | **已落地** | `config/config.go:3075 mergeConfigMapsAt` 在合并前调用 `normalizeConfigKeyAliases`；`368beeb2 sync319` |
| #47902 | `975d30c043` | 1 | 已等价（纯性能重构） | `config/config.go:3071 mergeConfigMaps`（Go 无 Rust 的重复 table clone 点，行为等价） |

附：**#49642（本车道已落地）** `config/api.go:556/561 AllowMXC`、`config/requirements_file.go:330`、`config/windows_sandbox_mode.go:123`，落主 `ee2d4e1d sync640`。
未列入已落地清单、但也**未在我域内**：#51527/#51407 按派单只作确认。

## 3. 真缺口候选：#47974 —— Git 目录保护跨 writable root
**上游**：`a92ccbde5328…` `Preserve Git directory protections across writable roots (#47974)`，Rust 3 文件（`protocol/src/permissions.rs` + 两份测试）。

**Rust 语义（逐点）**：
1. 在 `FileSystemSandboxPolicy` 计算 writable roots 时，对每条**可写**条目取其 `.git`；若是 gitdir 指针文件（`gitdir: …`），解析出真实 gitdir 目标。
2. `include_resolved_gitdirs`：`WritableRootPathResolution::Effective` ⇒ `cfg!(target_os = "linux")`；`PreserveMutableComponents` ⇒ macOS。**即 Linux 侧生效**。
3. 把解析出的 gitdir 作为 **Read 条目**（只读排除）追加，且**跨 writable root**：`.git` 指针可指向另一个 writable root 内的 gitdir（含 symlink 别名），该 gitdir 仍必须只读。
4. 解析时对排除目标做 canonicalize（跟随 symlink 别名），已存在显式条目则跳过。
5. 判定「metadata 写被拒，周围可写 root 的普通文件仍可写」。

**Go 载体图（逐步）**：
- `sandbox/policy.go:310 GetWritableRootsWithCWD(cwd)` → `sandbox/policy.go:333 buildWritableRoots` → `sandbox/sandboxpath/sandboxpath.go:95 WritableRootsWithProtectedSubpaths` → `sandbox/sandboxpath/sandboxpath.go:81 ProtectedSubpaths(root)` = `{root/.git, root/.agents, root/.gcode, root/.aws}`。
- 即 Go 只把 `<root>/.git`（**指针文件本体**）当只读 carveout，**不解析指针、不追加目标 gitdir**，也无跨 writable root 的解析。
- 0 命中举证：
  ```
  $ git -C /home/jacks/jacks_dev/codex_go grep -n 'gitdir' origin/main -- 'sandbox/'
  origin/main:sandbox/windowssandbox/sandbox_utils_test.go:9:func TestInjectGitSafeDirectoryForGitDirectory(...)
  ```
  （唯一命中与本题无关：Windows 侧 `git safe.directory` 注入；`sandbox/` 无任何 gitdir 指针解析。）

**行为差**：workspace 的 `.git` 指向 `writable/gitdir`（两个 root 都可写）时，Rust 让 `writable/gitdir` 只读；Go 保持可写（`writable/gitdir` 不在任何 `ReadOnlySubpaths` 中）。→ 与 Rust 断言 `writable_roots_protect_gitdir_target_outside_alias_root` 相反。

**规模 / 落点（最小）**：S。可在 `sandbox/sandboxpath/sandboxpath.go`（`ProtectedSubpaths` / `WritableRootsWithProtectedSubpaths`）内新增「解析 `.git` 指针 → 追加目标 gitdir（canonicalize + 跨 root）」；必要时在 `sandbox/policy.go:310` 传 roots 集合以裁决「已有显式条目则跳过」。**不触碰避让面**。

**Linux RC 可行性**：可（纯路径逻辑，无需 macOS/网络）：构造两 writable root + `.git` 指针 + symlink 别名，断言 `IsPathWritable(writable/gitdir/x)` == false；撤修复 ⇒ FAIL。

**判据原文**：`git -C /home/jacks/jacks_dev/codex show a92ccbde53 -- codex-rs/protocol/src/permissions.rs` 前 60 行（`include_resolved_gitdirs` / `resolve_gitdir_from_file` / `resolved_gitdir_entries`）。

## 4. 域级零命中证据（证明扫过，而非没扫）
- Rust 侧确认域内确有大量笔（61 条域内 commit / 600）；`config/` 的笔集中在 `config/src/{types,loader,key_aliases,merge,fingerprint,project_trust,strict_config,tui_keymap}.rs`。
- Go 侧正向比对（实际存在，非空扫）：
  - `config/` 相关载体：`config/config.go`（`validateKnownTopLevelConfigFields:893`、`validateKnownTuiConfigFields:875`、`configKeyAliases:3266`、`normalizeConfigKeyAliases:3284`、`configPathIsAliasPrefix:3316`、`mergeConfigMapsAt:3075`）、`config/fingerprint.go:13`、`config/project_trust.go:32/56/75`、`config/windows_sandbox_mode.go:123`、`config/requirements_file.go:330`。
  - `sandbox/` 相关载体：`sandbox/sandboxpath/sandboxpath.go:77/81/95/138/189/207/222`、`sandbox/linuxsandbox/linux.go:866`、`sandbox/linuxsandbox/linux_test.go:143/215`、`sandbox/seatbelt.go:90`、`sandbox/policy.go:310/333`。
- 反向零命中（3 条「无载体」的判据）：`sandbox/` 内 `mountinfo|daemon_socket_mask|SocketFilesystem` = 0；`seatbeltRegex|unreadableGlob|DeniedGlob` = 0；`TrustEvaluationAgent` = 0；`sandbox/` 内 `gitdir` 仅 1 条无关命中。

## 5. 结论
- 21 条逐条**全量判定**完成：`已落地 9`（#51547 #50525 #50354 #50059 #49784 #49295 #48469 #47908 #47904/#47903 同笔）、`已覆盖/已等价 5`（#51527 #51407 #49910 #47924 #47902）、`纯测试 1`（#49246）、`无载体 4`（#48565 #47968 #47943 #47920）、`真缺口候选 1`（**#47974**）。
- **本单产出 1 条真缺口候选**：`#47974`（S，非避让面，Linux 可跑值级 RC）。按派单要求**先报不动手**，待批准后出补丁（`update/r86_patches/syncl1_47974.patch` + 字节数 + sha256 + `git apply --check` 打当时 main + ≥1 条值级 RC）。
- 未决：`#49246` 是否需要在 Go 侧补 bundled-bwrap 夹具（仅测试基础设施，无生产语义）——默认判纯测试、不排期。

## 6. 纪律声明
只读：**0 补丁 / 0 commit / 0 push / 0 tag / 0 ref 移动**；未触碰 `main`/分支；未改主仓工作目录；探针未落仓；未跑 `go test ./...`。
