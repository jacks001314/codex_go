# `#53331` Finish remote exec cleanup when output streams remain open

Rust 依据：`7e9c061ceb388c67097acac069d6b7f8a60e5011`。

Go 的 remote unified-exec poll loop 之前只在收到 `process/closed` 时结束；远端
进程已 exit 但输出流保持打开时会一直等待。

本次修改：

- 记录 `process/exited` 通知时间；
- 在 exit 后保留 500ms close wait window，期间继续收集延迟输出；
- 窗口到期后执行一次非阻塞 `process/read` drain，然后结束进程并释放
  remote connection / exit watcher；
- `process/closed` 仍立即结束；
- sandbox-denied 与显式终止路径保持现有 finish 行为。

验证：

- `go test ./tool -run 'TestUnifiedExecConsumesPushedRemoteEventsAndRecoversReplayLikeRust' -count=1`

覆盖 `output-open-at-final-poll` 场景：exit 后 250ms 才到达的输出仍被聚合，
完成后不需要额外 read。
