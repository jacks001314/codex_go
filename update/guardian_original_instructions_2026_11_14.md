# #61185 保留指令原文准备

弹性section构建改用 OriginalRetainedInstructionSections，共用现有渲染与源顺序/角色分区逻辑，但不在收集阶段应用固定900-token限制。完整用户与assistant原文交给弹性分配；不完整记录仍产生原有缺失提示；固定渲染函数行为不变。

新增同步/异步presentation回归，验证超过固定限制的用户与assistant文本在分配前仍完整。相关RetainedInstructions/GuardianElastic/原始答案测试通过0.039s，go vet ./state退出0。日志 guardian_original_instructions_2026_11_14.log。

#61185仍未全部完成：请求级快照缓存、图像归一化和异步实际采样链路需要进一步验证。原全量go test session47498仍在运行，不覆盖这两轮新增生产修改。整体同步和提交推送未完成。
