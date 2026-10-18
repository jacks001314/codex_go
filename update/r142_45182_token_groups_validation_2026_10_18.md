# Round 670：Windows sandbox token-group SID validation（#45182）

Rust 依据：`e61f3819007ad283b79417b85f44ab19cf1dd781`。

## 落地

- `tokenGroupsForToken` 先查询所需 `TOKEN_GROUPS` 大小，拒绝零大小或超过
  caller 上限的 buffer，再读取到 owned byte slice。
- `decodeTokenGroups` 在复制 SID 前校验：
  - `TOKEN_GROUPS` header 与 `SID_AND_ATTRIBUTES` stride；
  - group count 是否超出 buffer；
  - 每个 SID 指针是否位于 buffer 内且至少能容纳 8-byte header；
  - revision、subauthority count、SID 长度上限 68；
  - `IsValidSid`。
- 复制 owned SID bytes 并保留 attributes、顺序与重复项。
- `scanTokenGroupsForLogonSID` 改用该 helper；无 logon SID 时仍回退 linked
  token。

## 验证

```text
go test ./sandbox/windowssandbox -run 'TokenGroups|DecodeTokenGroups|LogonSID' -count=1
go test ./sandbox/windowssandbox -count=1
```

测试覆盖 owned SID 不别名源 buffer、顺序/attributes、truncated count、错误
revision、超长 SID、nil/out-of-buffer SID、caller size limit 与当前 token 的
logon SID。
