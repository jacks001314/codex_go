# 嵌套只读规则审计

本轮未交付新的生产修复，已撤回未通过回归的试验实现与测试。

- Rust permissions.rs writable_roots 先用 matcher.can_write_path 过滤有效条目，再构造非写入子路径排除。
- Go sandbox/sandboxpath/enforcement.go writableRoots 仅收集 write 条目并装饰 metadata，不执行相同有效条目筛选。
- Linux linuxFilesystemPolicy.protectedReadOnlySubpaths 另从 read 条目生成只读挂载，因此不能仅凭共享 WritableRoots 推断 Linux 最终行为。
- Seatbelt 直接消费 ReadOnlySubpaths，但还有 profile → wire 的转换问题：用 file_system restricted/write(root)+read(root/docs) 解析，再执行 ResolveFileSystemPermissionsForEnforcement，docs 仍可写。应先定位转换时何处丢失条目，再修改共享策略，不能只在最终 roots 添加 carveout。
- 试验装饰函数导致现有测试发现较宽 Temp 根新增下级工作区 metadata 排除；必须审计宽根与窄根共同存在时的优先级。

失败日志：update/nested_read_enforcement_2026_11_14.log。撤回后 sandbox 测试日志：update/sandbox_after_nested_audit_2026_11_14.log。此缺口未解决，不表示 #61195 或整体同步完成。
