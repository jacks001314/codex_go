# 与主干基线（mac_baseline_codex_go.zip）的逐文件比对

基线 zip sha256 = eac9f9bc8030548f9b99289d066b91c020c462e779c898257c83c05d72a4cd82（已校验一致）。
比对方式：两侧 `tr -d '\r'` 后逐字节 sha256（工作树是 CRLF）。

| 基线文件 | 基线字节 | 交付版本 | 结论 |
|---|---|---|---|
| appserver/user_verification.go | 11391 | 12377 | **有差异**：仅 `validateUserVerificationRequest` 的 title 判空由 `strings.TrimSpace(x)==""` 改为 Rust 的 `x==""`（`adapter.rs:21`）；其余内容（含旧实例新增的错误构造器）与旧交付一致 |
| appserver/user_verification_runtime.go | 9575 | 14153 | **有差异**：nil provider 时激活门回落到平台 `device_supported` 探针；`runNativeWork` 超时取消 token；成功结果交付前复检取消 |
| doctor/desktop.go | 10155 | 9950 | **有差异**：日期目录与日志文件名改用 `filepath.Join`（原为 `\` 拼接）；`desktopSecurityProbe` 改收 `*desktopApp`，安装探测失败时传 nil |
| doctor/desktop_darwin.go | 2727 | 2998 | **有差异**：`desktopApp` 记录 bundle 路径；`desktopApplicationRunning` 用 `proc_pidpath` 判断可执行文件在 `<bundle>/Contents/MacOS` 下 |
| doctor/desktop_security_other.go | 252 | 289 | **有差异**：build tag `!windows` → `!windows && !darwin`；签名加 `*desktopApp` 参数（darwin 有自己的实现） |
| doctor/desktop_security_windows.go | 10060 | 9724 | **有差异**：仅 `desktopSecurityEnforcement` 签名加 `*desktopApp` 参数并忽略它，行为完全不变 |
| doctor/desktop_test.go | 7950 | 10088 | **有差异**：`writeDesktopLog` 用 `filepath.Join`；darwin 上日志根期望改为 `$HOME/Library/Logs/<identity>`；新增 `TestDesktopChecksFindDarwinSessionLogsOnDisk` 端到端回归 |
| app/user_verification_local.go | 5979 | — | **未改动**（非本 lane），交付中不含此文件 |
| app/user_verification_requests.go | 4324 | — | **未改动**（非本 lane），交付中不含此文件 |

补充：7 个 appserver/doctor 基线文件在 LF 归一化后与我此前使用的镜像版本**逐字节相同**（`doctor/desktop.go` 两侧都是 10155），
所以旧工作区里 10308 字节的 desktop.go 确实是被旧实例改过的版本，不能当基线；本次差量与我上一版交付的代码内容一致，只换了 base。
本 lane 未触碰 `app/`；`app/user_verification_local.go` 的 `Available()` 仍按 `unavailableReason==nil` 判断（Rust TUI 用 `device_supported()`），
这属于 app lane，未改，仅记录。
