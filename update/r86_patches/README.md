# update/r86_patches — 仅剩 2 个未落主补丁

round86–89 的候选补丁共 **66** 个。其中 **64 个已落主**（内容已在 `main` 中），
已按 leader 指示于 2026-10-07 分两批**删除**。删除前的完整副本在 git 历史里，可随时取回：

    git show 08caefa8:update/r86_patches/<name>          # 第一批（63 个，round88 归档提交）
    git log --diff-filter=D --name-only -- update/r86_patches

本目录只剩下面 2 个，且都有明确原因，**不是待办遗漏**：

| 文件 | 状态 | 原因 / 解锁条件 |
|---|---|---|
| `syncw3_49584.patch` | **故意不落** | 改 `appserver/turn_runtime.go`（既定避让面）+ guardian skills context 子系统（既有 backlog）。 |
| `syncl3.patch` | **被后继取代的旧草稿** | 由 `syncl3b.patch`（已落）与后续 split 补丁取代；新增行仅 3/6 命中树。 |

第 3 个 `syncw3_parity_manifest_51678.patch`（623 B）已**随 sync665 的 parity pin 推进落主**：
parity 检出 `C:\rw\codex-rs` 前进到 `95ec468619`（含 #51678），解锁条件满足，该改动现由
`parity/rust_unified_exec_sandbox_manifest_test.go` 的 `core/tests/windows_sandbox.rs` 条目承载
（落主时另加了两行注释，故补丁不再逐字适用，但它描述的改动已在树内）。

逐条判定方法（可复跑）与完整处置清单：`update/r88_patch_disposition_2026_10_07.md`；
pin 推进的实测证据：`update/r89_pin_advance_95ec468619_2026_10_07.md`。
