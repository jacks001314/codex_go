# SPIFFE平台构建边界（2026-11-14）

将仅runtime.GOOS==windows拒绝改为go:build unix/!unix常量选择，配置解析仅允许Unix；避免其他非Unix平台误进入socket获取。更新平台回归使用相同边界。

Windows相关auth回归及vet退出0；GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./auth退出0。日志spiffe_platform_tags_windows/linux/vet_2026_11_14.log。Linux仅交叉编译，未执行。

计划#61207从待核实更新为实际部分完成：环境过滤、来源冲突、ID/URI、gRPC、交换接入、指纹、重试已实现并有分层测试。真实Unix agent重启/HTTP交换/并发刷新用例已编写但未运行，不能整体关闭。参考spiffe_unix_integration_2026_11_14.md。整体同步未完成，未commit/push。
