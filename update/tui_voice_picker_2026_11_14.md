# TUI voice picker/save #61259（2026-11-14）

上游patch对应tui/app/realtime_settings.rs及chatwidget/realtime_settings.rs：picker使用V3目录，缺省配置不猜提供方默认值；save只确认配置，不再查询目录。

已修改app/voice.go及voice_local.go：本地/远端picker取V3列表，Current保留空偏好；远端save移除目录RPC。保持选择器需要目录、目录失败报错的行为。

测试：本地V3目录用例、缺省偏好用例、真实WebSocket远端picker/save用例。远端服务返回V1与V3不同列表，保存阶段目录返回错误，验证保存成功、随后打开picker报错。旧实现三个picker断言失败；修复后相关voice/interactive/remote回归全部通过0.171s。证据tui_voice_picker_red_2026_11_14.log和tui_voice_picker_green_2026_11_14.log。go vet ./app退出0。

app全包go test ./app -count=1 -timeout 8m已启动，session23204，日志app_after_voice_picker_2026_11_14.log；当前尚未收取结果，不能宣称通过。

结合前两轮完成#61259启动、默认值、reload及picker/save主要改动；真实音频端到端、配置读取失败/未知值显示的更广泛兼容性仍未认证，整体同步未完成，未commit/push。
