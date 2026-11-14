# SPIFFE #61207 环境隔离（2026-11-14）

精确上游6242064da0412b4c12176a2e53ef91afb333002d patch已获取（upstream_61207.patch）。完整功能要求Unix Workload API JWT-SVID获取、10秒超时、每次交换重连、身份/受众匹配、来源冲突检查与错误脱敏。目前Go仍为file-backed workload identity，本轮不宣称实现SPIFFE获取。

本轮落地独立协议差异：envutil的不可继承名单增加OPENAI_IDENTITY_TOKEN_SOURCE、OPENAI_SPIFFE_AUDIENCE、OPENAI_SPIFFE_ID；大小写不敏感。按照上游，SPIFFE_ENDPOINT_SOCKET不加入该名单。新增合成环境map回归证明三个变量移除、socket/PATH保留；修复前失败，修复后envutil全包及vet退出0。日志spiffe_environment_red/green/vet_2026_11_14.log。

现有模型可达执行路径复用envutil.ScrubMap，包括execserver，故无需在每处复制名单。本轮仅修改统一名单，没有读取真实环境值或凭据。

剩余：auth来源配置、Unix gRPC获取器、刷新与校验、原生Unix验证及文档。整体同步未完成，未commit/push。
