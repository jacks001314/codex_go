# 全仓库构建与测试核验（2026-11-14）

## 命令与结果

```
go build ./...                                        # 退出 0，无输出
go test ./... -count=1                                # 141 ok；codex_go/keyring 失败（见下）
go test ./... -count=1（复跑）                         # 140 ok；codex_go/doctor、codex_go/exec 以 0xc0000005 退出
go test ./... -count=1 -p 2                           # 退出 0；142 ok，无 FAIL
```

日志：`update/full_repo_test_2026_11_14.log`、`update/full_repo_test_2026_11_14_rerun.log`、`update/full_repo_test_2026_11_14_p2.log`。

## 两次偶发失败的分析

1. `codex_go/keyring` `TestOSKeyringRoundTrip`：`Delete = false, <nil>`，即 Save/Load 成功后 `CredDeleteW` 返回 `ERROR_NOT_FOUND`。单独 `-count=30` 全部通过，与 `auth`/`install` 同跑也通过；该测试使用 pid + 纳秒时间戳生成唯一目标名，不存在跨进程同名删除。判断为本机并行负载下 Windows 凭据管理器的瞬时不一致，而不是 Go 侧映射缺陷。
2. `codex_go/doctor` 与 `codex_go/exec`：测试二进制以 `exit status 0xc0000005`（ACCESS_VIOLATION）退出。两者单独运行分别 2.837s / 16.067s 通过；`-p 2` 全量运行通过。判断为本机同时启动大量测试二进制与子进程时的环境性问题。

## 结论

- 仓库可全量构建；降低测试并行度后全仓库测试通过（142 包）。
- 高并行下存在与本机环境相关的偶发失败，未观察到与近期提交（#61172/#61198/#61276/#61185）相关的失败。
- 该偶发性风险保留记录，后续如在上游 CI 复现，再按凭据管理器/进程托管方向单独排查。