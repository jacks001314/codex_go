# 图像请求 ID、schema bounds、Trusted Access 与 MCP 输出

本轮关闭：

- `346c4db7c2` Track image generation request IDs in analytics (#40714)
- `42624fd63b` Preserve bounds in reserved tool schemas (#40719)
- `62bfa41a83` Update the biology Trusted Access link (#40723)
- `02c9f83f74` Use the model info accessor for skill telemetry (#40735)
- `75cb7c903d` Preserve MCP tool output as content items (#40737)

## 结论与修复

- 图像生成/编辑响应头 `x-codex-imagegen-request-id` 已由 `turn` 层捕获，
  appserver 只把 ID 送入 analytics，不进入 item/rollout wire shape。
- `jsonschema` subset 之前会丢弃 `minimum` / `maximum` / `maxLength`。本轮补齐：
  - 保留精确的 `json.Number` 数值；
  - `maxLength` 作为唯一提示时推断 string；
  - 新增大整数、number、string bounds 回归测试。
- biology safety notice 已指向新的 ChatGPT Trusted Access 路由。
- skill telemetry 使用 turn 解析后的 model slug 输出 `model_slug` 标签。
- MCP tool output 通过共享 content-to-tool-output 转换保留 text/image/audio/
  reference 等内容类型。

## 验证

- `go test ./jsonschema -run 'Bounds|MaxLength|Normalize' -count=1`
- `go test ./turn -run 'ImageGeneration' -count=1`
- `go test ./appserver -run 'ImageGen|ImageGeneration|SkillInjection|SkillInvocation|SkillTelemetry' -count=1`
- `go test ./telemetry -run 'ImageGeneration' -count=1`
- `go test ./context -run 'ToolOutputParts|ContentItemKinds' -count=1`
