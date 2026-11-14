# SPIFFE endpoint边界复核（2026-11-14）

读取上游锁定spiffe0.18.0的transport/endpoint.rs，保存spiffe_endpoint_reference.rs。上游拒绝unix根路径/，接受unix:/path及unix:///path，拒绝任何query/fragment（含空值）。

新增边界回归，修复前unix:///与unix:/被错误接受；newSPIFFEAssertionSource现拒绝根路径。两种正常绝对路径形式保持相同socketPath，空query/fragment继续拒绝。相关auth测试与vet退出0，见spiffe_endpoint_red/green/vet_2026_11_14.log。

原生Unix成功交换与重启刷新仍未验证，整体同步未完成，未commit/push。
