# 订阅idle resume的live model/CWD响应

扩展现有集成回归：启动后直接更新ThreadExtras内存model/CWD而不改rollout，再以不同覆盖resume。修改前虽已丢弃覆盖，顶层response仍返回原rollout值；修改后在新增订阅前，用现有live settings覆盖model/provider/CWD/roots/service tier/effort，并同步Thread模型字段。

新增applySubscribedIdleResumeModelSnapshot仅在loaded、无active turn、有既有订阅者时执行。运行中响应继续使用active turn快照，冷恢复不走该路径。联合idle/running回归与vet结果见after/vet日志。

仍待权限/审批等live响应字段及无订阅idle关闭重建；本轮不声明完整#61136或整体同步完成。
