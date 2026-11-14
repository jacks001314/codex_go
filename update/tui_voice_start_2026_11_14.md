# TUI voice startup #61259（2026-11-14）

上游依据：upstream_61259.patch 的 tui/app/realtime_settings.rs configured_voice 与 tui/app/thread_routing.rs：启动只读取配置偏好，未设置/未知voice传None，不再调用realtime_voices。提供方默认值留给后端选择。

Go app/voice.go 的 resolveVoice此前查询目录，并把未设置值设成DefaultV1，既可能被目录错误阻塞，也会覆盖API的marin默认值。现取消启动目录查询，未设置/未知值返回nil，已知显式voice直接传递。目录回调仍保留给设置选择器，不改变其错误策略。

app/voice_test.go将旧版默认值/目录失败契约替换为新上游契约；TestVoiceRuntimeStartsWithoutCatalogLikeRust通过startParams验证未设置、显式marin、未知值，目录回调若被调用立即报错。修复前两个测试失败（默认voice非nil、catalog unavailable）；修复后相关voice/interactive/remote测试全部通过。日志tui_voice_start_red_2026_11_14.log、tui_voice_start_green_2026_11_14.log。go vet ./app退出0（tui_voice_start_vet_2026_11_14.log）。

此轮仅完成启动路径。远端保存仍有目录查询、本地和远端选择器仍用V1目录并显示DefaultV1，待后续修正；尚未完成#61259，也未运行本轮app全包或真实音频端到端验证，未commit/push。
