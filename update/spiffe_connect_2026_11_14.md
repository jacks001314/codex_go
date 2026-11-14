# SPIFFE连接失败分类（2026-11-14）

上游spiffe.rs在transport::connect失败时返回SpiffeUnavailable，RPC返回错误才保留gRPC code。Go grpc.NewClient惰性连接，原实现将dial失败误归为SpiffeRejected(Unavailable)。

本轮跟踪dial成功状态（atomic），尚未建立连接或context结束时返回固定workloadErrSPIFFEUnavailable；已连接的RPC拒绝仍保留code并丢弃详细消息。新增合成dial失败测试，修复前失败、修复后通过，验证私有socket错误文字不会出现。

相关auth回归及vet退出0，日志spiffe_connect_red/green/vet_2026_11_14.log。此回归不替代原生Unix成功交换/重启验证。整体同步未完成，未commit/push。
