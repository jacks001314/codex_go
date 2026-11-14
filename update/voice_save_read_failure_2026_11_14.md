# Voice save 配置读取失败（2026-11-14）

沿用#61259 upstream_61259.patch 的save-and-confirm语义：配置确认失败必须报告已保存但无法确认，不可当作缺省配置成功。

app/voice.go此前调用interactiveRemoteRealtimeSettings，该函数吞掉config/read错误并返回空设置，导致成功误报。现在save在已建立的同一RPC连接读取ConfigReadResponse，错误返回VoiceSavedMsg{Saved:true,Err:...}，避免额外建立连接；正常覆盖检查保留。

新测试TestRemoteVoiceSaveReportsConfigReadFailureLikeRust使用真实WebSocket，config/batchWrite成功、config/read返回-32603。修复前Saved=false且Err=nil；修复后已保存但确认失败，并断言只有一次写入。相关voice/interactive/remote测试和go vet ./app均退出0，日志voice_save_read_failure_red/green/vet_2026_11_14.log。

前轮app全包session23204已退出0，完整结果见app_after_voice_picker_2026_11_14.log；该运行含第122轮picker/save修改，不含本轮确认错误修复。本轮修改有定向回归验证。

仍待核对旧服务不支持config/read时的兼容分支、本地读取错误及启动读取错误等路径；本报告不宣称全域认证。整体同步仍未完成，未commit/push。
