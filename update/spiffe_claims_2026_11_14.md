# SPIFFE JWT-SVID选择校验（2026-11-14）

依据upstream_61207.patch的select_assertion，新增独立selectSPIFFEAssertion：要求恰好一个SVID，复用大小限制和NUL/空值限制；JWT三段、base64url payload、subject的SPIFFE URI基础结构、字符串/字符串数组audience匹配及可选指定ID匹配。多结果明确拒绝，不自行挑选。签名、有效期和kid不作本地认证判断，留给token endpoint。

合成JWT测试覆盖无kid/无expiry、单/多audience、空响应、多响应、audience/ID不匹配、错误scheme、错误aud类型、损坏JWT。相关auth测试及vet退出0，见spiffe_claims_2026_11_14.log和spiffe_claims_vet_2026_11_14.log。

该函数尚未接入生产交换路径，SPIFFE ID完整语法还需与上游spiffe crate核对；Unix gRPC来源、URI配置、session指纹、获取/刷新仍待完成。整体同步未完成，未commit/push。
