# syncw1 · Windows 原生路径 → POSIX-only `path.*` 审计（r88d，只读）

- 日期：2026-10-07
- 车道：syncw1（Windows 节点）
- 基线：`origin/main = d12d00dd`（`sync648: align fallback catalog field values with models.json`）
- 派单：r88d「Windows 原生路径审计（只读 scoping）」
- **复核基线**：审计期间 `origin/main` 又前进 3 笔至 `b041cbfd`（`sync649/650/651`，只触 `doctor/` + `tui/bottom_pane` 状态行面，与本审计的 11 个文件**零交集**）—— 已在 `b041cbfd` 的 LF 树上重跑同一扫描，`path.*` 调用点集合**逐条不变**（仍 11 文件 / 25 处）。
- 交付性质：**只读** —— 0 补丁 / 0 commit / 0 push / 0 ref 移动；本报告 = 清单 + 判据 + 证据，等队长按项放行再出补丁。
- 背景：`sync644` 已落主修掉 `tool/apply_patch_agents_md_metrics.go` 的 `path.Base` 漏计（本车道上轮交付的 `syncw1_51652_windows_path.patch`）。本报告系统排查**同类遗留面**。

---

## 0. 方法、完备性与判定口径

### 0.1 检出与扫描（可复跑，原文）

```powershell
git -C D:\qax\reagent\dev\codex_go fetch origin --prune
git -C D:\qax\reagent\dev\codex_go log --oneline -1 origin/main          # d12d00dd
git -C D:\qax\reagent\dev\codex_go -c core.autocrlf=false -c core.eol=lf archive -o %TEMP%\syncw1_probe\d12d00dd.tar d12d00dd
tar -xf %TEMP%\syncw1_probe\d12d00dd.tar -C %TEMP%\syncw1_probe\lf_d12d00dd
python %TEMP%\syncw1_probe\scanpath.py
```

- 全部扫描在 **LF 树**（`%TEMP%\syncw1_probe\lf_d12d00dd`，由 `core.autocrlf=false -c core.eol=lf` 导出）上进行，不受本机 `autocrlf=true` 影响。
- Rust 参照：`C:\rw\codex-rs`（LF 只读检出，pin `5b0b253035`）；上游 pin 不变 `b17c74cfd5`。

### 0.2 完备性论证（**全量，非抽样**）

全仓 **3470 个 `.go` 文件**（排除 `.git`）中，**只有 11 个文件 import `"path"`**：

```
appserver/rollout_archive.go                              => path
appserver/router.go                                       => pathpkg
appserver/skills.go                                       => pathpkg
appserver/skills_remote.go                                => pathpkg
appserver/turn_runtime.go                                 => pathpkg
sandbox/seatbelt.go                                       => pathpkg
state/migrations.go                                       => path
systemskills/systemskills.go                              => path
tool/apply_patch_agents_md_metrics_like_rust_test.go       => path
tui/clipboard.go                                          => path
utils/pathuri.go                                          => path
```

对这 11 个文件按 `<alias>.<ident>` 全量抓取（含 `pathpkg` 别名），得到成员集合的**穷举**：

```
appserver/rollout_archive.go   => ['path.Base']
appserver/router.go            => ['pathpkg.Clean']
appserver/skills.go            => ['pathpkg.Clean']
appserver/skills_remote.go     => ['pathpkg.Base', 'pathpkg.Clean', 'pathpkg.Dir', 'pathpkg.Join']
appserver/turn_runtime.go      => ['pathpkg.Clean']
sandbox/seatbelt.go            => ['pathpkg.Clean', 'pathpkg.Dir', 'pathpkg.Join']
state/migrations.go            => ['path.Join']
systemskills/systemskills.go   => ['path.Clean']
tool/apply_patch_agents_md_metrics_like_rust_test.go => ['path.Clean']
tui/clipboard.go               => ['path.Clean', 'path.Join']
utils/pathuri.go               => ['path.Clean']
```

⇒ 全仓 `path.*` 的成员只有 `Base / Dir / Join / Clean`（**无** `Ext / Split / Match / Rel / IsAbs`）；下表 **25 个调用点即全量**，无遗漏面。

`d12d00dd` 与 `b041cbfd` 两棵 LF 树上的扫描结果**逐条相同**：均为 11 个 import 文件、同一批 25 个调用点（`python %TEMP%\syncw1_probe\scanpath2.py` 打 `lf_b041cbfd` 得 `files importing path: 11` / `call sites: 25`）。

### 0.3 判定口径

| 判定 | 含义 |
|---|---|
| **真漏计 / 真错误** | Windows 原生输入（`\` 分隔 / 盘符）经 POSIX-only `path.*` 后产出与 Rust **不同**的可观测结果。 |
| **已归一** | 该调用点本身或其直接上游已把 `\`→`/`（含 `pathpkg.Clean(strings.ReplaceAll(x,"\\","/"))` 形态），或输入语义上恒为 `/`（URL path、`io/fs`、embed FS）。 |
| **无载体** | 输入恒为 POSIX —— Go 常量路径、embed FS 名、URL path、JSON pointer、codex 内部路径空间（`skill://` / agent path）。 |
| **仅测试** | 仅出现在 `_test.go`。 |

Rust 侧对照口径：Rust 是否**先归一**再做段操作 —— `PathUri`（`utils/path-uri/src/lib.rs:514-517` 在 Windows convention 下 `path.replace('\\', "/")` 再按 `/` 切段）、`PathBuf`、`Path::file_name()`（Windows 上同时切 `\` 与 `/`）。

---

## 1. 全量表（11 文件 / 25 个 `path.*` 调用点 + 1 条同字段对照行）

| # | file:line | 调用 | 值来源（调用链） | 用户/补丁输入? | Rust 对照 | 判定 | 建议修法 | 规模 |
|---|---|---|---|---|---|---|---|---|
| 1 | `appserver/rollout_archive.go:272` | `path.Base(attachmentPath.Path)` | `FeedbackAttachmentPath.Path` ← `FeedbackAttachmentPaths() (feedback.go:387)` ← `runtime_router.go:11179` 的 `params`（`rollout/list` RPC 客户端传入的 rollout 路径 + guardian 路径） | **是**（Windows 原生 rollout 路径） | `feedback/src/lib.rs:837` `rollout_id_from_path(&attachment.path)` → `rollout/src/metadata.rs:114-116` `path.file_name()` | **已归一（下游 callee 兜底）**，无实际分歧 | 可选（latent robustness）：改 `filepath.Base` | 1 文件 |
| 2 | `appserver/router.go:1725` | `pathpkg.Clean(strings.ReplaceAll(value, "\\", "/"))` | `cleanRuntimeWorkspaceRoot(value)`，`value` 来自 workspace root 参数 | 是 | 同上 `PathUri` Windows convention | **已归一** | 无需 | — |
| 3 | `appserver/skills.go:1368` | `pathpkg.Clean(normalized)` | `normalized := strings.ReplaceAll(value,"\\","/")`（`:1367`），`value` 来自 skill metadata 相对路径 | 是（已先归一） | skill asset 相对路径归一 | **已归一** | 无需 | — |
| 4 | `appserver/skills_remote.go:480` | `pathpkg.Clean(parsed.Path)` | `url.Parse(...).Path`（URL path，恒 `/`） | URL 输入 | URL 路径 | **已归一（URL path）** | 无需 | — |
| 5 | `appserver/skills_remote.go:493` | `pathpkg.Clean(strings.ReplaceAll(normalized,"\\","/"))` | 已归一 | 是（已先归一） | 同上 | **已归一** | 无需 | — |
| 6 | `appserver/skills_remote.go:737` | `pathpkg.Clean(strings.ReplaceAll(skillPath,"\\","/"))` | 已归一 | 是（已先归一） | 同上 | **已归一** | 无需 | — |
| 7 | `appserver/skills_remote.go:745` | `pathpkg.Dir(parsed.Path)` | `url.Parse().Path` | URL 输入 | URL 路径 | **已归一（URL path）** | 无需 | — |
| 8 | `appserver/skills_remote.go:749` | `pathpkg.Dir(strings.ReplaceAll(path,"\\","/"))` | 已归一 | 是（已先归一） | 同上 | **已归一** | 无需 | — |
| 9 | `appserver/skills_remote.go:755` | `pathpkg.Base(parsed.Path)` | `url.Parse().Path` | URL 输入 | URL 路径 | **已归一（URL path）** | 无需 | — |
| 10 | `appserver/skills_remote.go:757` | `pathpkg.Base(strings.ReplaceAll(path,"\\","/"))` | 已归一 | 是（已先归一） | 同上 | **已归一** | 无需 | — |
| 11 | `appserver/skills_remote.go:776` | `pathpkg.Clean(parsed.Path)` | `url.Parse().Path` | URL 输入 | URL 路径 | **已归一（URL path）** | 无需 | — |
| 12 | `appserver/skills_remote.go:787` | `pathpkg.Clean(strings.ReplaceAll(path,"\\","/"))` | 已归一 | 是（已先归一） | 同上 | **已归一** | 无需 | — |
| 13 | `appserver/skills_remote.go:795` | `pathpkg.Join(segments...)`，`segments[0]=parsed.Path` | `url.Parse()` 的 path + 常量 relative 段 | URL 输入 | URL 路径 | **已归一（URL path）** | 无需 | — |
| 14 | `appserver/skills_remote.go:801` | `pathpkg.Join(segments...)`，`segments[0]=strings.ReplaceAll(base,"\\","/")` | 已归一 | 是（已先归一） | 同上 | **已归一** | 无需 | — |
| 15 | `appserver/turn_runtime.go:9741` | `pathpkg.Clean(path)` | `:9740 path = strings.ReplaceAll(path,"\\","/")`；`path` 来自 `sourcePath` 或 `url.Parse().Path` | 是（已先归一） | `skill://` URI 组装 | **已归一** | 无需 | — |
| 16 | `sandbox/seatbelt.go:198` | `pathpkg.Dir(cleaned)` | `:197 if strings.HasPrefix(cleaned,"/")` —— 仅 POSIX 分支 | 否（分支保证） | macOS seatbelt（`codex-rs/sandboxing` seatbelt） | **无载体**（macOS-only；Windows 走 `filepath.Dir`） | 无需 | — |
| 17 | `sandbox/seatbelt.go:288` | `pathpkg.Clean(value)` | `:287 if strings.HasPrefix(value,"/")` | 否（分支保证） | 同上 | **无载体** | 无需 | — |
| 18 | `sandbox/seatbelt.go:295` | `pathpkg.Join(root, element)` | `:294 if strings.HasPrefix(root,"/")` | 否（分支保证） | 同上 | **无载体** | 无需 | — |
| 19 | `state/migrations.go:100` | `path.Join(directory, entry.Name())` | `directory` = `migrationDirectory(kind)` 常量（`"migrations/state"` 等，`:60-68`）；`entry.Name()` 来自 `fs.ReadDir`（`io/fs` 恒 `/`） | 否 | 内嵌迁移资源 | **无载体**（Go 自身常量拼装） | 无需 | — |
| 20 | `systemskills/systemskills.go:116` | `path.Clean(relative)` | `relative` 来自 `fs.WalkDir(embeddedSamples, ...)`（embed FS 名，恒 `/`） | 否 | system skills 内嵌样例 | **无载体** | 无需 | — |
| 21 | `tui/clipboard.go:313` | `path.Clean(path.Join(all...))` | `all` 由 `strings.FieldsFunc(rest, '\\'||'/')` 切出 + `"/mnt/"+drive`（`:310-312`） | 是（Windows 路径输入），但**输出去向 = WSL/POSIX** | `cli/src/wsl_paths.rs:6-23` `win_path_to_wsl`（`bytes[2]` 判 `\\`/`/` 后 `tail.replace('\\',"/")`） | **已归一（设计如此：目标即 POSIX `/mnt/...`）** | 无需 | — |
| 22 | `utils/pathuri.go:32` | `path.Clean(strings.ReplaceAll(value,"\\","/"))` | `CrossPlatformSlash` | 是（已先归一） | `PathUri` Windows convention | **已归一** | 无需 | — |
| 23 | `utils/pathuri.go:861` | `path.Clean(pathText)` | `:860 pathText = strings.ReplaceAll(pathText,"\\","/")`，Windows 分支 | 是（已先归一） | `path-uri/src/lib.rs:514-517` | **已归一** | 无需 | — |
| 24 | `utils/pathuri.go:864` | `path.Clean(pathText)` | POSIX 分支（`convention != Windows`） | 否（分支保证） | `PathConvention::Posix` | **无载体** | 无需 | — |
| 25 | `tool/apply_patch_agents_md_metrics_like_rust_test.go:260` | `path.Clean(normalized)` | `:253 normalized := strings.ReplaceAll(name,"\\","/")` | 仅测试 | Rust `agents_md` 计数（`#51652`） | **仅测试**（且已归一） | 无需 | — |
| 26 | `appserver/rollout_archive.go`（同 #1 的对照行）`appserver/rollout_archive.go:58` | `filepath.Base(attachment.Path)` | **同一 struct 字段** `FeedbackAttachmentPath.Path` | 是 | `feedback/src/lib.rs:458-465` `….file_name()`（`Path::file_name`，Windows-aware） | **正面基准**（同字段、同文件，已用 `filepath.Base`） | 见 §2 建议 | — |

> 注：#26 不是 `path.*` 调用点，列在此处用于说明 #1 的**同文件内部不一致**（同一字段在 `:58` 用 `filepath.Base`、在 `:272` 用 `path.Base`）。

---

## 2. 唯一需展开项：`appserver/rollout_archive.go:272`

### 2.1 现场

```go
// appserver/rollout_archive.go:271-273
for _, attachmentPath := range paths {
    if FeedbackAttachmentIsRollout(path.Base(attachmentPath.Path)) {
```

```go
// appserver/rollout_archive.go:47-50
func FeedbackAttachmentIsRollout(filename string) bool {
    _, ok := rollout.ThreadIDFromFilename(filename)
    return ok
}
```

```go
// rollout/rollout.go:2258-2260  ← callee，Windows-aware
func ThreadIDFromFilename(name string) (string, bool) {
    base := strings.TrimSuffix(filepath.Base(name), ".zst")
    base = strings.TrimSuffix(base, ".jsonl")
```

同文件 `:54-59` 对**同一个字段**用的是 `filepath.Base`：

```go
func FeedbackAttachmentPathFilename(attachment FeedbackAttachmentPath) string {
    ...
    return filepath.Base(attachment.Path)
}
```

### 2.2 值来源（确属 Windows 原生路径）

- `FeedbackAttachmentPath{Path: ...}` 的构造点只有一处生成器：
  `appserver/feedback.go:387-403` `FeedbackAttachmentPaths(rolloutPaths, guardianRolloutPath, threadID, ...)`
  → 直接 `add(path, nil)` / `add(*guardianRolloutPath, &override)`，**无任何归一**。
- 唯一调用方：`appserver/runtime_router.go:11179`
  `ExtraAttachmentPath: FeedbackAttachmentPaths(rolloutPaths, nil, threadID, nil, params.ExtraLogFiles)`
  ← `rolloutPaths` 来自 `rollout/list` 类 RPC 的**客户端传入参数**。
- ⇒ 在 Windows 上 `attachmentPath.Path` 可以是 `C:\Users\...\.codex\sessions\...\rollout-<ts>-<uuid>.jsonl`（原生 `\`）。
- `path.Base` 只切 `/` ⇒ 在 Windows 原生路径上**返回整条路径**（含 `\`），**未取到 basename**。

### 2.3 Rust 对照

```rust
// C:\rw\codex-rs\feedback\src\lib.rs:835-838
let (rollout_paths, diagnostic_paths): (Vec<_>, Vec<_>) =
    extra_attachment_paths.iter().partition(|attachment| {
        codex_rollout::rollout_id_from_path(&attachment.path).is_some()
    });
```

```rust
// C:\rw\codex-rs\rollout\src\metadata.rs:114-117
pub fn rollout_id_from_path(rollout_path: &Path) -> Option<RolloutId> {
    let file_name = rollout_path.file_name()?.to_str()?;
    Some(RolloutFileName::parse(file_name)?.rollout_id())
}
```

`Path::file_name()` 在 Windows 上同时按 `\` 与 `/` 切 ⇒ Rust **恒取到真 basename**，与主机无关。

### 2.4 行为探针（真实 Go 码，`go test -overlay=`，探针不落仓）

探针文件 `%TEMP%\syncw1_probe\zz_probe_winpath_test.go`，overlay `overlay_winpath.json`（把探针注入 `lf_d12d00dd\appserver\`）；命令：

```powershell
Push-Location %TEMP%\syncw1_probe\lf_d12d00dd
go test ./appserver/ -run 'TestProbeWindowsRolloutBasename' -count=1 -v -overlay=%TEMP%\syncw1_probe\overlay_winpath.json
Pop-Location
```

原文输出：

```
=== RUN   TestProbeWindowsRolloutBasename
    zz_probe_winpath_test.go:13: path.Base(win)                     = "C:\\Users\\alice\\.codex\\sessions\\2026\\10\\07\\rollout-2026-10-07T12-00-00-11111111-2222-3333-4444-555555555555.jsonl"
    zz_probe_winpath_test.go:14: filepath.Base(win)                 = "rollout-2026-10-07T12-00-00-11111111-2222-3333-4444-555555555555.jsonl"
    zz_probe_winpath_test.go:15: IsRollout(path.Base(win))          = true
    zz_probe_winpath_test.go:16: IsRollout(win)                     = true
    zz_probe_winpath_test.go:18: ThreadIDFromFilename(win)          = "11111111-2222-3333-4444-555555555555"/true
    zz_probe_winpath_test.go:21: trailing: path.Base="C:\\Users\\alice\\.codex\\sessions\\2026\\10\\07\\rollout-2026-10-07T12-00-00-11111111-2222-3333-4444-555555555555.jsonl\\"
    zz_probe_winpath_test.go:22: trailing: IsRollout(path.Base(...)) = true ; IsRollout(...) = true
    zz_probe_winpath_test.go:24: non-rollout: IsRollout(path.Base("C:\\Users\\alice\\.codex\\logs\\daemon.log")) = false
--- PASS: TestProbeWindowsRolloutBasename (0.00s)
PASS
ok  	codex_go/appserver	0.102s
```

### 2.5 判定：**已归一（下游 callee `filepath.Base` 兜底）— 无实际分歧**

- `path.Base` 在 Windows 原生路径上确实**没能**取到 basename（探针第 13 行证明），但随即被 callee `rollout.ThreadIDFromFilename` 的 **`filepath.Base`（Windows-aware）** 二次取 basename ⇒ 最终判定结果与 Rust 一致（`true/true`，非 rollout `false`）。
- 即：**当前无可观测分歧 ⇒ 不判真漏计**。（与 `#51652` 的差异：那里 basename 就是最终消费者 `filename=` 标签值，无人兜底，故真漏计。）
- 但这是 **latent robustness 隐患**：一旦 callee 换掉 `filepath.Base`、或 `IsRollout` 被内联，`path.Base` 会立即失效。且同文件 `:58` 对**同一字段**已用 `filepath.Base` ⇒ **内部不一致**。
- **建议修法（可选，1 文件 1 行）**：`path.Base` → `filepath.Base`；若无其它 `path.` 用法需删 `"path"` import（本文件仅此一处 `path.`，见 §0.2 穷举）。
- 规模：**1 文件 / 1 行**。

---

## 3. 相邻同类扫描（**越出本单声明范围**，仅登记，供队长裁量）

本单口径为 `path.*`。为防遗漏，另扫了同类的 POSIX-only 字符串操作 `strings.Split(x,"/")` / `strings.Index(x,"/")`，全仓命中如下（**未发现**喂 Windows 文件系统路径且未归一的点）：

- `appserver/precomputed_exports.go:149` — 上游即 `strings.Contains(relativePath,"\\")` **显式拒绝** 反斜杠 ⇒ 安全。
- `appserver/router.go:1867-1868` — `cleaned := filepath.ToSlash(filepath.Clean(path))` 后再 `Split(cleaned,"/")` ⇒ **已归一**。
- `prompt/skills_render.go:942,946,954,958` — `cleanSkillAliasRoot/Path` 先 `ReplaceAll(x,"\\","/")` + `filepath.Clean` ⇒ **已归一**（`:1015/:1033` 的 `Split` 输入已归一）。
- `appserver/executor_skill_provider.go:248`、`turn/skills_tools.go:532`、`appserver/turn_runtime.go:9716`、`appserver/subagent_completed_activity.go:20`、`agent/path.go:50,68,147` — `skill://` URI / agent 路径空间（恒 `/`）⇒ **无载体**。
- `codemode/codemode.go:883`、`jsonschema/jsonschema.go:531`、`mcp/openai_file_schema.go:132` — JSON Pointer（RFC 6901，恒 `/`）⇒ **无载体**。
- `doctor/doctor.go:590`、`mcp/oauth_callback_mode.go:95`、`network/proxy_config.go:427`、`realtime/transport.go:308`、`plugin/marketplace_source.go:174`、`appserver/accepted_lines_analytics.go:207` — URL / 代理 URL / git remote URL 的 path 段 ⇒ **无载体**。
- `safety/secrets.go:312`、`state/log_handler.go:300`、`config/api.go:726`、`tui/bottom_pane/chat_composer.go:210`、`tui/terminal_probe.go:124` — 密钥键名 / Go 模块路径 / 模型命名空间 / slash 命令 / 终端 `rgb:` 值 ⇒ **非路径**。
- `shell/display_command.go:248-251` — `shortDisplayPath`：`:249 normalized := strings.ReplaceAll(path, "\\", "/")` 先归一 ⇒ **已归一**。
- `appserver/skills_remote.go:571`、`appserver/skills.go:1375`、`appserver/router.go:1868`（如上）— 均已在上游归一。

结论：相邻面**未发现**新的真漏计/真错误。

---

## 4. 正面基准（已正确归一的现成范式，供后续复用）

Windows-aware 取 basename / 段的既有正确写法：

| file:line | 写法 |
|---|---|
| `utils/pathuri.go:20-29` | `CrossPlatformBase`：`strings.LastIndexAny(value, "/\\")` |
| `utils/pathutils.go:195-196` | `filepath.ToSlash(filepath.Clean(path))` |
| `tool/runtime_paths.go:156` | `LastIndexAny` 族 |
| `tui/chatwidget/input_submission.go:363`、`plugin/api.go:3383`、`tui/mention_codec.go:280`、`tui/bottom_pane/mentions_v2/render.go:159`、`tui/bottom_pane/feedback_view.go:399`、`tui/chatwidget/notifications.go:202` | `LastIndexAny(value, "/\\")` |
| `shell/display_command.go:84`、`config/filesystem_deny_read.go:122`、`sandbox/windowssandbox/deny_read_resolver.go:95` | `LastIndexAny` 族 |
| `execserver/windows_powershell_fallback.go:134` | `LastIndexAny(\`\\/\`)`（Windows-aware，正面基准） |
| `tui/bottom_pane/chat_composer.go:34` | 复用 `CrossPlatformBase` |
| `appserver/router.go:1867`、`prompt/skills_render.go:946,958` | `filepath.ToSlash` / `ReplaceAll("\\","/")` 先归一 |
| `tool/apply_patch_agents_md_metrics.go:45` | `filepath.Base`（**sync644 已落主的修复**，本单的对照参照） |

---

## 5. 结论

1. **全仓 `path.*` 面已穷举**（11 文件 / 25 调用点 / 成员仅 `Base·Dir·Join·Clean`；在 `d12d00dd` 与 `b041cbfd` 两基线上逐条相同）。
2. **未发现新的「真漏计 / 真错误」** —— 除已在 `sync644` 修掉的 `tool/apply_patch_agents_md_metrics.go:45` 之外。
3. 唯一需要展开的 `appserver/rollout_archive.go:272`：`path.Base` 确未取到 Windows basename，但被下游 callee `rollout.ThreadIDFromFilename` 的 `filepath.Base` 兜底 ⇒ **当前无观测分歧**；判 **已归一 / 无实际分歧**，登记为 **latent robustness（可选修法：`path.Base`→`filepath.Base`，1 文件 1 行）**。
4. 其余工作点全部为：**已归一**（13 处）、**无载体**（`io/fs` 常量 / `url.Parse().Path` / 分支保证 POSIX / embed FS）、**仅测试**（1 处）。
5. 相邻面（`strings.Split/Index(x,"/")`）**未发现** Windows 原生路径未归一。

### 待队长放行项（仅 1 条，可选）

- **A1**：`appserver/rollout_archive.go:272` `path.Base` → `filepath.Base`（+ 视情况删 `"path"` import）。规模 1 文件；值级 RC 可做（探针当前 PASS，改前/改后行为在本例相同 ⇒ RC 需构造「callee 兜底被绕开」的对照，或改用 `FeedbackAttachmentPathFilename` 一致性测试作值级锚点）。**若队长认为 latent-only 不值得动，可直接判 N/A。**

### 未决问题 / 风险

- U1：`#51652` 修复的 `tool/apply_patch_agents_md_metrics.go` 中，`filename` 标签取的是 `filepath.Base(changedPath)`（已修）；但**同一 PR 的 `move_path` 去重语义**是否也需 Windows 归一 —— 本单未涉，若队长需要可另开探针。
- U2：`appserver/rollout_archive.go:272` 若要出补丁，RC 需「撤 `filepath.Base` ⇒ FAIL」的**值级**锚点；当前下游兜底会吞掉差异，故建议**同时**把 callee 的二次 basename 视作待加固点（但改 callee 会扩散到 `rollout/` 包 —— 需队长授权，**本单未做**）。
