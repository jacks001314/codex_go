# #61134 关闭补齐：named 接纳与 CWD/legacy 归一（2026-11-14）

## 命名权限替换保留 live deny-read

复现：先以 legacy `read-only` 接纳（配置中带有 `secret` 的 deny 条目），再提交 named `danger-full-access`。修改前失败：

```
named admission lost deny-read restriction ...: []
```

根因：named 分支只替换 `ActivePermissionProfile` 身份，重新解析出 unrestricted 文件系统后直接写回，未并入已接纳的 deny 条目。Rust 在 `Session::apply_session_settings` 中先解析当前生效 profile（含身份推断），再用 `from_runtime_permissions_with_enforcement` 合并 deny。

修复：

- `appserver/runtime_router.go`：named 分支提交前调用新 helper 取得当前生效 profile，用 `sandbox.PreserveDeniedReads` 合并后重新序列化。
- `appserver/permission_settings_preservation.go`：优先使用 live normalized snapshot；否则从记录、CWD、workspace roots 与 legacy policy 重新解析；解析不可用（例如配置只定义 profiles 而未设 `default_permissions`）时保持旧行为、跳过保留。
- `sandbox/preserve_denied_reads.go`：`disabled` 目标 profile 在有旧 deny 时转化为 managed + restricted 文件系统，保留 deny 条目；网络保持 enabled（disabled 无独立网络限制）。
- 回归：`TestNamedPermissionAdmissionPreservesLiveDenialsLikeRust`、`TestPreserveDeniedReadsRetainsGlobDepthWithoutMutatingInputs`（新增 full-access 变体）。

## 同批 CWD + legacy 转换的 Git 元数据保护

Rust #61134 要求 legacy 转换使用更新后的工作目录，并保护通过 `.git` 指针指向的外部 git 目录。新增 `appserver/legacy_cwd_git_metadata_test.go`：

- `thread/settings.update` 同时提交 `cwd=next` 与 legacy `workspace-write`；
- 断言身份被清除、记录规范化 JSON；
- 下一轮以 new CWD 解析执行 profile，`ordinary-file` 可写，外部 `git-metadata/config` 与 `.git` 指针不可写（只读子路径出现在 `WritableRoots`）。

该测试首次运行暴露的是 Windows 临时目录可写这一既有约定（旧工作区位于 temp 下因此可写），不作为产品缺陷；断言范围收敛到 Rust 同款两项（新工作区普通文件、外部 git 元数据）。

## 事务边界审计

Rust `Session::update_settings_if` 在 state 锁内完成校验与内存发布，随后返回 `SessionSettingsCommit`，磁盘落地由 app-server 层在之后执行。Go 的顺序相同：转换/规范化错误发生在内存发布前，发布后再持久化与通知。因此“持久化失败不回滚内存”与 Rust 一致，不构成额外差异。

## 验证

- `go test ./appserver -run 'TestRuntimeRouterDispatchesThreadExtrasAndRealtime|TestRuntimeRouterPermissionProfileUpdateAppliesToNextTurnLikeRust|TestNamedPermissionAdmissionPreservesLiveDenialsLikeRust'`：通过。
- `go test ./appserver -run TestLegacyPermissionCWDChangeProtectsGitMetadataLikeRust`：通过。
- `go vet ./appserver`：通过。
- `go test ./sandbox ./appserver -count=1` 全包结果见 `named_permission_full_2026_11_14.log`。
