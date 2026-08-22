# codex_go 版本与发布（Versioning / Git Release / npm Publish）规划

> 目标：参照 Rust 版 Codex（`../git/codex`，即 `openai/codex`）的发布模型，为 `codex_go`
> 建立一套可重复、可审计的版本号、Git Release 与 npm Package 发布流程。
>
> 本文档是**规划/设计稿**，不代表已经启用自动发布；首次发布前需按文末 Runbook 验证。

---

## 1. 现状盘点

| 项 | 现状 | 说明 |
| --- | --- | --- |
| 版本来源 | 分散 | `npm/codex/package.json` = `0.0.0-dev`；Go 内 `doctor`/`appserver`/`mcp` 各有一个 `buildVersion`，默认 `0.0.0`；`appserver.defaultCodexVersion = "0.0.0"` |
| 版本注入 | ldflags | `-X codex_go/doctor.buildVersion=${V} -X codex_go/appserver.buildVersion=${V} -X codex_go/mcp.buildVersion=${V}` |
| 运行时版本 | 支持 env 覆盖 | `doctor.Version()` 优先读 `CODEX_GO_VERSION`；`appserver` 也支持构建期版本 |
| Git 标签 | **无** | 尚未打任何 release tag |
| 分支/远程 | `main`；`origin=https://github.com/jacks001314/codex_go.git` | 个人空间 scope |
| 二进制发布脚本 | `scripts/release.ps1` / `release.sh` | 交叉编译 6 目标 → `zip/tar.gz` + `SHA256SUMS` + `release.json`，输出到 `dist/v<VER>/` |
| npm 构建脚本 | `npm/scripts/build-packages.mjs` | 构建主包 `@jacks001314/codex-go` + 平台包 `@jacks001314/codex-go-<os>-<cpu>` |
| npm 发布脚本 | `npm/scripts/publish-packages.mjs` | `npm publish` 平台包 + 主包；**硬编码期望 6 平台包 + 1 主包** |
| **npm 构建/发布不匹配** | ⚠️ | `build-packages.mjs` 仅构建 `linux-amd64`、`windows-amd64`（其余被注释），而 `publish-packages.mjs` 强校验 6 平台包 → 当前必然 `throw` |
| CI | 仅门禁 | `macos-platform-gates.yml`、`parity-baseline.yml`、`sqlite-platform-gates.yml`；**无 release / artifact / npm 工作流** |
| npm scope | `@jacks001314` | 为非 OpenAI 官方个人/组织 scope，发布需该 scope 的 npm token |

**结论**：发布所需的“原料”（`codex.js` 平台启动器、`.ps1/.sh` 打包脚本、npm build/publish 脚本）已基本具备，但缺少 (a) 单一版本来源；(b) 触发发布的 CI 工作流；(c) 平台目标数与发布脚本的一致性；(d) 首次发布的明文 Runbook。

---

## 2. Rust 参考实现（`../git/codex`）要点

### 2.1 版本与标签
- Release 由 **git tag** 触发：`.github/workflows/rust-release.yml` 的 `on.push.tags` 匹配 `rust-v*.*.*`。
- 现有 tag 形如 `rust-v0.148.0-alpha.9`、`rust-v0.146.0-alpha.10`。
- `tag-check` 校验 tag 与版本来源一致；tag 格式为正则：
  `^rust-v[0-9]+\.[0-9]+\.[0-9]+(-(alpha(\.[0-9]+){0,2}|beta(\.[0-9]+)?))?$`
- 真正注入二进制的版本来自 **构建期环境/标签**（`CODEX_VERSION`/build-info，stamped commit），`codex-rs/Cargo.toml` 里 `[workspace.package] version = "0.0.0"` 只是开发期占位。

### 2.2 Release 流水线（`rust-release.yml`）
1. `tag-check`：验证 tag 与版本来源一致。
2. `build`：按目标矩阵（linux musl / darwin / windows，x64+arm64）编译，产出各目标 artifact。
3. 追加资产：`codex-package_*` 校验和清单、`config-schema.json`、`install.sh`/`install.ps1`。
4. `release`（`softprops/action-gh-release`）：`name=版本`、`tag=github.ref_name`、带 `files: dist/**`、按 `-` 后缀决定 `prerelease`/`make_latest`。
5. **npm 发布决策**（稳定版 → `latest`；`-alpha.N` → `alpha`；其它预发布 → 不发布）。
6. `stage_npm_packages.py` 从 `rust-release` 工作流产物里挑出 6 个目标二进制，用 `codex-cli/scripts/build_npm_package.py` 组装 npm 包。

### 2.3 npm 包模型（`build_npm_package.py`）
- 主包：`@openai/codex`，`version = 发布版本`，`bin/codex.js` 由启动器驱动。
- 平台包：`@openai/codex-<os>-<cpu>`，`version = <发布版本>-<平台tag>`（如 `0.1.0-linux-x64`）。
  - 后缀原因：npm 不允许同 `name@version` 重复发布，为每次“平台变体”制造唯一版本。
- 主包 `optionalDependencies` 用 npm alias 引用平台包：
  `"@openai/codex-linux-x64": "npm:@openai/codex@0.1.0-linux-x64"`。
- `bin/codex.js` 通过 `require.resolve("@openai/codex-<os>-<cpu>/package.json")` 找到 vendor 二进制。
- 随 `codex` 一起发布的还有 `codex-responses-api-proxy` 与 `codex-sdk`（SDK 用 `dependencies` 引用主包版本）。

---

## 3. codex_go 发布策略设计

### 3.1 版本号格式
采用 SemVer：`MAJOR.MINOR.PATCH[-prerelease][+build]`，与 Rust 一致，便于对照和归类预发布。

| 场景 | 示例 |
| --- | --- |
| 正式版 | `0.1.0` |
| 预发布（可发布 npm `alpha` tag） | `0.1.0-alpha.1` |
| 其它预发布（默认不发布 npm） | `0.1.0-beta.1`、`0.1.0-rc.1` |

### 3.2 Git 标签
推荐：**`vX.Y.Z`**（纯 semver，最标准）。

> 备选：若要与 Rust 的 `rust-vX.Y.Z` 明确区分，可用 `go-vX.Y.Z`。建议先在 3.7 决策表中定夺。

触发器（GitHub Actions）匹配：`v*.*.*`（含预发布）。预发布资格由标签中是否出现 `-` 决定。

### 3.3 单一事实来源（Single Source of Truth）
当前版本分散，必须收敛为“一个输入、处处引用”。推荐：

1. 新增仓库根文件 `VERSION`，内容仅为 semver 文本（如 `0.1.0`），作为**唯一权威**版本。
2. `scripts/release.sh` / `release.ps1` 与 `npm/scripts/*.mjs` 在未显式传 `--version` 时读取该文件作为默认值。
3. Go 二进制版本保持 ldflags 注入（`doctor`/`appserver`/`mcp`），无需改代码；新增校验确保 `VERSION` 与 `npm/codex/package.json` 的 `version` 一致。
4. 可选：把 `codex_go/internal/version` 作为占位常量引入，避免“三方各自默认值”漂移；但这是增强项，非必须。

### 3.4 构建与注入
沿用现有 ldflags 注入（已在 build/scripts 中实现）：
```bash
go build -trimpath -buildvcs=false \
  -ldflags "-s -w \
    -X codex_go/doctor.buildVersion=${V} \
    -X codex_go/appserver.buildVersion=${V} \
    -X codex_go/mcp.buildVersion=${V}" \
  -o bin/codex ./cmd/codex
```
运行时 `codex --version` / `codex doctor` 即输出该版本；也支持 `CODEX_GO_VERSION` 覆盖（开发期便利）。

### 3.5 发布产物
- **GitHub Release 资产**：6 目标二进制归档（`zip`/`tar.gz`）、`SHA256SUMS`、`release.json`、`scripts/install.sh`/`scripts/install.ps1`。
  - 当前仓库**没有** `config.schema.json` 资产（Rust 有 `codex-rs/core/config.schema.json`）；如后续生成配置 JSON Schema，再作为 release 资产随附（见 §6）。
- **npm 包**：主包 `@jacks001314/codex-go@<V>` + 6 平台包 `@jacks001314/codex-go-<os>-<cpu>@<V>`。

### 3.6 npm 发布模型（对齐 Rust，但使用本仓库命名）
当前 `codex.js` 启动器已支持 6 平台映射，`build-packages.mjs` 用 `optionalDependencies` 平台包 + vendor 二进制。建议：

- 主包 `optionalDependencies`：
  `{ "@jacks001314/codex-go-linux-x64": "<V>", ... }`
  （Go 用**不同包名 + 相同版本**即可，无需 Rust 的 `name@version` 后缀；因为包名不同不冲突。）
- 若决定**严格镜像 Rust 变体版本后缀**（同 `@jacks001314/codex-go` 名、多个版本），需同步改 `codex.js` 的解析与 `optionalDependencies` 写法，属于可选重构。
- **必须**：先发布 6 个平台包，再发布主包（主包 `optionalDependencies` 依赖平台包）。

### 3.7 决策表（需拍板/确认）
| # | 决策点 | 推荐值 | 理由 |
| --- | --- | --- | --- |
| D1 | 标签前缀 | `vX.Y.Z`（备选 `go-vX.Y.Z`） | 与 Rust 区分、最通用 |
| D2 | 是否发布 npm `-beta`/`-rc` | 默认不发布（仅 stable + `alpha`） | 镜像 Rust 行为，防水 |
| D3 | 平台包版本 | 相同版本（不同包名） | 现有 `codex.js` 依赖此写法 |
| D4 | 目标平台数 | 启用全部 6 平台 | 与 `codex.js` 映射一致，覆盖用户 |
| D5 | npm scope | `@jacks001314` | 当前仓库远程归属 |
| D6 | 是否随附 `codex-responses-api-proxy`/SDK | 本阶段仅 `codex` | 与 Rust 对齐；其它组件成熟后扩展 |

---

## 4. Git Release 流程（步骤化）

**触发方式**：推送 `v*` 标签到 `origin/main` 后由 CI 完成，避免手工上传。

1. 收敛版本：更新 `VERSION`（及 `npm/codex/package.json` 的 version，或由 CI 覆盖）。
2. 提交并推送：`git commit -m "chore: release v0.1.0"`。
3. 打标签并推送：
   ```bash
   git tag -a v0.1.0 -m "Release 0.1.0"
   git push origin v0.1.0
   ```
4. CI `release` 工作流执行：
   - `validate`：标签与 `VERSION` 一致；semver 合规。
   - `build`：交叉编译 6 目标 二进制归档；生成 `SHA256SUMS`、`release.json`、拷贝 `scripts/install.sh`/`scripts/install.ps1`（以及可选的 config schema）。
   - `npm`：`build-packages.mjs` → `publish-packages.mjs`（需 `NPM_TOKEN`）。
   - `release`：`softprops/action-gh-release` 上传全部资产；按 `-` 后缀决定 `prerelease`/`make_latest`。
5. 发布后核对：GitHub Release 可下载、`npm view @jacks001314/codex-go` 版本正确、各平台 `npx @jacks001314/codex-go --version` 正常。

---

## 5. 推荐的 GitHub Actions 工作流（草案）

> 已落地为 `.github/workflows/release.yml`（`validate`/`build`/`npm`/`release` 四 job）。下面为其设计稿；首次启用前需补 `NPM_TOKEN` secret 并确认 runner 可用。

```yaml
name: release

on:
  push:
    tags:
      - "v*.*.*"

permissions:
  contents: write

jobs:
  validate:
    runs-on: ubuntu-latest
    outputs:
      version: ${{ steps.out.outputs.version }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Validate tag vs VERSION
        id: out
        run: |
          set -euo pipefail
          tag="${GITHUB_REF_NAME#v}"
          [[ "$tag" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo "bad semver: $tag"; exit 1; }
          [[ "$tag" == "$(tr -d '\r\n' < VERSION)" ]] || { echo "tag $tag != VERSION"; exit 1; }
          echo "version=$tag" >> "$GITHUB_OUTPUT"

  build:
    needs: validate
    runs-on: ubuntu-latest
    env:
      VERSION: ${{ needs.validate.outputs.version }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Build release archives
        run: scripts/release.sh --version "$VERSION" --output-dir "dist/v$VERSION"
      - name: Copy installers (+ optional config schema)
        run: |
          cp scripts/install.sh dist/v$VERSION/install.sh
          cp scripts/install.ps1 dist/v$VERSION/install.ps1
          # If a config JSON Schema is generated in the future, stage it here:
          # cp <schema-path> dist/v$VERSION/config-schema.json
      - uses: actions/upload-artifact@v4
        with:
          name: release-assets
          path: dist/v$VERSION

  npm:
    needs: build
    runs-on: ubuntu-latest
    env:
      VERSION: ${{ needs.validate.outputs.version }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          registry-url: https://registry.npmjs.org
      - name: Stage npm packages (all 6 platforms)
        run: node npm/scripts/build-packages.mjs --version "$VERSION" --output-dir "dist/v$VERSION/npm"
      - name: Publish npm (tag: latest or alpha)
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
        run: |
          tag="latest"
          [[ "$VERSION" == *-alpha.* ]] && tag="alpha"
          node npm/scripts/publish-packages.mjs --directory "dist/v$VERSION/npm" --tag "$tag"

  release:
    needs: validate
    runs-on: ubuntu-latest
    permissions:
      contents: write
    env:
      VERSION: ${{ needs.validate.outputs.version }}
    steps:
      - uses: actions/download-artifact@v4
        with:
          name: release-assets
          path: dist
      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          name: ${{ env.VERSION }}
          tag_name: ${{ github.ref_name }}
          files: dist/**
          make_latest: ${{ !contains(needs.validate.outputs.version, '-') }}
          prerelease: ${{ contains(needs.validate.outputs.version, '-') }}
```

> 注：`VERSION` 由 `validate` 通过 job `outputs` 传递（`needs.validate.outputs.version`），`release` 与 `npm` 只依赖 `validate` 即可，不必串行等待 `build`。

---

## 6. 需要补齐/修复的点

> 状态：以下 `[x]` 项已随本轮（选项 A）落地；`[ ]` 项为待办/后续增强。

- [x] **修复平台构建与发布不一致**：`npm/scripts/build-packages.mjs` 已启用全部 6 目标（linux/darwin/windows × amd64/arm64）；`publish-packages.mjs` 改为动态判断（恰 1 主包 + ≥1 平台包）。
- [x] **验证 6 平台可交叉编译**：已在本机验证 `CGO_ENABLED=0` 下 6 目标 `go build` 全部通过，并完整跑通 `build-packages.mjs` 产出 6 平台包 + 主包。
- [x] **单一版本源**：新增根 `VERSION`（当前 `0.0.0`），`scripts/build.sh`/`build.ps1` 与 `scripts/release.sh`/`release.ps1` 及 `npm/scripts/build-packages.mjs` 均默认读取；`npm/codex/package.json` 的 version 在 build 时由参数覆盖。
- [x] **版本注入统一**：确认 `codex --version`（`codex-cli <version>`）、`codex doctor`、`app-server` 使用同一 `doctor.Version()`/ldflags 注入。
- [x] **prerelease 判定**：`.github/workflows/release.yml` 已统一 stable→`latest`、`-alpha`→`alpha`，并据此设置 `make_latest`/`prerelease`。
- [ ] **config schema 资产（可选）**：当前仓库无 `config.schema.json`；若需要与 Rust 对齐，先生成 Schema（代码生成或手写），再在 release 时随附。
- [ ] **npm token/scope 权限**：`NPM_TOKEN` 需对 `@jacks001314` scope 具备 publish 权限；CI 用 `setup-node` 的 `registry-url` + `NODE_AUTH_TOKEN`。
- [ ] **签名/公证**：macOS 公证或 Windows 签名（本期可先省略，作为后续增强；Rust 用 Azure Key Vault + codesigning 环境）。
- [ ] **changelog / release notes**：Rust 的 CHANGELOG 指向 GitHub Releases。建议为 `codex_go` 维护 `CHANGELOG.md` 或由 tag 提交信息生成 release notes（Rust 用 tag 指向的 commit message）。

---

## 7. 首次发布 Runbook（手工预演）

在本地先 dry-run，再推 CI：

```bash
# 1) 设定版本并校验
export VERSION=0.1.0
echo "$VERSION" > VERSION

# 2) 本地构建并核对二进制版本（含 6 平台归档）
scripts/release.sh --version "$VERSION" --output-dir "dist/v$VERSION"
tar -xzf dist/v$VERSION/codex-go-v$VERSION-linux-amd64.tar.gz -C /tmp/cg
/tmp/cg/codex --version   # 打印 0.1.0

# 3) 本地构建 npm 包并 dry-run 校验（不真正推 npm）
node npm/scripts/build-packages.mjs --version "$VERSION" --output-dir "dist/v$VERSION/npm"
node npm/scripts/publish-packages.mjs --directory "dist/v$VERSION/npm" --tag latest   # 仅用于校验文件数
# 发布前请改为：npm publish --dry-run 逐一验证，再放 true publish。

# 4) 提交 + 打标签 + 推送（触发 CI）
git add VERSION && git commit -m "chore: release v$VERSION"
git tag -a "v$VERSION" -m "Release $VERSION"
git push origin main && git push origin "v$VERSION"
```

---

## 8. 与 Rust 的差距对照（后续可选）

| 能力 | Rust | codex_go | 建议 |
| --- | --- | --- | --- |
| 标签触发 CI | `rust-v*` | 待建 | 加 `v*` 触发 |
| 版本来源 | 构建期 env / 标签 | 待收敛 | 根 `VERSION` + ldflags |
| 多平台二进制 | 6 目标 | 已有脚本 | 启用 6 目标 |
| 归档+校验和 | 有 | 已有 | 复用 |
| config-schema | 有 | 暂无 | 可选生成后随附 |
| 安装脚本 | 有 | `scripts/install.sh`/`.ps1` | 复用 |
| npm 发布 | stable/alpha | 待建 | 加 job + token |
| 额外 npm 包 | proxy、SDK | 暂无 | 可选后续 |
| 签名/公证 | 有 | 无 | 增强项 |

---

## 9. 结论 / 建议顺序

1. 先定 **D1（标签前缀）** 与 **D3（平台包版本写法）**；其余用推荐值。
2. 落地 **代码改动**：根 `VERSION`、`build-packages.mjs` 启 6 平台、`publish-packages.mjs` 动态化校验、版本一致性校验脚本。
3. 落地 **CI**：以上 `.github/workflows/release.yml`（补 token/secrets、跨 job 取版本）。
4. 按 **Runbook** 先本地 dry-run，再推首版标签验证端到端。
5. 后续增强：changelog 生成、macOS 公证/签名、多 npm 包（proxy/SDK）。

> 注意：npm 发布到 `@jacks001314` 后即对外可见且不可覆盖同名版本；上线前务必用 `npm publish --dry-run` 与 `npm view` 做灰度校验。
