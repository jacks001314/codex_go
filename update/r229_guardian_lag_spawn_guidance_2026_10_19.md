# Guardian default lag 与 V2 spawn model guidance

本轮关闭：

- `#41158` Reduce Guardian V2's default tool-call lag：Go 默认值为 2，已与上游一致。
- `#41165` Require explicit requests for spawn model overrides：更新 V2
  spawn_agent 指引为“仅当用户明确要求不同模型时才设置 `model`”；V1 保持上游
  的旧文案。

## 验证

- `go test ./appserver -run 'Guardian.*Lag|Guardian.*Review' -count=1`
- `go test ./agent -run 'Spawn' -count=1`
