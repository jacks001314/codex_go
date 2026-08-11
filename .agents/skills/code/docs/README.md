# Go 编码规范权威资料库

本目录收集 **Go 语言官方及业界权威的编码规范/风格指南**，用于日常 Go 代码编写、评审与重构时查阅。

> 收集时间：2026-08-05　|　来源均为官方或权威仓库，正文保留原文（英文），未做二次翻译，避免失真。

---

## 目录结构

```
docs/
├── README.md                        本索引
├── 01_official/                     Go 官方资料（go.dev / Go 官方仓库）
│   ├── effective_go.md              Effective Go（官方编码风格指南）
│   ├── code_review_comments.md      Go 代码评审常见意见（官方 Wiki）
│   ├── test_comments.md             测试相关代码评审意见（官方 Wiki）
│   ├── go_spec.md                   Go 语言规范（官方参考手册）
│   ├── go_proverbs.md               Go 谚语（Rob Pike 演讲）
│   └── how_to_write_go_code.md      如何编写 Go 代码（工程/模块组织约定）
├── 02_google/                       Google Go 风格指南系列
│   ├── index.md                     总览（关于/何时使用）
│   ├── style_guide.md               核心风格指南（Google 立场）
│   ├── style_decisions.md           风格决策（细节裁定，最常用）
│   └── best_practices.md            最佳实践
└── 03_uber/                         Uber Go 风格指南
    └── style.md                     Uber Go Style Guide
```

---

## 文档清单与来源

### 01_official — Go 官方

| 文件 | 内容 | 原始来源 |
| --- | --- | --- |
| `effective_go.md` | 官方编码风格指南：命名、格式（gofmt）、控制结构、函数、错误处理、并发等惯用法。**入门必读** | https://go.dev/doc/effective_go |
| `code_review_comments.md` | 代码评审中常见的风格问题清单（Gofmt、注释句子、Context、错误字符串、声明零值、包注释等）。**评审必读** | https://go.dev/wiki/CodeReviewComments |
| `test_comments.md` | 测试相关评审意见（表驱动测试、测试命名、assert 库、t.Helper 等） | https://go.dev/wiki/TestComments |
| `go_spec.md` | Go 语言规范（语法、类型、表达式、语句、包等权威定义，go1.26） | https://go.dev/ref/spec |
| `go_proverbs.md` | Rob Pike 在 2015 年 Gopherfest 提出的 Go 设计谚语（如 "Don't communicate by sharing memory"） | https://go-proverbs.github.io/ |
| `how_to_write_go_code.md` | 模块/包/导入的组织方式与构建约定 | https://go.dev/doc/code |

### 02_google — Google Go 风格指南（go/styleguide）

| 文件 | 内容 | 原始来源 |
| --- | --- | --- |
| `index.md` | 总览：指南的定位、何时使用 | https://google.github.io/styleguide/go/ |
| `style_guide.md` | 核心风格指南：格式、命名、注释、声明、导入等 | https://google.github.io/styleguide/go/guide |
| `style_decisions.md` | 风格决策：对"未写入核心指南"的具体问题的裁定（如命名、接口、错误处理、测试），**解决争议首选** | https://google.github.io/styleguide/go/decisions |
| `best_practices.md` | 最佳实践：关于性能、接口、并发、测试等主题的深入建议 | https://google.github.io/styleguide/go/best-practices |

### 03_uber — Uber Go 风格指南

| 文件 | 内容 | 原始来源 |
| --- | --- | --- |
| `style.md` | Uber 工程实践中总结的模式与约定（指针/接口、零值、性能、依赖注入、错误包装等），社区广泛采用 | https://github.com/uber-go/guide/blob/master/style.md |

---

## 查阅建议

| 场景 | 推荐文档 |
| --- | --- |
| 新手学习 Go 惯用法 | `01_official/effective_go.md` |
| 代码评审打回意见 | `01_official/code_review_comments.md` + `01_official/test_comments.md` |
| 团队规范争议裁决 | `02_google/style_decisions.md` → `02_google/style_guide.md` |
| 写测试 | `01_official/test_comments.md` + `02_google/best_practices.md`（Testing 章节） |
| 错误处理 / 并发 / 性能 | `03_uber/style.md` + `02_google/best_practices.md` |
| 语法语义权威定义 | `01_official/go_spec.md` |

---

## 工具链约定（官方强制/推荐）

- **gofmt / go fmt**：Go 官方强制格式化工具，所有代码必须通过 gofmt（Effective Go 与 Google 指南均要求）。
- **go vet**：官方静态检查工具，提交前必跑。
- **goimports**：gofmt 的超集，额外管理 import 分组与排序（Google 指南推荐）。
- **staticcheck**（dominikh/go-tools）：社区最广泛使用的增强版静态检查（可选）。

---

## 更新说明

各文件均抓取自对应官方页面的当前版本（2026-08-05）。如需更新，重新抓取原始来源 URL 替换对应文件即可。
