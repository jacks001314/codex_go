# r86 补丁账本审计（syncl5，2026-10-07）· **r87c 基线刷新版**

> 只读审计。未对任何补丁执行 `git apply` 实写、未 `project_delete`、未 commit/push/移动 ref。
>
> 本版基线：`e9849503`（sync638）→ **`8b2453a9`（sync641）**；覆盖 **44** 个 `.patch`。
> 计数对账：r87b 版 49 ⇒ r87c P0 清理删 7 `.patch`+1 `.md` ⇒ +2 新补丁（`syncl1_49642`/`syncw3_49855`）= **44**。
>
> 基线：**Go `origin/main = 8b2453a9ee619e7a84a95165b3e2474af082d71f`（= sync641 = `origin/integ86g`）。
> 抽检树：`git -c core.autocrlf=false -c core.eol=lf archive 8b2453a9 | tar -x` ⇒ 干净 LF 树；
> 逐包 `git apply --check --verbose`（前向）+ `git apply --check --reverse`（反向，**已落地强信号**）。

## 已落主区间（r87b → r87c）

| 提交 | sha | 内容 |
|---|---|---|
| sync639 | `1a465823` | #40665 队列派发的 turn 归类 `turn_trigger=queue` |
| sync640 | `ee2d4e1d` | #49642 遵守托管 `windows.allow_mxc` 要求 |
| sync641 | `8b2453a9` | #49855 提升权限的 Windows 终端以警告方式启动 embedded |

## 方法（可复跑）
```bash
TREE=$(mktemp -d /tmp/ledger_8b2453a9.XXXXXX)
git -C /home/jacks/jacks_dev/codex_go -c core.autocrlf=false -c core.eol=lf archive \
    8b2453a9ee619e7a84a95165b3e2474af082d71f | tar -x -C "$TREE"
cd "$TREE"
# 每个补丁：
git apply --check --verbose  "$P/<patch>"   # 0 ⇒ APPLIES
git apply --check --reverse  "$P/<patch>"   # 0 ⇒ ALREADY-LANDED（内容已在 main，强信号）
# 二者皆非 0 ⇒ CONFLICTS（逐文件反向复核 + git log -1 --format='%h %s' <base> -- <file> 找漂移）
```
判读：**前向 0** ⇒ `APPLIES`；**反向 0** ⇒ `ALREADY-LANDED`；**皆非 0** ⇒ `CONFLICTS`。
**处置枚举**：`已落地` / `已取代` / `APPLIES(禁重放)` / `无法判定`。

## 汇总（44 包）

| # | patch | bytes | sha256 | fwd | rev | 逐文件 landed | 处置 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 1 | `integ86c.patch` | 63410 | `9491acde881d8ac7…` | 1 | 1 | 7/8 | **已落地** | 7/8；残留 `config/cloud_config_policy_test.go`（已被 sync600 取代） |
| 2 | `syncl1.patch` | 10558 | `b201eef2bd967812…` | 1 | 0 | 5/5 | **已落地** |  |
| 3 | `syncl1_49642.patch` | 15559 | `cd2384265dbacb4f…` | 1 | 0 | 5/5 | **已落地** | **已落主 sync640 `ee2d4e1d`** |
| 4 | `syncl1_50531.patch` | 11941 | `d382c867dfcdc097…` | 1 | 0 | 2/2 | **已落地** |  |
| 5 | `syncl1b.patch` | 21778 | `54df1051c355f215…` | 1 | 0 | 5/5 | **已落地** |  |
| 6 | `syncl3.patch` | 51156 | `e780efba650263c4…` | 1 | 1 | 5/10 | **已落地** | 5/10；4×`state/guardian*`+`parity/*` 残留（drift sync631/sync595） |
| 7 | `syncl3_49076.patch` | 5851 | `166dc8e9bbec890e…` | 1 | 1 | 2/4 | **已落地** | 2/4；`utils/gitinfo*` 残留（drift sync622） |
| 8 | `syncl3_49951.patch` | 26615 | `3fa48129ac429edf…` | 1 | 0 | 4/4 | **已落地** |  |
| 9 | `syncl3_51627.patch` | 21803 | `5d7e58d7f378c293…` | 1 | 0 | 5/5 | **已落地** |  |
| 10 | `syncl3_gitinfo_gitfile.patch` | 10431 | `e8976249dad2e9aa…` | 1 | 0 | 2/2 | **已落地** |  |
| 11 | `syncl3_tui_ctrlspace.patch` | 8309 | `4b6a3d97a0c2be9c…` | 1 | 0 | 3/3 | **已落地** |  |
| 12 | `syncl3_tui_navkey_priority.patch` | 7632 | `de288da5142ca529…` | 1 | 0 | 2/2 | **已落地** |  |
| 13 | `syncl3b.patch` | 13675 | `07efa748c4776af9…` | 1 | 0 | 2/2 | **已落地** |  |
| 14 | `syncl4.patch` | 2388 | `fceac2cfcf7f3402…` | 1 | 0 | 1/1 | **已落地** |  |
| 15 | `syncl4_47898.patch` | 9297 | `5a32fa5acc448a0a…` | 1 | 0 | 2/2 | **已落地** |  |
| 16 | `syncl4_51650.patch` | 10587 | `5663461f204594b5…` | 1 | 0 | 2/2 | **已落地** |  |
| 17 | `syncl5.patch` | 7817 | `f84184a216aba85a…` | 1 | 0 | 3/3 | **已落地** |  |
| 18 | `syncl5_49785.patch` | 6122 | `46d35f32a22de637…` | 1 | 0 | 2/2 | **已落地** |  |
| 19 | `syncl5_49852.patch` | 4130 | `151875201ae5244f…` | 1 | 0 | 2/2 | **已落地** |  |
| 20 | `syncl5_50756.patch` | 20783 | `47ffc8f1abb3b27e…` | 1 | 0 | 5/5 | **已落地** |  |
| 21 | `syncl5_51185.patch` | 10198 | `c823766ffa943498…` | 1 | 0 | 3/3 | **已落地** |  |
| 22 | `syncl5b.patch` | 15494 | `e0fc06e8d1a3dee0…` | 1 | 0 | 2/2 | **已落地** |  |
| 23 | `syncl6.patch` | 34241 | `f598f14e3af25050…` | 1 | 1 | 3/4 | **已落地** | 3/4；= `integ86c` 的 config 子集 |
| 24 | `syncl6_49147.patch` | 4163 | `0af7efb02893023a…` | 1 | 0 | 1/1 | **已落地** |  |
| 25 | `syncl6_49269_permguard.patch` | 2191 | `19911916d8a86ff5…` | 1 | 0 | 0/0 | **已落地** | 纯上下文片段（无 `diff --git` 头）；rev=0 ⇒ 已落地 |
| 26 | `syncl6_49345.patch` | 9996 | `16add9b4e1765ace…` | 1 | 1 | 1/2 | **已落地** | 1/2；`model/catalog_test.go` 残留（drift sync634） |
| 27 | `syncl6_49959.patch` | 2060 | `a15c02c5a91e1648…` | 1 | 0 | 1/1 | **已落地** |  |
| 28 | `syncl6_gpt6_bundled.patch` | 15125 | `1c356f1e830b4829…` | 1 | 0 | 3/3 | **已落地** |  |
| 29 | `syncw1_26114.patch` | 5038 | `6007576660920990…` | 1 | 0 | 2/2 | **已落地** |  |
| 30 | `syncw1_49097.patch` | 7878 | `2483297a54cf6bb5…` | 1 | 0 | 2/2 | **已落地** |  |
| 31 | `syncw1_51652.patch` | 14705 | `b96b1a6f8a06f1cd…` | 1 | 0 | 4/4 | **已落地** |  |
| 32 | `syncw1_v1guidance.patch` | 3994 | `20fffe65a7111459…` | 1 | 0 | 2/2 | **已落地** |  |
| 33 | `syncw2.patch` | 12115 | `790021145acf54d0…` | 1 | 0 | 6/6 | **已落地** |  |
| 34 | `syncw2_49912.patch` | 10492 | `1d72cbd983b1b39e…` | 1 | 0 | 2/2 | **已落地** |  |
| 35 | `syncw2_resumecwd.patch` | 12697 | `3add39145a520d63…` | 1 | 0 | 2/2 | **已落地** |  |
| 36 | `syncw3_49584.patch` | 4444 | `3d12d831859e8874…` | 1 | 1 | 1/2 | **已落地** | 1/2；测试已改名 `guardian_skill_catalog_like_rust_test.go`（drift sync627） |
| 37 | `syncw3_49855.patch` | 9330 | `a5abcaed3c442514…` | 1 | 1 | 2/3 | **已落地** | 2/3；`app/daemon_startup_test.go` BOM 例外 ⇒ **已落主 sync641**（见 §例外登记） |
| 38 | `syncw4_49079.patch` | 4999 | `b8d6136e502c0c06…` | 1 | 0 | 3/3 | **已落地** |  |
| 39 | `syncw4_49308.patch` | 6022 | `40980adbb616df4d…` | 1 | 0 | 2/2 | **已落地** |  |
| 40 | `syncw4_49714.patch` | 26414 | `3c82883afffe5b63…` | 1 | 0 | 5/5 | **已落地** |  |
| 41 | `syncw4_49782.patch` | 8599 | `7243cb34065d36c6…` | 1 | 0 | 6/6 | **已落地** |  |
| 42 | `syncw4_50359.patch` | 14707 | `2183e286b9673abc…` | 1 | 0 | 3/3 | **已落地** |  |
| 43 | `syncw4_50437.patch` | 20491 | `ad2a6390ab61c479…` | 1 | 0 | 11/11 | **已落地** |  |
| 44 | `syncw4_50727.patch` | 8380 | `2344606789f91008…` | 1 | 0 | 6/6 | **已落地** |  |

**统计（处置列）**：`已落地` **44**（= ALREADY-LANDED(rev=0) 37 包 + 「内容已落、残留 hunk 漂移」的 7 个冲突包） / `已取代` 0 / `APPLIES(禁重放)` **0** / `无法判定` **0**。
**仍 APPLIES（前向 exit 0、reverse 非 0）的包 = 0** ⇒ **无「待裁定是否落主」项**。
（r87b 时存在的 2 个 APPLIES 包 `syncw4_49147`/`syncw4_51652` 已在 r87c P0 清理中移除。）

## 冲突 / 漂移明细（7 包，fwd=1 / rev=1）

> 判据：逐文件反向复核 + `git log -1 --format='%h %s' 8b2453a9 -- <file>` 找漂移来源。

### `integ86c.patch`

| 文件 | 反向 | 漂移来源（最后改它的提交） |
|---|---|---|
| `config/cloud_config.go` | LANDED | c84a80ab sync599(#49269) |
| `config/cloud_config_policy_test.go` | **CONFLICT** | d5dbe694 sync600(#49269 Windows guard) |
| `config/cloud_config_storage.go` | LANDED | c84a80ab sync599 |
| `config/config.go` | LANDED | 8d1177ee sync616(#49260) |
| `model/responses_agent.go` | LANDED | 658dc9c1 sync629(#49714) |
| `model/responses_agent_test.go` | LANDED | f6aecaf6 sync597(#49675) |
| `prompt/skills_render.go` | LANDED | 39b953b4 sync596(#49127) |
| `prompt/skills_render_test.go` | LANDED | 39b953b4 sync596 |

### `syncl6.patch`

| 文件 | 反向 | 漂移来源（最后改它的提交） |
|---|---|---|
| `config/cloud_config.go` | LANDED | c84a80ab sync599 |
| `config/cloud_config_policy_test.go` | **CONFLICT** | d5dbe694 sync600 |
| `config/cloud_config_storage.go` | LANDED | c84a80ab sync599 |
| `config/config.go` | LANDED | 8d1177ee sync616 |

### `syncl3.patch`

| 文件 | 反向 | 漂移来源（最后改它的提交） |
|---|---|---|
| `appserver/retained_context_test.go` | **CONFLICT** | ab4b9f3e sync631(#51627) |
| `appserver/turn_runtime.go` | LANDED | af6815b6 sync627(#49584) |
| `context/fragments_test.go` | LANDED | 2e898a12 syncl3(#48611) |
| `context/persistent_mode.go` | LANDED | 2e898a12 syncl3(#48611) |
| `features/persistent_mode.go` | LANDED | 2e898a12 syncl3(#48611) |
| `features/persistent_mode_test.go` | LANDED | 2e898a12 syncl3(#48611) |
| `parity/rust_snapshot_test.go` | **CONFLICT** | 39dd8e94 sync595(#51595) |
| `state/guardian.go` | **CONFLICT** | ab4b9f3e sync631(#51627) |
| `state/guardian_retained_context.go` | **CONFLICT** | ab4b9f3e sync631(#51627) |
| `state/guardian_retained_context_test.go` | **CONFLICT** | ab4b9f3e sync631(#51627) |

### `syncl3_49076.patch`

| 文件 | 反向 | 漂移来源（最后改它的提交） |
|---|---|---|
| `appserver/skill_invocation_repo_like_rust_test.go` | LANDED | 944ac1f8 sync608(#49076) |
| `appserver/turn_runtime.go` | LANDED | af6815b6 sync627 |
| `utils/gitinfo.go` | **CONFLICT** | cd102426 sync622(.git gitfile) |
| `utils/gitinfo_test.go` | **CONFLICT** | cd102426 sync622 |

### `syncl6_49345.patch`

| 文件 | 反向 | 漂移来源（最后改它的提交） |
|---|---|---|
| `model/catalog.go` | LANDED | c99ac975 sync634(GPT-6) |
| `model/catalog_test.go` | **CONFLICT** | c99ac975 sync634 |

### `syncw3_49584.patch`

| 文件 | 反向 | 漂移来源（最后改它的提交） |
|---|---|---|
| `appserver/guardian_skills_context_test.go` | APPLIES(文件不存在) | 测试已改名 |
| `appserver/turn_runtime.go` | LANDED | af6815b6 sync627(#49584) |

### `syncw3_49855.patch`

| 文件 | 反向 | 漂移来源（最后改它的提交） |
|---|---|---|
| `app/daemon_startup.go` | LANDED | 8b2453a9 sync641(#49855) |
| `app/daemon_startup_test.go` | **CONFLICT(BOM 例外)** | 8b2453a9 sync641 ⇒ 见 §例外登记 |
| `app/interactive.go` | LANDED | 8b2453a9 sync641 |

**残留 hunk 原文（前向 `--verbose`）**：
```
integ86c/syncl6 : error: patch failed: config/cloud_config_policy_test.go:1
                  error: config/cloud_config_policy_test.go: already exists in working directory
syncl3          : error: patch failed: state/guardian.go; parity/rust_snapshot_test.go: patch does not apply
syncl3_49076    : error: patch failed: utils/gitinfo.go:38; utils/gitinfo.go: patch does not apply
syncl6_49345    : error: patch failed: model/catalog_test.go:1372
syncw3_49584    : error: patch failed: appserver/turn_runtime.go:9155
syncw3_49855    : error: patch failed: app/daemon_startup_test.go:403  （= BOM 例外，预期）
```
⇒ 7 包**内容均已在 main**，残留 hunk 只是后续提交改写上下文所致；**全部禁重放**。

## 例外登记：`syncw3_49855.patch` 的 BOM 有意偏差（已落主 sync641）

该补丁给 `app/daemon_startup_test.go` 带了 **UTF-8 BOM**；队长落主前**去掉前 3 字节**（`gofmt -l` 转绿）。
因此该文件的 `--check --reverse` **必然失败**，属**预期**，**不判「未落地」**：
- 落主后 `app/daemon_startup_test.go`：LF 大小 **22995 B**，sha256 `265bea7f0ceb07da5a65b051f3476b8b1407374bbb62cd2232a75028a1c3dded`（无 BOM，首字节 `package `）。
- 同包另两文件 `app/daemon_startup.go` / `app/interactive.go` 反向 **LANDED**。
- 结论：`syncw3_49855.patch` = **已落地（sync641 `8b2453a9`）**，含 1 处已登记的有意偏差。

## r87c P0 清理记录（本目录已移除的 8 项）

> 由队长 r87c 授权、syncl5 执行（`project_delete`，逐文件）。**本单不再删任何 `.patch`**；P1/P2 原地保留。

| 文件 | 原处置 | 说明 |
|---|---|---|
| `syncw4_51652.patch` | **已取代 / 禁重放** | **已被 sync638 (`e9849503`, #51652) 取代**；竞争实现，重放会**二次接线 `codex.agents_md.edit`** |
| `syncw4_49147.patch` | APPLIES(禁重放) | 纯测试；`#49147` 生产面已对齐，裁定不收录 |
| `syncw1_26114v1.patch` | 已落地(冗余) | 与 `syncw1_v1guidance.patch` 逐字节相同（保留后者） |
| `syncl1_49912.patch` | 已取代 | 生产已由 sync611 `985ccf59` 经 `syncw2_49912.patch` 落地 |
| `syncw1_49076.patch` | 已取代 | 已由 sync608 `944ac1f8` 覆盖 |
| `syncl5_session_index.patch` | 已取代 | 已拆入 sync618 `8ade48ed` + sync620 `4ee41b7a` |
| `syncw2_49799.patch` | 已落地(冗余) | 反向 0（4/4 已落 sync625 `fa85639d`） |
| `syncw2.patch.DEPRECATED.md` | 弃用标记 | 非 `.patch`；自陈 `syncw2.patch` 作废 |

## 全部 sha256（44 包，供对账）

```
9491acde881d8ac7603087dc03daaf6ea12a199eda2f71205184edbf2f9de251  integ86c.patch
b201eef2bd9678124f31a5bf2e541879650134437ad32b8f349696da85097609  syncl1.patch
cd2384265dbacb4f73d8e7822d25d69370bd352cf930a3de287ea4fa1f37a022  syncl1_49642.patch
d382c867dfcdc097d848a8f2d663816347b20d52379cfe9d8ff580bb417f89ec  syncl1_50531.patch
54df1051c355f215b38dbdc9ffa3a04766873ae3412b4d2461ed6dff28e4196e  syncl1b.patch
e780efba650263c4e887b9769c527214fdf7f3b673f62680f23def26f822f2fd  syncl3.patch
166dc8e9bbec890eda7c24faed9a115371e6e44d9fb92e6baa8c44b93f3356e6  syncl3_49076.patch
3fa48129ac429edf065668727fd9959738747cd3b8dcbcda286680f046f82188  syncl3_49951.patch
5d7e58d7f378c29311c928e5bc2c55076668761e2a751378541105c408370539  syncl3_51627.patch
e8976249dad2e9aa607828cce16a07695a75d5fcc939b9a86d5b45173fe81db8  syncl3_gitinfo_gitfile.patch
4b6a3d97a0c2be9c973f0078f2497cced9026fed45805fa62377cda6db89dc19  syncl3_tui_ctrlspace.patch
de288da5142ca5294d63f6a6ed88731647513c539a37853632c33e271ad95cc1  syncl3_tui_navkey_priority.patch
07efa748c4776af909090552853045833da16af4079e14fd6db3cd3947c48343  syncl3b.patch
fceac2cfcf7f340236307a782537ab8de9f231249b37bb19c881a29d270cb1f1  syncl4.patch
5a32fa5acc448a0a6ed2baefea620f95ebef4a0705076a5aeabfd32b6dfa6d39  syncl4_47898.patch
5663461f204594b5608572d4a56151d873be899f118d3b71b88b7444969aceda  syncl4_51650.patch
f84184a216aba85a583a9a35b6fff056feaa0d0ea15239fb4749e0eba043f611  syncl5.patch
46d35f32a22de6371cd5e4cfe74cf9a33bc5c344eba186c9739fad04d21708e0  syncl5_49785.patch
151875201ae5244f39671c0634321156ec80dd5c16848a9f5d4a0f741dfa1618  syncl5_49852.patch
47ffc8f1abb3b27e07c551f12e5a0dcaa2b4e3780624d50af45cfb3aa8f8bb32  syncl5_50756.patch
c823766ffa9434986a77a9193327fbce4515ece9a219c208ce2108232b4f8f92  syncl5_51185.patch
e0fc06e8d1a3dee0f913abad5393a40ecac18811a296cf77138b94b50e4ef7a4  syncl5b.patch
f598f14e3af25050b1028db734ced1fe43bf1f5a82be378138a3e9690c18849d  syncl6.patch
0af7efb02893023acd028cb5fc2f23c8c0857cedca28e4ad31206f7d42f74197  syncl6_49147.patch
19911916d8a86ff57a6a9b3c8800ada0c8240758e1096e5f112ec116c4a4437c  syncl6_49269_permguard.patch
16add9b4e1765ace796b46c4bf21b5910bbd5ceb067afca0df2ad3fbf7fad31a  syncl6_49345.patch
a15c02c5a91e16481f83bb17f582f2bb407607fe55889695feaaeda528a98f69  syncl6_49959.patch
1c356f1e830b48290fc8b4f2eea3bc3f03646fa7a40b388c6d1b96e93c6ed075  syncl6_gpt6_bundled.patch
600757666092099022a7b95b2d03c02782a592170b7510d87811ce1e967f0a4e  syncw1_26114.patch
2483297a54cf6bb5ec9ac20d15565e187f742b03dc55398d101d5badc00638c6  syncw1_49097.patch
b96b1a6f8a06f1cd560a570e06e20e9cbe8e29338fe1cce4cfa8a788ea822d72  syncw1_51652.patch
20fffe65a7111459f4c458fffababbe1f21bcc89be986534b4d56283ddebb36b  syncw1_v1guidance.patch
790021145acf54d0c9b5f22a91f47245da89a0e9671bc844cd2df1d34de0f868  syncw2.patch
1d72cbd983b1b39e1a614b9ea951f45b62db4e8cdf3668ad56f250cd2d62347e  syncw2_49912.patch
3add39145a520d63e041b80508254693a6e1440c73b6519b61a900e7cca85249  syncw2_resumecwd.patch
3d12d831859e88749339c28038146bb9ce17b09a4388592e2c758ca6f383e4fa  syncw3_49584.patch
a5abcaed3c44251492214a5624cf4009119256f5662fa260b057bbb30309cf91  syncw3_49855.patch
b8d6136e502c0c06de8e147bc3eeb9f5d0e25ae6fd41fc8595f0ed56b3b3e8b5  syncw4_49079.patch
40980adbb616df4df6e1c848ffbabe94943016def935c91e2a7456646e49b619  syncw4_49308.patch
3c82883afffe5b6373cd63b0387c1f2148b675d7e5b4e9279ccc4f96ccdf6882  syncw4_49714.patch
7243cb34065d36c61e2dc6b8f0960c89068fd12ba666801f56acf8437e5dc909  syncw4_49782.patch
2183e286b9673abc9ccea380b6683168f627a65b4340689deb1b126ed9ba150d  syncw4_50359.patch
ad2a6390ab61c4795032a14dbe5b7c26ec68900193265ef03f414f06ebbf76c6  syncw4_50437.patch
2344606789f91008c113c2aa7490534ddaf7e17e26fb5f8bc50db7782836328b  syncw4_50727.patch
```

## 只读声明

- 仅 `git -C /home/jacks/jacks_dev/codex_go` 只读 `fetch/archive/log/grep/show/cat-file` + `git apply --check`（**只读校验，无 `--index`/无实写**）；抽检树在 `/tmp`。
- **0 补丁 / 0 commit / 0 push / 0 ref 移动**；本单**未 `project_delete` 任何 `.patch`**（P0 已于 r87c 前序单完成）。
- 本报告 + `r87c_patch_ledger_incr_2026_10_07.md` 是仅有的两项产物。

---
*生成：syncl5 · Linux 节点 · 2026-10-07 · 基线 Go `8b2453a9`（sync641）*