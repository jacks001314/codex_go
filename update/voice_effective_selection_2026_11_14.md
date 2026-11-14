# Voice保存有效值核对（2026-11-14）

上游upstream_tui_settings_61259.rs:250附近只有effective_voice == Some(voice)才报告已应用；None或其他值报告saved but not applied。

Go本地/远端此前仅在VoiceSet且不相等时拒绝，空或未知值被误报成功。现读取成功时要求VoiceSet且精确相等；不匹配标记Saved=true并报错。本地保存改为直接读取响应，保留写入status；旧服务不支持config/read仅在非okOverridden时使用保存值，其余错误传播。

测试覆盖null、未知、其他已知值、匹配值；本地额外覆盖旧服务ok/okOverridden及内部错误，远端使用真实WebSocket。修复前空值断言失败；修复后相关voice/interactive/remote/local测试及go vet ./app通过。日志voice_effective_selection_red/green/vet_2026_11_14.log。

前轮app全包session93812退出0，见app_after_voice_read_errors_2026_11_14.log；不含本轮修改。本轮有定向验证。整体同步未完成，未commit/push。
