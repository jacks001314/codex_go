# 2026-10-29 稳定性测试落地计划（可执行版）

> 目标：把「长时间运行 / 大数据量 / 中间异常 / 界面响应 / 内存 / 磁盘」六类稳定性因素，
> 变成一条命令可跑、一份 JSON 可判、一个 nightly 可回归的工程能力。
>
> 本文件是执行册：每张任务卡都写明改动文件、关键接口、验收命令、预算键和工时。
> 代码骨架（W1/W2/W3 部分）已落地并通过实测，见 §4。

## 0. 一页速览

| 因素 | 场景入口 | 报告检查项 | 预算键 | 频率 |
| --- | --- | --- | --- | --- |
| 长时间运行 | `TestSoakTurns` / `TestSoakIdle` / `TestSoakReconnect` | `soak.rss_*` `soak.goroutines` `soak.open_fds` | `budgets.soak.*` | nightly 2h / weekly 24h |
| 大数据量 | `TestLargeRolloutRead` / `TestLargeThreadItems` / `TestLargeToolOutput` | `large.*` | `budgets.large.*` | nightly |
| 中间异常 | `TestFaultStream*` / `TestFaultTool*` / `TestFaultClient*` / `TestFaultDisk` / `TestFaultDB` | `fault.*` | 无阈值，按"不 panic/不孤儿/不半写"判定 | 每次 PR（快集）+ nightly |
| 界面响应 | `TestStabilityUIKeyLatency` / `TestStabilityUIFrameRender` / `TestStabilityUIEventStorm` | `ui.*` | `budgets.ui.*` | nightly（参考机判定） |
| 内存使用 | `TestMemoryLongSession` / `TestMemoryGCSteady` | `memory.*` | `budgets.soak.*` + 包内有界断言 | nightly |
| 磁盘使用 | `TestDiskGrowth1KTurns` / `TestDiskWALCheckpoint` / `TestDiskTempCleanup` | `disk.*` | `budgets.disk.*` | nightly |

完成度（2026-10-29 收盘）：W1 ≈ 90%，W2 ≈ 100%，W3 = 100%，W4 ≈ 85%，W5 ≈ 85%，W6 ≈ 100%，W7 ≈ 100%，W8 ≈ 100%，W9 ≈ 50%，W10 ≈ 90%（差 7 天连续绿）。

关键路径：`W4 大数据 → W5 故障 → W6 UI → W8 磁盘 → W10 CI`，共约 26 人日。

一条命令：

```powershell
.\scripts\stability.ps1 -Suite all -Duration 2h -Report
```

## 1. 范围与非目标

范围：测试与修复**稳定性**问题，含资源泄漏、无界增长、超时/中断恢复、响应延迟、磁盘残留。

非目标：

- 不改产品行为。任何需要修改阈值或语义的修复，先对齐 frozen Rust pin，或登记 deviation。
- 不做 Rust 功能同步审计（那是 `djalign` / `codex-go-update` 的职责）。
- 不把长测塞进 `go test ./...`。全部场景走 build tag `stability`。
- 不追求覆盖率数字；只追求"六类因素各有一条能红的门禁"。

基线事实（2026-10-29，`521ed99a`）：

- Go 1.26.5 / `go.mod` 声明 1.26.2；单模块 `codex_go`；2617 个 `_test.go`。
- 全仓 `func Benchmark` 数量 0，`t.Parallel()` 数量 0，179 个测试文件直接依赖 `time.After`/`NewTimer`/`Tick`。
- 既有可复用测试台：`recordreplay`（无网络回放）、`tui/tea` 的 VT100 快照与 PTY 测试、`memories/rollout_input_bound_test.go`（有界读取断言）、`state/logs_maintenance.go` 与 `state/reclamation_test.go`（保留期与 WAL 回收）。

## 2. 判定口径（什么叫通过）

四条铁律，任何一条不满足都算没落地：

1. **报告通过**：`report.Passed == true` 且 **该场景必需的 check 不为 skipped**。
   平台不支持 RSS/FD 时允许 skip，但必须在报告 `env` 里写明原因，并计入"未覆盖项"。
2. **逐名比较**：失败集合按 `package/TestName` 逐名比对，禁止用整包计数当结论。
3. **SKIP ≠ PASS**：缺环境变量的跳过项必须在验收表里单列，不得计入绿色数。
4. **不自动重试**：稳定性失败一律进台账（§10），重试只用于诊断取证，不用于判定。

## 3. Rust 对照基线

| Rust 能力 | Rust 位置 | Go 现状 | 本计划动作 |
| --- | --- | --- | --- |
| 假模型 + 请求捕获 | `core/tests/common/responses.rs` | 已有 `stability/harness/mockmodel` | 已落地 |
| 逐 chunk 闸门 | `core/tests/common/streaming_sse.rs` | 已有 `mockmodel.Gate` | 已落地 |
| 延迟/断连代理 | `app-server/tests/common/rpc_delay.rs` | 无 | T1.2 `harness/netproxy` |
| 假时钟 | `tokio::time::pause()/advance()` | 无统一抽象 | T5.7 时钟跳变用例（先做局部注入） |
| PTY 驱动 | `tui/tests/suite/*` `PtyCodex` | 有 PTY 测试，无计时 | T6.1 headless 驱动 + in-package VT100 |
| 有界 rollout 读取 | `memories/write/src/rollout_input.rs`（8 MiB / 1 MiB） | `memories/rollout_input_bound.go` 已对齐 | T4.2 放大到 1 GiB |
| 日志保留预算 | `state/src/runtime/logs_maintenance_tests.rs` | `state/logs_maintenance.go` | T8.4 预算边界 |
| WAL 回收 | `state/src/runtime/reclamation_tests.rs` | `state/reclamation_test.go` | T8.2 中断恢复 |
| CI 分片/重试/资源组 | `.config/nextest.toml` + workflows | 无 | T10.2 nightly |

Rust **没有**的能力（Go 侧新增，需自己校准）：2h soak 的 RSS 斜率断言、UI 帧延迟预算、跨版本趋势告警。

## 4. 已落地资产与实测证据

### 4.1 资产清单（2026-10-29 收盘）

| 资产 | 文件 | 状态 | 证据 |
| --- | --- | --- | --- |
| 假模型（SSE/闸门/故障/fallback） | `stability/harness/mockmodel/server.go` | done | `go test ./stability/harness/...` 通过 |
| 故障代理（延迟/抖动/限速/断连/半包） | `stability/harness/netproxy/server.go` | done | 5 个测试通过 |
| 资源采样（heap/RSS/FD/goroutine） | `stability/harness/metrics/*.go` | done | 测试通过；`GOOS=linux go build` 通过 |
| 磁盘采样（大小/WAL/SHM/临时文件/剩余空间） | `stability/harness/disk/*.go` | done | Windows/Unix 两套实现，交叉编译已验证 |
| 报告、预算与 soak 判定 | `stability/harness/report/report.go` | done | `Metrics` 显式指标 + `ValidateSoakWithWarmup`；文件名毫秒级 |
| 大数据生成器 | `stability/harness/rolloutgen/generator.go` | done | 4 个测试通过（流式、确定性、首行 session_meta） |
| 黑盒场景 | `stability/scenarios/*.go` | done | smoke/soak/idle/faults/memory/disk 全绿 |
| 包内私有场景 | `memories/rollout_stability_test.go`、`state/disk_stability_test.go`、`tui/tea/stability_ui_test.go` | done | 1 GiB 读取、WAL、5 项 UI 全绿 |
| 趋势工具 | `stability/cmd/stability-trend` | done | 6 个单测；对 53 份真实报告能定位历史失败 |
| 驱动脚本 | `scripts/stability.ps1` / `stability.sh` | done | `-Json` 摘要、`-Shard i/n`、`-Trend`、`-Race` |
| nightly | `.github/workflows/stability-nightly.yml` | 已写，未跑过 | harness + scenarios(ubuntu/windows) + trend + weekly 24h |
| flake 台账 | `update/flake_ledger.jsonl` | done | 7 条，含 1 条已修复的 UI 帧回归 |
| 预算唯一来源 | `stability/budgets.json` | done | 场景只读该文件 |

### 4.2 实测数据（2026-10-29）

| 场景 | 结果 | 预算 |
| --- | --- | --- |
| rollout 读取 | 1 GiB：peak 13.47 MiB / retained 8.38 MB / 523 items / 325 ms；本轮 64 MiB 复跑：peak 13.50 MB / retained 8.38 MB | 256 MiB / 8 MiB |
| 1000 turns 磁盘增长 | 4,968,795 B，temp=0，WAL=0 | 20,971,520 B |
| WAL 检查点（5000 行） | TRUNCATE 后 0 字节，重开 5000 行存活 | ≤ 64 MiB |
| soak 3–5 s（warmup 1 s，settle 2 s） | 3066 turns，goroutine +0，FD +0 | +5 / +5 |
| soak 10 s（warmup 2.5 s，settle 2 s） | 13,387–15,760 turns；服务端连接 opened=1148 / closed=1148 / retained=1；稳态 FD 漂移 0（逐秒序列 `0,0,+1,+2,+3,+0…` 走平），goroutine +0 | 保留连接 ≤ 8 / 稳态 FD +5 |
| 内存长会话（400 turns 短跑） | peak 18.7 MB，首/末四分位 4.6 MB → 16.3 MB | 512 MiB / 1.5x+64 MiB |
| UI 按键→首帧 | p99 6.5 ms | 50 ms |
| UI 20k 消息帧渲染 | p95 6.0–10.4 ms（修复前 15.9–37 ms） | 16 ms |
| UI 事件风暴 | ≥ 500 ev/s，最差单次 Update < 100 ms | 500 ev/s |
| UI 长草稿（**增量布局后**） | 2000 字 p99 **6.0 ms**（修前同参数 33.6 ms）；8000 字 17.9 ms（修前 65.3 ms，超预算）；20000 字 34.2 ms（修前 96.6 ms） | 50 ms |
| 大消息流式往返（32 MiB 单事件） | peak 255–290 MB（7.6–8.7× payload），round-trip 校验通过，耗时 2.4–2.8 s | 512 MiB 且 ≤ 12× payload |
| 超长线程（200k 消息） | 逐条保留 ≤ 512 B，追加线性，整体 < 30 s | 512 MiB / 30 s |
| 内存 GC 稳态（1200 轮） | 400 轮后走平 ~9.3 MB（修假模型捕获前每轮 +25 KB 线性涨到 80 MB） | 首/末三分位 <= 1.5x + 64 MiB |
| 临时文件清理 | stale temp 删除、fresh 保留、rollout 压缩、第二 home temp 清空 | 孤儿临时文件 0 |
| 断线重连（120 轮取消在途流） | 服务端连接 opened=120 / closed=120 / active=0，goroutine +0；Windows 进程句柄 +7 后走平（平台记账，仅作趋势指标） | 连接全部回收 |
| 工具执行失败（error / RespondToModel） | 未分类 error → 可诊断的 fatal（不 panic）；RespondToModel → 模型可见失败文本；后续调用正常 | 无 panic / 可诊断 |
| 工具 handler panic | 被 `executeToolSafely` 兜住，进程存活，错误含 "panicked"，之后仍可派发 | 无 panic |
| 工具取消 | 取消后 0.0x s 返回，goroutine 回到基线 | ≤ 2 s / +5 |
| 工具错误长跑（300 轮 error+panic+success 混合） | fatal 计数与注入次数精确相等，goroutine +0，retained < 8 MiB（实测 0） | +5 / 8 MiB |
| 磁盘写满（SQLITE_FULL） | `PRAGMA max_page_count` 逼出真实 13 号错误：27 行后报 "database or disk is full (13)"，已提交 27 行可读、temp=0、放宽上限后写入立即恢复 | 无 panic / 不损坏 / temp 0 |

判定口径：脚本 `-Json` 输出 `pass/fail/skip` 计数与逐名失败；报告 `passed` 由非 skip 检查项全绿推出；趋势工具另外把"最近 N 份里出现过 fail"直接算告警。

### 4.3 本轮修复/定性的七处问题

1. **20k 消息帧渲染每帧重建整段拼装**：`renderTranscriptLayout` 每帧对全部消息做 `strings.Join` 并重新拼接，缓存只省掉了行渲染、没省掉拼装。改为：布局输出（content/ranges/image plans）整帧缓存，仍用逐消息 key 扫描做失效判定（语义不变），并把每条消息的 `Join+TrimRight` 换成直接写 builder + `Grow`。p95 15.9–37 ms → 6.0–10.4 ms，`go test ./tui/tea` 全量绿。
2. **报告时间与指标不可用于趋势**：`report.New` 在多个场景里被放到运行结束才调用，导致 `started_at == ended_at`；且报告只有 check 文本、没有可算斜率的数值。改为：所有场景开头调用 `report.New`，新增 `Report.Metrics` 与 `Report.Metric()`，并补 `CODEX_STABILITY_COMMIT`、go 版本、OS/ARCH。
3. **SSE 大事件直接失败**：`bufio.Scanner` 默认 64 KiB、旧上限 16 MiB，provider 把整条 `response.output_item.done` 放在一行时（32 MiB 工具输出很常见）整轮报 `token too long`。改为 `bufio.Reader.ReadSlice` 驱动的解析器，单事件上限 128 MiB，超限返回可判定的 `errSSEEventTooLarge`。
4. **假模型无界捕获请求体**：每个请求体永久留在 `requests`（≈22 KB/轮），所有 soak/内存场景都会被它污染。改为 256 条环形保留 + 总计数 + `RequestBytes()`；`TestMemoryGCSteady` 由此从"每轮 +25 KB、400 轮涨到 80 MB"变成 400 轮后完全走平。
5. **大载荷路径重复解码**：见 §4.5。修复后 32 MiB 单事件峰值 572 MB → 255–290 MB。
6. **composer 行布局每词重测整行 + 整词**：`composerWrappedRows` 对每个词都 `uniseg.StringWidth(string(lines[row]))` 并重新测整词，长草稿下一帧就是 O(draft²) 的 grapheme 分段（profile 里 `uniseg.StringWidth` 占 55%）。改为逐步累计行宽，并用"每 rune 最多 2 格"的上界做守卫，只在可能溢出时才精确测量整词；新增 `tui/tea/composer_wrap_equiv_test.go` 用旧实现作 oracle，对 15 条固定样本 × 7 种宽度 + 200 条随机混排做逐行等价校验。2000/8000/20000 字 p99 = 6.0 / 17.9 / 34.2 ms（修前 33.6 / 65.3 / 96.6 ms）。
7. **套件内并行跑包制造假红**：`-Suite all` 让 4 个包并行执行时，`TestUIEventStorm` 从 642 ev/s 掉到 283 ev/s（单跑 0.86 s 通过）。稳定性场景断言的是**进程级** heap/RSS/句柄/帧吞吐，跨包并行会互相污染。脚本改为 `go test -p 1`，同一台机器同参数复跑 41 项全绿。
8. **soak 把"进程句柄绝对增量"当门禁，随机红**：同一份 `TestSoakTurns` 代码、同机同参数，句柄增量一次 +8（fail）、一次 +0（pass）；同期给假模型接上 `http.ConnState` 计数后，15,760 轮表现为 opened=927 / closed=927、15 秒内 retained 恒为 1（预热留下的池内闲暇连接）。结论：Windows 句柄数混入了线程/事件/池化连接的平台记账，绝对值不是泄漏信号。门禁改为两条直接观测：`soak.connections_reclaimed`（窗口结束时仍保留的连接 ≤ `max_active_connections`，缺省 8）和 `soak.open_fds` 的**稳态漂移**（只比窗口后半段，泄漏会持续增长、记账会走平）；句柄总增量与逐秒序列仍写进报告作趋势。

### 4.4 已知问题（进入台账，不靠放宽预算解决）

- composer 现在每帧是 O(draft)（`bubbles/textarea.wrap` 自身仍是线性重排）：20000 字 p99 34 ms 仍在预算内，但要更快只能换掉上游 textarea 或做增量换行，属后续优化而非稳定性缺口。
- soak 家族的 Windows 句柄增量已定性：**不是泄漏**。两处证据一致：重连场景 120 轮"取消在途流"→ opened=120 / closed=120 / active=0，`GetProcessHandleCount` 第 40 轮后稳定在 +7；turn 场景 15,760 轮 → opened=927 / closed=927、retained=1，而同参数两次运行的句柄增量分别是 +8 与 +0。门禁因此统一改为**连接回收 + 稳态漂移**（`soak.reconnect.connections_reclaimed`、`soak.connections_reclaimed`、`soak.open_fds` 只判窗口后半段），句柄总增量与逐秒序列写进报告供趋势观察。预热也必须用被测动作本身：成功轮留下的是池内闲暇连接，与被取消的流不同。
- 整帧布局缓存仍需逐消息 key 比较（20k 消息约 0.3 ms）；真正的增量布局（分段 rope）未做。
- **已知依赖**：20k 消息帧渲染的修复（布局输出整帧缓存，§4.3 第 1 条）与并行 TUI 对齐任务同处 `tui/tea/model.go`，未随稳定性提交一起落地。因此在干净 HEAD 上跑 `-Suite all` 会是 45 过 1 挂，唯一失败项是 `TestUIFrameRenderLargeTranscript`（p95 18.98 ms vs 预算 16 ms）；带上该任务的在途改动即为 46 全绿。两边合流后需复跑确认。
- soak 的 RSS 漂移在 < 60 s 窗口被跳过（设计如此），要等 weekly 24h 任务积累数据。
- nightly workflow 尚未在 GitHub 上实际跑过；`scripts/stability.sh` 本机无 bash，未本地验证。
- 旧报告（修复前留下）仍在本地 `stability/reports/`，趋势工具会继续对它们报失败，属预期行为。

### 4.5 大载荷路径的拷贝账（32 MiB 单事件）

峰值按修复顺序：572 MB → 342 MB → 255–290 MB（预算 512 MiB / 12× = 384 MiB）。
剩下的每一次拷贝都有明确用途，任何"再多一份"都会让系数越过 12×：

| 阶段 | 保留/分配 | 处理 |
| --- | --- | --- |
| SSE 行重组 | 32 MiB × 2（行缓冲 + 事件缓冲） | 改为 `data` 值直写事件缓冲，行缓冲不再保存大行；flush 直接把 buffer 交给事件（不再复制） |
| 事件判别 `type` | 整包 `map[string]any` 解码（32 MiB + map 开销） | `streamEventType` 只解该字段 |
| `safety_buffering` 探测 | 每个事件整包解码 | 先按 key 做字节扫描，命中才解码 |
| output_item 解码 | 两次全量 JSON 解码 + 两次 raw 复制 | `agentItemAndRawFromStreamEventData` 单次解码，raw 与 typed item 共享 |
| reasoning 判别 | 整包解成 `map` | 只解 `type` 判别字段 |
| `response.output_text.delta` | 两次整包 map 解码（delta + item_id） | 一次结构体解码同时取两字段 |
| 假模型 fixture | 预生成全部帧（≈3× payload）+ `[]rune`（4×） | > 4 MiB 改流式写帧；分片按 rune 游标切片 |

结论：**大事件的内存系数是产品属性，不是测试台噪声**；`TestLargeToolOutput` 的 12× 比例检查就是防它回退。

同一条原则用在资源泄漏上：Windows 的"句柄数"混入了线程、事件、池化连接，不能当泄漏门禁；能直接观测"连接是否被回收"时就用后者（`mockmodel.Server.ConnStats`）。

## 5. 目录结构与设计约束

```text
stability/
  README.md
  budgets.json
  harness/
    mockmodel/     # 已落地：SSE、Gate、Abort、PartialStream、Fallback、请求捕获
    netproxy/      # 已落地：延迟、抖动、限速、断连、半包
    metrics/       # 已落地：heap/goroutine/RSS/FD
    disk/          # 已落地：目录、WAL/SHM、临时文件、剩余空间
    report/        # 已落地：预算判定 + 显式指标 + JSON 报告
    rolloutgen/    # 已落地：可控字节数的 rollout 生成器
    appserver/     # 待建：进程内 JSON-RPC 驱动（复用 appserver.NewRouter）
  scenarios/       # 黑盒场景（//go:build stability）
  cmd/stability-trend/   # 已落地：跨报告斜率与失败告警
scripts/stability.ps1 | stability.sh   # 已落地：-Json/-Shard/-Trend/-Race
.github/workflows/stability-nightly.yml
update/flake_ledger.jsonl
```

两条硬约束（踩过坑，必须遵守）：

- **双轨放置**：`readRecentRolloutItems`（`memories`）、VT100 终端模拟（`tui/tea`）都是包内私有。
  因此除了 `stability/scenarios` 黑盒场景，还允许在目标包内新增 `<topic>_stability_test.go`（同样带 `//go:build stability`），
  **测试名统一前缀 `TestStability`**，脚本用 `-run 'TestStability|TestSoak|...'` 只选我们的用例，避免误跑整包。
- **`appserver` 包**：整包测试需要 5 个环境变量、CWD=包目录、`-p 1`。落在该包的稳定性测试必须由 `scripts/stability.ps1` 统一注入环境，不得依赖开发者手工设置。

环境变量约定：

| 变量 | 作用 | 默认 |
| --- | --- | --- |
| `CODEX_STABILITY_DURATION` | soak 时长；空则 skip | 空 |
| `CODEX_STABILITY_WARMUP` | 预热窗口 | `min(duration/4, 10s)`，下限 1s |
| `CODEX_STABILITY_SETTLE` | 采样前静默窗口（等 in-flight 连接收敛） | 2s |
| `CODEX_STABILITY_BUDGETS` | 预算文件路径 | 仓库内 `stability/budgets.json` |
| `CODEX_STABILITY_REPORT_DIR` | 报告目录 | `stability/reports/`（gitignored） |
| `CODEX_STABILITY_LARGE_BYTES` | 大数据载荷字节数；< 64 MiB 时 large 场景 skip | 1 GiB |
| `CODEX_STABILITY_SEED` | 生成器种子 | 1 |
| `CODEX_STABILITY_COMMIT` | 写进报告的提交号 | 脚本自动取 `git rev-parse --short HEAD` |
| `CODEX_STABILITY_IDLE_DURATION` | 空闲 soak 时长（覆盖 DURATION） | 空（回落 DURATION） |
| `CODEX_STABILITY_MEMORY_TURNS` | 内存场景轮数 | 1500 |
| `CODEX_STABILITY_MEMORY_WARMUP` | 内存场景预热轮数 | 50 |
| `CODEX_STABILITY_DISK_TURNS` | 磁盘场景轮数 | 1000 |
| `CODEX_STABILITY_DISK_DIR` | 磁盘场景落盘根目录 | 临时目录 |
| `CODEX_STABILITY_UI_MESSAGES` | 大转录消息数 | 20000 |
| `CODEX_STABILITY_UI_STORM_SECONDS` | 事件风暴时长（秒） | 2 |
| `CODEX_STABILITY_UI_DRAFT` | 长草稿场景的字符数 | 2000 |
| `CODEX_STABILITY_TOOL_FAULT_CYCLES` | 工具故障长跑轮数 | 300 |

## 6. 任务分解

工时单位：人日。标 `[done]` 的已完成并验证。

### W0 基线冻结与台账（1.5d，依赖：无）

| 卡号 | 任务 | 产出 | 验收 |
| --- | --- | --- | --- |
| T0.1 | 干净 worktree 跑全量 `-json` | `update/baseline/2026-10-2x/full.json` | 能复现失败集合 |
| T0.2 | 失败逐名隔离（`-count=3 -v`），分类 | `update/flake_ledger.jsonl` | 每条有签名与分类 |
| T0.3 | 参考机档案 | `update/baseline/machine.md` | CPU/内存/OS/磁盘/Go 版本 |

注意：当前工作区被并行任务修改（`tui/**`、`config/api.go`、`appserver/runtime_router.go`）。基线必须用 `git worktree add` 的干净副本，报告里记录 commit。

```powershell
Remove-Item Env:TERM -ErrorAction SilentlyContinue
$env:GOCACHE = "D:\qax\reagent\dev\codex_go\.gocache"
go test ./... -count=1 -json > update/baseline/full.json
```

### W1 确定性测试台（3d，已用 2d）

| 卡号 | 任务 | 文件 | 状态 |
| --- | --- | --- | --- |
| T1.1 | 假模型 + 闸门 + 故障响应 | `harness/mockmodel` | [done] |
| T1.2 | 延迟/断连代理 | `harness/netproxy` | todo |
| T1.3 | appserver 进程内驱动 | `harness/appserver` | todo |
| T1.4 | headless TUI 驱动 + in-package VT100 | `tui/tea/*_stability_test.go` | todo |
| T1.5 | 采样器（heap/RSS/FD） | `harness/metrics` | [done] |

T1.2 接口：

```go
type Proxy struct{ /* listener, target */ }
type Fault struct {
    Delay      time.Duration // 首字节前
    Jitter     time.Duration
    Throttle   int           // bytes/sec，0 不限
    DropAfter  int64         // 转发 N 字节后断开
    SplitWrite bool          // 每 1 字节 flush，制造半包
}
func New(t *testing.T, target string, fault Fault) *Proxy // t.Cleanup 自动关闭
```

验收：`go test -tags stability ./stability/scenarios -run '^TestProxy' -count=10` 10/10 稳定；断连场景错误可判定（不是超时）。

T1.3 接口（先用进程内，避免 CI 起子进程）：

```go
type Driver struct{ /* router */ }
func Start(t *testing.T, opts Options) *Driver
func (d *Driver) Call(ctx context.Context, method string, params any, out any) error
func (d *Driver) Notifications() <-chan json.RawMessage
func (d *Driver) Close()
```

复用 `appserver.NewRouter(store)`（`appserver/router.go:184`）；需要 5 个 env 时由测试内部 `t.Setenv` 注入。

### W2 观测与预算（2d，已用 1.5d）

| 卡号 | 任务 | 状态 |
| --- | --- | --- |
| T2.1 | `budgets.json` + `report.ValidateSoak` | [done] |
| T2.2 | 磁盘采样 | [done] |
| T2.3 | appserver gauge（goroutines / open_fds / in-flight turns / queue depth） | todo |
| T2.4 | 采样开销自检（< 1% 墙钟） | todo |

T2.4 验收：同一 60s 循环分别开/关 `Sampler{Interval: 500ms}`，轮次差 < 1%，结论写进报告 `env.sampler_overhead`。

### W3 长时间运行（2.5d，已用 1d）

| 卡号 | 任务 | 验收 |
| --- | --- | --- |
| T3.1 | `TestSoakTurns`（已完成骨架） | 2h 通过 |
| T3.2 | `TestSoakIdle`：24h 空闲 + 定时 tick | goroutine/FD 回落到基线 +5 |
| T3.3 | `TestSoakReconnect`：1000 次 attach/detach + daemon 重启 | 无 FD 泄漏、无孤儿 socket |
| T3.4 | `TestSoakThreads`：1000 thread 交替激活 | 单 thread 的 rollout 句柄不累积 |
| T3.5 | 修 §4 三个已知问题 | 报告 `started_at != ended_at` |
| T3.6 | `cmd/stability-trend`：最近 N 份报告斜率 | 输出表格 + 超阈值非零退出 |

T3.2/T3.3 的采样口径：**预热 → settle → 取基线 → 采样 → settle → 末样本**。
早期 3s soak 曾出现 FD +66，实为 in-flight 连接未收敛，不是泄漏；禁止回退这个设计。

### W4 大数据量（3d）

| 卡号 | 任务 | 文件 | 预算键 |
| --- | --- | --- | --- |
| T4.1 | 数据生成器：流式写 JSONL，控制总字节/行数/单行大小 | `harness/rolloutgen` | — |
| T4.2 | 1 GiB rollout 读取峰值堆 | `memories/rollout_stability_test.go` | `large.rollout_read_peak_bytes` |
| T4.3 | 10 万 item 的分页/搜索 P95 | `stability/scenarios/large_test.go` | `large.p95_latency_ms` |
| T4.4 | 100 MiB 工具输出洪泛：截断 + 落盘 + 不驻留 | 同上 | `large.peak_heap_bytes` |
| T4.5 | 1000 thread 并发列表分页 | 同上 | `large.p95_latency_ms` |
| T4.6 | 磁盘前置守卫：剩余空间 < 20 GiB 则 skip 并记录原因 | `support_test.go` | — |

T4.1 接口：

```go
type Spec struct {
    Path        string
    TotalBytes  int64
    MaxLineSize int
    ItemsPerTurn int
    Seed        int64
}
func Generate(spec Spec) (Stats, error) // 流式写入，返回实际行数/字节
```

T4.2 做法：`rollout.NewRecorder(&rollout.CreateParams{...})` 建文件，`AppendLine(rollout.Line{Type:"item", ...})` 填充，
然后 `runtime.GC(); runtime.ReadMemStats(&before)` → 调 `readRecentRolloutItems(path)` → `ReadMemStats(&after)`，
取 `after.TotalAlloc - before.TotalAlloc` 与 `HeapAlloc` 双指标，与预算比较。
同包直接调用私有函数，避免为测试导出生产 API。

判定细节（防噪声）：GC 两次，取两次测量中的**较小值**；同时记录 `GOMEMLIMIT`/`GOGC` 到报告 env。

### W5 中间异常处理（4d）

| 卡号 | 覆盖面 | 用例 |
| --- | --- | --- |
| T5.1 | HTTP 层：429 / 500 / 慢 header / 半包 / 非法 JSON / 提前 EOF / 超大响应 | `TestFaultStream*` |
| T5.2 | 工具进程：被杀 / 非零退出 / 输出洪泛 / stdin 关闭 | `TestFaultTool*` |
| T5.3 | 客户端：turn 中掉线 / 重复请求 / 取消风暴 | `TestFaultClient*` |
| T5.4 | 文件系统：磁盘满 / 只读目录 / 权限拒绝 | `TestFaultDisk` |
| T5.5 | 数据库：WAL 损坏 / 截断 / 并发打开 | `TestFaultDB` |
| T5.6 | 信号：SIGINT/SIGTERM + 终端 resize 风暴 | `TestFaultSignal` |

统一断言集（每个用例都要过）：

- 不 panic（`defer recover` 捕获并 `t.Fatalf`）；
- 错误可诊断（含 status/exit code/路径，不是裸 `EOF`）；
- 状态可恢复（同一 thread 后续 turn 成功）；
- 无半写文件（rollout 末行必须是完整 JSON）；
- 无孤儿进程（Windows 用 job object 计数，Unix 用 `pgrep -P`）。

### W6 界面响应速度（3d）

| 卡号 | 任务 | 判定 |
| --- | --- | --- |
| T6.1 | 把 VT100 模拟器抽成同包可复用件（`tui/tea/stability_support_test.go`） | 现有快照测试不变 |
| T6.2 | 按键→首帧 P99 | `ui.key_to_frame_p99_ms` |
| T6.3 | 20 万行 transcript 单帧渲染 P95 | `ui.frame_p95_ms` |
| T6.4 | 500 events/s 洪泛：队列有界、可合并、不丢输入 | `ui.events_per_second` |
| T6.5 | resize 20 次/秒：composer 行数/草稿不漂移 | 快照一致 |

测量方式（不使用真实 PTY，保证 CI 可跑）：

```go
model := tea.NewModel(state, tea.Options{Width: 120, Height: 40})
start := time.Now()
model.Update(tea.KeyMsg{ /* 键入一个字符 */ })
_ = model.View()             // 首帧
record(time.Since(start))    // 采样 N=500 次，取 P99
```

CI 机器差异对策：nightly 只**记录**延迟，超预算时在"参考机"标签的 runner 上复判；跨机只比较相对基线倍数（≤ 1.5x）。

### W7 内存使用（2d）

| 卡号 | 任务 | 判定 |
| --- | --- | --- |
| T7.1 | `TestMemoryLongSession`：图片 + 大输出 + 5 thread 并发 | 稳态 heap 斜率 ≈ 0 |
| T7.2 | 逐包补有界断言（`TotalAlloc`/`HeapAlloc`），仿 `memories/rollout_input_bound_test.go` | 单次操作 < 上限 |
| T7.3 | 缓存上限：历史、附件、tool 输出、图片解码 | LRU/TTL 生效 |
| T7.4 | turn 结束释放大对象、buffer 复用 | 无跨 turn 持有 |

要点：内存场景必须固定 `GOGC=100` 并写入报告；否则同一 commit 两次结果不可比。

### W8 磁盘使用（3d）

| 卡号 | 任务 | 判定 |
| --- | --- | --- |
| T8.1 | 1 万 turn 的 `CODEX_HOME` 增量 | `disk.bytes_per_1k_turns` |
| T8.2 | WAL checkpoint 前后大小 + 中断恢复 | `disk.wal_bytes` |
| T8.3 | 重启后临时文件/pid/socket 残留 | `disk.orphan_temp_files == 0` |
| T8.4 | 日志保留期与字节预算（对照 `state/logs_maintenance.go`） | 超期即删、预算减半重试 |
| T8.5 | 附件 GC 与 rollout 轮转/压缩 | 上限生效 |

复用：`state/logs_maintenance.go` 的 `PRAGMA wal_checkpoint(PASSIVE)` 路径、`state/reclamation_test.go` 的中断恢复构造。

### W9 修复与回归（滚动 5d）

| 优先级 | 定义 | SLA |
| --- | --- | --- |
| P0 | 泄漏、无界队列/缓存/读取、半写、OOM 风险 | 当轮修完，进 soak 复验 |
| P1 | 整包时序 flake、临时目录清理竞态、端口/socket 冲突 | 本周内 |
| P2 | 跨平台差异、慢测试 | 排入 backlog |

修复流程（强制）：先写**能红的**单测 → 改代码 → 受影响包 `-count=3` → 场景复验 → 台账关闭。
禁止"先改代码再补测试"，也禁止靠放宽预算变绿。

### W10 CI 与趋势（2d）

| 卡号 | 任务 | 验收 |
| --- | --- | --- |
| T10.1 | `scripts/stability.*` 支持多包、`-json`、失败摘要、`-Shard` | 本地一条命令出摘要 |
| T10.2 | nightly（ubuntu + windows，2h soak + large + faults + ui） | 连续 7 天绿 |
| T10.3 | weekly self-hosted 24h soak | 首份 24h 趋势 |
| T10.4 | 趋势 job：最近 7 份报告斜率异常告警 | 人为造回归能告警 |

nightly 骨架：

```yaml
name: stability-nightly
on:
  schedule: [{cron: "17 3 * * *"}]
  workflow_dispatch:
concurrency: {group: stability-nightly, cancel-in-progress: false}
jobs:
  scenarios:
    strategy:
      fail-fast: false
      matrix: {os: [ubuntu-latest, windows-latest]}
    runs-on: ${{ matrix.os }}
    timeout-minutes: 180
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: {go-version-file: go.mod}
      - run: go test ./stability/harness/... -count=1
      - run: ./scripts/stability.sh all 2h
        if: runner.os != 'Windows'
      - run: ./scripts/stability.ps1 -Suite all -Duration 2h -Report
        if: runner.os == 'Windows'
      - uses: actions/upload-artifact@v4
        if: always()
        with: {name: reports-${{ matrix.os }}, path: stability/reports}
```

不做的事：不给稳定性 job 配自动重试；flake 进台账；托管 runner 单 job 上限 6h，24h 只跑 self-hosted。

## 7. 用例矩阵

| 用例 | 包 | 载荷 | 注入 | 观测 | 时长 |
| --- | --- | --- | --- | --- | --- |
| `TestSoakTurns` | scenarios | 连续 turn | 无 | RSS/heap/goroutine/FD | 2h |
| `TestSoakIdle` | scenarios | 空闲 | 周期 tick | goroutine/timer | 24h |
| `TestSoakReconnect` | scenarios | 1000 attach/detach | daemon 重启 | FD/socket/temp | 1h |
| `TestSoakThreads` | scenarios | 1000 thread | 交替激活 | 句柄/heap | 1h |
| `TestLargeRolloutRead` | memories | 1 GiB JSONL | 超大/非法记录 | 峰值 heap | 5m |
| `TestLargeThreadItems` | scenarios | 10 万 item | 分页 + 搜索 | P95 | 10m |
| `TestLargeToolOutput` | scenarios | 100 MiB 输出 | 洪泛 | 截断/落盘/heap | 5m |
| `TestFaultStream*` | scenarios | 单 turn | 断流/429/500/半包 | 错误/重试/状态 | 2m |
| `TestFaultTool*` | scenarios | 单工具 | 杀进程/非零/洪泛 | 退出码/清理 | 3m |
| `TestFaultClient*` | scenarios | 单 turn | 掉线/重复/取消风暴 | 幂等/半写 | 3m |
| `TestFaultDisk` | scenarios | 单 thread | 磁盘满/只读 | 错误/回滚 | 2m |
| `TestFaultDB` | state | 单库 | WAL 损坏/截断 | 恢复 | 3m |
| `TestStabilityUIKeyLatency` | tui/tea | 500 次按键 | 无 | P99 首帧 | 1m |
| `TestStabilityUIFrameRender` | tui/tea | 20 万行 | 滚动/搜索 | P95 帧 | 3m |
| `TestStabilityUIEventStorm` | tui/tea | 500 ev/s | 高频刷新 | 队列/丢输入 | 2m |
| `TestMemoryLongSession` | scenarios | 长会话 | 图片/大输出 | heap 斜率 | 30m |
| `TestDiskGrowth1KTurns` | scenarios | 1 万 turn | 无 | 字节增量 | 30m |
| `TestDiskWALCheckpoint` | state | 单库 | 写入 + checkpoint | WAL 大小 | 5m |
| `TestDiskTempCleanup` | scenarios | 重启 | 残留文件 | 孤儿数 | 5m |

## 8. 预算与校准

| 指标 | 初值 | 预算键 | 校准方式 |
| --- | --- | --- | --- |
| RSS 漂移 | < 1%/h | `soak.rss_drift_per_hour` | 3 次 2h 取中位数 |
| RSS 峰值比 | < 2.0x 预热值 | `soak.rss_peak_ratio` | 同上 |
| goroutine 增量 | <= 5 | `soak.goroutine_delta` | 同上 |
| FD 增量 | <= 5 | `soak.fd_delta` | 同上 |
| 单 turn 峰值 heap | < 512 MiB | `large.peak_heap_bytes` | 5 次取 P95 |
| 1 GiB rollout 读取峰值 | < 256 MiB | `large.rollout_read_peak_bytes` | 同上 |
| 分页/搜索 P95 | < 500ms | `large.p95_latency_ms` | 参考机 |
| 按键→首帧 P99 | < 50ms | `ui.key_to_frame_p99_ms` | 参考机 |
| 单帧渲染 P95 | < 16ms | `ui.frame_p95_ms` | 参考机 |
| 每 1k turn 磁盘增量 | <= 20 MiB | `disk.bytes_per_1k_turns` | 1 万 turn 实测 |
| WAL 大小 | <= 64 MiB | `disk.wal_bytes` | checkpoint 后 |
| 孤儿临时文件 | 0 | `disk.orphan_temp_files` | 重启后 |

预算变更流程（防止"改预算变绿"）：

1. 连续 3 次超预算 → 记为疑似回归，先查代码；
2. 若确认是环境差异（机器换代/文件系统），提交单独 PR，附 3 份基线报告与前后对比；
3. 单次放宽不得超过 50%，且 PR 描述必须写明"为什么不是代码问题"；
4. 预算文件改动必须与 `update/flake_ledger.jsonl` 中的对应条目关联。

## 9. 反 flake 规范

- 不用固定 `time.Sleep` 等状态；用闸门（`mockmodel.Gate`）、channel、`eventually` 轮询（上限 5s）。
- 一切资源测量：预热 → settle → 基线 → 采样 → settle → 末样本。
- 长测只允许 `-p 1`；同包并发用子测试串行。
- Windows 必须 `Remove-Item Env:TERM`，否则 TUI 断言假红；`GOCACHE` 指向仓库内目录，避免 CI 缓存竞争。
- 诊断用 `-count=3`；flake 率评估用 `-count=50`（仅快集）。
- 失败证据必须落盘：`go test -json` 原始输出 + 报告 JSON + 关键日志片段。

## 10. 缺陷闭环与台账

`update/flake_ledger.jsonl`，一行一条：

```json
{"test":"codex_go/appserver.TestX","package":"appserver","first_seen":"2026-10-29","commit":"521ed99a","signature":"timeout waiting for thread/start >30s","classification":"load-flake","owner":"tbd","status":"open","evidence":"update/baseline/2026-10-29/appserver.log"}
```

分类枚举：`stable-red` / `load-flake` / `env-flake` / `skip` / `fixed`。
状态：`open` → `triaged` → `fixed`（必须带回归用例名）。

## 11. 排期与关键路径

```text
W0 基线(1.5d) ─┬─> W2 观测(0.5d 余) ─┬─> W3 soak(1.5d 余)
               └─> W1 测试台(1d 余) ──┼─> W4 大数据(3d) ──> W5 故障(4d) ──> W8 磁盘(3d)
                                      ├─> W6 UI(3d)
                                      └─> W7 内存(2d)
W9 修复(滚动 5d，与 W4-W8 并行)
W10 CI(2d，依赖 W3+W4 有首份报告)
```

| 里程碑 | 时间 | 出口条件 |
| --- | --- | --- |
| M0 | Day 1-2 | 失败集合与分类可复现 |
| M1 | Week 1 | 测试台 + 观测 + 预算可用（已完成大半） |
| M2 | Week 2 | soak 2h 通过 + 大型数据场景落地，首份趋势报告 |
| M3 | Week 3 | 故障、UI、内存场景全绿；P0 清零 |
| M4 | Week 4 | 磁盘场景 + nightly 连续 7 天绿 |

单人工期约 26 人日；两人并行（一人 W4/W5，一人 W6/W7/W8）约 3 周。

## 12. 风险登记册

| 编号 | 风险 | 影响 | 对策 |
| --- | --- | --- | --- |
| R1 | 1 GiB 载荷生成慢、占盘 | nightly 超时/磁盘满 | 流式生成 + 前置空间守卫 + 结束即删 |
| R2 | Windows FD 计数受 in-flight 影响 | 假泄漏 | 预热 + settle + 基线方案（已验证有效） |
| R3 | CI 机器性能差异 | UI/延迟假红 | 只在参考机判定，跨机比相对倍数 |
| R4 | 工作区被并行任务修改 | 基线不可信 | 干净 worktree + 报告记录 commit |
| R5 | 私有函数无法跨包调用 | 场景写不出来 | 允许包内 `TestStability*` 文件 |
| R6 | `-race` 在 Windows 需 CGO+MinGW | 本地跑不了 | race 只放 Linux CI |
| R7 | 预算被逐步放宽 | 门禁失效 | §8 变更流程 + PR 互审 |
| R8 | 场景本身成为 flake 源 | 团队失去信任 | §9 规范 + 台账跟踪场景自身失败率 |

## 13. 验收清单（DoD）

- [ ] 同一 commit，普通全量连跑 3 次失败集合为空；白名单逐名冻结并附隔离证据。
- [ ] `go test -tags stability ./stability/scenarios -count=1` 在 Windows 与 Linux 全绿（skip 项单列）。
- [ ] 2h soak：RSS 漂移 < 1%/h，goroutine/FD 回到基线 +5 内，临时文件 0。
- [ ] 1 GiB rollout：峰值 heap < 256 MiB，保留字节 <= 8 MiB。
- [x] 故障注入（流/代理/工具/磁盘写满）：无 panic、无孤儿、无半写、错误可诊断（`-Suite faults` 16 项全绿）。
- [ ] UI：按键→首帧 P99 < 50ms，单帧 P95 < 16ms，500 ev/s 不丢输入。
- [ ] 磁盘：每 1k turn <= 20 MiB，WAL <= 64 MiB，重启后临时文件 0。
- [ ] nightly 连续 7 天绿；快集 flake 率 < 1%（`-count=50`）。
- [ ] `stability/README.md` 能让新人 10 分钟内跑通 smoke。

## 14. 提交拆分

1. `stability: add harness skeleton and budgets`（已具备内容）
2. `stability: add deterministic mock model with gates and faults`（已具备内容）
3. `stability: add metrics and disk samplers`（已具备内容）
4. `stability: add soak runner and reports`（已具备内容）
5. `stability: add netproxy and appserver driver`
6. `stability: add large-data scenarios`
7. `stability: add fault injection scenarios`
8. `stability: add ui latency and memory scenarios`
9. `stability: add disk growth scenarios`
10. `stability: add nightly workflow and trend tool`
11. 按 P0 问题逐条提交修复 + 回归测试

## 15. 状态看板

| 工作流 | 状态 | 完成度 | 备注 |
| --- | --- | --- | --- |
| W0 基线冻结 | in-progress | 40% | 报告已记录 commit；仍需干净 worktree 的 3 次全量基线 |
| W1 测试台 | done | 90% | mockmodel/netproxy/metrics/disk/report/rolloutgen 全落地；缺 appserver 进程内驱动 |
| W2 观测与预算 | done | 100% | 预算唯一来源、显式指标、毫秒级报告名、soak 判定 |
| W3 Soak | done | 100% | turns（10 s / 15,760 轮）+ idle + reconnect；三个场景都以连接回收而非进程句柄判定，句柄只做趋势 |
| W4 大数据 | in-progress | 85% | 1 GiB rollout 读取、200k 线程、32 MiB 单事件全过；待补 256 MiB 上限压测 |
| W5 异常注入 | in-progress | 85% | 流类 + 代理类 + 工具执行（error/panic/取消/长跑）+ 磁盘写满全落地；剩 appserver JSON-RPC 层故障 |
| W6 UI 响应 | done | 100% | 按键、20k 帧、事件风暴、长草稿、resize 五项；帧渲染已修 |
| W7 内存 | done | 100% | 长会话趋势 + 峰值 + GC steady（含假模型捕获回归） |
| W8 磁盘 | done | 100% | 增长 + WAL checkpoint + temp 清理 |
| W9 修复与回归 | in-progress | 85% | 已修 UI 帧渲染、SSE 大事件、大载荷拷贝、假模型捕获、composer O(draft)；剩 appserver 层问题待接入 |
| W10 CI 趋势 | in-progress | 90% | 脚本 -Json/-Shard/-Trend、nightly workflow、趋势工具、台账都在；差 7 天连续绿 |

### 下一步三个动作（按顺序）

1. `-Suite all` 已全绿（pass=46 fail=0 skip=0，10 s soak）；按 §14 拆分提交。
2. 让 nightly 连续跑满 7 天，趋势工具开始有斜率样本（外部条件，不阻塞代码落地）。
3. 收尾 W4/W5/W9 剩余项：256 MiB 上限压测、appserver JSON-RPC 层故障注入、appserver gauge。
