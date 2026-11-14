# #61259整合状态（2026-11-14）

本报告为当前状态；plan后面的逐轮记录是历史快照，不应把其中“尚未实现”的旧描述当作当前状态。

已实现并通过定向回归及相关vet：V3模型gpt-live-1.1-codex；API默认marin/backend-api默认cove；请求限定的偏好重载及会话覆盖保留；TUI启动不查询目录；V3选择器与未设置偏好；保存不查询目录；读取错误、旧服务兼容、类型校验；有效值不一致报错并更新UI，读取失败保留旧UI。

证据串联：realtime_voice_boundary_2026_11_14.md、realtime_session_layers_2026_11_14.md、tui_voice_start_2026_11_14.md、tui_voice_picker_2026_11_14.md、voice_save_read_failure_2026_11_14.md、voice_save_legacy_2026_11_14.md、voice_config_types_2026_11_14.md、voice_read_errors_2026_11_14.md、voice_effective_selection_2026_11_14.md、voice_effective_ui_2026_11_14.md。

本轮启动最新整合测试：go test ./app ./realtime ./tui/tea -count=1 -timeout 8m，session72253，输出voice_integrated_2026_11_14.log，运行中。测试退出前不作为通过证据。

尚需核对：Rust读取有效配置传当前会话cwd，而Go voice入口多处使用ConfigReadParams{}；项目配置上下文可能不一致。真实音频/远端完整链路、全平台验证未完成。#61259不标记完全认证。

计划表同步纠正#61129状态：已有注册安装来源的真实Git首次plugin/list回归，配置marketplace/git-subdir入口仍待覆盖。整体同步未完成，未commit/push。
