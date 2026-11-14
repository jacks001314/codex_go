# Windows helper glob 路径惯例修正

复核上一轮实现发现 InferredPathURI 会把 /hidden/*.env 当成 POSIX 绝对路径，而 Rust bind_windows_cwd_relative_deny_read_globs 使用明确的 Windows ToPathURI 判断。这会导致 Windows 当前盘根路径未绑定 policy CWD 的盘符。

改用 LegacyAppPathString.ToPathURI(ConventionWindows)。/hidden/*.env 和反斜杠根路径均通过 policy URI Join 绑定为 C:\hidden\*.env；已有盘符、UNC、家目录语义不变。新增两个样例；修改前 /hidden/*.env 回归失败，证据 helper_glob_convention_before_2026_11_14.log。修改后两包测试及 vet 见 after/vet 日志。

整体同步仍未完成：helper统一准备、平台隔离及其余上游差异尚待落实。现有全模块session29603仍运行，且起始时间早于最新helper修改，不能视为最终全量认证。
