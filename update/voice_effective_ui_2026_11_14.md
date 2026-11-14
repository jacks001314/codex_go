# Voice保存后的有效UI状态（2026-11-14）

上游upstream_tui_settings_61259.rs先set_realtime_voice(effective_voice)，再判断是否等于保存值；被覆盖或空偏好仍更新UI。

VoiceSavedMsg新增可选EffectiveVoice：nil表示未确认，空字符串表示已确认无偏好。本地/远端保存成功读取配置后携带有效值；TUI在显示错误前更新已确认状态。读取失败继续保留旧值。

新增TUI回归覆盖覆盖值及空偏好，修复前失败；远端真实WebSocket确认测试增加返回有效值断言。go test ./app ./tui/tea -run 'TestVoice|TestModelVoice|TestInteractive.*Voice|TestRemoteVoice|TestRealtime|TestLocalVoice' -count=1通过（app0.731s，tui/tea0.215s），go vet ./app ./tui/tea退出0。见voice_effective_ui_red/green/vet_2026_11_14.log。

整体同步尚未完成，本轮未运行全包/硬件音频验证，未commit/push。
