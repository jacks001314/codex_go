# #61134 当前交付与剩余契约

已交付的生产链路：legacy sandboxPolicy在commit前转为规范JSON；转换失败不发布内存设置；从已接受live快照、保存profile及当前命名配置取得deny；保持glob扫描深度；legacy/named身份互斥；规范profile写入metadata和rollout；冷恢复清除旧named身份；下一轮使用规范profile；连续未持久化更新保留live deny；恢复时managed deny写入实际launcher JSON。

可引用回归：permission_admission_after_2026_11_14.log（接纳错误）、legacy_settings_write_final_2026_11_14.log（store/rollout写入）、cold_resume_identity_after_2026_11_14.log（冷恢复身份）、permission_next_turn_after_2026_11_14.log（下一轮）、restored_canonical_denial_after_2026_11_14.log（规范JSON受管规则）、named_permission_admission_2026_11_14.log（尚未物化named配置）、consecutive_permission_after_2026_11_14.log（连续更新）。对应变更均已同步Master，定向测试及vet已通过。

尚不能关闭本项：
- 新请求为named permissions时，目前仅替换身份，尚未像legacy分支一样完整规范化并保留旧deny。
- 同一次CWD/environment变化与legacy转换的Git元数据保护、profile workspace roots清理与Rust契约仍须核实。
- 当前只保证转换错误在内存发布前；磁盘I/O失败的事务边界不等于Rust原子settings commit，需分析正确边界。
- 拒绝条目的特殊路径、不可便携snapshot与cold restore规则仍需与当前CWD/执行器配合验证。

最新根模块回归已启动：go test -count=1 -p 1 -timeout 12m -json ./...，session29603，日志go_test_latest_2026_11_14.jsonl。尚在运行，初期agent/agentboard包通过不代表全模块通过。测试期间未继续修改Go代码。

整体仍有MCP作用域、其它核心/TUI、历史partial/blocked清单、Linux原生验证及commit/push未完成。保持原认证基线，不把已拉取Rust HEAD当作已认证目标。
