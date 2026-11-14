# SPIFFE认证模式选择标记（2026-11-14）

上游upstream_61207.patch的ProcessEnvironment::has_marker新增identity_token_source、spiffe_audience、spiffe_id，socket不作为模式标记。

Go新增对应环境名常量及存在性字段，workloadReadProcessEnv读取存在性，hasMarker纳入三个OpenAI标记。合成结构测试验证每个标记选中模式且缺少federation rule时失败，不读取真实凭据值。

已有workload identity相关回归与新测试通过，go vet ./auth退出0，日志spiffe_markers_green/vet_2026_11_14.log。

这是来源选择的第一部分，不是完整SPIFFE支持：source值解析、file/spiffe冲突、endpoint/audience/ID校验、session fingerprint及Unix获取流程尚未接入；现有完整file配置加source时的分流仍待实现。整体同步未完成，未commit/push。
