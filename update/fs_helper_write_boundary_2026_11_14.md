# Required helper 写入副作用验证

新增 TestFilesystemRequiredSandboxRejectsWriteWithoutMutation，使用真实临时文件与生产 writeFile 路径：明确断言空 restricted filesystem 的写操作 required=true，Windows sandbox disabled 返回 -32602；已有文件内容保持 original，不存在的目标不被创建。再切换为 unrestricted filesystem，同一请求入口正常写入 allowed，确认拒绝范围没有扩大到不需helper的操作。

将旧读测试改名为 TestFilesystemRequiredSandboxRejectsReadWhenDisabled，移除不可达的非Windows成功分支。测试明确Windows范围，没有以本机测试替代Linux隔离验证。

验证：go test -count=1 ./execserver 全包通过21.574s；go vet ./execserver退出0。日志 fs_helper_write_boundary_2026_11_14.log 与 fs_helper_write_boundary_vet_2026_11_14.log。

本轮补足上轮生产修复的可观察文件副作用验证。仍待helper根别名/独立Linux runtime路径、原生隔离和其余同步范围。整体未完成、未commit/push。
