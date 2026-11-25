# macOS 原生对齐验证（darwin-align）

任务标签：Go 基线 e524c8d69、Rust 目标 3f65e9f1（由任务提供）。本节点按项目镜像取得 Go 文件，未直接验证 Git commit。所有修改已通过项目工具写回 project-1whyle5g4v99，未操作 D:\qax。

## 结果

- CGO=1 的 UserVerification 测试通过；默认工厂、nil-service fallback、默认 runtime router 均实际选择 `*darwinUserVerificationProvider` + `cgoDarwinUserVerificationBackend`。
- CGO=0 的反向选择测试通过，选择 `UnsupportedUserVerificationProvider`。
- appserver/doctor 指定测试通过。state、network 全包通过；sandbox 全包有 5 项失败，详见下文，未改为 skip。
- darwin/amd64 CLI 原生构建成功，产物是 x86_64 Mach-O。未执行 darwin/arm64 构建或运行。
- Go 内嵌 SQLite 实测 3.54.1（modernc.org/sqlite v1.62.0）；系统 sqlite3 为 3.37.0。
- 真实生物识别 readiness 不可用：`DeviceSupported=false`，`com.apple.LocalAuthentication/-6`，`Biometry is not available.`。readiness 测试真实 SKIP；没有声明 Secure Enclave 创建凭据或交互签名成功。

## 修改与 diff

1. `appserver/user_verification_macos_cgo_test.go`：将选择断言与硬件探测分离。选择测试覆盖工厂、nil fallback、默认 router 三入口并断言具体 CGO backend；独立 readiness 测试记录 typed error 和缺失前置条件，并在不可用时 skip。
2. `appserver/user_verification_platform_unsupported_test.go`：新增 `!darwin || !cgo` 测试，断言互补构建选择 unsupported provider。

只修改测试，无生产实现修改。`gofmt -d` 两文件无输出。可应用 diff：`provider-selection.patch`。测试选择不调用硬件/API；readiness 仅调用非交互探测。已有 software P-256/SEC1 导出测试不能证明 Secure Enclave 硬件成功。

## 环境与命令约定

原始环境见 `platform.log`。本机 macOS 12.4 / 21F79 / Darwin 21.5.0 / x86_64，Go 1.27.1 darwin/amd64，clang + Command Line Tools SDK，默认 CGO_ENABLED=1。

下列 Go 验证命令均在项目根执行，设置 `GOPROXY=https://goproxy.cn,direct`。默认 proxy.golang.org 连接超时（curl 10s），最初下载命令被中断后改用可达公共 proxy；依赖版本及 go.mod/go.sum 均未修改。个别重复的首次编译命令被中断后，使用顺序 runner 完整重跑；下列结果只列完成运行的输出。逐项退出码/命令环境见 `check-results.json`，runner 见 `run-checks.py`。

Go CGO 测试和 CLI 链接有原始警告：`object file (...) was built for newer macOS version (13.0) than being linked (12.0)`。命令均以实际退出码记录；本机通过不代表已完成所有 macOS 12 兼容性验证。

## 完成的命令与真实输出

### user-verification

```sh
CGO_ENABLED=1 go test ./appserver -run 'UserVerification' -count=1
```

```text
# codex_go/appserver.test
ld: warning: object file (/tmp/go-link-1730642882/go.o) was built for newer macOS version (13.0) than being linked (12.0)
ok  	codex_go/appserver	2.022s
```

### appserver-doctor

```sh
CGO_ENABLED=1 go test ./appserver ./doctor -run 'UserVerification|Desktop|Platform' -count=1
```

```text
# codex_go/doctor.test
ld: warning: object file (/tmp/go-link-338440442/go.o) was built for newer macOS version (13.0) than being linked (12.0)
# codex_go/appserver.test
ld: warning: object file (/tmp/go-link-1287458457/go.o) was built for newer macOS version (13.0) than being linked (12.0)
ok  	codex_go/appserver	3.154s
ok  	codex_go/doctor	1.041s
```

### native-selection-readiness

```sh
CGO_ENABLED=1 go test ./appserver -run 'TestDarwinUserVerificationNative(ProviderSelection|BiometricReadiness)$' -count=1 -v
```

```text
# codex_go/appserver.test
ld: warning: object file (/tmp/go-link-4092768245/go.o) was built for newer macOS version (13.0) than being linked (12.0)
=== RUN   TestDarwinUserVerificationNativeProviderSelection
=== RUN   TestDarwinUserVerificationNativeProviderSelection/factory
=== RUN   TestDarwinUserVerificationNativeProviderSelection/nil_service_fallback
=== RUN   TestDarwinUserVerificationNativeProviderSelection/default_router
--- PASS: TestDarwinUserVerificationNativeProviderSelection (0.04s)
    --- PASS: TestDarwinUserVerificationNativeProviderSelection/factory (0.00s)
    --- PASS: TestDarwinUserVerificationNativeProviderSelection/nil_service_fallback (0.00s)
    --- PASS: TestDarwinUserVerificationNativeProviderSelection/default_router (0.00s)
=== RUN   TestDarwinUserVerificationNativeBiometricReadiness
    user_verification_macos_cgo_test.go:49: DeviceSupported=false; BiometricsAvailable=biometric authentication is not available right now
    user_verification_macos_cgo_test.go:51: native error kind=unavailable reason=biometricsUnavailable message="biometric authentication is not available right now"
    user_verification_macos_cgo_test.go:54: native biometric verification unavailable: DeviceSupported=false; error=biometric authentication is not available right now; requires Touch ID hardware, enrolled biometrics, and an available local authentication session
--- SKIP: TestDarwinUserVerificationNativeBiometricReadiness (0.01s)
PASS
ok  	codex_go/appserver	1.422s
```

### sqlite

```sh
CGO_ENABLED=1 CODEX_EXPECT_GOOS=darwin CODEX_EXPECT_GOARCH=amd64 go test ./state -run TestNativeSQLiteArtifactPlatform -count=1 -v
```

```text
# codex_go/state.test
ld: warning: object file (/tmp/go-link-2782968964/go.o) was built for newer macOS version (13.0) than being linked (12.0)
=== RUN   TestNativeSQLiteArtifactPlatform
    sqlite_test.go:95: native artifact darwin/amd64 embeds SQLite 3.54.1
--- PASS: TestNativeSQLiteArtifactPlatform (0.00s)
PASS
ok  	codex_go/state	0.808s
```

### build

```sh
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o /tmp/darwin-align-codex ./cmd/codex
```

```text
# codex_go/cmd/codex
ld: warning: object file (/tmp/go-link-515904788/go.o) was built for newer macOS version (13.0) than being linked (12.0)
```

### nocgo

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go test ./appserver -run UserVerificationUnsupportedPlatformProviderSelection -count=1 -v
```

```text
=== RUN   TestUserVerificationUnsupportedPlatformProviderSelection
--- PASS: TestUserVerificationUnsupportedPlatformProviderSelection (0.00s)
PASS
ok  	codex_go/appserver	1.358s
```

构建退出码为 0；`file /tmp/darwin-align-codex` 输出 `Mach-O 64-bit executable x86_64`。产物暂存本节点 /tmp，交付的是源码补丁和验证证据，由 lead 合并/构建发布。

## state / sandbox / network 全包原生结果

```sh
CGO_ENABLED=1 GOPROXY=https://goproxy.cn,direct go test ./state ./sandbox ./network -count=1
```

组合退出码 1。完整输出见 `state-sandbox-network.log`（含失败用例生成的完整 Seatbelt policy）。关键原始行：

```text
ok  	codex_go/state	65.620s
--- FAIL: TestBuildCommandRunPlanRejectsSandboxedProfilesWithoutBackend (0.00s)
--- FAIL: TestBuildCommandRunPlanAppliesSandboxStateReadableRootsAsReadOnly (0.00s)
--- FAIL: TestNativeSeatbeltEnforcesWorkspaceAndMetadata (0.06s)
    seatbelt_darwin_test.go:49: run native seatbelt command: exit status 21
--- FAIL: TestBuildSeatbeltPolicyAllowsTrustedCodexHomeSymlinksLikeRust (0.00s)
--- FAIL: TestBuildSeatbeltPolicyProtectsResolvedSymlinkAncestorsLikeRust (0.00s)
FAIL
FAIL	codex_go/sandbox	4.686s
ok  	codex_go/network	34.874s
FAIL
```

失败只读诊断（没有改 sandbox）：

- `run_test.go` 的两个失败分支只特判 Linux/Windows，对其他平台期待 unsupported；本机实现实际已返回 macosSeatbelt 计划，因此断言过时。
- `TestNativeSeatbeltEnforcesWorkspaceAndMetadata` 返回 exit 21：测试把 outside 文件放在 `t.TempDir()`（当前 TMPDIR 未设置，Go 使用 /tmp）中，默认 scratch grant 允许 /tmp 写入；应审查测试是否需关闭 scratch grants 并核对实际产品策略。原始失败不应称作硬件或权限不可用 skip。
- 两个 symlink 测试在 /tmp 与 /private/tmp 的逻辑/解析路径比较时失败。使用 canonical TMPDIR 单独重跑二者通过，证据如下；这不覆盖或抹除原始全包失败。

```sh
CGO_ENABLED=1 GOPROXY=https://goproxy.cn,direct TMPDIR=/private/tmp go test ./sandbox -run 'TestBuildSeatbeltPolicy(AllowsTrustedCodexHomeSymlinksLikeRust|ProtectsResolvedSymlinkAncestorsLikeRust)$' -count=1 -v
```

```text
=== RUN   TestBuildSeatbeltPolicyAllowsTrustedCodexHomeSymlinksLikeRust
--- PASS: TestBuildSeatbeltPolicyAllowsTrustedCodexHomeSymlinksLikeRust (0.00s)
=== RUN   TestBuildSeatbeltPolicyProtectsResolvedSymlinkAncestorsLikeRust
--- PASS: TestBuildSeatbeltPolicyProtectsResolvedSymlinkAncestorsLikeRust (0.00s)
PASS
ok  	codex_go/sandbox	0.661s
```

## Darwin provider / doctor build tag 与路径检查

`build-selection.log` 是实际 `CGO_ENABLED=1 go list -e` 输出：appserver 的 CgoFiles 为 `[user_verification_macos_cgo.go]`，doctor 为 `[proc_pidpath_darwin.go]`，Error 均 `<nil>`；nocgo 和非 Darwin provider 文件被忽略。CGO=0 的选择输出另见 `nocgo-selection.log`，无 CgoFiles，appserver 选择 macos_nocgo，doctor 选择 proc_pidpath_darwin_nocgo。

检查的 Go 路径：user verification lifecycle lock 为 `$HOME/Library/Application Support/com.openai.codex/user-verification/<label>.lock`（0700/0600 测试通过）；doctor log root 为 `$HOME/Library/Logs/com.openai.codex`；安装探测按 `/Applications`、`$HOME/Applications` 搜索 ChatGPT.app/Codex.app；运行进程通过 proc_pidpath 并检查 bundle 的 `Contents/MacOS` 路径边界。现有 Darwin 指定测试通过，未发现需改动的明显 build tag 冲突或 Windows 分隔符问题。CGO=0 的进程探测明确保守返回 false。

限制：本镜像没有 Rust 源码树，直接访问 raw.githubusercontent.com 目标文件也超时；已经向 lead 发出所需 Rust 3f65e9f1 源文件请求，交付时尚未收到。因此不能声称已完成与目标 commit 的逐行对照。Go 中的历史 Rust 注释并不构成目标 commit 证据。另观察到 doctor 的 Info.plist 读取仅扫描 XML 字符串，binary plist/实体处理需结合 Rust 目标源码后另行验证，本轮未扩大生产修改范围。

## 原生生物识别证据与前置条件

为保留原始 OS error（Go backend 会映射为统一 typed message），从当前 `user_verification_macos_cgo.go` 提取原样 Objective-C preamble，附加只读 `canEvaluatePolicy` main，使用相同 framework 进行 clang 构建并执行。完整源见 `native-probe.m`；相关生产文件 SHA256 见 `source-sha256.log`。

```sh
clang -fblocks -x objective-c /tmp/darwin-align-native-probe.m -framework Security -framework LocalAuthentication -framework CoreFoundation -framework Foundation -o /tmp/darwin-align-native-probe
/tmp/darwin-align-native-probe
```

```text
provider C probe: DeviceSupported=0 BiometricsAvailable=0 domain=1 code=-6
LAContext: canEvaluatePolicy=0 biometryType=0 error.domain=com.apple.LocalAuthentication error.code=-6 error.description=Biometry is not available.
```

需要支持 Secure Enclave/Touch ID 的硬件、已登记且当前可用的生物识别、可交互的本地登录会话及可用的 Keychain；部署二进制还需具备其 Keychain 访问方式要求的签名/entitlement。当前 -6 只证明本进程无法使用生物识别，不能据此断定 SEP 物理不存在或仅缺少登记。没有进行真实 enrol/delete 或成功交互签名，也没有读取用户凭据文件。

## 未完成与风险

- Secure Enclave enrol → biometric authorize → sign → verify 全链路需有硬件/登记/会话前置条件的节点继续验证。
- Rust 3f65e9f1 目标源直接对照待 lead 提供可读源码；doctor binary plist 行为待该对照确认。
- sandbox 全包 5 项失败仍需负责该范围的 lead/Agent 处理；canonical TMPDIR 的补充通过仅定位其中两项环境差异。
- 未覆盖 darwin/arm64；macOS 13 编译对象与 macOS 12 链接目标警告需在发布工具链策略中处理。
- 未合并到 D:\qax；本轮补丁和证据均已交回项目，由 lead 合并。
