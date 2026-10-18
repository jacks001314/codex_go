# `#46899` uniform completed-list spacing

- Rust 依据：`4ca8e7afff4803ca98fa4d0f94bcd7dd4ccf2646`。
- 流式 assistant/plan markdown 继续使用紧凑列表（`RenderWithThemeCwd`）。
- 完成的 assistant message、reasoning block 与 proposed plan 使用
  `RenderUniformListsWithThemeCwd`：按缩进、引用深度和列表类型分组；任一
  兄弟项跨多行时，为其余兄弟项插入一个分隔行。
- 嵌套单行列表保持紧凑；引用列表的分隔行为 `│`；代码块项与含段落项参与
  多行判定。间距在渲染后按当前宽度计算，resize 会重新分组。
- 分隔行插入不触碰 rendered 行内容，OSC 8 链接元数据保留。

- 同一列表项内的多个段落通过内部标题标记保留空行：渲染前在段落边界插入
  私用区字符标题，渲染后替换为分隔行，再由 uniform 间距统一处理。

reader position：

- 完成/中断重绘前记录 viewport 顶部第一个非空可见行及其相对偏移；重绘后
  在新内容中寻找最近的相同行并恢复偏移。找不到锚点时回退到旧 `YOffset`。
- 测试覆盖插入行后顶部可见文本不变，以及空行/缺失锚点回退。

结论：native scrollback 的 `AfterMultiline` 不适用。Go 的 inline transcript
仍由 app 重绘，没有 Rust 那种 terminal-owned markdown cell 渲染路径；
per-message cache key 已包含 text/width，列表间距策略按 surface 固定。

验证：

- `go test ./tui/... -count=1`
- `go vet ./tui/...`
