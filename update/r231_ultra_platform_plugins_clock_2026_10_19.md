# Ultra fallback、executor OS、per-repo plugins 与 clock tools

本轮关闭：

- `#41206` Ultra reasoning fallback：按模型支持的推理等级回退。
- `#41207` executor OS：PlatformOS 进入 environment info、permissions context 和
  turn environment。
- `#41208` per-repository plugin configuration：catalog 请求按 CWD 读取有效的
  marketplace 配置。
- `#41210` clock tools：根据模型 metadata 与 effort/mode 决定 current-time /
  sleep 工具是否注册。

## 验证

- `go test ./model -run 'ReasoningEffortForRequestModelAwareUltraFallback|Ultra' -count=1`
- `go test ./appserver -run 'EnvironmentInfoReportsLocalPlatformOS|DecideClockToolEnablement' -count=1`
- `go test ./appserver -run 'Plugin.*Config|Marketplace.*Config' -count=1`
