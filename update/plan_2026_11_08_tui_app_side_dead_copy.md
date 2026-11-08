# 技术债：`tui/app/side.go` 与 `tui/tea/side.go` 的重复实现（2026-11-08）

不进 manifest，只是清理项。判定基线：`/tmp/m14`（你 19:04 那次提交后的主干树副本，manifest sha256 `3647d8df9ed129d2…`）。

## 0. 结论

- `tui/app/side.go` 里的 11 个函数，**没有一个有生产调用方**；唯一引用它的是 `tui/app/side_test.go`。
- 与 `tui/tea/side.go` 同名的有 **3 个**：`SideDeveloperInstructions`、`SideStartErrorMessage`、`SideCloseErrorMessage`。生产路径全部走 `tui/tea`。
- 建议：删掉这 3 个同名副本 + 它们对应的 3 个测试（或把断言语义并入 `tui/tea` 的用例），**再评估整份 `tui/app/side.go` 是否一并清掉**（见 §4）。

## 1. 三处同名函数与调用方

| 函数 | `tui/app/side.go` | `tui/tea/side.go` | 生产调用方（全部指向 tea） |
|---|---|---|---|
| `SideDeveloperInstructions` | :189 | :476 | `app/side_tui.go:59` → `codextea.SideDeveloperInstructions(existingInstructions)`；`app/remote_tui.go:6149` → `codextea.SideDeveloperInstructions("")` |
| `SideStartErrorMessage` | :207 | :498 | `tui/tea/side.go:605` → `m.notice = SideStartErrorMessage(msg.Err)`（同包内） |
| `SideCloseErrorMessage` | :220 | :512 | `tui/tea/side.go:779` → `m.notice = SideCloseErrorMessage(side.SideThreadID, msg.Err)`（同包内） |

证据：`rg -n --glob '!*_test.go' '\bSide(DeveloperInstructions|StartErrorMessage|CloseErrorMessage)\b' .` 的全部命中只有
① 两处定义；② `app/` 里的两处 `codextea.` 调用；③ `tui/tea/side.go` 自己的两处调用。
`rg -n 'tuiapp\.Side\w+'` 只命中 `tuiapp.SideEditPreviousUnavailableMessage`（另一个符号，`tui/tea/backtrack.go:238`、`:375`），**没有任何 `tuiapp.SideDeveloperInstructions` 之类**。

## 2. 为什么会出现"改了没变红"

`TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust`（`tui/app/side_test.go:52`，**至今仍登记在 `side-chat-policy-first-request` 的 verifier 里**）直接调用**包内**的 `SideDeveloperInstructions`，所以：

- 改 `tui/tea/side.go:476` → 该用例**不会**变红（我上轮实测）；
- 改 `tui/app/side.go:189` → 该用例变红，但**生产路径一点没变**。

这正是你发现的那个"verifier 测的是没人用的那份"。你已经补了 `tui/tea/side_developer_instructions_like_rust_test.go`（`TestSideDeveloperInstructionsKeepExistingPolicyLikeRust`）覆盖生产实现，这条债算是被"绕开"了——但死代码还在，下次有人照着 `tui/app/side.go` 改，仍然测不出问题。

## 3. 建议的清理动作（按风险从低到高）

| 步骤 | 动作 | 风险 |
|---|---|---|
| 1 | 删 `tui/app/side.go:189-195` 的 `SideDeveloperInstructions` + `tui/app/side_test.go:52` 的用例（语义已由 `tui/tea` 的新用例覆盖） | 极低：无生产调用方 |
| 2 | 删 `:207` / `:220` 的 `SideStartErrorMessage` / `SideCloseErrorMessage` + `side_test.go:31` 的 `TestSideStartErrorMessageMatchRust` | 低：两条消息的断言可以照搬到 `tui/tea`（`side.go:498/512` 的实现同源） |
| 3 | 复核 `tui/app/side.go` 其余 8 个函数（`NewSideThreadState`、`IsActionable`、`SideParentStatusForRequestKind`、`SideParentStatusChangeForNotification`、`ApplySideParentStatusChange`、`SideContextLabel`、`SideStartBlockMessage` 等）是否有外部调用 | 中：`SideParentStatus*` 一族可能被 `app/` 用它自己的类型；我在非测试代码里没找到跨包调用（`rg '\bapp\.Side\w+'` → 0），但需要你确认 `tui/app` 内部是否还有使用者 |

如果第 3 步也确认无人使用，`tui/app/side.go`（229 行）可以整份删除；`tui/app` 包本身仍被 `app/` 与 `tui/tea` 以 `tuiapp "codex_go/tui/app"` 引用（`app/interactive.go:52`、`tui/tea/backtrack.go` 等），**删文件不等于删包**。

## 4. 一个附带的观察（不进本清理项）

`side-chat-policy-first-request` 现在的 verifier 是三条：`TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust`（**跑的是死实现**）、`TestSideBoundaryPromptMarksInheritedHistoryReferenceOnlyMatchRust`、你新补的 `TestSideDeveloperInstructionsKeepExistingPolicyLikeRust`（跑生产实现）。你补了 tea 的用例之后它已经成立，但影响面：那条**死实现的 verifier 还挂在契约里**——建议在删副本时同步把它从 verifier 移除（否则它继续为一条已不存在的实现背书）。另外可顺手把 `goPaths` 从 `["app","context"]` 收敛成 `["app","tui/tea","context"]`（`app/side_tui.go:59` 是 tea 的调用方，`app` 要保留）。这两条只是建议，我这次没有提案化。
