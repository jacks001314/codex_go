# 规范权限执行解析修复

- 根因：PermissionProfile.runtimeJSON 保留原始条目，但 ResolveFileSystemPermissionsForEnforcement 和 enforcementEntries 直接调用 legacy wire 重建，丢失自定义 read 子路径，并继承 legacy 全盘读标志。
- 修复：执行解析优先读取规范 runtimeJSON；restricted 不借用 legacy 全盘读写标志；规范条目按有效权限筛选 write 根，将有效非 write 子路径保留为只读排除。旧式无 runtimeJSON 的 profile 保持原路径。
- 新回归 TestEnforcementKeepsCanonicalReadOnlySubtree 验证普通文件可写、docs 子树不可写、未扩大为全盘读写，以及 Seatbelt PROTECTED_WRITE 参数保留 docs。
- go test ./sandbox -count=1 通过0.099s；sandbox与appserver vet退出0。跨 sandbox/... 与 appserver 权限/执行解析/CWD回归退出0，appserver1.137s。见同名log及 canonical_enforcement_regression_2026_11_14.log。
- 验证边界：Windows主机上的策略生成和权限解析测试，未在macOS实际启动Seatbelt。嵌套write重新开放、符号链接/路径别名以及默认metadata覆盖的完整Rust语义仍需审计；不得声称全部权限对齐完成。整体62项同步及最终全量测试、Git提交推送仍未完成。
