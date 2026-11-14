# SPIFFE可选字符串语义（2026-11-14）

读取精确上游login/src/auth/workload_identity.rs，保存upstream_workload_auth_61207.rs。optional_unicode只检查Unicode转换，不trim，也不把空字符串变None；required_unicode才trim。

Go source/ID改为保留原字符串。显式空source、带空格file/spiffe、大小写错误均拒绝；Unix下显式空ID拒绝，未设置ID仍允许。缺省file行为保留。

Windows相关auth回归与vet通过（spiffe_optional_strings_2026_11_14.log、spiffe_optional_strings_vet_2026_11_14.log）；Unix显式空ID用例已新增，Linux交叉编译通过（spiffe_optional_strings_linux_2026_11_14.log），尚未原生执行。

整体同步未完成，原生Unix验证仍待完成，未commit/push。
