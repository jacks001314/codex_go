# leader 独立验证：`syncl4_39935c.patch`（r88m，2026-10-07）

对象：`update/r86_patches/syncl4_39935c.patch` — 14036 B / etag `0c5107f96b25aa9f`
（档 A：issuer 绑定被拒 ⇒ MCP OAuth login 直接失败、不回落 `<server>/oauth/authorize`；3 文件）
基线：`c9fbd10f56d75e011b193f2bc3ad6e195bde05a9`（sync659，Go `origin/main`）
工作树：`D:\tmp\leader_39935c`（`git worktree add --detach … c9fbd10f`，apply 前 `status --porcelain` 为空）
纪律：**0 commit / 0 push / 0 ref 移动**；全部在本机 scratch 工作树内；`C:\rw\codex-rs` 只读。

---

## 1. apply 与预处理

```
$ git apply --check --verbose <patch>
Checking patch mcp/api.go... / mcp/oauth_discovery.go... / mcp/oauth_issuer_binding_test.go...
exit=0
$ git apply <patch>        -> exit=0
$ git status --porcelain
 M mcp/api.go
 M mcp/oauth_discovery.go
 M mcp/oauth_issuer_binding_test.go
```
该仓 `core.autocrlf=true` ⇒ 落盘为 CRLF，已 LF 归一（`api.go` 去 CR 3059、`oauth_discovery.go` 857、
`oauth_issuer_binding_test.go` 376）。
归一后 sha256 与提交者报告**逐字节一致**：
`api.go e687b0aa72ec1886e80e4f50cf55985a459117a83585d951569c2379121abdc9`、
`oauth_discovery.go 0c7fc49c14bb5f4bf4b1488ca3b992beb1617e6c00e1b97485162196fc79342f`。

## 2. 静态门禁

```
$ gofmt -l mcp/api.go mcp/oauth_discovery.go mcp/oauth_issuer_binding_test.go   -> (空) exit=0
$ go build ./...        -> exit=0
$ go vet ./mcp/         -> exit=0
```

## 3. 整包对拍（本机）

```
$ go test ./mcp/ -count=1   -> ok  codex_go/mcp  13.668s   exit=0   （无 FAIL 行）
$ go test ./app/ -count=1   -> ok  codex_go/app  33.041s   exit=0   （无 FAIL 行）
```
> 比提交者报告更干净：其环境里 `./mcp/` 与 `./app/` 各有一条既有 env 型失败，本机两侧**全绿**。

## 4. 值级 RC（leader 自跑，逐条复现提交者的 FAIL 原文）

方法：对真实生产接线做**单行外科禁用**（先断言 needle 出现次数 == 1），跑新测试 → 期望值级 FAIL → 字节级还原。

| RC | 改动 | 结果 |
|---|---|---|
| RC-1 | `mcp/api.go:1913` `if mcpOAuthIssuerBindingRejected(err) {` → `if false && …` | FAIL `oauth_issuer_binding_test.go:337: OauthLogin() error = <nil>, want the unbound authorization server rejected` |
| RC-2 | `mcp/oauth_discovery.go:183` 同上改法 | FAIL `oauth_issuer_binding_test.go:358: OauthLogin() = (&mcp.MCPServerOauthLoginResponse{AuthorizationURL:"http://127.0.0.1:12238/mcp/oauth/authorize?client_id=client-1&…"}, <nil>), want the unbound authorization server rejected without a fallback URL`（**被猜出来的兜底 URL 直接可见**） |

两条 FAIL 文本与提交者报告**逐字相同**（含行号 337 / 358）。
还原后：
```
$ go test ./mcp/ -run TestMCPServiceOauthLoginFailsForUnboundAuthorizationServerLikeRust -count=1  -> ok 0.041s
$ sha256(api.go)=e687b0aa…  sha256(oauth_discovery.go)=0c7fc49c…   （与 RC 前备份一致）
```

## 5. parity（含环境对照）

```
$ CODEX_RUST_ROOT=C:\rw\codex-rs go test ./parity/ -count=1
--- FAIL: TestRustCollaborationModeTemplatesMatchGo   （go 9312 bytes vs rust 9184 bytes）
```
**判为环境（CRLF 检出）问题，非本补丁引入**，两条对照证据：
1. **对照实验**：把本补丁 3 文件**全部还原成 c9fbd10f 干净树**后重跑同一测试 ⇒ **同样的 FAIL**
   （`--- go (9312 bytes) --- / --- rust (9184 bytes) ---`）。
2. 冻结终点 `integ86g` 工作树（LF）上 `go test ./parity/ -count=1` ⇒ **ok 55.994s，无 FAIL 行**。
另：parity 与本补丁无因果关系（补丁只动 `mcp/`，失败测试比的是 plan 模板字节）。

## 6. 结论

**`syncl4_39935c.patch` 经 leader 独立验证：可落 `c9fbd10f`。**
apply 干净、静态门禁全过、`./mcp/`+`./app/` 全绿、两组生产接线各有**值级 RC** 且禁用后 FAIL 原文与提交者一致、
还原后字节一致；唯一红项已证明为 CRLF 检出环境产物（对照实验 + LF 树全绿）。
**是否落主仍待用户裁定（未决项 ⑦）。**

## 7. 现场与清理

- 保留：`D:\tmp\leader_39935c`（3 文件改动就位，供复核）、`D:\tmp\rc_bak`（RC 前备份）。
- 落主时可直接复用该树：`git commit -F <msgfile>` → push `main` + 强推 `integ86g`。
- 注意：该树为 CRLF 检出，提交前勿再改动其它文件。
