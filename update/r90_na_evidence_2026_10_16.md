# 第 90 轮 build/发布类提交的 not_applicable 正向证据（2026-10-16）

范围：`95ec468619..4eb4a65407` 中 **class=build 的 16 项**（全量分诊见 `r90_lane_batches_2026_10_16.tsv`）。
口径（runbook R3）：不可落地必须给「**正向陈述 + 可复跑命令 + 实际输出**」，禁用「结构性等价 / 语义相同」代替证据。

## 0. 共用证据块（在 Go 仓库 D:\qax\reagent\dev\codex_go 实跑）

### E1 · Go 端口不存在 Bazel 构建体系
```
$ git ls-files | Select-String -Pattern 'bazel' -SimpleMatch | Measure-Object   →  Count 0
$ foreach ($f in 'patches','defs.bzl','MODULE.bazel','.bazelrc','BUILD.bazel') { Test-Path $f }
  patches : False   defs.bzl : False   MODULE.bazel : False   .bazelrc : False   BUILD.bazel : False
```

### E2 · Go 端口不存在 Rust/Cargo 构建体系
```
$ git ls-files | grep 'Cargo.toml'   → 0
$ git ls-files | grep '\.rs$'        → 0
```

### E3 · Rust 侧依赖由 Cargo 管理，Go 侧由 go.mod 管理（无同名依赖）
```
$ Select-String -Path go.mod -Pattern 'gix|rustls|quinn|libyaml' | Measure-Object  →  Count 0
```

### E4 · Go 端口无 V8 / JS 引擎载体
```
$ git ls-files | Select-String -Pattern 'v8' -SimpleMatch | Measure-Object  →  Count 0
$ ls third_party  →  bwrap  ripgrep  voice      （无 v8；亦无 rusty_v8 制品）
```

### E5 · Go 端口不存在上游 scripts/codex_package（Python 打包器）体系
```
$ ls scripts/  →  build.ps1  build.sh  extract_snapshot_scripts.py  generate_project_ppt.py
                 install.ps1  install.sh  leader_gate.ps1  release.ps1  release.sh
                 slash-parity-runner.mjs  windows_helpers_test.go
（上游 #52213/#52178/#52177/#51849/#51847 改动面 scripts/codex_package/*.py、dotslash.py、smoke_tests/* 在 Go 侧无对应文件）
```

### E6 · 发布工作流集合不同
```
$ ls .github/workflows  →  macos-platform-gates.yml  parity-baseline.yml  release.yml  sqlite-platform-gates.yml
$ ls .github/actions    →  （目录不存在）
（上游 rust-release.yml / rust-release-zsh.yml / rust-release-windows.yml / bazel.yml 与 .github/actions/* 在 Go 侧无对应物）
```

### E7 · 发布构建已剥离符号（#52178 在 Go 侧已满足）
```
$ Select-String -Path scripts\build.sh -Pattern '-ldflags' 
  build.sh:96  set -- build -trimpath -buildvcs=false -ldflags "-s -w -X codex_go/doctor.buildVersion=$VERSION ... "
$ Select-String -Path scripts\build.ps1 -Pattern 'ldflags' -SimpleMatch
  build.ps1:88  $ldflags = "-s -w -X codex_go/doctor.buildVersion=$ResolvedVersion ..."
```

### E8 · 安装脚本无归档下载路径（#52177 无载体）
```
$ Select-String -Path scripts\install.sh,scripts\install.ps1 -Pattern 'curl|wget|Invoke-WebRequest|User-Agent'  →  0 命中
```

## 1. 逐项处置

| # | sha | 上游标题 | 上游改动面 | 处置 | 证据 |
| --- | --- | --- | --- | --- | --- |
| 54087 | cd8d61b223 | Upgrade Bazel's rules_python to 2.4.2 | .bazelrc MODULE.bazel(.lock) | not_applicable | E1 |
| 54078 | 71923ba305 | Update gix 0.89.0 / gix-url 0.40.0 | Cargo.toml/.lock MODULE.bazel.lock | not_applicable | E2,E3 |
| 53996 | 954750be9c | Bump rustls to 0.23.46 | Cargo.toml/.lock MODULE.bazel.lock | not_applicable | E2,E3 |
| 53673 | 7b06e4af66 | Publish Rust releases with the GitHub CLI | .github/workflows/rust-release.yml | not_applicable | E6 |
| 53485 | 4a93bd249a | Update quinn-proto to 0.11.18 | Cargo.lock MODULE.bazel.lock | not_applicable | E2,E3 |
| 53443 | 3ece80b784 | Use local MSVC toolchains for native Windows Bazel tests | .bazelrc .github/workflows/bazel.yml | not_applicable | E1,E6 |
| 53382 | 0c7362d121 | Ignore unsafe-libyaml advisory in dependency audits | codex-rs/.cargo/audit.toml deny.toml | not_applicable | E2（cargo audit 体系不存在） |
| 52299 | b6130e72f3 | Make Bazel release jobs advisory | rust-release*.yml | not_applicable | E1,E6 |
| 52213 | a645742bb5 | Stop bundling patched zsh in Codex packages | .github/actions, .github/scripts, scripts/codex_package/zsh.py+codex-zsh（删除） | not_applicable | E5（Go 侧无 codex_package/zsh 打包路径；Go 的 zsh 快照行为对齐面在 #54432，由车道 B 承担） |
| 52178 | e9e6cf6349 | Strip symbols from source-built release package binaries | scripts/codex_package/symbols.py 等 | already aligned | E7（Go 发布构建已用 `-ldflags "-s -w"`） |
| 52177 | bc1f22455d | Set a User-Agent for package archive downloads | scripts/codex_package/dotslash.py | not_applicable | E8（Go 安装脚本无归档下载逻辑） |
| 51890 | c333d755d4 | Add missing mxc-sdk UTF-8 resource patch for Bazel | patches/BUILD.bazel, patches/*.patch | not_applicable | E1 |
| 51856 | 47b195de5c | Build Bazel release artifacts alongside Cargo | rust-release*.yml | not_applicable | E1,E6 |
| 51855 | 41d5adfbe6 | Add Bazel support to the Codex package build action | .github/actions/* | not_applicable | E1,E6 |
| 51849 | 391bf191c2 | Fix package smoke tests for Cargo and Bazel debug symbols | scripts/codex_package/smoke_tests/* | not_applicable | E5 |
| 51847 | 6ea82c59c6 | Preserve Cargo package names in Bazel Rust builds | defs.bzl, patches/*, scripts/codex_package/smoke_tests/* | not_applicable | E1,E5 |

## 2. 待独立复核

- 本节 16 项由 leader 自行取证，**尚未经独立成员复核**（派生宽度 3/3 被车道占满，验证位待回收后补建）。
- 复核要求：随机抽 3 项，复跑 E1–E8 中对应命令，确认输出一致；若不符则改判为「可落地」并派工。
