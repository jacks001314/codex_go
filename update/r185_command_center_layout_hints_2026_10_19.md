# Round 742: command-center layout, metadata and hints audit

## `#46838` Refine the agent command center layout and metadata editing

Rust 依据：`569888ac65e124ca752f6a0937b8722d245651eb`。

Go 的 command-center renderer 已覆盖：

- header 显示当前 grouping；
- search/rename editor 位于任务列表上方；
- metadata 单行编辑并按 grapheme 边界截断；
- footer notice 存在时替代普通 action hints。

验证：

- `go test ./tui/agents_overview -run 'CommandCenter|Footer|Cancel|Rename|Layout|Metadata' -count=1`

## `#46840` Simplify agent command center hints and back navigation

Rust 依据：`724cdfbd32d35fde9e0da1f1e141b836836f9a65`。

Go 的 `footerHintRows` 按 keymap 与窄屏宽度打包完整 hint 行；Esc/back 先取消
rename/search，再按 standalone/embedded 语义退出或返回。selection 与
worktree/action hints 保持一致。

验证：

- `go test ./tui/agents_overview -run 'Footer|Cancel|Hint|Selection' -count=1`
