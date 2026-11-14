# Voice配置类型契约（2026-11-14）

精确上游b05266d61b04efd4aa783316acf7b36476fe4066的tui/app/realtime_settings.rs configured_voice：null/缺省为None；字符串反序列化失败忽略；非字符串反序列化失败必须返回错误。源码保存于upstream_tui_settings_61259.rs。

Go RealtimeSettingsFromConfigValues增加VoiceErr：已知精确名称保留、未知字符串及带空格名称忽略，其他类型返回realtime.voice must be a string。该错误传递至startParams、实际启动（打开helper前拒绝）、本地/远端picker、保存确认。保存确认类型错误标记Saved=true。

新增类型矩阵（未知、空格、null、bool、number、array、object）并修正旧测试中非字符串被忽略的预期。red日志证明旧实现不符合契约；修复后go test ./app -run 'TestVoice|TestInteractive.*Voice|TestRemoteVoice|TestRealtime' -count=1及go vet ./app退出0。见voice_config_types_red/green/vet_2026_11_14.log。

本轮没有全包或音频硬件端到端验证。配置RPC错误的启动/本地传播仍需处理，整体同步尚未完成；未commit/push。
