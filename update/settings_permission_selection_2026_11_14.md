# 权限设置替换修复（#61134 部分）

Rust 1f1f6ebb6c156d5ab1d4745155f340b897b24f02 在 admission 将 legacy sandbox policy 转为规范权限配置并清除命名身份。核对Go发现 ThreadExtraService 更新 sandboxPolicy 后仍保留 ActivePermissionProfile，未来turn继续继承旧身份。

已修复：legacy设置清除命名身份；命名设置清除旧legacy字段。下一轮只在调用方未指定任何权限种类时继承设置，避免显式legacy与继承named（及反向）混用。保留既有不可变commit快照机制。

测试 TestSettingsPermissionSelectionReplacesPreviousKind 修复前失败：legacy selection retained named identity: :read-only。修复后与ThreadExtra/Settings权限及workspace相关回归通过0.595s；go vet ./appserver退出0，Master worktree diff --check退出0。日志 settings_permission_selection_before_2026_11_14.log 和 settings_permission_selection_after_2026_11_14.log。所有代码已同步Master。

剩余：#61134 canonical更新与现有deny-read/glob深度合并、preview不修改输入、CWD变化及durable持久化/恢复仍需验证。特别是 persistThreadSettingsUpdate 的提前返回与元数据逻辑没有覆盖纯sandboxPolicy更新，后续必须修复并验证，不能用本次内存修复代表完成。

MCP #61283审计：Rust新for_host保留thread_init（初始扩展数据）但不带thread_store；Go现有host上下文只保留threadID/source，插件投影仍取共享静态列表。未证明自定义初始数据隔离等价，此项保持部分完成。

appserver完整回归39925仍运行，该运行早于本次权限设置修复；不代表最新代码全包通过。整体同步、Linux原生验证及commit/push未完成。
