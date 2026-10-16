# 第 90 轮独立验证 · mc-r90-verify · batch V1

- 验证者：`mc-r90-verify`（独立验证 Agent，agent-3aeedb70f34908191798ec63，node=19ff0e1d456d25a010ee9b62127663ae）
- 被验对象：Go 仓库 `D:\qax\reagent\dev\codex_go` commit **661abcbe**（`sync666: support line breaks in Mermaid flowchart node labels (#54419)`）
- 上游对照：`D:\qax\reagent\dev\git\codex` HEAD **4eb4a65407**；本项上游提交 **9fcb1f5ed49c938b6fa5a9b4c8f897f3ddfd734c**（#54419，2026-10-15T21:18:49Z，author Eric Traut）
- 判定台：`D:\qax\reagent\dev\codex_go_wt\v90ver`（detached @661abcbe，新建时干净）
- 结论：**verified（4/4 子项全部通过，含 6 个新构造反例与真实 Rust 执行对照）**

## 1 判定汇总

| # | 子项 | 判定 | 证据 |
| --- | --- | --- | --- |
| V1.1 | 门禁复跑（mermaid + tui/markdown + tui/streaming + build） | verified | 见 §2 |
| V1.2 | 反向对照 RC（撤改动 ⇒ FAIL，恢复 ⇒ PASS；另做 draw.go 单撤） | verified | 见 §3 |
| V1.3 | 上游快照逐行一致性（四方向 stadium 快照，109 行） | verified | 见 §4 |
| V1.4 | 与上游 Rust **真实执行** 的 18 例双向对照（含 6 个新反例） | verified（同构，逐字节相同） | 见 §5 |

## 2 门禁复跑（干净判定台 @661abcbe）

```
$ go test ./mermaid/ -count=1
ok  	codex_go/mermaid	0.030s                                   exit=0

$ go test ./tui/markdown/ ./tui/streaming/ -count=1
ok  	codex_go/tui/markdown	0.377s
ok  	codex_go/tui/streaming	0.099s                          exit=0

$ go build ./...
(无输出)                                                        exit=0  elapsedSec=35.2
```
无新增失败；本项未触发任何既有基线失败包（appserver/tool/execserver/config/model/applypatch/mcp/tui 等本轮未跑，因其与 mermaid 改动无交集）。

## 3 反向对照 RC（我独立重做，不引用他人结论）

### RC-1：撤 `mermaid/parse.go` + `mermaid/draw.go` 全部 hunk
```
$ git -C <bench> checkout 661abcbe^ -- mermaid/parse.go mermaid/draw.go
M  mermaid/draw.go
M  mermaid/parse.go

$ go test ./mermaid/ -count=1
--- FAIL: TestStadiumsWithOtherShapesInEveryDirectionMatchesRust (0.00s)
    mermaid_test.go:823: TD: Render error = unsupported Mermaid syntax or label
--- FAIL: TestFlowchartLineBreakLabelsMatchRust (0.00s)
    mermaid_test.go:866: Render("flowchart TD; A[one<br>two]") error = unsupported Mermaid syntax or label
FAIL
FAIL	codex_go/mermaid	0.035s
exit=1
```

### RC-1 恢复
```
$ git -C <bench> checkout 661abcbe -- mermaid/parse.go mermaid/draw.go
$ git -C <bench> status --porcelain      → 0 行

$ go test ./mermaid/ -count=1
ok  	codex_go/mermaid	0.035s                                   exit=0
```

### RC-2（更强）：只撤 `draw.go`
```
$ git -C <bench> checkout 661abcbe^ -- mermaid/draw.go
$ go test ./mermaid/ -count=1
--- FAIL: TestStadiumsWithOtherShapesInEveryDirectionMatchesRust (0.00s)
    mermaid_test.go:823: TD: Render error = unsupported Mermaid syntax or label
--- FAIL: TestFlowchartLineBreakLabelsMatchRust (0.00s)
    mermaid_test.go:866: Render("flowchart TD; A[one<br>two]") error = unsupported Mermaid syntax or label
FAIL	codex_go/mermaid	0.034s              exit=1
$ git -C <bench> checkout 661abcbe -- mermaid/draw.go   → 工作区恢复 0 行
```
结论：两个文件的 hunk 都是行为必需（缺任一侧，两个目标测试即失败），不是「只加测试」的空转改动。

## 4 上游快照逐行一致性（109 行）

- Rust 快照：`codex-rs/mermaid/src/snapshots/codex_mermaid__tests__stadiums_with_other_shapes_in_every_direction.snap`（去掉 5 行 insta 头后 109 行）
- Go 夹具：`mermaid/mermaid_test.go` 中 `TestStadiumsWithOtherShapesInEveryDirectionMatchesRust` 的 `const expected` 字面量（同一 109 行）
- 我按行逐字节比较（区分大小写，含 CJK 与制表符空格）：
  - `goBodyLines=109 snapBodyLines=109`
  - `identical_109_lines=True`（首行 `TD` … 末行 `└┄…┘` 全等）
- 且上游快照本提交的实际变更（每方向新增 `│ Ready   │` / `│ next    │` / `│ Retry?  │` 三行、`assertion_line: 35→36`）与 Go 夹具新增行一一对应（见 `git show 9fcb1f5ed4 -- <snap>`）。
- Go 侧 `TestStadiums...` 同时断言 `render(source, width) == output` 与 `render(source, width-1) == ErrTooWide`，与 Rust 测试体（tests.rs:31-33）同构。

## 5 与上游 Rust 真实执行的 18 例双向对照（核心证据）

方法：把上游 mermaid crate 源码（仅依赖 `unicode-width`，无其它外部依赖）以 `#[path]` 直接挂进一个一次性 probe crate，调用其 `pub fn render(source, max_width)` 真实执行；Go 侧在判定台内加一次性 probe 测试调用 `Render`。两侧对同一组 (source, width) 输出同一格式文本，再逐字节比较。

- Rust 探针：`D:\tmp\r90ver_rustprobe`（`#[path="D:/qax/reagent/dev/git/codex/codex-rs/mermaid/src/lib.rs"]`，只读引用上游源码，未修改上游仓库）
- 结果：`rustBytes=3901 goBytes=3901`，`sha256_rust = sha256_go = FDE2ED96C7F6E2B428681DA5FE2C26F874A8B6E2330159B5439DAA356CFE4A97` ⇒ **18/18 用例输出逐字节相同**

### 5.1 本轮新构造的反例（Go 与 Rust 同结论）

| # | 反例输入 | 观测（Go 与 Rust 完全一致） |
| --- | --- | --- |
| 1 | `flowchart TD; A["one<br>"]`（`<br>` 在**末行之后**） | OK：盒子含 2 行，第 2 行为空行（`one` + 空行）——未被静默裁剪 |
| 2 | `flowchart TD; A["one<br><br>two"]`（**连续两个** `<br>`） | OK：3 行，中间为空行 |
| 3 | `flowchart TD; A["<BR>one"]`（**大小写变体**） | ERR：`unsupported Mermaid syntax or label`（markup 探测对 `is_ascii_alphabetic` 大小写均命中） |
| 4 | `flowchart TD; A["<br  />one"]`（**两个空格**） | ERR：同上（只识别三种精确拼写 `<br>`/`<br/>`/`<br />`） |
| 5 | `flowchart TD; A["a<br >b"]`（`<br >`） | ERR：同上 |
| 6 | `flowchart TD; A["<br>"]`（**只含 break**） | ERR：同上（`label.trim().is_empty()` / `strings.TrimSpace == ""` 路径） |
| 7 | `flowchart TD; A["界×21"]`（42 显示列 > MAX_LABEL=40） | ERR：`diagram exceeds prototype limits` |
| 8 | `flowchart TD; A["界×20"]`（恰 40 列，**限长边界**） | OK（未被 ErrLimit 误伤） |
| 9 | `flowchart TD; A["x×40<br/>y×40"]`（两行**各自**恰在 40 列边界） | OK（限长按「每渲染行」判定，与 #54419 声明一致） |
| 10 | `flowchart TD; A["界×20<br/>x×40"] --> B[Done]`（`graph LR` + 宽字符 + 多行） | OK，且 width/width-1 行为与上游快照测试同构 |
| 11 | `flowchart TD; A["one<br/>two<br/>"]`（**尾随 break**） | OK：3 行（第 3 行空行） |
| 12 | `flowchart TD; A["one<br/>two"] & B["three<br>four"]`（**同一声明里多节点多行 + `&` 展开**） | OK：两节点各自 2 行，箱体高度与连接桩位置一致 |
| 13 | `flowchart TD; A{Done?<br/>Retry?}`（◇ 决策符） | OK：`◇` **只在首行**，第二行 `Retry?` 无重复 ◇ |
| 14 | `flowchart TD; A([\"Req<br/>Ready\"])`（stadium 形状） | OK：2 行 |
| 15 | `flowchart TD; A[one<br/>two]`（**非引号**标签） | OK：与引号写法输出一致 |
| 16 | `flowchart TD; A -->|one<br/>two| B` / `A -- one<br>two --> B` | ERR：`unsupported Mermaid syntax or label`（边标签不接受 break，判据先于展开） |
| 17 | `flowchart TD; A[<br/><br />]` | ERR：同上 |
| 18 | `flowchart TD; A --> B`，width=5 | ERR：`diagram exceeds available display width` |

**差异结论：同构（isomorphic），未发现任何 Go/Rust 行为差异。** 具体地：
- 三个 break 拼写的展开顺序（`<br>` → `<br/>` → `<br />`）在两侧等价（三者互不为子串，替换顺序无关）；
- 「每行限长 40 + 空行跳过校验 + 全空白判 Unsupported」的判定顺序一致（上游 parse.rs:142-151 ↔ Go parse.go:221-241）；
- 决策符插入首行（上游 draw.rs:23-27 `lines[0].insert_str(0, "◇ ")` ↔ Go draw.go:12-18 `lines[0] = "◇ " + lines[0]`）一致；
- members 分隔线宽度 `"─".repeat(node.label.width())` / `strings.Repeat("─", textWidth(label))`：`unicode-width` 对 `\n` 记 0、`uniseg` 对 `\n` 记 0，两侧一致（本例不可达：flowchart 节点无 members）。

## 6 附录：18 例双向对照原始输出（Go@661abcbe 与 Rust@4eb4a65407 逐字节相同，sha256=FDE2ED96...4A97）

```text
### case 1 width=120
SRC:flowchart TD; A["one<br>"]
OK lines=4
┌─────┐
│ one │
│     │
└─────┘
### case 2 width=120
SRC:flowchart TD; A["one<br><br>two"]
OK lines=5
┌─────┐
│ one │
│     │
│ two │
└─────┘
### case 3 width=120
SRC:flowchart TD; A["<BR>one"]
ERR:unsupported Mermaid syntax or label
### case 4 width=120
SRC:flowchart TD; A["<br  />one"]
ERR:unsupported Mermaid syntax or label
### case 5 width=120
SRC:flowchart TD; A["a<br >b"]
ERR:unsupported Mermaid syntax or label
### case 6 width=120
SRC:flowchart TD; A["<br>"]
ERR:unsupported Mermaid syntax or label
### case 7 width=120
SRC:flowchart TD; A["界界界界界界界界界界界界界界界界界界界界"]
OK lines=3
┌──────────────────────────────────────────┐
│ 界界界界界界界界界界界界界界界界界界界界 │
└──────────────────────────────────────────┘
### case 8 width=120
SRC:flowchart TD; A["界界界界界界界界界界界界界界界界界界界界界"]
ERR:diagram exceeds prototype limits
### case 9 width=120
SRC:flowchart TD; A["xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx<br/>yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"]
OK lines=4
┌──────────────────────────────────────────┐
│ xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx │
│ yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy │
└──────────────────────────────────────────┘
### case 10 width=100
SRC:graph LR; A["界界界界界界界界界界界界界界界界界界界界<br/>xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"] --> B[Done]
OK lines=9
┌──────────────────────────────────────────┐  ┌──────┐
│ 界界界界界界界界界界界界界界界界界界界界 │  │ Done │
│ xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx │  │      │
└┬─────────────────────────────────────────┘  └┬─────┘
 │                                             ▲
 │                                             │
 │                                             │
 │                                             │
 └─────────────────────────────────────────────┘
### case 11 width=120
SRC:flowchart TD; A[one<br/>two]
OK lines=4
┌─────┐
│ one │
│ two │
└─────┘
### case 12 width=120
SRC:flowchart TD; A -->|one<br/>two| B
ERR:unsupported Mermaid syntax or label
### case 13 width=120
SRC:flowchart TD; A[<br/><br />]
ERR:unsupported Mermaid syntax or label
### case 14 width=120
SRC:flowchart TD; A["one<br/>two<br/>"]
OK lines=5
┌─────┐
│ one │
│ two │
│     │
└─────┘
### case 15 width=120
SRC:flowchart TD; A["one<br/>two"] & B["three<br>four"]
OK lines=10
┌───────┐
│ one   │
│ two   │
└───────┘


┌───────┐
│ three │
│ four  │
└───────┘
### case 16 width=5
SRC:flowchart TD; A --> B
ERR:diagram exceeds available display width
### case 17 width=120
SRC:flowchart TD; A{Done?<br/>Retry?}
OK lines=4
┌─────────┐
│ ◇ Done? │
│ Retry?  │
└─────────┘
### case 18 width=120
SRC:flowchart TD; A(["Req<br/>Ready"])
OK lines=4
╭───────╮
│ Req   │
│ Ready │
╰───────╯
```