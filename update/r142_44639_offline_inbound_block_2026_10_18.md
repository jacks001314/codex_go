# Round 671：Windows offline inbound firewall block（#44639）

Rust 依据：`e25bedc166b54acadb505b587a387091097b2393`。

## 落地

- `firewallBlockRuleSpec` 新增显式 `direction`；既有 loopback/outbound 规则固定
  `netFwRuleDirOut`。
- 新增 `codex_sandbox_offline_block_inbound` rule，direction 为 inbound，
  protocol Any、local port Any、remote port Any、local addresses Any，
  remote addresses 使用同一 non-loopback 集合，LocalUser 限定 offline SID。
- `EnsureOfflineNetworkBlocks` 一次配置 outbound + inbound 两条规则；
  `EnsureOfflineOutboundBlock` 保留为兼容入口并委托新函数。
- setup 调用新函数，失败信息改为 “ensure offline network blocks failed”。
- uninstall 列表已预留 inbound rule name，无需新增删除路径。

## 验证

```text
go test ./sandbox/windowssandbox/bin/setup_main/win -run 'Firewall|OfflineNetwork|BlockedLoopback' -count=1
go test ./sandbox/windowssandbox/bin/setup_main/win -count=1
go test ./sandbox/windowssandbox -count=1
```

测试校验规则名、direction、Any protocol、non-loopback remote addresses 和
offline SID scope。真实的 ActiveStore firewall inspection 仍需 elevated
integration；当前未声称执行了提权集成测试。
