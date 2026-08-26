---
name: codex-go-update
description: 每天检查rust版本codex github 库的更新情况, 根据最新的更新情况，制定go版本codex的更新计划，并实施更新同步和测试验证。
---

# codex-go更新同步

## 概述

codex-go是官方rust版本codex cli的go版本同步实现，需要功能高度对齐，每天必须检查github上rust版本codex cli的变化更新，制定便于追踪，开发，测试的对齐计划，并落实更新对齐代码落地功能。

## 目标
 - 所有提交能够落地的功能更新，必须在go版本codex中实现，不要以“暂不实现”作为理由，除非有明确的技术难点或不可行性。
 - 不要以改动大为理由，拒绝实现rust版本codex cli的功能更新。
 - 不要任何形式的延后处理

## 工作流
0. git 当git 连接失败的时候，要用系统代理

1. 上游rust codex cli 本地git库位于目录：D:\qax\reagent\dev\git\codex，使用git pull origin main拉取最新更新

2. 比对当前go版本codex项目和rust codex cli相关功能，制定功能更新计划，输出到update/plan_年_月_日.md

3. 根据更新计划开始落地更新同步代码，并进行测试验证

4. git commit and push更新后的go版本codex项目到github远程仓库

5. 总结更新对齐情况

