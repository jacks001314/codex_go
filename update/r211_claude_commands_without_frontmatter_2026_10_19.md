# #40643 无 frontmatter 的 Claude 命令迁移

Rust 依据：`a9e447a69dee4f2789dd8d8c776e314772c1f049`。

## 上游行为

Claude 命令迁移使用两套 profile：第一轮只接收带显式 description 的命令，
第二轮为缺少或无法使用 description 的命令派生
`Migrated source command \`<source-name>\``。两轮都先按归一化 skill 名去重：
同名存在多个来源时跳过。因此：

- 无 frontmatter、空 description、非标量 description 的命令均可进入回退轮；
- 同名候选中仅一个显式 description 时保留该候选；
- 仅多个回退候选、或多个显式候选冲突时全部跳过；
- `$ARGUMENTS` 等不支持的模板在两轮中都被过滤；
- 用户级与仓库级命令目录行为一致。

## Go 落地

`config/external_tools_import.go`：

- `externalCommandMigration` 增加 `FallbackDescription` 标记；
- `discoverExternalCommands` 不再要求 frontmatter/description，缺少可用描述时
  生成 source-name 回退描述；
- 同名候选按上述规则收敛：单候选保留，只有一个显式项时保留显式项，其余
  冲突跳过；
- 渲染路径继续应用 Claude/Codex 术语重写，因此回退描述中的产品词也与上游
  一致。

## 验证

- `go test ./config -run 'ExternalCommandsMigration|ExternalToolsMigration|ExternalCoreMigration' -count=1`
- `go test ./config -count=1`
- `go test ./parity -count=1`

新增 `config/external_commands_import_test.go`，覆盖无 frontmatter 迁移、
不支持模板过滤、回退描述、显式/回退同名冲突、用户级与仓库级两种 scope，
以及导入后再次检测为空。
