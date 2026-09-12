# Rust 版 voice 实现原理 vs codex_go voice 实现现状（静态对照）

## 0. 方法与范围

- 基准：Rust 上游 `D:\qax\reagent\dev\git\codex\codex-rs`；
  Go 侧 `D:\qax\reagent\dev\codex_go`（对照时 HEAD `ca4e8c9`，工作区干净）。
- 方法：djalign 静态层（文件/模块映射 + 协议逐条比对 + 符号与常量核对）。
  本文是**静态对照**，未做真实设备/网络的动态差分。
- 依据：Rust `voice-host/README.md`、`voice-host/Cargo.toml`、
  `realtime-webrtc/`、`core/src/realtime_conversation`、
  `app-server-protocol/src/protocol/v2/realtime.rs`；Go `voicehost/`、
  `realtime/`、`appserver/realtime_*`、`app/voice*.go`、`tui/*voice*`。
- 归属：按 operator 指令，voice 相关**代码改动**归并行 voice 进程；本文只做
  对照与findings沉淀，不越权改动 voice 行为。

## 1. 结论摘要

两端是**同一套设计原理的同构实现**：`控制层父进程 + 独立原生 helper 子进程`，
控制面走同一套 stdio 帧协议，媒体面都是 `WebRTC + Opus(48 kHz / 20 ms / PT111)`。
Go 不是新设计，而是把 Rust 的 native 栈整体替换后按同构语义重写。

| 维度 | Rust | Go | 判定 |
|---|---|---|---|
| 进程模型 | `realtime-webrtc`(控制) + `voice-host`(bin) | `voicehost/` + `cmd/codex-voice-host` | 同构 |
| 控制帧协议 | u32 BE + JSON ≤128 KiB | 同 | 一致 |
| WebRTC | `webrtc`/`rtc` 0.20.3 | `pion/webrtc/v4` | 等价替换 |
| 设备 I/O | CPAL 0.18 | miniaudio(`gen2brain/malgo`) | 等价替换（需 CGO） |
| 重采样 | rubato 5.0 → 48 kHz | **无** | 缺口 |
| AEC/降噪/AGC | sonora 0.2 全套 | **无** | 缺口 |
| Opus | `opus` crate + GStreamer 解码 | 动态加载 libopus（purego/LoadLibrary） | 方式不同、目标等价 |
| 音频运行时 | 私有 GStreamer + 7 插件 | 无 GStreamer，只需 libopus | 简化替代 |
| 输入音频准备 | `utils/audio`（data URL/token 估算） | 无 | 缺口 |
| 默认开关 | `realtime_conversation` Stable/on | `StageRemoved`（默认关） | 漂移（voice-owned） |

## 2. 架构分层

```
TUI/app ──(app-server RPC thread/realtime/*)──► app-server
   │                                                 ▲
   └──(本地) spawn codex-voice-host ──stdio 帧协议──► helper 进程
                          helper ──WebRTC(oai-events + Opus RTP)──► 远端
```

两端一致的核心原则：

- **音频绝不走控制管道**（Rust `protocol.rs` 原文 "audio never crosses this
  pipe"）；父进程只交换控制、SDP/ICE 信令与电平。
- **helper 是独立进程**：父进程只解析物理包内的 helper，过滤子进程环境，
  独占其生命周期与回收。
- **双路径**：本地（helper 拥有麦克风/扬声器）+ 服务端（`thread/realtime/
  appendAudio` + websocket sideband）。

模块映射：

| Rust | Go |
|---|---|
| `realtime-webrtc/src/protocol.rs` | `voicehost/protocol.go` |
| `realtime-webrtc/src/client.rs` | `voicehost/host.go` + `manager.go` |
| `realtime-webrtc/src/session.rs` | `voicehost/session.go` |
| `voice-host/src/devices.rs` (+`device_buffers`) | `voicehost/miniaudio.go` + `audio_pipeline.go` |
| `voice-host/src/processing.rs` | **无对等（无 DSP）** |
| `voice-host/src/transport.rs` | `voicehost/transport.go` (Pion) |
| `voice-host/src/{audio_track,incoming,playback,playout,audio_sink}` | `voicehost/media.go` |
| `voice-host/src/runtime.rs` | `voicehost/runtime_package.go`（简化） |
| `core/src/realtime_conversation` | `realtime/realtime.go` + `history.go` |
| `codex-api/.../realtime_call.rs`、`realtime_websocket` | `realtime/transport.go` + `wire.go` |
| `app-server-protocol/.../v2/realtime.rs` | `realtime/realtime.go` + `appserver/realtime_*.go` |
| `tui/src/chatwidget/realtime*`、`bottom_pane/voice_strip.rs` | `tui/chatwidget/voice.go`、`tui/bottom_pane/voice_strip.go`、`tui/history_cell/spoken.go` |
| `utils/audio` | **无对等** |

## 3. 控制面协议（**高度对齐**）

| 项目 | 结论 |
|---|---|
| 帧格式 | u32 BE 长度 + JSON ≤ `MaxFrameBytes = 128*1024` — 一致 |
| SDP | ≤ 64 KiB、诊断脱敏 — 一致 |
| 握手顺序 | `hello/ready → initializeRuntime/runtimeReady → startTransport/offer → applyAnswer/transportReady → openDevices/devicesOpened → setAudioControls/audioControlsApplied → inspectAudio/audioState → close/closed` |
| 超时 | 握手 5s / runtime 30s / transport 20s / device 5s / close 5s — 一致 |
| build commit | hello 带精确 buildCommit，不匹配 fail-closed — 一致 |
| 退出阶段码 | `HelperExitStage` 数值同构 |
| 环境白名单 | systemroot/home/proxy/ALSA 等 — 一致 |
| 消息集 | **完全一致**：两侧都是 17 个变体（含 `transportTimedOut` / `inspectAudio` / `audioState`），无差异 |

## 4. 媒体面（核心差异）

已对齐参数：**Opus PT 111、48 kHz、单声道、20 ms 帧 = 960 样本、mute 发静音、
epoch/代际使静音不可回放**（Go `voicehost/media.go`）。

| 环节 | Rust | Go |
|---|---|---|
| 采集→编码 | 设备回调 → rubato 重采样 48k → sonora(AEC/NS/AGC) → 20 ms Opus | 设备回调 → 有界打包（480 样本块 / 32 块队列）→ libopus |
| 编码/解码 | `opus` crate 编码 + GStreamer 解码 | 同一 libopus 动态加载，编解码共用 |
| 接收→播放 | GStreamer 解码+重采样 → CPAL | libopus 解码 → `voicePlayout` 60 ms 抖动缓冲 → miniaudio |
| Linux ALSA | `linux_alsa.rs` 设 `ALSA_PLUGIN_DIR` | 无 |

已核验：Go `voicehost` 源码中**没有任何 resample / echo cancel / noise
suppression / gain 实现**；Rust `voice-host/src/processing.rs` 明确
`use rubato::...; use sonora::{AudioProcessing, EchoCanceller, NoiseSuppression,
GainController2}`。

## 5. app-server realtime 协议

- 方法：`thread/realtime/{start,appendAudio,appendText,appendSpeech,stop,listVoices}`
  — Go 侧字符串已核实（`realtime/*.go`）。
- 通知（Go 已核实）：`started / itemAdded / item/started /
  item/transcript/delta / item/completed / transcript/delta / transcript/done /
  outputAudio/delta / sdp / error / closed`。
- 历史 item 合约与 Rust `app-server-protocol/.../v2/realtime.rs` 一致：
  `RealtimeSessionStarted / TranscriptSegment / BemItemPromoted{WholeItem,
  InlineMarkdown, InlineVisualization} / RealtimeSessionClosed{Ended,Failed}`。
- 传输/版本：`v1/v2/v3`、`websocket | webrtc | existingCall`、BEM handoff —
  Go 都有。
- 缺口：`thread/realtime/start` 缺 Rust 的 `realtimeStartInstructions` /
  `realtimeEndInstructions` 两个 wire 字段（Go 用配置项
  `experimental_realtime_start_instructions` 替代，客户端语义不同）。

## 6. TUI 表现层

| 能力 | Rust | Go |
|---|---|---|
| voice strip / 静音控制 | `bottom_pane/voice_strip.rs` | `tui/bottom_pane/voice_strip.go` |
| 会话相位机/字幕/电平 | `chatwidget/realtime.rs` (~73 KB) | `tui/chatwidget/voice.go` (~30 KB) |
| 字幕历史 cell | inline | `tui/history_cell/spoken.go` |
| split-flap 动画字幕板 | `chatwidget/realtime_split_flap.rs` | 未实现 |
| 独立 realtime 设置面板 | `chatwidget/realtime_settings.rs` | 内联处理 |
| 语音快照矩阵 | 30+ 语音快照 | 以单元测试为主 |

## 7. 打包与运行时

| 项目 | Rust | Go |
|---|---|---|
| 准备脚本 | `third_party/voice/assemble_package.py`（GStreamer runtime.json + 7 插件 + helper） | `third_party/voice/prepare_opus.py` + `sources.json`（仅 libopus 1.5.2 + ninja） |
| 运行时校验 | 物理路径 + 逐文件 hash + 插件列表 | `InspectRuntimePackage`：helper + codec 存在性 |
| build identity | Bazel `STABLE_GIT_COMMIT` | `-ldflags` 注入 `buildCommit`（默认 `dev`） |

## 8. 发现（按优先级）

### 8.1 24 kHz 设备 / 48 kHz 编码链 不一致（**已修复**）

证据（Go，逐行核对）：

- `voicehost/hostmain.go:28` —
  `defaultSessionFormat = AudioFormat{SampleRate: 24000, Channels: 1, ...}`，
  且 `miniaudio.go:221/229` 打开采集/回放时直接使用它，
  `miniaudio.go:263` 把它写进 `config.SampleRate`。
- 编码/解码链却按 48 kHz 建立：`media.go:23 opusClockRate = 48000`、
  `media.go:25 voiceFrameSamples = 960`（注释即 "one 20 ms monaural frame"）、
  `opus.go` 用 `opusClockRate` 创建 encoder/decoder。
- 打包与计时同样硬编码 48 kHz：`audio_pipeline.go:22 audioBlockSamples = 480`
  （注释 "10 ms of 48 kHz mono"）、`audio_pipeline.go:195` 与
  `miniaudio.go:554` 均 `/ 48000`。
- 全流程**没有重采样**（见 §4）。

推论：若设备按请求交付 24 kHz PCM，则 960 样本实际是 40 ms，却被当作 20 ms
编码 → 远端 2× 变速/音高偏移；且 `audioPacker` 的时间戳按 48 kHz 步进，会以
半速漂移。Rust 正是用 rubato 把设备采样率归一到 48 kHz 来规避。

修法（采用方案 1，无重采样方案）：`voicehost/hostmain.go` 的
`defaultSessionFormat.SampleRate` 由 24000 改为 **48000**，让设备、打包、Opus、
RTP 全程 48 kHz；`voicehost/manager_test.go` 的 24000 断言同步改为 48000。
新增回归测试 `voicehost.TestDefaultSessionFormatMatchesOpusRTPPipeline`，锁定
`defaultSessionFormat.SampleRate == opusClockRate`、`audioBlockSamples == 10 ms`、
`voiceFrameSamples == 20 ms`，防止再次漂移。

> 仍建议实机（真实麦克风/扬声器）验证一次音频回环；静态不一致已消除。

### 8.2 `realtime_conversation` feature stage 漂移（voice-owned，未改）

- Rust：`features/src/lib.rs` — `key: "realtime_conversation", stage:
  Stage::Stable, default_enabled: true`（commit `3f59eb965a` #44921）。
- Go：`features/features.go:192` — `{Key: "realtime_conversation", Stage:
  StageRemoved}`（`DefaultEnabled` 缺省 false）。
- 影响：Go 端语义上"默认关闭"，需在 config 显式
  `[features] realtime_conversation = true` 才启用；与上游默认开启不一致。
- 归属：`update/plan_2026_09_12.md` 已记录 #44921 为 voice 进程负责的
  alignment 工作，本轮**未改动**，仅登记。

### 8.3 DSP 缺失（**已实现，见 §12**）

原先 Go 只有有界缓冲/打包/mute 时序，无重采样、无 AEC/NS/AGC。
本轮已补齐回声消除 / 降噪 / AGC（见 §12），重采样由"设备固定 48 kHz +
miniaudio 内部转换"覆盖（未单独实现 rubato 等价）。

### 8.4 输入音频准备（`utils/audio`）缺失

Rust 有音频 data URL 归一化、时长→token 估算、超限/不支持格式占位符与
LRU 缓存；Go 侧无对等实现。

### 8.5 原生依赖模型不同（已实测）

- `CGO_ENABLED=0 go build ./voicehost/` **失败**（`undefined:
  malgo.AllocatedContext` 等）；`CGO_ENABLED=1` 通过。即 helper 依赖 CGO。
- Rust helper 编译期不依赖原生库、运行期加载私有 GStreamer；Go 只有 Opus 是
  运行期动态加载（`opus.go` + `purego`/`LoadLibrary`），设备层 `malgo` 需 CGO。
  `opus.go` 注释的 "release builds stay CGO-free" 只对 codec 成立。

## 9. 建议下一步（djalign 闭环）

1. 由 voice owner 决策并验证 §8.1（这是潜在功能性缺陷，优先级最高）。
2. 决策 §8.3 的 DSP：纯 Go 补齐，或登记为平台差异契约。
3. 补 §8.2 的 feature stage（1 行 + 相关断言），使 `/experimental` 与默认
   行为对齐上游。
4. 把 voice 纳入静态门禁：`voicehost/protocol.go` ↔ Rust `protocol.rs` 双向
   roundtrip fixture；方法/通知名、退出阶段码做 manifest 快照。
5. 动态抽验（L2/L3）：mute 边界、静音保活、字幕时序，用录制-重放或
   `/voice` 黑盒场景。

## 10. L1 静态契约（本轮已落地）

新增 `parity/rust_voice_contract_test.go`，并在
`parity/contracts/manifest.json` 登记 3 条 contract：

| contract id | Rust oracle | Go | verifier |
|---|---|---|---|
| `voice-control-protocol` | `realtime-webrtc/src/protocol.rs` | `voicehost/protocol.go` | `TestRustVoiceControlProtocolSurfaceAgainstGo` |
| `voice-helper-exit-stages` | `realtime-webrtc/src/helper_exit.rs` | `voicehost/helper_exit.go` | `TestRustVoiceHelperExitStagesAgainstGo` |
| `thread-realtime-surface` | `app-server-protocol/src/protocol/common.rs` | `realtime/realtime.go` | `TestRustThreadRealtimeSurfaceAgainstGo` |

覆盖内容：

- 帧界 `MAX_FRAME_BYTES`（128 KiB）、SDP 界（64 KiB，含空/超限拒绝行为）；
- 17 个控制消息的**双向 round-trip**（大端 u32 长度前缀 + 逐类型编码/解码）
  与消息集对齐；
- 7 个 `GST_*` 运行时环境键序与取值（`GST_REGISTRY` 允许平台条件值）；
- 18 个 helper 退出阶段码（正向 code + `HelperExitStageFromCode` 反向）；
- 17 个 `thread/realtime/*` 方法与通知名。

## 11. 真机回环验证（2026-09-12）

环境（本机 Windows）：

- 输入：`麦克风阵列 (适用于数字麦克风的英特尔® 智音技术)`（default）
- 输出：`Speaker (Realtek(R) Audio)`（default）、`LEN LI2364 (HD Audio Driver for Display Audio)`

门控用例（`CODEX_VOICE_REAL_DEVICE=1`）：

| 用例 | 内容 | 结果 |
|---|---|---|
| `TestRealDeviceLoopback` | 真机采集 + 真机扬声器播放 1 kHz 音调，计墙钟/样本时长比 | ✅ PASS |
| `TestRealDeviceInputProbe` | 逐个枚举输入设备并采集，报告峰值 | ✅ PASS |
| `TestSelectedDeviceIDFailsClosedWithoutPanic` | 非门控：非法 device id 必须返回错误而非崩溃 | ✅ PASS |

实测数据：

- 采集：**48000 Hz**、480 样本/块；基线 9600 样本/208 ms（比值 1.040），
  播放期间 48000 样本/996 ms（比值 **0.996**）→ 墙钟与 `samples/48000` 一致。
  若设备仍按 24 kHz 而管线按 48 kHz，比值会接近 2.0；此结果确认修复有效。
- 扬声器：播放音调时 `speakerPeak = 24000`（振幅 12000 × 2），真实渲染成功。
- **声学耦合成立**：麦克风峰值由静默基线 **48 → 265**（约 5.5×），
  即麦克风确实拾取到了扬声器播放的音调。

### 11.1 回环过程中发现并修复的真实缺陷：选定 device id 触发 cgo panic

- 现象：`ListInputDevices` 返回的设备 id 再传给 `OpenInput`/`OpenOutput` 时，
  `malgo.InitDevice` **panic**：`runtime error: argument of cgo function has Go
  pointer to unpinned Go pointer`。默认设备（id 为空）不受影响，所以此前未被发现。
- 根因：`miniaudio.go` 用 `nativeID = &decoded; config.Capture.DeviceID =
  unsafe.Pointer(nativeID)` 把 **Go 内存**塞进 device config；`malgo` 的
  `DeviceConfig.toC()` 会把该 Go 指针放进一个 Go 内存里的 C 结构再传给
  `ma_device_init`，触发 cgo "Go pointer to unpinned Go pointer" 检查
  （`malgo.DeviceID.Pointer()` 本身用的是 `C.CBytes`，说明预期是 C 内存/固定内存）。
- 修复：
  1. `openDevice` 用 `runtime.Pinner` 固定 device id 内存，`defer Unpin()`；
  2. 新增 `initMiniAudioDevice` 包装 `malgo.InitDevice`，`recover()` 把后端
     panic 转成 typed error（对齐 Rust 的 fail-closed 语义，而不是让 helper 崩溃）。
- 回归：`TestSelectedDeviceIDFailsClosedWithoutPanic`（硬件无关）+ 门控
  `TestRealDeviceInputProbe` / `TestRealDeviceLoopback`。

### 11.2 仍存差异（真机验证后）

- 麦克风默认设备在静默时也接近 0 电平，是设备/环境特性，非代码问题。
- DSP（重采样/AEC/NS/AGC）仍缺；本次通过"设备与管线同频 48 kHz"规避了速率问题，
  但设备非 48 kHz 时依赖 miniaudio 内部转换，且没有回声消除。

## 12. DSP 缺口实现（2026-09-12）

新增纯 Go DSP 链路并接入媒体路径（`media.go`）：

```
解码后的播放音频 ──(render reference)──► AEC
麦克风 480 样本块 ──► AEC ──► NS ──► AGC ──► 20 ms Opus 编码
```

| 模块 | 文件 | 说明 |
|---|---|---|
| FFT | `dsp_fft.go` | 迭代 radix-2 复数 FFT/IFFT（1/N 归一化），供 AEC/NS 复用 |
| 回声消除 | `dsp_aec.go` | 10 ms/480 分块频域自适应滤波（PBFDAF）：FFT 1024、13 分区 ≈130 ms 尾长、跨分区共享功率归一化、步长 0.15（>0.22 发散） |
| 降噪 | `dsp_ns.go` | 512/256 sqrt-Hann STFT 谱减；噪声底快降慢升 + 帧能量语音门控（冻结） |
| AGC | `dsp_agc.go` | 目标 RMS 0.05、增益 [1/8, 8]、快攻(0.5)慢放(0.05)、0.98 限幅 |
| APM 编排 | `dsp_apm.go` | `processRender`/`processCapture`/`reset`（对应 sonora 的 render/capture 接口） |

接入点（`media.go`）：

- 播放：每个 480 样本解码块在入播放队列前喂给 `apm.processRender`（仅在扬声器未抑制时），作为回声参考。
- 采集：`pumpCapture` 的每个 480 样本块先过 `apm.processCapture`（AEC→NS→AGC）再组装 20 ms 帧。
- mute：`setControls` 在麦克风静音时 `apm.reset()` 并丢弃半包，避免跨边界历史泄漏。

实测（确定性合成信号，非门控）：

| 指标 | 结果 |
|---|---|
| AEC 回声抑制 | 回声能量降到 **17.79%**（衰减 ~82%） |
| AEC 差分（有/无参考） | 全链 47.7% vs 对照 100% |
| NS 稳态噪声 | 残留 **23.4%**（突发语音保留通过） |
| AGC | 弱语音抬升、强语音不削波 |
| 稳定性 | 全链输出有限值（无 NaN/Inf） |

与 Rust 的差异（诚实说明）：

- Rust 用 `rubato` sinc 重采样 + `sonora`(WebRTC APM)；Go 是**纯 Go 近似**，不是比特级等价。
- 重采样：Rust 显式把设备原生率归一到 48 kHz；Go 通过设备请求 48 kHz 让 miniaudio
  内部转换，未单独实现 rubato 等价的 sinc 重采样器。
- `setStreamDelayMS` 仅记录诊断值：Go 的分区自适应滤波在其尾长内自行吸收
  render/capture 延迟，无需显式对齐。

测试：`dsp_aec_test.go`、`dsp_stages_test.go`、`dsp_apm_test.go`。
