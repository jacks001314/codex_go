# syncw1 · 路径前缀比较归一审计（r88e，只读）

- 日期：2026-10-07 ｜ 车道：syncw1（Windows 节点）
- 基线：**`origin/main = b041cbfd`**（`sync651`；本单接手时 main 刚从 `d12d00dd` 前进 3 笔，审计在同一 LF 树上进行）
- 性质：**只读** —— 0 补丁 / 0 commit / 0 push / 0 ref 移动；给完清单**等队长按项放行**。
- 范围（派单口径）：核查「**Windows 上 `filepath.*` 与 `path.*` 混用导致的同值不同结论**」，即 `strings.HasPrefix(path, root)` 一类**前缀/包含比较未做段边界 / 分隔符 / 大小写归一**的点，与 Rust 逐条对照。**最多 5 条候选**。
- 不含：已在 r88d 判过「已归一/无载体/仅测试」的 `path.*` 面（`shell/display_command.go:249`、`appserver/router.go:1867`、`prompt/skills_render.go:942/946/954/958`、`appserver/precomputed_exports.go:149`），不重做。

---

## 0. 方法与可复跑命令

LF 树（两棵，结果一致）：

```powershell
git -C D:\qax\reagent\dev\codex_go -c core.autocrlf=false -c core.eol=lf archive -o %TEMP%\syncw1_probe\b041cbfd.tar b041cbfd
tar -xf %TEMP%\syncw1_probe\b041cbfd.tar -C %TEMP%\syncw1_probe\lf_b041cbfd
```

扫描（脚本在 `%TEMP%\syncw1_probe\`，均对 LF 树全仓 3470 个 `.go` 执行）：

| 脚本 | 作用 | 命中 |
|---|---|---|
| `scanprefix.py` | 函数名含 subpath/within/contains/prefix/startsWith 的**包含判定**函数 | 199（多为非路径语义，人工筛） |
| `scanprefix2.py` | `strings.HasPrefix/CutPrefix/TrimPrefix(` 且第一实参名含 path/dir/root/file/abs/cwd/… | 247（人工筛） |
| `scanprefix3.py` | 上者中**硬编码 `+"/"`** 或 **`+string(os/filepath.PathSeparator)`** 的前缀比较（段边界/分隔符裸用） | 16 |
| `scanmix.py` | 同一函数体内**同时**出现 `ToSlash/FromSlash` 与 `HasPrefix/CutPrefix`（混用嫌疑） | 23 |

Rust 参照：`C:\rw\codex-rs`（LF 只读检出，pin `5b0b253035`）。

**Rust 判定语义基准**（本单的关键对照物）：

```rust
// C:\rw\codex-rs\utils\path-uri\src\lib.rs:327-334
/// Containment is computed using URI authority and path-segment boundaries,
/// without consulting the host filesystem. Windows path segments are
/// compared ASCII-case-insensitively; POSIX path segments remain case-sensitive.
pub fn starts_with(&self, base: &Self) -> bool { ... }      // :335

// C:\rw\codex-rs\core\src\agent\control.rs:698-710
fn agent_matches_prefix(agent_path: Option<&AgentPath>, prefix: &AgentPath) -> bool {
    if prefix.is_root() { return true; }
    agent_path.is_some_and(|agent_path| {
        agent_path == prefix
            || agent_path.as_str().strip_prefix(prefix.as_str())
                   .is_some_and(|suffix| suffix.starts_with('/'))
    })
}
```

⇒ Rust 的前缀判定 = **整段比较**（边界必须落在 `/`），跨主机还对 Windows 段做 ASCII 大小写折叠。Go 侧若用裸 `strings.HasPrefix`，三点都可能不等价：① 段边界 ② 分隔符 ③ 大小写。

---

## 1. 候选表（5 条）

| # | file:line | 形态 | Rust 对照 | 判定 | 规模 |
|---|---|---|---|---|---|
| **C1** | `appserver/agent_controller.go:826` + `exec/agent_controller.go:1347` | `strings.HasPrefix(path, prefix)`（agent 路径，**缺 `/` 段边界**） | `core/src/agent/control.rs:698-710` `agent_matches_prefix`（要求 `suffix.starts_with('/')`） | **真分歧（生产可达，值级已证）** | 2 文件 |
| **C2** | `plugin/provider.go:153` | `strings.HasPrefix(path, root)`（裸前缀，无边界/分隔符/大小写归一） | `plugin/src/provider.rs:89-99` `path.starts_with(root)`（`PathUri` 逐段 + Windows 大小写折叠） | **真分歧（语义），但 Go 载体仅测试 ⇒ 无生产载体** | 1 文件 |
| **C3** | `appserver/codex_home_metrics.go:90,92` | `strings.HasPrefix(path, sessions/archived)`（裸前缀） | `app-server/src/codex_home_metrics.rs:106,108` `path.starts_with(&sessions)`（`Path::starts_with` 逐段） | **无载体（walk 根构造保证）/ latent** | 1 文件 |
| **C4** | `sandbox/windowssandbox/setup.go:537-538` | 两侧 `CanonicalPathKey` + `protectedKey+"/"` | —（Go 侧 Windows 沙箱自有面） | **已归一（正面范本）** | 0 |
| **C5** | `tui/chatwidget/plugins.go:257-258`；`appserver/executor_plugin_ownership.go:108-113` | 两侧 `CrossPlatformSlash` / `remoteNormalizePathKey` + `root+"/"` | `PathUri::starts_with` | **已归一 / 无观测分歧** | 0 |

---

## 2. 逐条明细

### C1（真分歧，生产可达）`appserver/agent_controller.go:826` / `exec/agent_controller.go:1347`

```go
// appserver/agent_controller.go:816-829
prefix := ""
if args != nil && args.PathPrefix != nil {
    prefix = runtimeCanonicalAgentPath(c.scopePath, *args.PathPrefix)   // ⇒ "/root/work"，无尾斜杠
}
if prefix == "" || strings.HasPrefix("/root", prefix) {
    result.Agents = append(result.Agents, agent.ListedAgent{AgentName: "/root", ...})
}
for _, metadata := range c.registry.LiveAgents() {
    path := string(metadata.Path)
    if prefix != "" && !strings.HasPrefix(path, prefix) {   // ← 缺 "/" 段边界
        continue
    }
```

`exec/agent_controller.go:1336-1349` 是同形态的孪生实现（`cleanExecAgentPath`，同样 `strings.HasPrefix(task.path, prefix)`）。

- **值来源**：`args.PathPrefix` 来自 v2 `list_agents` 工具参数（`agent/tools_v2.go:81` `PathPrefix *string`）；`path` 是注册表中 agent 路径（`registry.go:154-168 LiveAgents`）。两侧都经 `ReplaceAll("\\","/")` + `FieldsFunc('/')` 归一（`runtimeCanonicalAgentPath:976-991`、`cleanExecAgentPath:1398-1408`）⇒ **分隔符已归一、大小写语义与 Rust 一致**，唯一缺口是 **段边界**。
- **Rust**：`agent_matches_prefix`（`core/src/agent/control.rs:698-710`）要求 `strip_prefix(prefix)` 后 `suffix.starts_with('/')`，即 `/root/work` **不**匹配 `/root/worker`。
- **值级探针（真实 Go 码，`go test -overlay=`，探针不落仓）**：

```
=== RUN   TestProbeListAgentsPrefixBoundary
    zz_probe_pathprefix_test.go:29: PathPrefix="work" (canonical "/root/work") -> [/root/work /root/work/child /root/worker]
    zz_probe_pathprefix_test.go:32: DIVERGENCE: /root/worker (not a descendant of /root/work) was returned
    zz_probe_pathprefix_test.go:35: Rust agent_matches_prefix("/root/worker", "/root/work") = false (suffix "er" does not start with '/')
--- PASS: TestProbeListAgentsPrefixBoundary (0.00s)
```

命令：

```powershell
Push-Location %TEMP%\syncw1_probe\lf_b041cbfd
go test ./appserver/ -run 'TestProbeListAgentsPrefixBoundary' -count=1 -v -overlay=%TEMP%\syncw1_probe\overlay_pathprefix.json
Pop-Location
```

⇒ Go 返回 3 个（含 `/root/worker`），Rust 语义下只应有 2 个（`/root/work`、`/root/work/child`）。

- **建议修法（最小）**：两处各改为段边界判定，与同文件 `:826` 附近的 `+"/"` 惯例一致：
  ```go
  if prefix != "" && path != prefix && !strings.HasPrefix(path, prefix+"/") { continue }
  ```
  （`prefix` 已被 `runtimeCanonicalAgentPath`/`cleanExecAgentPath` 保证 `/`-归一且**无尾斜杠**；`path` 同上。）
- **规模**：2 文件（`appserver/agent_controller.go`、`exec/agent_controller.go`）。
- **值级 RC 可做**：撤掉修复 ⇒ 探针 FAIL（`/root/worker` 出现在结果里）；恢复 ⇒ ok。
- **测试护栏**：Rust 测试 `core/src/tools/handlers/multi_agents_tests.rs:1547 multi_agent_v2_list_agents_filters_by_relative_path_prefix` 只覆盖嵌套（`/root/researcher` vs `/root/researcher/worker`），**未**覆盖兄弟前缀；Go 侧无任何 `PathPrefix` 测试 ⇒ 现有护栏两边都挡不住。

### C2（真分歧但无生产载体）`plugin/provider.go:153`

```go
// plugin/provider.go:152-155
func environmentResource(environmentID string, root string, path string) (PluginResourceLocator, error) {
	if !strings.HasPrefix(path, root) {
		return PluginResourceLocator{}, newResolvedPluginError(root, path)
	}
```

- **Rust**：`plugin/src/provider.rs:89-99`
  ```rust
  fn environment_resource(environment_id: &str, root: &PathUri, path: PathUri) -> Result<...> {
      if !path.starts_with(root) { return Err(ResolvedPluginError::ResourceOutsideRoot { root: root.clone(), path }); }
  ```
  `PathUri::starts_with`（`utils/path-uri/src/lib.rs:327-358`）= 逐段 + Windows 段 ASCII 大小写折叠 + 分隔符无关 ⇒ 与裸 `strings.HasPrefix` 三处都不等价：
  1. **段边界**：Go `HasPrefix("/a/bc","/a/b")` = true；Rust `starts_with` = false。
  2. **分隔符**：`C:\a\b` vs `C:/a/b`。
  3. **Windows 大小写**：Rust 折（`windows_identity_path_bytes`），Go 不折。
- **载体可达性（关键）**：`environmentResource` 只被 `NewResolvedPluginFromEnvironment` 调用；后者在**整个 Go 仓只有 `plugin/features_test.go:25,56` 两个测试调用点**（`rg -n 'NewResolvedPluginFromEnvironment' <lf>` 原文见 §0 脚本输出）⇒ **生产不可达（仅测试）**。
- **判定**：语义分歧成立，但**无生产载体** ⇒ latent；本单**不建议**修（无生产接线 ⇒ 造不出「撤修复⇒FAIL」的值级 RC，会退化成测试内自证）。
- **建议修法（将来若接线）**：改用 Go 侧已成型的等价实现 `utils.PathURI.StartsWith`（`utils/pathuri.go:544-591`，已实现 `EqualFold` 的 Windows 折叠），或至少 `path == root || strings.HasPrefix(path, root+"/")`。规模 1 文件。

### C3（无载体 / latent）`appserver/codex_home_metrics.go:90-92`

```go
path := filepath.Join(dir, entry.Name())
...
if strings.HasPrefix(path, sessions) { sizes.sessions += bytes
} else if strings.HasPrefix(path, archived) { sizes.archivedSessions += bytes }
```

- **Rust**：`app-server/src/codex_home_metrics.rs:106,108` `path.starts_with(&sessions)` —— `Path::starts_with` 是**逐段**比较；Go 是**裸字符串前缀**，理论上 `<home>/sessions_bak/x` 会被计入 `sessions`（Rust 不会）。
- **可达性**：`directory_sizes` 的 walk 根**恰为** `<codexHome>/sessions` 与 `<codexHome>/archived_sessions` 两个目录（`:50-52` 构造 `pending`），因此被访问的 `path` 必在这两棵子树内，前缀判定恒真/恒假，**无可观测分歧**；分隔符两侧同出 `filepath.Join` ⇒ 一致。
- **判定**：**无载体（构造保证）/ latent**。可选加固（不推荐本单做）：按 walk 根分别累加，或改 `filepath.Rel` 判定。

### C4（正面范本）`sandbox/windowssandbox/setup.go:537-538`

```go
protectedKey := strings.TrimRight(CanonicalPathKey(protectedRoot), "/")
if key == protectedKey || strings.HasPrefix(key, protectedKey+"/") {
```
两侧均 `CanonicalPathKey`（`sandbox/windowssandbox/path_normalization.go:23-29`：`ToLower(ReplaceAll(filepath.Clean(p), "\\", "/"))`）⇒ **分隔符 + 大小写都归一 + 段边界**。全仓最标准的写法，可作为 C1 修法的风格参照。

### C5（已归一）`tui/chatwidget/plugins.go:257-258`、`appserver/executor_plugin_ownership.go:108-113`

```go
root := utils.CrossPlatformSlash(root); path := utils.CrossPlatformSlash(*marketplace.Path)
if path == root || strings.HasPrefix(path, root+"/") { ... }         // tui/chatwidget/plugins.go:252-258
root = strings.TrimSuffix(root, "/"); return path == root || strings.HasPrefix(path, root+"/")  // appserver/executor_plugin_ownership.go:112-113
```
两侧先归一到 `/`（`CrossPlatformSlash` = `path.Clean(ReplaceAll("\\","/"))`；`remoteNormalizePathKey` 同族）+ `root+"/"` 段边界 ⇒ 与 Rust `PathUri::starts_with` 在分隔符/边界上等价；残余仅 Windows 大小写折叠差异（这两处的值分别为 marketplace 路径与 `environment://` 远端 key，后者 convention 非 Windows ⇒ 风险低）。**判定：已归一 / 无观测分歧。**

---

## 3. 已核、无分歧（附录，不列入候选）

| file:line | 形态 | 判定依据 |
|---|---|---|
| `doctor/render.go:435-448` | `HasPrefix(normalizedPath, home+"/")` | 两侧都 `ReplaceAll("\\","/")` + `TrimRight("/")` ⇒ 分隔符一致 |
| `mcp/ema_auth_policy.go:145-153` | `HasPrefix(serverPath, resourcePath)` | URL path（恒 `/`），且 `:151` 已补 `suffix` 必须以 `/` 开头 ⇒ **有段边界** |
| `filesearch/filesearch.go:114-119,197-217` | `HasPrefix(relative, TrimSuffix(pattern,"/**")+"/")` | `relative` 与 `pattern` 均 `filepath.ToSlash` ⇒ 已归一 + 有边界 |
| `plugin/marketplace_loader.go:486-497`、`appserver/fs.go:626,870`、`worktree/manager.go:322-328`、`appserverdaemon/managed_install.go:130-139`、`tool/windows_safety.go:101-119`、`envutil/trusted_executable.go:90`、`gitutil/worktree.go:277`、`plugin/api.go:1873` | `filepath.Rel` / `root+string(filepath.Separator)` | 全程 `filepath.*`，分隔符与本机一致 ⇒ 无混用 |
| `appserver/skills.go:1941-1946,1963-1967` | `path/cleanCWD` 均 `filepath.Clean`；`skill.Path` 由 `canonicalSkillPathForIdentity`（`:928-933` `filepath.Clean`）产生 | 两侧同为本机原生分隔符 ⇒ 一致（无 `path.*` 混入） |
| `tui/markdown/local_links.go:220-237` | `CutPrefix(pathText, cwdText)` + `TrimPrefix(rest,"/")` | **与 Rust `tui/src/markdown_render/local_links.rs:169-185` 逐行等价**（含缺边界的行为）⇒ 1:1 移植，非本轮新增分歧 |
| `exec/agent_controller.go:826-829` | `TrimSuffix(task.path,"/")+"/"` 后 `HasPrefix` | 已内建段边界（close_agent 后代枚举）⇒ 正确，**与 C1 同文件但形态不同** |

---

## 4. 结论与待放行

1. **C1 是本轮唯一「真分歧 + 生产可达 + 值级已证」的项**：`list_agents` 的 `path_prefix` 过滤缺 `/` 段边界（`appserver/agent_controller.go:826`、`exec/agent_controller.go:1347`），Go 会把 `/root/worker` 当成 `/root/work` 的后代返回，Rust 不会。2 文件、每处 1 行、可造值级 RC。
2. **C2 语义真分歧但无生产载体**（`plugin/provider.go:153`，仅 `plugin/features_test.go` 调用）⇒ 建议登记台账、**不修**（造不出生产值级 RC）。
3. **C3 latent**（`codex_home_metrics.go:90-92`）⇒ walk 根构造保证无分歧，建议**不修**。
4. C4/C5 与附录各点均为**已归一/无分歧**（含 3 处正面范本可复用：`CanonicalPathKey`、`CrossPlatformSlash`、`utils.PathURI.StartsWith`）。
5. 本单 **0 补丁 / 0 commit / 0 push / 0 ref**。

### 待队长放行项

- **P1（建议）**：C1 —— `appserver/agent_controller.go:826`、`exec/agent_controller.go:1347` 改为段边界判定（`path == prefix || HasPrefix(path, prefix+"/")`）。规模 2 文件；交付补丁 + 仓内回归测试（`/root/work` vs `/root/worker` 兄弟前缀）+ 值级 RC（撤 ⇒ 探针 FAIL；恢复 ⇒ ok）。
- **P2（可选）**：C2 —— 若队长同意给 `plugin/provider.go` 换 `utils.PathURI.StartsWith`，则需同 PR 加可达性说明（否则仍是测试内自证）；**默认建议不修**。

### 未决 / 风险

- U1：C1 若落地，`exec/` 与 `appserver/` 是**两个独立实现**，是否只修其中一处（另一处由别的车道在飞）——请裁定；两处文件均不在我的避让面（`appserver/runtime_router.go` / `appserver/turn_runtime.go` / `tui/state.go` / `sandbox/windowssandbox/`）。
- U2：Rust `agent_matches_prefix` 的 `prefix.is_root()` 短路（`:699-701`）在 Go 侧由 `HasPrefix("/root", prefix)`（`:821`）近似实现；当 `prefix` 恰为 `/root` 时两者一致（已核），但 Go 的 `path_prefix` 若被解析为 `/roo` 这类**非 `/root` 前缀**，Go 会把 `/root` 排除而 Rust 的 `AgentPath::resolve` 会先归一 —— 该组合在当前 `runtimeCanonicalAgentPath`（`:976-991`，相对引用一律挂到 scope 之下）下不可达，故未列入候选。
