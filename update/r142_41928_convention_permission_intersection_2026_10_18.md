# `#41928` convention-aware permission intersection

- Rust 依据：`c4350b4ca2a5af7dced6784e8c36f2f2cdab2dd6`。
- approval broker 现在从 native cwd 或 file URI 推导 executor convention。
- requested/granted profile 中的 file-URI 路径先转成同 convention 的
  native 形式；foreign convention、malformed URI fail closed。
- `IntersectPermissionProfilesWithConvention` 的路径解析、glob
  materialization、containment、equal/overlap/dedup 使用 executor
  convention：POSIX 大小写敏感，Windows 折叠大小写并统一分隔符。
- `ShellRequest` 携带 `PathConvention`，app-server/exec turn 从 CWD 推导；
  `effectiveShellPermissionProfile` 与 internal-permission merge 使用
  `PermissionProfileWithAdditionalPermissionsForConvention`。
- 剩余 partial：apply_patch/extensions 尚未接收同一 context，symbolic
  tmp/project grant 的显式 fail-closed gate 仍在 broker 之外。

验证：

- `go test ./sandbox -run 'Convention|Intersect|NormalizeRequest' -count=1`
- `go test ./sandbox -run 'Convention|Intersect|NormalizeRequest|AdditionalPermissionsMaterialize' -count=1`
- `go test ./tool -run 'CarriesAllowedSymlinked|ShellExecutor' -count=1`
- `go test ./appserver -run 'ServerRequestBrokerIntersectsURIPermissions|ServerRequestBrokerIntersectsPermissions' -count=1`
