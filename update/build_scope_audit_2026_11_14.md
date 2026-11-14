# 构建提交适用性审计

目标 Rust：69231e119bfca0b14453515e97d99688ce1c47c9。

| 提交 | 上游具体改动 | Go 项目证据与结论 |
| --- | --- | --- |
| #61187 / 4490060db60a2053bc3c6095127cfc10a9f3e34d | Bazel CI 的 GCS OIDC 缓存、Bazel wrapper 与 Rust release 产物选择 | Master project_list 的 **/*bazel* 为空；.github/workflows/release.yml 使用 setup-go、go.mod 与 scripts/release.sh。没有相应 Bazel pipeline，不迁入上游组织 GCS 凭据或缓存设置。该提交不适用当前 Go 构建。 |
| #61254 / c123aa2c10e3a11e17d67617f98c19169da13149 | Bazel coreutils sha256sum；Bazel 原生 voice 的 GLib 生成器 PATH 加入 Python 解释器目录 | third_party/bwrap/prepare_bwrap.py 的 sha256_file 使用 hashlib.sha256；voice 目录仅 sources.json 与 prepare_opus.py，没有上游 build_native.py/GLib Bazel recipe。因此没有对应工具定位路径可修改，该提交不适用当前构建。 |
| #61255 / 7b2ed14efe872527bae38cde98a402859e351bac | MODULE.bazel 中 V8 pip.parse 为索引 URL 添加 envsubst | 项目没有 MODULE.bazel/v8_python_deps；go.mod 使用 github.com/grafana/sobek。不是项目一般 pip 配置变更，不适用当前 Go 引擎依赖流程。 |

#61205 不因标题为 Rust toolchain 就整项关闭：已查看原子操作、UTF16 分块、Clippy 和调试快照等改动，但全提交审计仍保留待办。Go go.mod 当前声明 1.26.2；本机运行工具版本与模块声明不是同一事实，不把技能中的 1.26.5 描述当成 go.mod 已更新。

本审计不表示整体同步完成，也不表示 Linux/语音发布产物通过构建验证。未修改 CI 或发布凭据。
