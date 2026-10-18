# Round 740: Linux voice runtime bundle audit

Rust 依据：`da20788df913189878ebca7f4963d8a363ee6bf2`（`#44714`）。

该提交为 Rust voice-host 增加 Linux 原生运行时 bundle，并从 plugin directory
查找/加载对应共享库。Go 的 voicehost 使用 Go 与 miniaudio 运行时，由 Go
工具链构建和启动，不存在 Rust `.so` bundle 或 plugin-directory 解析路径，
因此记为 `not_applicable`。

验证：

- `go test ./voicehost -run 'Runtime|AudioFormat|MiniAudio' -count=1`
