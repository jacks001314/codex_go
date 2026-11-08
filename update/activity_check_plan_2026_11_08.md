# activity_check 最小接入方案（macOS lane 只读分析，2026-11-08）

结论先说：**钩子已经全在 `appserver/` 里，且都在同一个 Go 包里，不需要改 `runtime_router.go`。**
我这边的改动集中在 `appserver/user_verification_runtime.go`（约 40 行）+ 一个测试文件。
唯一可选的跨 lane 补丁是 1 行：`RuntimeRouter.Close()` 里给内嵌（in-process）连接补一次 `markConnectionClosed`。

## 1. Rust 侧要复刻的是什么

`codex-rs/app-server/src/user_verification.rs:96-101`：

```rust
let guard = native::UserVerificationRequestGuard::with_activity_check(move || {
    !connection.is_closed()
        && !cancellation.is_cancelled()
        && *auth_changes.borrow() == auth_revision
        && self::identity(&auth_manager).as_ref() == Some(&identity)
});
```

四个条件缺一不可，guard 的 `check()`（`user-verification/src/guard.rs:37-46`）在任何检查点返回
`Cancelled { reason: Interrupted, message: "the verification operation is no longer active" }`；
原生等待循环每 50 ms 轮询一次（`native_operation.rs:15-31`），取消时 `LAContext.invalidate()`。

Go 侧现状：`userVerificationRequestBinder.BindUserVerificationRequest(accountUserID string, cancelled func() bool)`
的第二个参数**就是** Rust 的 `is_active`，provider 在锁等待、生物识别等待、签名前都会调用它
（`user_verification_macos.go` / `uv_verify` 的 50 ms 轮询）。今天它只接了显式 cancel / 超时 cancel 的 token，
缺的正是连接、auth revision、identity 三项。

## 2. 现成钩子（全部已存在，只读使用）

| Rust | Go 钩子 | 位置 |
|---|---|---|
| `connection.is_closed()` | `(*RuntimeRouter).connectionClosed(id string) bool` | `appserver/runtime_router.go:3240`（写入口 `markConnectionClosed` :3225；`ConnectionClosed` :2832） |
| `*auth_changes.borrow() == auth_revision` | `router.authChangeTracker.Snapshot()` → `auth.AuthChangeState{Generation, OwnerGeneration, LoginGeneration}` | 字段 `runtime_router.go:467`，初始化 :1038，推进 :1324（`publishAuthSnapshot`），类型定义 `auth/change_state.go:13-27`（三个 uint64，可直接 `==`） |
| `identity(&auth_manager)` | `auth.ChatGPTAccountUserIDFromAuth(router.services.Account.AuthSnapshot())` | 已有封装 `userVerificationAccountUserID`（我 lane） |
| （可选）即时唤醒 | `(*RuntimeRouter).authChangedChannel()` :1445 / `authChangeTracker.Changed()` :166 | 不需要，见 §5 |
| 请求的连接 id | `request.normalizedConnectionID()`：`protocol.go:514`，空值归一为 `defaultRequestConnectionID = "default"`（:512） | stdio `stdio.go:240`、websocket `websocket.go:444` 会显式设置 |

`ConnectionClosed` 的现有清理链（`runtime_router.go:2840-2860`）已经包含 `releaseUserVerificationConnection`（:2855，
清 eligible 连接 + `CancelVerificationPending`），但**不会**动正在跑的 native worker。要补的就是这一段。
连接 id 复用是安全的：`rememberConnectionClientInfo`（:3095-3103）在 initialize 成功时 `delete(closedConnections, id)`。

## 3. 最小实现（草稿，尚未落地）

全部写在 `appserver/user_verification_runtime.go`：

```go
// userVerificationActivity is Rust's with_activity_check closure: the request is
// live only while its connection is open, its credential revision is unchanged,
// and the captured account-user identity still matches.
type userVerificationActivity struct {
	router       *RuntimeRouter // nil for direct service construction in tests
	connectionID string
	authState    auth.AuthChangeState
	identity     string
	token        *userVerificationCancelToken
}

func (a *userVerificationActivity) active() bool {
	if a == nil {
		return true
	}
	if a.token != nil && a.token.IsCancelled() {
		return false
	}
	if a.router == nil {
		return true
	}
	if a.router.connectionClosed(a.connectionID) {
		return false
	}
	if a.router.authChangeTracker != nil && a.router.authChangeTracker.Snapshot() != a.authState {
		return false
	}
	return userVerificationAccountUserID(a.router.services.Account) == a.identity
}
```

三处接线：

1. `handleUserVerificationRuntime`（我的文件）在构造 service 时捕获：
   `activity := &userVerificationActivity{router: r, connectionID: request.normalizedConnectionID(), authState: r.authChangeTracker.Snapshot(), identity: userVerificationAccountUserID(r.services.Account), token: service.cancel}`；
   cancel/ack 方法不捕获（Rust 也不为它建 guard）。
2. `service.cancellation()` 改为返回 `func() bool { return !activity.active() }`（原 token 判断下沉进 `active()`）——
   provider 的 `cancelled` 参数自动带上三项检查，**provider 代码一行不用改**。
3. `runNativeWork` 的"成功后交付前复检"（现有 `s.cancelled()`）与超时分支保持不变，只是 `cancelled()` 改读 `activity.active()`。
   这样对应 Rust `user_verification.rs:241` 的 `cancel.0.check()`。

## 4. 边界与坑（实现时别踩）

- **in-process（内嵌 TUI）请求**：`ConnectionID` 为空 → 归一为 `"default"`，而目前只有 stdio(:301)、websocket(:335)、
  remote_control(:130) 会调 `ConnectionClosed`，所以内嵌 TUI 断开这一路是 no-op。可选 1 行补丁（appserver lane）：
  `RuntimeRouter.Close()` 里加 `r.markConnectionClosed(defaultRequestConnectionID)`；不加也能靠 auth revision / 显式 cancel 兜住。
- **auth 比较要比整个 `AuthChangeState`**，不要只比 `OwnerGeneration`：Rust 比的是整个结构，普通 refresh 也会取消进行中的验证。
- **identity 用捕获值比较**：捕获为空时本来就落到 unsupported provider，不会进 worker。
- **不要在 `active()` 里做阻塞或有副作用的调用**（Rust 注释要求 nonblocking、不得弹窗）；`Snapshot()` 只取互斥锁，且在 50 ms 轮询点调用，可接受。
- **不要为 `authChangedChannel()` 起 goroutine**：Rust 本来就是轮询，起 goroutine 反而引入泄漏与停机顺序问题。
- 超时分支仍要 `token.Cancel()`（已实现），否则 `CancelOnDrop` 的等价语义会丢。

## 5. 测试方案（我可以直接补）

1. `TestUserVerificationActivityCancelsOnConnectionClosed`：router 注入阻塞 provider（现成的 `slowUserVerificationProvider` 扩展为
   记录 `cancelled()`），请求在途时调 `router.ConnectionClosed(connID)`，断言 provider 观察到取消且响应为 `cancelled/interrupted`。
2. `TestUserVerificationActivityCancelsOnAuthRevision`：请求在途时 `router.publishAuthSnapshot(other, nil)`，断言同上。
3. `TestUserVerificationActivityCancelsOnIdentityChange`：`Account.ApplyAuthSnapshot` 换 `account_user_id`，断言同上。
4. `TestUserVerificationActivityIgnoresOtherConnections`：关闭另一个连接不得取消（防误伤）。

## 6. 交付方式

等你确认"就这么接"之后，我在 macOS lane 内改 `appserver/user_verification_runtime.go` + 新增测试，
仍以 zip（完整文件 + SHA256SUMS + MANIFEST.tsv）回传；不碰 `runtime_router.go`、不碰 `app/`。
如果你更愿意自己改，上面 §3 的三处接线和 §4 的坑已经足够直接落地。
