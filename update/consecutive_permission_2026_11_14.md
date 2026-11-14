# 连续权限接纳保留内存拒绝规则

复现两次未持久化更新的问题：初始named audit含deny，第一次接纳read-only；之后旧named配置移除deny，再接纳workspace-write。原实现重新读取旧命名配置，第二次profile丢失已接纳deny。

normalizeThreadLegacySettings现在优先选择Settings.normalizedPermissionProfile作为当前权限；只有尚无已接纳匿名快照才使用record和命名配置来源。所有转换都在设置发布前完成，规范快照仍复制保存。

新增真实store/config/两次commit回归，修复前second update discarded live denial，修复后与其它接纳/下一轮/恢复测试通过0.219s，go vet ./appserver与Master diff --check通过。代码及日志同步Master。

整体未完成：这仅修复连续legacy更新，named请求的全分支合并、CWD/git保护、I/O事务、MCP作用域及其它核心/TUI、Linux验证和commit/push尚未完成。最近完整appserver运行失败于已修正的MCP分析测试；没有声称最新全包通过，也没有正在运行的完整测试。
