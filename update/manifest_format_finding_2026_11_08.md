# `parity/contracts/manifest.json` 排版复检（任务 C 结论）

日期：2026-11-08 · 判定对象：你把 manifest 用 `json.dumps(indent=2, ensure_ascii=False)` 重写后的 **73,948 B** 版本（sha256 `8b8cc3635b3be9b1558e747891972483ac1be00b796bfaa8d7a20137b474dce2`）。

## 0. 结论

1. **原排版惯例 = 1 空格缩进 + CRLF + 无尾换行 + `ensure_ascii=False`**，数组**多行展开**（不是单行内联）。
2. **你这次的改写制造了无意义 diff**：行数没变（2,148），但 **2,146 行内容都变了**（只有首行 `{` 和末行 `}` 相同），字节 +6,778。
3. 修复脚本：`update/reflow_contract_manifest_2026_11_08.py`（`--check` / `--write`）。在你当前文件上跑 `--write` 得到 **67,170 B / sha256 `8009982728c0aaf9fdd0d02991c47ded5b51a3639ec0ab9be847565331fb2d47`**，语义与顺序完全不变。

## 1. 怎么确认「原惯例」而不靠猜

拿你上一轮给我的**重写前**副本（`manifest-r9.json`，137 契约）做**逐字节复现**：

```python
raw = open('manifest-r9.json','rb').read()
doc = json.loads(raw)
cand = json.dumps(doc, indent=1, ensure_ascii=False).replace('\n','\r\n').encode()
assert cand == raw          # True
```

- 原始字节：66,535 B，sha256 `ca0a1d878731e6ff…`
- 复现字节：66,535 B，**逐字节相等**
- 关键特征（`cat -A` / `repr` 可见）：`{\r\n "schemaVersion": 1,\r\n "targetRustCommit": ...`，即 **1 空格缩进 + CRLF**，末尾**没有**换行符
- 数组是展开的：`"rustPaths": [\r\n    "features/src/lib.rs"\r\n   ]`（内层 4 空格 = 1 + 3 层）

`indent=2` 在同一份数据上是 71,141 B，`indent=4` 是 84,619 B —— 都与你历史文件的字节数对不上，只有 `indent=1` + CRLF 对得上。

## 2. 这次改写到底改了多少

| 指标 | 你的重写前（137 契约） | 你的重写后（138 契约） | 修复后（同 138 契约） |
| --- | --- | --- | --- |
| 字节 | 67,054（你给的数字） | **73,948** | **67,170** |
| 行数 | — | 2,148 | 2,148 |
| 缩进 | 1 空格 | **2 空格** | 1 空格 |
| 行尾 | CRLF | CRLF | CRLF |
| sha256 | — | `8b8cc363…b474dce2` | `8009982728c0aaf9…331fb2d47` |

行数完全一致（2,148 = 2,148），说明**没有删改内容**，纯粹是缩进被放宽；但 diff 上 **2,146/2,148 行都变**（除 `{`、`}`），等于整文件重写一次。你的 +6,778 字节与「每行多 1 个前导空格」吻合（2,148 行里带缩进的行数 × 1 ≈ 6.7k）。

这不是洁癖问题：之后任何一次「改两条契约状态」的三行 diff，都会被这次重写掩盖；`git blame` / review 也失去意义。

## 3. 修复脚本

```bash
# 只检查（不写盘，非规范时 exit 1，并打印字节/行数增量）
python3 update/reflow_contract_manifest_2026_11_08.py --check parity/contracts/manifest.json

# 重排并写回（先写 .bak，写完回读校验 JSON 语义不变）
python3 update/reflow_contract_manifest_2026_11_08.py --write parity/contracts/manifest.json
```

在你当前文件上的实跑：

```
NOT canonical
  current :  73948 bytes, 2148 lines, CRLF, sha256 8b8cc3635b3be9b1558e747891972483ac1be00b796bfaa8d7a20137b474dce2
  canonical:  67170 bytes, 2148 lines, CRLF, sha256 8009982728c0aaf9fdd0d02991c47ded5b51a3639ec0ab9be847565331fb2d47
  delta   : -6778 bytes, +0 lines
```

对**重写前**副本跑 `--check` 则输出 `canonical: 66535 bytes, sha256 ca0a1d87…` 并 exit 0 —— 说明脚本的「规范形态」判定与你的历史文件一致，不是我为这次改动发明的格式。

## 4. 语义变更清单（重排之后应该只剩这些）

我把你现在的 138 契约与我手上的 137 契约副本做了 JSON 级比对，**语义差异只有 6 处**，重排后 diff 应该正好是这些：

| 类型 | 契约 | 变化 |
| --- | --- | --- |
| 新增 | `ui-tree-terminal-escape-stripping` | 新契约，complete / shared-fixtures / P1 / goPaths `['tui']` / verifier `TestUITreeTextSanitizerStripsControlStrings` |
| 状态 | `multi-agent-hosted-tools-guidance` | partial → complete（verifier 同时变） |
| 状态 | `thread-model-metadata-exposure` | partial → complete（verifier 同时变） |
| 状态 | `protected-model-profile-downgrade` | partial → complete（verifier 同时变） |
| 状态 | `skills-tool-name-resolution` | partial → complete（verifier 同时变） |
| 仅 verifier | `runtime-auth-websocket-revalidation` | verifier 列表变化（状态仍 complete） |

（也就是说：你提到的「本轮更新两条」之外，`multi-agent-hosted-tools-guidance` 与 `thread-model-metadata-exposure` 也在我上一份副本之后转成了 complete —— 137→138 的 partial 数 25→21 正好对上。）

## 5. 一个建议（可选）

如果以后还会用脚本改 manifest，建议把重排做进脚本尾部（一行）：

```python
open(path, 'wb').write(json.dumps(doc, indent=1, ensure_ascii=False).replace('\n', '\r\n').encode())
```

这样任何 `json.dump` 都不会再把文件写宽。
