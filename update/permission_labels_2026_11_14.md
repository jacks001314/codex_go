# #61136 权限标签对齐（部分）

核对 Rust d3fc4a73 / models.rs legacy_sandbox_policy_label 和 permissions.rs has_legacy_write_intent。新增 PermissionProfile.LegacySandboxPolicyLabel，以规范 runtimeJSON 分类 disabled、external、managed 全盘访问及写入意图；全盘访问且网络受限标为 external-sandbox。

sandbox 全包测试与 vet 通过，日志 permission_labels_2026_11_14.log。当前仅新增经测试的标签基础函数，尚未接入运行中 resume 的覆盖诊断：Go 没有直接对应 collect_resume_override_mismatches 的实现，不能标为完整同步。待补诊断调用、运行中恢复路径集成测试及网络受限全盘权限场景。整体同步、最终测试和提交推送仍未完成。
