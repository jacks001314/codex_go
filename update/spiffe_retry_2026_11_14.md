# SPIFFE错误重试分类（2026-11-14）

按upstream_61207.patch WorkloadIdentityError::is_transient，接入workloadErrorIsTransient：获取不可用/超时为临时错误；gRPC仅Unavailable、DeadlineExceeded、ResourceExhausted、Aborted、Canceled、Internal为临时错误。其他状态及身份歧义、无效断言不可重试。支持包装后的错误识别。

新增全部gRPC codes矩阵（直接/包装两种）、不可用和永久错误回归；修复前临时SPIFFE错误未识别，修复后通过。auth全包go test ./auth -count=1退出0（19.329s），go vet ./auth退出0，日志spiffe_retry_red/green/vet_2026_11_14.log。

仍缺原生Unix完整获取→交换→刷新/agent重启及并发验证，平台/URI兼容审计仍未完全结束。整体同步未完成，未commit/push。
