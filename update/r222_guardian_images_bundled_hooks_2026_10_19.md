# Guardian computer-use images 与 bundled browser cleanup hooks

本轮关闭：

- `9dea1f709f` Default Guardian v2 to computer-use reviews with images (#40846)
- `0340e12f5d` Allow bundled browser plugins to run executor cleanup hooks (#40993)

## 结论

- Guardian 默认保留已准入的 computer-use 图像用于 review；图像按来源去重，
  失败/拒绝 review 的 ledger 语义保持不变。
- bundled browser cleanup hook 通过 Builtin 标记和 bundled allowlist 准入；
  malformed/untrusted 输入仍被过滤，用户/executor 插件 hook 的信任路径不变。

## 验证

- `go test ./appserver ./context -run 'Guardian.*Image|AdmittedImage' -count=1`
- `go test ./appserver -run 'BundledHook|Bundled.*Cleanup|ExecutorPluginHook' -count=1`
