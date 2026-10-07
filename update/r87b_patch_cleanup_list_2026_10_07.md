# r87b 补丁「作废 / 重复」删除建议清单（syncl5，2026-10-07）

> 只读产物。**本车道不执行 `project_delete`** —— 由队长按本清单决定。
> 基线：Go `origin/main = e9849503cff38b6379cb1e043e1443292aee8638`（sync638）。
> 证据：`git apply --check` / `--check --reverse` 打在干净 LF 树（`git archive e9849503`）上；`--check --reverse` = 「已落地」强信号。详见 `r86_patch_ledger_2026_10_07.md`。

## 计数

| 组 | 数量 | 处置 |
|---|---|---|
| P0-A 字节重复 | 1 | 删（保留另一份） |
| P0-B 已取代/禁重放 | 4 | 删 |
| P0-C 已裁定不收录 | 1 | 删 |
| P1 冲突（内容基本已落） | 6 | 删（残留 hunk 无独立价值） |
| P2 已落地（冗余） | 37 | 建议删（若无审计留档需求） |
| **合计 .patch** | **49** | |
| 另有非 .patch | 1 | `syncw2.patch.DEPRECATED.md`（见 P0-C'） |

## P0-A · 字节重复（必删其一）

| 文件 | bytes | sha256 | 建议 | 理由 |
|---|---|---|---|---|
| `syncw1_26114v1.patch` | 3994 | `20fffe65a7111459…` | **删** | 与 `syncw1_v1guidance.patch` **逐字节相同**（`cmp` 相同、sha256 相同），均已落 **sync632 `185d03c9`** |
| `syncw1_v1guidance.patch` | 3994 | `20fffe65a7111459…` | 保留 | 名字更贴合语义（v1 guidance）；与上者同字节 |

## P0-B · 已取代 / 禁重放（删）

| 文件 | bytes | sha256 | 建议 | 理由 |
|---|---|---|---|---|
| `syncl1_49912.patch` | 10153 | `96ac9ce88e8a8e1c…` | **删** | 同一 `#49912` 的较旧版本；生产面已由 sync611 `985ccf59` 经 `syncw2_49912.patch` 落地（fwd=1/rev=1，0/3 已落） |
| `syncw1_49076.patch` | 7433 | `6168a0b9fbdfbed2…` | **删** | 同一 `#49076` 的较旧/竞争版本；已由 sync608 `944ac1f8`（+`syncl3_49076.patch`，sync622 的 gitinfo 改写）覆盖（0/4 已落） |
| `syncl5_session_index.patch` | 3014 | `4ef0550aa55c9e2a…` | **删** | 内容已拆分入 main：sync618 `8ade48ed` + sync620 `4ee41b7a`（0/2 已落） |
| `syncw4_51652.patch` | 14871 | `9877a5d7ecfd867b…` | **删** | `#51652` 的竞争实现；sync638 `e9849503` 已用 `syncw1_51652.patch` 落地（fwd=0 但**禁重放**，否则二次接线 `codex.agents_md.edit`） |

## P0-C · 已裁定不收录（删）

| 文件 | bytes | sha256 | 建议 | 理由 |
|---|---|---|---|---|
| `syncw4_49147.patch` | 1969 | `a87a6dd131d19637…` | **删** | 纯测试；`#49147` 生产面已对齐，等价回归 `chatgptapi/cloud_tasks_normalize_test.go` 已在 main ⇒ 队长裁定不收录（fwd=0） |

### P0-C' · 弃用标记（建议随 `syncw2.patch` 一并删）

| 文件 | bytes | 建议 | 理由 |
|---|---|---|---|
| `syncw2.patch.DEPRECATED.md` | 1323 | **删**（与 `syncw2.patch` 同批） | 自陈 `syncw2.patch` 作废；本版实测 `syncw2.patch` 反向 0（6/6 已落）⇒ 标记已被本账本取代 |
| `syncw2.patch` | 12115 | **删** | 6/6 已落（`#50200`/`#49145`/`#50454` 均在 main）⇒ 冗余 |
| `syncw2_49799.patch` | 21649 | **删** | 队长列为「不收录」；本版实测反向 0（4/4 已落 **sync625 `fa85639d`**）⇒ 冗余（与「不收录」同为禁重放） |

## P1 · 冲突（内容基本已落；残留 hunk 无独立价值 → 删）

| 文件 | bytes | sha256 | 建议 | 理由 |
|---|---|---|---|---|
| `integ86c.patch` | 63410 | `9491acde881d8ac7…` | **删** | 7/8 已落；仅 `config/cloud_config_policy_test.go` 残留（drift sync599/sync600） |
| `syncl6.patch` | 34241 | `f598f14e3af25050…` | **删** | 3/4 已落；同上 config 子集 |
| `syncl3.patch` | 51156 | `e780efba650263c4…` | **删** | 5/10 已落；4 个 `state/guardian*` + `parity` 残留（drift sync631/sync595） |
| `syncl3_49076.patch` | 5851 | `166dc8e9bbec890e…` | **删** | 2/4 已落；`utils/gitinfo*.go` 残留（drift sync622） |
| `syncl6_49345.patch` | 9996 | `16add9b4e1765ace…` | **删** | 1/2 已落；`model/catalog_test.go` 残留（drift sync634） |
| `syncw3_49584.patch` | 4444 | `3d12d831859e8874…` | **删** | 1/2 已落；测试已改名 `guardian_skill_catalog_like_rust_test.go`（drift sync627） |

## P2 · 已落地（冗余，禁重放） — 37 包

> 反向校验 0 ⇒ 补丁后态逐字存在于 main。全部**禁重放**；若无「离线审计留档」需求，建议一并删除。

| 文件 | bytes | sha256 | 建议 | 理由 |
|---|---|---|---|---|
| `syncl1.patch` | 10558 | `b201eef2bd967812…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl1_50531.patch` | 11941 | `d382c867dfcdc097…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl1b.patch` | 21778 | `54df1051c355f215…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl3_49951.patch` | 26615 | `3fa48129ac429edf…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl3_51627.patch` | 21803 | `5d7e58d7f378c293…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl3_gitinfo_gitfile.patch` | 10431 | `e8976249dad2e9aa…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl3_tui_ctrlspace.patch` | 8309 | `4b6a3d97a0c2be9c…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl3_tui_navkey_priority.patch` | 7632 | `de288da5142ca529…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl3b.patch` | 13675 | `07efa748c4776af9…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl4.patch` | 2388 | `fceac2cfcf7f3402…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl4_47898.patch` | 9297 | `5a32fa5acc448a0a…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl4_51650.patch` | 10587 | `5663461f204594b5…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl5.patch` | 7817 | `f84184a216aba85a…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl5_49785.patch` | 6122 | `46d35f32a22de637…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl5_49852.patch` | 4130 | `151875201ae5244f…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl5_50756.patch` | 20783 | `47ffc8f1abb3b27e…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl5_51185.patch` | 10198 | `c823766ffa943498…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl5b.patch` | 15494 | `e0fc06e8d1a3dee0…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl6_49147.patch` | 4163 | `0af7efb02893023a…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl6_49269_permguard.patch` | 2191 | `19911916d8a86ff5…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl6_49959.patch` | 2060 | `a15c02c5a91e1648…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncl6_gpt6_bundled.patch` | 15125 | `1c356f1e830b4829…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw1_26114.patch` | 5038 | `6007576660920990…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw1_49097.patch` | 7878 | `2483297a54cf6bb5…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw1_51652.patch` | 14705 | `b96b1a6f8a06f1cd…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw1_v1guidance.patch` | 3994 | `20fffe65a7111459…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw2.patch` | 12115 | `790021145acf54d0…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw2_49799.patch` | 21649 | `0f1806061a6b8f15…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw2_49912.patch` | 10492 | `1d72cbd983b1b39e…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw2_resumecwd.patch` | 12697 | `3add39145a520d63…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw4_49079.patch` | 4999 | `b8d6136e502c0c06…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw4_49308.patch` | 6022 | `40980adbb616df4d…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw4_49714.patch` | 26414 | `3c82883afffe5b63…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw4_49782.patch` | 8599 | `7243cb34065d36c6…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw4_50359.patch` | 14707 | `2183e286b9673abc…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw4_50437.patch` | 20491 | `ad2a6390ab61c479…` | 删（低优先） | 反向 0 ⇒ 已落地 |
| `syncw4_50727.patch` | 8380 | `2344606789f91008…` | 删（低优先） | 反向 0 ⇒ 已落地 |

## 不建议删除（保留）

- 本清单与 `r86_patch_ledger_2026_10_07.md`：审计凭证。
- 若要保留「本轮已落地补丁」的离线快照，建议只保留 **P0-A 中二者之一** 的代表性文件，其余按上表删除（磁盘 49 包 ≈ 700 KB）。

## 只读声明

- 仅只读 `git apply --check`（无 `--index`、无实写）+ `git grep/log/show/archive/cat-file`；树在 `/tmp`。
- **0 补丁 / 0 commit / 0 push / 0 ref 移动；未执行任何 `project_delete`。**

---
*生成：syncl5 · Linux 节点 · 2026-10-07*