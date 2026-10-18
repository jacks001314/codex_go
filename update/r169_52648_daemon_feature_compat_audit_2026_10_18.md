# Round 726: daemon feature compatibility audit

`#52648` 要求 running shared daemon 的 feature compatibility 只检查显式 CLI
overrides，配置文件/default 差异不应触发 warning/restart。

Go 现状已对齐：

- `daemonStartupServerFeatures` 只提取 launch 显式 enable 的 shared-service
  features；
- `daemonServerFeatureDisabled` 只检测显式 disable；
- `daemonRestartFeatureOverrides` 只校验并重放 CLI feature toggles；
- 没有基于配置文件/default 的 daemon features/list 比较路径。

验证：

- `go test ./app -run 'Daemon.*Feature|DaemonRestartFeature' -count=1`
