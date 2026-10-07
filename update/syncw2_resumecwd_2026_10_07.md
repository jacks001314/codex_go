# syncw2 · D2 定案 + 修复：remote TUI resume 未带 caller cwd override（round87）

- 车道: **syncw2**（Windows）；域: `app/`
- 纪律: **0 commit / 0 push / 0 tag / 0 ref 移动**；交付 = 补丁 + 本报告
- 补丁: `update/r86_patches/syncw2_resumecwd.patch` — **12697 bytes**，sha256 `3add39145a520d63e041b80508254693a6e1440c73b6519b61a900e7cca85249`
- 基线: `origin/main` = **`185d03c933086ddd2b0a1f498fdcf7f9d2683d70`**（sync632）
- 工作树: 改动 `D:\tmp\syncw2\wt632`（@185d03c9）；基线对拍 `D:\tmp\syncw2\wt632base`（@185d03c9 干净）；
  LF parity 树 `D:\tmp\syncw2\wt632lf`（`git -c core.autocrlf=false worktree add`，CRLF=0，补丁已 apply）

## 0. 定案（一行）

**D2 = 真缺口**（同一观测面），已修：remote TUI 的 `thread/resume` 现在把 caller cwd 作为被恢复线程的 cwd override 发出，与 Rust `resume_thread_with_permission_overrides → thread_resume_params_from_config(..., remote_cwd_override, ...)` 一致。

---

## 1. 可观测面对照（契约第 1 条）

### 1a. Rust：Remote 模式下确实带 `cwd = cli.cwd`

`origin/main:codex-rs/tui/src/app_server_session.rs`：

```
2180: fn thread_resume_params_from_config(
2184:     remote_cwd_override: Option<&std::path::Path>,
...
2222:     let mut params = ThreadResumeParams {
2226:         service_tier: service_tier_override_from_config(&config),
2227:         cwd: thread_cwd_from_config(&config, thread_params_mode, remote_cwd_override),
...
2300: fn thread_cwd_from_config(
2302:     thread_params_mode: ThreadParamsMode,
2303:     remote_cwd_override: Option<&std::path::Path>,
2304: ) -> Option<String> {
2305:     match thread_params_mode {
2306:         ThreadParamsMode::Embedded => Some(config.cwd.to_string_lossy().to_string()),
2307:         ThreadParamsMode::Remote => {
2308:             remote_cwd_override.map(|cwd| cwd.to_string_lossy().to_string())
2309:         }
2310:     }
2311: }
```

生产调用方 `origin/main:codex-rs/tui/src/app_server_session/rollout_history.rs:150-174`：

```
167:         let mut params = thread_resume_params_from_config(
168:             session_config,
169:             thread_id,
170:             self.thread_params_mode(),
171:             self.remote_cwd_override.as_deref(),
172:             model_settings,
173:             permission_overrides,
174:         );
```

⇒ **Remote 模式 ⇒ `cwd = remote_cwd_override`**；交互式 remote TUI 的 override 由 `codex-rs/tui/src/lib.rs:1237` `with_remote_cwd_override(remote_cwd_override.clone())` 注入（= 调用方 cwd）。（本 PR 新增的 `session_archive_commands.rs:253-270` 也在 remote 命令引导里设同一个 override。）

### 1b. 服务端语义：Go 与 Rust **一致**（都是「覆盖服务端 cwd」，非「仅本地提示」）

Rust 服务端：`codex-rs/app-server/src/request_processors/thread_processor.rs`

```
141:     if let Some(requested_cwd) = request.cwd.as_deref() {
143:         if requested_cwd_path != config_snapshot.cwd().as_path() {   // 与活动 cwd 比对 = 覆盖语义
1667:     fn build_thread_config_overrides(..., cwd: Option<String>, ...) -> ConfigOverrides {
1686:             cwd: cwd.map(PathBuf::from),                            // 进 ConfigOverrides
3927:         let mut typesafe_overrides = self.build_thread_config_overrides(
3931:             cwd,                                                    // 来自 params.cwd
```

Go 服务端：`appserver/runtime_router.go`

```
6869: func (r *RuntimeRouter) applyThreadResumeSettingsUpdate(result any, request *Request) {
6881:     settingsUpdate, hasSettingsUpdate := threadResumeSettingsUpdateParams(&params, response.Thread.ID)
6885:     r.applyTurnStartSettingsUpdate(settingsUpdate)

7226: func threadResumeSettingsUpdateParams(params *ThreadResumeParams, threadID string) (*SettingsUpdateParams, bool) {
7235:     if cwd := stringPtrValue(params.CWD); cwd != "" {
7236:         update.CWD = &cwd            // thread settings 的 CWD 覆盖
```

⇒ **同一观测面**：`thread/resume` 的 `cwd` 两侧都是「把被恢复线程的 cwd 覆盖为请求值」。

### 1c. Go 客户端的缺口

`origin/main:app/remote_tui.go`（改动前）：

```
1570:  resumeErr := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, appserver.ThreadResumeParams{ThreadID: threadID}, &resumed)   // TUI resume
1862:  resumeErr := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, appserver.ThreadResumeParams{ThreadID: threadID}, &resumed)   // agent switch attach
```

`appserver/protocol.go:1662` `CWD *string \`json:"cwd,omitempty"\`` —— 字段存在但两处都未填。
另：Go 的 remote **thread/start**、**thread/fork** 已带 caller cwd（`app/remote_tui.go:4552` `CWD: shared.CWD`、`:4593-4595` fork `params.CWD`），唯独 resume 未带 ⇒ 不对称，确认为真缺口。

---

## 2. 补丁内容（2 文件，+119/−10）

- `app/remote_tui.go`（+26/−10）
  - 新增 `remoteTUIResumeParams(root, threadID)`：`interactiveSessionPickerCWD(root)`（= `root.Shared.CWD`，退化 `os.Getwd()`）非空时写 `params.CWD`；
  - `interactiveRemoteResumeSessionHandler(ctx, endpoint, root)` 与 `interactiveRemoteSwitchAgentThread(ctx, endpoint, root, threadID)` 各在 `thread/resume` 处改用该 helper；两处构造点（`runInteractiveRemoteTUI` 内 :444 / :464）传入既有 `root`。
- `app/app_test.go`（+93/−0 净 +103/−3 显示）
  - 新增 `TestInteractiveRemoteResumeSessionForwardsCallerCWD`（resume handler 路径，此前 0 覆盖）；
  - 既有 `TestInteractiveRemoteSwitchAgentThreadReadsTranscript` / `…FallsBackToReadOnlyHistory` 传入 `root` 并断言捕获到的 `thread/resume` 参数 `cwd == "/caller/worktree"`。

## 3. 门禁实跑

| 门 | 命令 | 结果 |
|---|---|---|
| ① gofmt | 对 **LF index blob** 逐个 `git cat-file blob :<path> \| gofmt` 比对 | `OK app/remote_tui.go` / `OK app/app_test.go`（两 blob 与 gofmt 输出逐字节一致） |
| ② build | `go build ./...` | exit 0 |
| ③ vet | `go vet ./app/` | exit 0（无输出） |
| ④ 整包测试 | `go test ./app/ -count=1` | **改动前基线**（wt632base @185d03c9）：`ok codex_go/app 18.630s`（0 FAIL）；**改动后**（wt632）：`ok codex_go/app 16.151s`（0 FAIL）⇒ **新增失败 0** |
| ⑤ RC | 见 §4 | 撤接线 ⇒ 3 条值级 FAIL；恢复 ⇒ ok |
| ⑥ parity | `$env:CODEX_RUST_ROOT='C:\rw\codex-rs'; go test ./parity/ -count=1` | LF 树（wt632lf，补丁已 apply）：**`ok codex_go/parity 20.752s`** |

⚠️ gofmt 说明：本机工作树 `core.autocrlf=true`，`gofmt -l <file>` 对**任何**文件都报（连未改动的 `app/session.go`、`app/recap.go` 也报），属 CRLF 假阳性；故按 LF index 内容判定（上方口径）。

⚠️ parity 说明：CRLF 工作树里跑 parity 会红**唯一一条** `TestRustCollaborationModeTemplatesMatchGo`（`go vocab embed` 读工作树的 CRLF 文本）。已实测该红在**未改动的基线树**同样出现（wt632base @185d03c9：`--- FAIL: TestRustCollaborationModeTemplatesMatchGo`），与本次改动无关；LF 树全绿。这与队长 round86 记录的 CRLF 陷阱一致。

## 4. RC 原文（真实生产接线）

撤掉接线 = 把两处 `remoteTUIResumeParams(root, threadID)` 还原为 `appserver.ThreadResumeParams{ThreadID: threadID}`（仅此 2 处，`assert count==2`）。

**撤掉 ⇒ FAIL（值级）：**
```
$ go test ./app/ -run 'TestInteractiveRemoteResumeSessionForwardsCallerCWD|TestInteractiveRemoteSwitchAgentThread' -count=1
--- FAIL: TestInteractiveRemoteResumeSessionForwardsCallerCWD (0.00s)
    app_test.go:5259: thread/resume cwd = <nil>, want "/caller/worktree"
--- FAIL: TestInteractiveRemoteSwitchAgentThreadReadsTranscript (0.01s)
    app_test.go:5356: thread/resume cwd = <nil>, want "/caller/worktree"
--- FAIL: TestInteractiveRemoteSwitchAgentThreadFallsBackToReadOnlyHistory (0.00s)
    app_test.go:5459: thread/resume cwd = <nil>, want "/caller/worktree"
FAIL
FAIL	codex_go/app	0.164s
FAIL
```

**恢复 ⇒ ok：**
```
$ git diff --stat
 app/app_test.go   | 103 +++++++++++++++++++++++++++++++++++++++++++++++++++---
 app/remote_tui.go |  26 ++++++++++----
 2 files changed, 119 insertions(+), 10 deletions(-)
$ go test ./app/ -run 'TestInteractiveRemoteResumeSessionForwardsCallerCWD|TestInteractiveRemoteSwitchAgentThread' -count=1
ok  	codex_go/app	0.180s
```

## 5. apply --check 原文（打在本单基线 `185d03c9`）

```
$ git -C D:\tmp\syncw2\wt632base apply --check --verbose D:\tmp\syncw2\syncw2_resumecwd.patch   # 干净 185d03c9
Checking patch app/app_test.go...
Checking patch app/remote_tui.go...
(exit=0)

$ git -C D:\tmp\syncw2\wt632lf apply --check --verbose D:\tmp\syncw2\syncw2_resumecwd.patch     # 干净 185d03c9 (LF)
Checking patch app/app_test.go...
Checking patch app/remote_tui.go...
(exit=0)
```

## 6. 探针/临时物位置

- 探针与脚本均在 `D:\tmp\syncw2\`（含 `edit632.py`、`rc632.py`、`gofmt632.py`、`syncw2_resumecwd.patch`、`remote_tui.go.bak`）；**未落仓**。
- 未使用 `git commit/push/tag/stash`，未移动任何分支指针；`git add -A -N` 仅用于生成 diff。

## 7. 未决 / 风险

1. **取值口径**：本补丁用 `interactiveSessionPickerCWD(root)`（= `root.Shared.CWD` 或 `os.Getwd()`）作为 Rust `cli.cwd` 的对应物，与 `app/remote_tui.go:436`/`:1720` 既有的「caller cwd」用法一致。若你希望严格只用 `root.Shared.CWD`（不退化到 `os.Getwd()`），改动是一行，请告知。
2. Rust 交互式 remote TUI 的 override 在 reconnect 路径（`tui/src/app/reconnect.rs:60`）也会重设；Go 的 remote reconnect 若走同一 `interactiveRemoteResumeSessionHandler`，本补丁自动覆盖；若另有 reconnect 专用 resume 点，需要另开条目（本轮 `git grep MethodThreadResume -- app/` 只找到这两处）。
3. D1（queue 的 `turn_trigger="queue"`，#40665 漏项）按上轮裁定仍挂「待避让面开闸」，本单未动。
