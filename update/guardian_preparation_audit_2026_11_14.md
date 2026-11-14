# #61185 Guardian 请求准备审计

已核对 Rust d4242793：原始根证据、保留指令、assistant上下文及已验证答案延迟到请求准备阶段裁剪；固定根视图缓存；新图像先归一化再去重和计费。

Go已有 prepareGuardianRootContextWithBudgeting，固定/弹性路径明确分开。新增 TestGuardianFixedPreparationKeepsRootOriginalsForElasticRetry 验证20条大文本根用户证据经fixed→elastic→fixed准备后：源切片不变，elastic仍保留所有完整原文，fixed输出稳定且受限。连同现有elastic根证据测试通过0.115s；go vet ./appserver退出0。

这只证明根用户证据的准备函数行为；未证明同步/异步请求共享原始快照、assistant/verified answers的完整调用链、固定视图缓存及新图像归一化。#61185仍部分，继续实施这些缺口。未声称全量同步完成。

全量go test ./... session47498仍运行，输出已推进到app包通过144.124s，appserver等尚无最终结果；不要重复启动全量测试。全量vet此前已通过。
