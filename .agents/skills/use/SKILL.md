---
name: usealign
description: 通过对 Rust 与 Go 版 Codex cli tui 进行真实使用的比较，确定差异，向rust版本的codex 对齐功能。
---

# 基本信息

1. rust版本的codex cli 源码路径："D:\qax\reagent\dev\git\codex"，比对时候直接使用系统安装的codex命令，不要编译源码。
2. go版本的codex cli 源码路径："D:\qax\reagent\dev\codex_go"，应该编译最新的codex.exe 进行比较。

## 基本原则

- 不要进行如何git pull 操作，以当前仓库状态为准。
- rust 版本的可以直接使用 codex --no-daemon 启动
- go 版本的可以使用最新编译的 codex.exe --no-daemon启动


## 界面对齐

1. 创建一个临时的project,可以在此项目上 启动rust 版本的codex 和go 版本的codex

2. 尝试让他们做一些相同的工作，然后通过截屏或是其他的视觉手段比较他们的差异，将go版本的功能修复对齐rust版本功能。

## slash "/" 命令对齐
  1. 创建一个临时的project,可以在此项目上 启动rust 版本的codex 和go 版本的codex
  2. 每一个/命令 都做比较，通过数据流，视觉手段比较差异，将go版本的功能修复对齐rust版本功能。

## “@，$” 引用对齐
  1. 创建一个临时的project,可以在此项目上 启动rust 版本的codex 和go 版本的codex
  2. 每一个@ 或者 $ 引用 都做比较，通过数据流，视觉手段比较差异，将go版本的功能修复对齐rust版本功能。

