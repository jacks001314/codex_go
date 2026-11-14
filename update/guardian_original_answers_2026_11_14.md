# #61185 原始已验证答案

修复弹性请求通过 RenderVerifiedAnswers 固定900-token渲染限制提前丢弃长答案的问题。新增 CollectOriginalVerifiedAnswers，保留原文和源顺序标签；弹性section构建改用此候选集，保留缺失证据提示，最终预算仍按整个答案分配。固定渲染保持不变。

新增回归覆盖同步与异步presentation，确认超过固定渲染限制的答案在分配前完整存在；现有whole-answer保留/丢弃测试一起通过。go vet ./state退出0。日志 guardian_original_answers_2026_11_14.log。

这不是#61185全部完成：保留指令/assistant原文、共享快照及图像归一化仍待完成。当前全量test session47498仍运行，且启动于本次生产修改之前，不覆盖本次修改；之后需要完整复验。整体同步及提交推送未完成。
