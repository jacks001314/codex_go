# Voice config/read 错误传播（2026-11-14）

沿用upstream_config_update_61259.rs明确的兼容边界：只忽略不支持config/read的RPC错误，其他错误传播。

远端settings读取现将连接/读取失败放入VoiceErr，已有启动及picker路径据此报告错误。localVoiceRequest保留RPC错误码并包装remoteRPCError，本地settings可区分不支持方法与内部失败；无效响应也报告错误。未配置本地router的原有缺省行为保留。

新增本地与真实WebSocket测试覆盖-32601容忍、-32603传播，远端同时验证picker。修复前内部失败被吞；修复后go test ./app -run 'TestVoice|TestInteractive.*Voice|TestRemoteVoice|TestRealtime|TestLocalVoice' -count=1及go vet ./app退出0。日志voice_read_errors_red/green/vet_2026_11_14.log。

最新app全包go test ./app -count=1 -timeout 8m已启动，日志app_after_voice_read_errors_2026_11_14.log，尚未收取结果。整体同步未完成，未commit/push。
