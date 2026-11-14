# SPIFFE原生Unix整合用例（2026-11-14）

新增仅Unix编译的TestSPIFFEUnixExchangeRefreshAfterAgentRestart：真实Unix socket gRPC agent→生产resolveWorkloadIdentityConfig/交换分支→本地HTTP token endpoint；验证缓存不重复获取；关闭并在原路径重启agent，更新合成JWT；八个并发Refresh应合并为一次获取/交换，并返回第二代token。

当前Windows节点只完成GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./auth和go vet ./auth，均退出0。日志spiffe_unix_compile_2026_11_14.log、spiffe_unix_vet_2026_11_14.log。此为交叉编译/静态验证，**没有执行Unix测试，不构成运行通过证据**。可执行命令：go test ./auth -run '^TestSPIFFEUnixExchangeRefreshAfterAgentRestart$' -count=1。

原生执行仍待具备授权执行通道的Unix节点；整体同步未完成，未commit/push。
