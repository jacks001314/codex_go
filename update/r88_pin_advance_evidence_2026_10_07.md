# parity pin 推进证据（leader 首手，2026-10-07 · r88j）

针对未决项 ①「parity 快照是否推进」。以下全部为本机实测命令与输出，可复跑。

## 0. 环境
```
Rust 镜像  D:\qax\reagent\dev\git\codex      (origin = https://github.com/openai/codex.git)
Go  落主树 D:\qax\reagent\dev\codex_go_wt\integ86g   HEAD = c9fbd10f (sync659)
```
```
$ git -C D:\qax\reagent\dev\git\codex fetch origin --prune
   d83bb540ec..95ec468619  main -> origin/main
$ git -C … rev-parse origin/main   -> 95ec468619386ebb93506ac2091a48e5a558d25c
$ git -C … log --oneline -1 origin/main
95ec468619 Add a feature flag for Code Mode tool description ordering (#51690)
```
（此前该镜像的 `origin/main` ref 停在 `d83bb540ec`，本地缺 `1199b39762` / `95ec468619` 对象；本次 fetch 后已可核对。）

## 1. parity 关键文件 pin（9 条）在三个 rev 上的实测

方法：解析 Go 侧 `parity/rust_snapshot_test.go` 的 `{Path, SHA256}` 9 条，
对每条算 `git cat-file blob <rev>:codex-rs/<path>` 的 sha256 并比对。

| rev | 结果 | 漂移项 |
|---|---|---|
| `5b0b253035`（现 parity 检出 pin） | **9/9 MATCH，0 drift** | — |
| `d83bb540ec` | 8/9 MATCH | `core/tests/suite/mod.rs` pin=`62853612f780dde8…` actual=`4a94d1807b041100…` |
| `1199b39762`（syncl1 建议的 pin 目标） | 8/9 MATCH | 同上（逐字相同） |
| `95ec468619`（当前 Rust origin/main） | 8/9 MATCH | 同上（逐字相同） |

**结论 A**：parity 快照在自身 pin rev `5b0b253035` 上自洽；一旦前进到 `d83bb540ec` 及以后，
**需要重钉的关键文件恰好 1 条**（`core/tests/suite/mod.rs`），三个候选 rev 上完全一致。

> 更正：syncl1 的 triage 记为「2 条需重钉（`exec/src/lib.rs` + `core/tests/suite/mod.rs`）」。
> 实测 `exec/src/lib.rs` 在 `d83bb540ec` 仍为 pin 值 `8f53a22a562e913f…` ⇒ **MATCH，无需重钉**。

## 2. `#51690` 的 Go 侧缺口（pin 含它时的阻塞项）

```
$ git -C <rust> show --stat --format='%h %s' 95ec468619
95ec468619 Add a feature flag for Code Mode tool description ordering (#51690)
 codex-rs/core/config.schema.json | 6 ++++++
 codex-rs/features/src/lib.rs     | 8 ++++++++
 2 files changed, 14 insertions(+)

$ git -C <rust> show 95ec468619:codex-rs/features/src/lib.rs | Select-String 'code_mode_tool_description_first'
        key: "code_mode_tool_description_first",

$ # Rust features registry 的 key 计数（选择器：^\s+key: "）
   1199b39762 -> 167
   95ec468619 -> 168        ← 恰 +1

$ git -C <go> grep -c -F 'code_mode_tool_description_first' HEAD -- '*.go' '*.json'
   (无输出, exit=1)          ← Go 侧 0 命中
```

**结论 B**：若 parity pin 推进到 **含 `#51690`** 的 rev（`95ec468619` 及以后），
Rust FEATURES 由 **167 → 168**，而 Go 侧无该 key。
`parity/rust_features_registry_test.go` 解析 Rust `FeatureSpec` 并逐 key 比对 Go `features` 包
⇒ 该测试**必红**，除非先在 Go `features/` registry 补一行（S 级）。
若 pin 只推进到 `1199b39762`（不含 `#51690`），**无此项阻塞**。

`config.schema.json` 的新键位于 `properties.features.properties` 内，顶层键数不变 ⇒
`config_schema_surface_test.go` 不受影响（顶层仍 102）。

## 3. 推进 pin 的动作清单（供裁定）

| 目标 rev | 需重钉 pin | 需先补 Go features 行 | 备注 |
|---|---|---|---|
| `1199b39762` | 1（`core/tests/suite/mod.rs`） | 否 | 低风险 |
| `95ec468619`（当前 origin/main） | 1（同上） | **是**（`code_mode_tool_description_first`） | 否则 features 注册表测试红 |

另：HOLD 中的 `syncw3_parity_manifest_51678.patch`（623 B）的落主条件 = pin 推进到含 `#51678` 的 rev；
本次 fetch 后 `D:\qax\reagent\dev\git\codex` 已能验证该前向结论。

## 4. 纪律
仅 `git fetch`（更新镜像 `origin/main` ref）与只读命令；**0 补丁 / 0 commit / 0 push / 0 远端写入**；
未触碰 parity 检出 `C:\rw\codex-rs`。
