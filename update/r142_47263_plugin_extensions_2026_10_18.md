# Round 660：hosted plugin extensions in summaries（#47263）

Rust 依据：`a1f40f3f1326eff7c81b860a4f4e27c1be186e10`。

## 落地

- `fetchInstalledRemotePluginDetails` 请求
  `includeExtensions=true`，并把安装项顶层的 `extensions` 解析到
  `PluginSummary.Extensions`。
- `plugin.PluginExtensions` 覆盖 global/settings/thread/file entrypoints、
  quick action、settings tools、icons 与 search mention providers。
- 输入接受 Rust 支持的 snake_case 别名（如 `app_id`、`tool_name`、
  `quick_action`、`settings_entrypoints`），输出统一使用 camelCase。
- 未知 entrypoint type、未知 quick-action target、缺失必填声明会让整个
  `extensions` 变为 absent，但已安装插件本身仍保留在 inventory 中。
- `PluginSummary.MarshalJSON` 与 `cloneSummary` 保留并深拷贝 extensions，
  因此 live 与后续 cached `plugin/installed` 读取一致。

## 验证

```text
go test ./plugin -run 'PluginExtensions|SummaryMarshalsExtensions' -count=1
go test ./plugin -count=1
go test ./appserver -run 'Plugin' -count=1
go vet ./plugin ./appserver
```
