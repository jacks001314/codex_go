# Round 720: OTLP metrics exporter analytics audit

`#52278` 要求 analytics consent 只控制内置 statsig metrics exporter；显式配置的
OTLP HTTP/gRPC collector 在 analytics 关闭时仍继续导出，`metrics_exporter =
"none"` 才关闭 metrics。

Go `otelinit` 已实现该语义：

- `analytics.enabled=false` 只让 statsig route 失效；
- OTLP HTTP/gRPC exporter 不受 analytics 影响；
- `none` 不构建 metrics client；
- 未设置 analytics table 时使用调用方 default，但同样只影响 statsig。

验证：

- `go test ./otelinit -count=1`
