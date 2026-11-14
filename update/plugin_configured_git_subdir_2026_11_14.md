# #61129 配置源 Git 子目录插件刷新与读取物化

## 缺口

上游 #61129 的首个完整 `plugin/list` 场景从有效配置派生非 curated 插件来源，并直接覆盖 Git 子目录插件。Go 侧此前存在三处问题：

1. `plugin/read` 读取配置 marketplace 中 `source: git, path: <subdir>` 的条目没有真实 Git 物化回归。
2. `StartConfiguredPluginCacheRefresh` 和单插件刷新入口把 `pluginDetailNeedsMarketplaceMaterialization` 当作刷新条件。该判断只在清单缺失时成立，导致正常已安装、缓存清单可读的 Git 插件永远不会检查新版本。
3. 版本替换后仍从旧 `PluginDetail` 克隆目录元数据，缓存内容已更新但 `version`/skills 仍是旧值。

## 实现

- `pluginDetailHasRefreshableGitSource`：只选择 `source_type` 为 `git`/`git-subdir` 且有 URL 的已安装插件；本地插件不进入网络刷新。
- `refreshMarketplacePluginVersionChecked` 与后台批处理筛选改用上述来源判断，已缓存插件也会进入 staging 下载和同版本比较。
- `reloadMarketplacePluginDetailAfterRefresh`：清空旧 `ManifestPath` 后走既有物化路径，从刚激活的树重新读取 manifest、skills 和版本，同时保留 installed/enabled/source 身份。
- 未改动 `pluginDetailNeedsMarketplaceMaterialization` 的原语义；它仍用于判断是否需要首次下载。

## 回归

`appserver/plugin_read_git_subdir_test.go` 新增两条真实 Git 用例：

- `TestPluginReadMaterializesConfiguredGitSubdirLikeRust`：临时 Git 仓库、`file://` 源、marketplace 条目带 `path: plugin`；读取前安装目录不存在，`RuntimeRouter` 处理 `plugin/read` 后 manifest 位于 `<installRoot>/installed-marketplace-plugins/debug/remote-sample/plugin/.codex-plugin/plugin.json`，且保留 skills。
- `TestFirstConfiguredPluginRefreshKeepsSameGitSubdirVersion`：同一仓库两个子目录插件，`stable` tag 从 1.0.0 发布到 `docs` 同版本改内容、`release-guide` 升到 1.1.0；配置派生刷新后 `release-guide` 内容为 `staging` 且目录版本为 1.1.0，`docs` 仍为 `production`。

红例证据：

- 还原生产修复后，首轮配置刷新用例失败：`configured Git subdirectory refreshes were not scheduled`。
- 保留筛选修复、恢复旧元数据克隆时失败：`refreshed catalog version = <old pointer>`。

## 验证

- `go test ./appserver -run "TestPluginReadMaterializesConfiguredGitSubdir|TestFirstConfiguredPluginRefreshKeepsSameGitSubdirVersion" -count=1`：通过 1.913s。
- `go test ./plugin -count=1`：通过 2.162s。
- `go test ./appserver -run "Plugin|plugin" -count=1`：通过 7.588s。
- `go vet ./plugin ./appserver`：退出 0。

明细见 `update/plugin_configured_git_subdir_2026_11_14.log`。据此 #61129 的配置源、真实 Git 子目录、同版本保留、强制刷新和首轮完整列表行为均已覆盖，计划表该项转为已完成。