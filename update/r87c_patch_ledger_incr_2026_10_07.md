# r87c 补丁台账「增量」报告（syncl5，2026-10-07）

> 与 `r86_patch_ledger_2026_10_07.md`（r87c 基线刷新版，覆盖 44 包）配套，专记**本次相对 r87b 版的差异**。
> 只读；0 补丁 / 0 commit / 0 push / 0 ref；未 `project_delete` 任何 `.patch`。

## 1. 基线移动
| | r87b 版 | r87c 版 |
|---|---|---|
| Go `origin/main` | `e9849503`（sync638） | **`8b2453a9`（sync641）** |
| 覆盖 `.patch` | 49 | **44** |
| 抽检树 | `git archive e9849503` | `git archive 8b2453a9`（干净 LF 树） |

新增 3 笔落主提交：
```
1a465823 sync639: classify queue-dispatched turns with turn_trigger=queue like Rust (#40665)
ee2d4e1d sync640: honor the managed windows.allow_mxc requirement like Rust (#49642)
8b2453a9 sync641: launch embedded with a warning from an elevated Windows terminal like Rust (#49855)
```

## 2. 目录集变化（`update/r86_patches/`）
- **−8**（r87c P0 清理，队长授权、syncl5 执行 `project_delete`）：`syncw4_51652.patch`、`syncw4_49147.patch`、`syncw1_26114v1.patch`、`syncl1_49912.patch`、`syncw1_49076.patch`、`syncl5_session_index.patch`、`syncw2_49799.patch`、`syncw2.patch.DEPRECATED.md`（末者非 `.patch`）。
- **+2**（r87b 扫掠后才出现 / 未水合，本轮纳入）：`syncl1_49642.patch`（15559 B）、`syncw3_49855.patch`（9330 B）。
- 计数：49 − 7（.patch） + 2 = **44**。

## 3. 两条新补丁定性
| patch | bytes | sha256 | fwd | rev | 逐文件 | 处置 | 落主 |
|---|---|---|---|---|---|---|---|
| `syncl1_49642.patch` | 15559 | `cd2384265dbacb4f…` | 1 | 0 | 5/5 | **已落地** | sync640 `ee2d4e1d`（#49642 windows.allow_mxc） |
| `syncw3_49855.patch` | 9330 | `a5abcaed3c442514…` | 1 | 1 | 2/3 | **已落地**（含 BOM 例外） | sync641 `8b2453a9`（#49855） |

`syncl1_49642.patch` 反向 0（5/5 逐字存在）⇒ 干净已落地。
`syncw3_49855.patch` 见 §5 例外登记：`app/daemon_startup_test.go` 因**落主前去掉 UTF-8 BOM** 致 `--reverse` 失败，属**预期**，**不判「未落地」**。

## 4. 本次结论差异（vs r87b 版）
| 维度 | r87b（`e9849503`） | r87c（`8b2453a9`） | 变化 |
|---|---|---|---|
| ALREADY-LANDED（rev=0） | 37 | 37 | 0（3 个已取代包被删、+1 新包 `syncl1_49642`、`syncw3_49855` 计入冲突） |
| APPLIES（fwd=0） | 2（`syncw4_49147`/`syncw4_51652`） | **0** | −2（两包均已在 P0 清理中移除） |
| CONFLICTS（fwd=1,rev=1） | 9 | **7** | −3 已取代包移除、+1 新包 `syncw3_49855`（BOM 例外） |
| 无法判定 | 0 | **0** | — |

**基线前进（sync639–641）未使任何「已落地」包退化为冲突**（新增 3 笔提交只触及 `app/daemon_startup*.go`、`app/interactive.go`、queue-turn 相关面，均在已落地包的上下文之外）。

现存 7 个冲突包：`integ86c` / `syncl6` / `syncl3` / `syncl3_49076` / `syncl6_49345` / `syncw3_49584` / `syncw3_49855` —— 逐文件反向证据见主台账「冲突/漂移明细」。

## 5. 例外登记（BOM 有意偏差）
`syncw3_49855.patch` 的 `app/daemon_startup_test.go` hunk 带 **UTF-8 BOM**；落主 sync641 前删除前 3 字节（`gofmt -l` 转绿）。
- 落主后该文件 LF 大小 **22995 B**，sha256 `265bea7f0ceb07da5a65b051f3476b8b1407374bbb62cd2232a75028a1c3dded`；首字节 `package `（已复测，`sha256sum` 与队长值逐字一致）。
- ⇒ 该文件 `--check --reverse` **必然失败**，属**预期**；该包整体仍判 **已落地**。

## 6. 剩余未定性项
- **仍 APPLIES 的包 = 0** ⇒ **无「待队长裁定是否落主」项**。
- **无法判定 = 0**。
- 唯一需要留意的非零差异是 §5 的 BOM 例外（已登记，非未定性）。
- 仍存在 7 个 CONFLICTS 包，但**内容均已在 main**，按队长裁定**原地保留、只标 `禁重放`**（P1/P2 不再删除）。

## 7. 只读声明
- 仅只读 `git archive/log/show/grep/cat-file` + `git apply --check`（无 `--index`/无实写）；抽检树在 `/tmp`。
- **0 补丁 / 0 commit / 0 push / 0 ref 移动**；**未 `project_delete` 任何 `.patch`**。
- 产物仅本报告 + `r86_patch_ledger_2026_10_07.md`。

---
*生成：syncl5 · Linux 节点 · 2026-10-07 · 基线 Go `8b2453a9`（sync641）*