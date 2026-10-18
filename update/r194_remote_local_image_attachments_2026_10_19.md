# Round 751: portable local images for remote app servers

Rust 依据：`d38b5260a1b8af0884bfe1abdfd66c92c60ff510`（`#45499`）。

Go 的 remote TUI 提交前现在会：

- 读取本地图片附件；
- 通过 `http.DetectContentType` 确认 image 媒体类型；
- 编码成自包含 `data:<type>;base64,...` URL；
- 将 `localImage` 输入转换为 remote `image` 输入后再调用 `turn/start`；
- 文件读取失败或非图片类型在发请求前直接报错。

验证：

- `go test ./app -run TestRemoteTurnStartSnapshotsLocalImagesLikeRust -count=1`
- `go vet ./app`
