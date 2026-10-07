# update/r86_patches — 仅剩 3 个未落主补丁

round86–88 的候选补丁共 **66** 个。其中 **63 个已落主**（内容已在 `main` 中），
已按 leader 指示于 2026-10-07 **删除**。删除前的完整副本在 git 历史里，可随时取回：

    git show 08caefa8:update/r86_patches/<name>          # 删除前的归档提交
    git log --diff-filter=D --name-only -- update/r86_patches

本目录只剩下面 3 个，且都有明确原因，**不是待办遗漏**：

| 文件 | 状态 | 原因 / 解锁条件 |
|---|---|---|
| `syncw3_parity_manifest_51678.patch` | **被 Rust pin 卡住** | 它把 parity manifest 指向 `core/tests/windows_sandbox.rs`（Rust #51678 移动了该文件），但 parity 检出 `C:\rw\codex-rs` 固定在 `5b0b253035`，该 pin 上这个路径不存在（`suite\windows_sandbox.rs` 存在）⇒ 落上去 `TestRustUnifiedExecSandboxSuiteManifest` 必红。**解锁**：parity 检出推进到含 #51678 的提交后直接 `git apply`。 |
| `syncw3_49584.patch` | **故意不落** | 改 `appserver/turn_runtime.go`（既定避让面）+ guardian skills context 子系统（既有 backlog）。 |
| `syncl3.patch` | **被后继取代的旧草稿** | 由 `syncl3b.patch`（已落）与后续 split 补丁取代；新增行仅 3/6 命中树。 |

逐条判定方法（可复跑）与完整处置清单：`update/r88_patch_disposition_2026_10_07.md`。
