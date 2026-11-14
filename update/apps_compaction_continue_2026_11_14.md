# #61213 Apps 使用说明压缩后继续回合

## 新增覆盖

`TestAppsCatalogGuidanceInTurnRequestAndHistory` 在原有“实际模型请求携带 catalog 说明并落入历史”和“压缩替换上下文保留说明”之后，把替换历史按生产转换写回线程记录，再用新的 Router 冷加载该线程并启动下一回合。

下一回合的模型输入必须恰好包含一次 `literal catalog guidance`：既证明压缩上下文没有丢失说明，也证明已发送说明不会被 world-state 重复插入。该用例同时覆盖了恢复后继续回合的切片路径。

## 验证

- 目标测试通过 0.52s。
- `go test ./appserver -count=1` 通过 253.767s，日志 update/apps_full_2026_11_14.log。
- `go vet ./appserver` 退出 0。

结合已有的 Apps gate 矩阵、checkpoint 缺失/旧 bool 恢复、原始历史回滚和 Rust JSON 指纹边界测试，#61213 的计划验收项已闭合，逐提交表转为已完成。明细见 update/apps_compaction_continue_2026_11_14.log。