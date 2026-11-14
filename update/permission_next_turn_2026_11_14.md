# 接纳权限快照传入下一轮

已复现legacy模式传递丢失限制：记录含deny glob和glob_scan_max_depth=2，接纳read-only后通过内存Settings准备下一轮，原实现仅传read-only字符串，解析结果无拒绝条目。失败日志permission_next_turn_before_2026_11_14.log。

修复Settings保存接纳时规范JSON，cloneSettings复制字节，新的命名选择清除此匿名快照。下一轮在无显式权限值时解析并传入PersistedPermissionProfile，让现有解析入口继续校验当前requirements；显式turn sandbox或named权限优先。测试覆盖真实store记录、commit设置、未来turn构造及最终profile解析，并断言deny与扫描深度都保留。

相关回归通过0.996s，go vet ./appserver和Master diff --check退出0。代码和日志已同步Master。当前全包会话84456仍运行，启动早于此修改，不可当作最新代码全包验证。

边界：当前拒绝规则来源仍是已存metadata profile；实时命名配置、CWD变化完整语义、I/O失败事务边界及恢复时不便携profile限制等仍需对齐，#61134整体未关闭。MCP作用域、其它核心/TUI、Linux验证、commit/push仍未完成。
