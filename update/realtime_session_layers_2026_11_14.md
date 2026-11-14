# Realtime session layers 与全量测试（2026-11-14）

上游 b05266d61b04efd4aa783316acf7b36476fe4066 的 config_manager.rs:293 调用 Config::rebuild_with_session_layers。core/config/mod.rs:2093 的 layer_stack_preserving_session 保留 SessionFlags；config_manager.rs:680 附近将 request_overrides 合并进 CLI overrides 构造配置。这表明 thread/start 的显式配置覆盖应保留，用户文件值则重新加载。

Go修复：effectiveRealtimeThreadConfig重载用户/项目配置后，重新应用threadRecordConfigOverrides，再提取voice。原测试将cove写入thread/start.config，却期望文件更新覆盖它，前提不符合Rust；现清空偏好测试只在文件中写voice，会话只覆盖version。新增会话cove优先于文件marin的断言，修复前失败，修复后通过。该结论修正上一轮报告对session overrides的未决判断；其余请求边界仍保留。

验证：go test ./appserver ./realtime -run 'TestRealtimeVoiceReload|TestV3ProviderVoiceDefaults|TestRealtimeV3VoiceValidation' -count=1，appserver 0.284s、realtime 0.035s通过。go vet ./appserver ./realtime退出0。日志realtime_session_layers_red/green/vet_2026_11_14.log。

前轮全量go test -count=1 -timeout 12m -json ./appserver已退出0，4080测试/子测试pass、20skip、0fail，484.652s。日志appserver_after_admission_fixture_2026_11_14.jsonl及_summary.json。该运行不含第119、120轮的realtime修改，不据此宣称最新树全量通过。

#61259仍需TUI启动/保存与目录行为核对，整体同步未完成，未commit/push。Go配置加载对更广泛CLI/managed层的完整保留并非本轮认证范围。
