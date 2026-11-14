# SPIFFE来源冲突校验（2026-11-14）

依据upstream_61207.patch resolve_config，新增identityTokenSource值读取和分支：默认/file模式拒绝SPIFFE audience/ID存在；spiffe模式拒绝token file存在；其他来源（包括SPIFFE大写）拒绝。文件认证已有路径校验保留。

表驱动回归：默认文件+audience、显式file+ID、未知source、大小写错误、spiffe+file这五例修复前错误接受，修复后拒绝；默认file与显式file正常通过。auth全包go test ./auth -count=1退出0，go vet ./auth退出0。见spiffe_source_conflicts_red/green/vet_2026_11_14.log。

未完成范围：没有file的spiffe分支尚未构造专用来源，目前仍会进入缺少token file错误；endpoint/audience/ID解析、Windows不支持错误、session fingerprint、Unix gRPC获取/刷新仍需实现。不能将本轮视为可用SPIFFE认证。整体同步未完成，未commit/push。
