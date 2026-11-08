# mac-align-2 交付说明（macOS lane，按主干基线重打包）

基线：`mac_baseline_codex_go.zip`，20,105 字节，sha256 `eac9f9bc8030548f9b99289d066b91c020c462e779c898257c83c05d72a4cd82`（已校验）。
上一实例：mac-align / agent-32791ff849094dd77563f399（额度耗尽被回收）。

## 0. 关于基线更换

上一版我用的是自己镜像（project-7gj3ldep1ge2）当 base。收到主干基线后逐字节复核（两侧 LF 归一化）：

- 7 个 appserver/doctor 基线文件与我的镜像版本**完全一致**（`doctor/desktop.go` 均为 10155 字节），
  因此本版代码内容与上一版相同，只有 base 换成了你的 zip，DIFF_VS_BASE/ 已全部按新 base 重算。
- 旧工作区里 10308 字节的 docter/desktop.go 确实是被旧实例改过的版本，确认不能当基线。
- 我没有改动 `app/user_verification_local.go`、`app/user_verification_requests.go`（非本 lane），交付中不含这两个文件。

## 1. 关于 `appserver/user_verification_macos_test.go` 在你镜像里是否存在

**不存在。** 证据：`project_list(appserver, glob="user_verification_macos*")` 返回 `entries: []`；
我的镜像 appserver 下共有 9 个 `user_verification*.go`（`user_verification.go`、`_runtime.go`、`_test.go` 以及
`_desktop_activation/_mcp_reason/_meta/_metadata/_ownership/_worker_like_rust*_test.go`），没有任何 macos 专属文件。
所以 size / sha256 无从给出（N/A）——我的 scope 里没有带别的 mac 代码。

对 etag 拒收的解读：冲突记录里同时有 `expectedEtag=b5edac79b7507284` 与 `currentEtag=b246b88e1b0e4c67`，
带 expected 说明 Master 当时认为该文件已存在——最可能是旧实例先成功写入一次、随后带着旧 etag 重写，被判定为 etag 竞争。
这与"我的 scope 被污染"无关：交付的 `appserver/user_verification_macos_test.go` 内容 = 冲突副本 + 我的修订，
原始冲突副本原样放在 `conflicts/appserver/user_verification_macos_test.go`（对应目标路径 `appserver/user_verification_macos_test.go`）。

## 2. 旧实例结论不可采信（本机复现，均已修）

1. `CGO_ENABLED=0 GOOS=darwin go build ./appserver` 编译失败（build tag 重复定义 `newUserVerificationPlatformProvider`）。
2. 未 enroll 时 verify 必崩：C 侧对 NULL 执行 `CFRelease` → `SIGILL ... signal arrived during cgo execution`，栈顶 `uv_verify`。
3. status 在生物识别不可用时丢掉 credentialId（Rust 同时返回 credential + reason）。
4. 初始化激活门看注入 provider 而非平台探针（生产环境该字段 nil）→ 真机永不激活。
5. 超时不取消原生操作、成功结果交付前不复检取消。
6. 另有 fallback 标题应为空串、verify 应先 canEvaluatePolicy、title 不该 TrimSpace、SPKI 改由 crypto/x509 生成、doctor 两个探测改并发等。

## 3. 验证（用「你的基线 zip + 本交付文件」重建的完整树实跑）

```
go build ./...                                             → exit 0
CGO_ENABLED=0 GOOS=darwin  go build ./appserver ./doctor    → exit 0   （旧交付此配置编译失败）
CGO_ENABLED=0 GOOS=linux   go build ./...                   → exit 0
CGO_ENABLED=0 GOOS=windows go build ./appserver ./doctor    → exit 0
go test ./doctor ./appserver -run 'UserVerification|Doctor' -count=1
  ok doctor 1.099s / ok appserver 1.684s                    → exit 0
```

未覆盖：Secure Enclave / Touch ID 真实签名与生物识别弹窗（本机 Intel macOS 12.4，biometryType=0）。相关改动一律 partial。
