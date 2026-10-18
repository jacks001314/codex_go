# `#53109` Handle broken pipes gracefully in plugin list output

Rust 依据：`a07c8ebd57feedc4cf5dfe854c8e9101de54186b`。

- `plugin list` 的 JSON 与 table 输出统一经过 `bufio.Writer`；
- 所有 `Fprintf`/JSON encode/flush 错误都会传播；
- `syscall.EPIPE` 与 `io.ErrClosedPipe` 视为成功（例如 `| head`），
  其他输出错误仍返回。

验证：

- `go test ./app -run 'PluginListBrokenPipeLikeRust|PluginListRemoteCatalogLikeRust|PluginRemoteAddAndRemoveLikeRust' -count=1`

新增覆盖：`TestPluginListBrokenPipeLikeRust`。
