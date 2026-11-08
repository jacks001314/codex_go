# 契约 scope 调整提案（2 条，2026-11-08）

**这是提案，不是改动**：`parity/contracts/manifest.json` 我没有碰，字段值在 `update/scope_adjustment_proposal_2026_11_08.json` 里，等你签名后自己灌。
基线：manifest `138 / 130 complete / 8 partial`，sha256 `3647d8df9ed129d2…`。

---

## 提案 1：`account-security-setup-appserver-fetch`（建议 A：收窄 scope）

### 事实

Rust 侧这条行为分在三处：

| Rust 文件 | 职责 | Go 有没有对应行为 |
|---|---|---|
| `app-server/src/request_processors/account_processor/security_setup.rs` | **取数 + 校验**（只允许与当前登录一致的 ChatGPT 凭据；`ClientRouteClass` 路由） | 有：`app/security_setup.go:19 interactiveLocalSecuritySetupReader`（`cache`？不——`chatgptapi/security_setup.go:55 GetSecuritySetup` 取数，`app/security_setup.go:68 securitySetupIdentity` 做身份绑定与复核） |
| `app-server/src/request_processors/account_processor.rs` | app-server 的 **RPC 装配** | **无**（Go 不经 app server） |
| `app-server/src/message_processor.rs` | app-server 的 **RPC 分发** | **无**（同上） |
| `app-server/src/request_processors/account_processor/workspace_routing.rs` | workspace 路由发现（与 security setup 无关，只是同目录） | 无（也不需要） |
| `app-server-protocol/src/protocol/v2/account.rs` | notice 的 wire 形状 | 有：`chatgptapi.SecuritySetupNotice`（`Valid()` 校验） |
| `tui/src/security_setup.rs` | 展示（stale identity / banner 优先级） | 有：`tui/tea` + 已登记的 `TestSecuritySetupStaleIdentityAndBannerPriorityLikeRust` |

也就是说：**行为已在，缺的只是「app-server 作为一个进程边界」**。而 Go 的 app-server 里 `rg -i securitysetup appserver/ protocol/` → **0 命中**，本条契约点名的正是这个 Go 不存在的边界。

### 提案（before → after）

```diff
 id: account-security-setup-appserver-fetch          # id 建议一并改成 account-security-setup-fetch（可选）
 goPaths:
-  ["app", "appserver", "chatgptapi", "tui"]
+  ["app", "chatgptapi", "tui"]
 rustPaths:
-  ["app-server/src/request_processors/account_processor.rs",
-   "app-server/src/request_processors/account_processor/workspace_routing.rs",
-   "app-server/src/message_processor.rs"]
+  ["app-server/src/request_processors/account_processor/security_setup.rs",
+   "app-server-protocol/src/protocol/v2/account.rs",
+   "tui/src/security_setup.rs"]
 verifier: 不变（两条）
```

**为什么不是简单删三条**：删完 `rustPaths` 会空，oracle（`TestAlignmentRustContractOraclePathsExist`）和语义都没了。上面三条是**行为真正的出处**（取数 / wire 形状 / 展示），删掉的三条是"app-server 进程边界"。我核过这三条在 pin `748ab55ca7` 上都存在。

### 签名后会发生什么

- 契约语义从「app-server 提供 security setup」变成「Go 在进程内提供 security setup」，与 Go 的实际架构一致；
- 现有两条 verifier 与三条 rustPaths 一一对得上（`chatgptapi` ↔ `security_setup.rs` / `account.rs`，`tui` ↔ `tui/src/security_setup.rs`）；
- 这条**可以立刻转 complete**（我没有代为判断，因为它依赖你先点头改 scope）。

### 备选 B（不推荐）：补 app-server 端点

最小改动：`appserver/protocol.go` 加 `MethodAccountSecuritySetupGet`、`appserver/runtime_router.go` 加 dispatch、新 handler 复用 `chatgptapi.GetSecuritySetup` + `app/security_setup.go` 的 eligibility、加 `TestAccountSecuritySetupGetMatchesInProcessReaderLikeRust`。规模 S，但纯为对齐一个 Go 不用的进程边界，收益低。

---

## 提案 2：`cloud-image-messages-and-draft-handoff`（建议：给真实承载文件 + 拆条）

### 事实

- **image 半**：Go 的承载点是 `cloudclient/turns.go:144 encodeAddUserMessageRequest`（只写文本 content item）+ `cloudclient/transcript.go`（解码侧同样只有文本）+ `cloudbackend/submission.go`（透传）。`rg -i image cloudclient/ cloudbackend/` → **0 命中**。
- **draft handoff 半**：Go 侧**没有任何承载**（`rg -i "draft" app/cloud*.go cloudclient/ cloudbackend/` 只命中无关文件）。

### 提案（before → after）

```diff
 id: cloud-image-messages-and-draft-handoff
 goPaths:
-  ["cloudbackend", "cloudclient"]
+  ["cloudclient/turns.go", "cloudclient/transcript.go", "cloudbackend/submission.go"]
 rustPaths: 不变（app-server-client/src/cloud_backend/requests.rs、cloud-client/src/items.rs）
```

同时**建议拆成两条**（拆与不拆都能落，取决于你）：

| 新 id | 内容 | 现在能否转 complete |
|---|---|---|
| `cloud-image-messages` | image content item 的编码/解码（`cloudclient/turns.go` + `transcript.go`） | 补上改动即可（见 closeout §6） |
| `cloud-draft-handoff` | draft 交接（Rust `app-server-client/src/cloud_backend/requests.rs`） | **不能**：Go 无承载面，建议先不登记，等 cloud lane 有了再立 |

若不拆：那这条就必须等 draft handoff 落地才能 complete；只做 image 半只会让契约更"半对"。

---

## 附：两条提案的机械改动（供你直接抄）

见 `update/scope_adjustment_proposal_2026_11_08.json`：

```json
{"proposals":[
  {"id":"account-security-setup-appserver-fetch","action":"adjust-scope",
   "before":{"goPaths":[...],"rustPaths":[...]},
   "after":{"goPaths":["app","chatgptapi","tui"],
            "rustPaths":["app-server/src/request_processors/account_processor/security_setup.rs",
                         "app-server-protocol/src/protocol/v2/account.rs",
                         "tui/src/security_setup.rs"]},
   "suggestedStatus":"complete-after-rename"},
  {"id":"cloud-image-messages-and-draft-handoff","action":"adjust-scope-and-split", ...}
]}
```

`rustPaths` 里三条我都用 `test -e` 在 pin `748ab55ca7` 上验过存在（`security_setup.rs`、`v2/account.rs`、`tui/src/security_setup.rs`），sign 完再跑 `TestAlignmentRustContractOraclePathsExist` 不会 MISS。
