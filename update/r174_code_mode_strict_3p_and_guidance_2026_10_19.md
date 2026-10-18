# Round 731: strict third-party deferral and Code Mode guidance audits

## `#50687` strict third-party deferral

Rust 依据：`58ae3ba61186c39b849a6ebe60e60f4b11690373`。

Go 实现：

- `tool.Spec` 增加内部 `ThirdParty` 标记；MCP/app 与 client-supplied dynamic
  tool 在注册时置位，内置工具保持 false。
- 仅当 feature `code_mode_only_strict_3p_tools` 开启且有效 tool mode 为
  `Code Mode Only` 时执行 strict finalization。
- 非 Hidden 第三方工具统一改为 `discoverable`，覆盖 direct/code-mode-only、
  tool-search 与 omit 暴露设置；不会把 Hidden/预算超限/策略拒绝的工具复活。
- deferred 工具仍出现在 `ALL_TOOLS` 并可通过 `exec` 调用；内置同名工具不被
  dynamic collision 改写为第三方工具。

验证：

- `go test ./tool -run TestRegistryEnforceStrictThirdPartyToolsLikeRust -count=1`
- `go test ./turn -run TestBuildToolRegistryStrictThirdPartyToolsLikeRust -count=1`
- `go test ./config -run TestCodeModeOnlyStrictThirdPartyToolsLikeRust -count=1`

## Existing Code Mode audits

- `#50464` / `a0bfaac70c`：`incremental_tools` feature key 已注册、默认关闭，
  并接入 Responses Lite declaration lifecycle。
- `#50536` / `e1b5b56a4f`：Code Mode Only 无条件包含 shared MCP TypeScript
  preamble。
- `#50546` / `55b6f282a8`：MCP resource helpers 在存在服务器或 Code Mode Only
  时始终注册。
- `#50562` / `58ca099b03`：deferred-tool discovery guidance 无条件输出，
  eager exec description 不随 catalog 变化。

验证：

- `go test ./features -run Incremental -count=1`
- `go test ./codemode -run TestSharedMCPTypesPreambleStableInCodeModeOnly -count=1`
- `go test ./codemode -run TestDeferredNestedToolsGuidanceStableAcrossCatalogChanges -count=1`
- `go test ./turn -run TestBuildToolRegistryRegistersMCPResourceHelpersInCodeModeOnlyLikeRust -count=1`
- `go test ./turn ./config ./features -count=1`
- `go test ./tool -run CodeMode -count=1`
- `go test ./tool -run StrictThirdParty -count=1`
- `go vet ./config ./turn ./tool ./appserver`
- `go build ./...`
- `go test ./parity -count=1`
