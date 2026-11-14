# Voice save 旧服务兼容（2026-11-14）

依据精确上游b05266d61b04efd4aa783316acf7b36476fe4066的tui/src/config_update.rs:458（保存在upstream_config_update_61259.rs）：config/read缺失只接受-32601，或-32600且消息同时含config/read与unknown variant/unknown method。

app/voice.go保存确认实现上述兼容分支；仅不支持读取且write status不为okOverridden时返回原保存结果。okOverridden返回已保存但无法确认覆盖状态，其余读取错误继续报告，避免上一轮修复破坏旧服务。

真实WebSocket表驱动覆盖missing method、旧enum、旧method、被覆盖、无关invalid request及内部错误。前三例修复前失败，修复后相关voice/interactive/remote回归通过；go vet ./app退出0。日志voice_save_legacy_red/green/vet_2026_11_14.log。

本轮不宣称整个#61259认证完成：本地/启动配置错误路径、未知配置值和真实音频行为仍待核对。整体同步未完成，未commit/push。
