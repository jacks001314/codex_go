# V3语音协议整合验证（2026-11-14）

上一轮修复isKnownVoice漏掉V3后，本轮新增TestV3VoiceStartProtocolPreservesCatalogValues：对每个内置V3语音执行JSON启动请求解码→Normalized→session.update序列化，断言值保持；未知名称、空格名称、数字和布尔值必须在协议解码拒绝。这与配置层未知字符串忽略的行为有意不同。

go test ./realtime -count=1与go vet ./realtime均退出0，日志realtime_v3_protocol_full_2026_11_14.log、realtime_v3_protocol_vet_2026_11_14.log。

计划#61259状态更新：前轮三包整合已通过；动态cwd与真实项目配置链路已验证；V3专属语音校验遗漏已修复。不能继续使用旧状态中的cwd未接入/整合测试运行中。真实音频/跨平台认证仍未完成，整体同步未完成，未commit/push。
