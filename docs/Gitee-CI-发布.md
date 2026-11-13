# Gitee Go 发布配置

流水线文件是 `.workflow/trace-dock-release.yml`，由测试与打包、发布发行版两个串行阶段组成。前者失败时，后者不执行。

当前 Git remote 指向私有部署 `gitee.pjlab.org.cn`。需要在该实例的仓库中启用 Gitee Go，并确认其提供 `build@golang` 和 `release@gitee` 插件；原 GitHub Actions 配置已移除。本配置不调用 gitee.com API，也不在源码中保存发布令牌。

## 首次接入

1. 在 Gitee 仓库的“流水线”中创建流水线，使用代码视图导入 `.workflow/trace-dock-release.yml` 并保存。只提交 YAML 文件不代表实例已经启用流水线。
2. 为 Go 构建步骤选择 Linux x64 执行环境，提供 Go 1.26.5、Git 和 `sha256sum`。若当前实例的语言插件未提供该 Go 版本，需要管理员配置相应构建环境。运行环境还需能够下载 Go 模块，并允许交叉编译；不需要 Python、Node.js、npm、系统 zip、dpkg-deb、rpmbuild 或 Apple pkgbuild 工具。npm 包是发布物，只有 npm 用户安装时需要 Node.js/npm。
3. 确保代码拉取包含本次标签：构建工具以 `git tag --points-at HEAD` 取得当前提交的唯一 `vX.Y.Z` 标签。缺少标签或同一提交含多个正式版本标签时会停止，避免错误版本发版。若 Gitee Go 默认浅克隆不带标签，需要在代码拉取设置中启用标签，或在构建命令开始处执行 `git fetch --tags --force`。
4. 在 Gitee Release 插件中选择当前仓库及有发行版和附件写权限的发布身份。私有化实例如需凭据，请配置在流水线凭据管理中。
5. 将 `release.json` 的 `default_server_url` 改为参与者可访问的 HTTPS 服务地址；`npm.name` 必须是你拥有发布权限的 npm scope/package 名称。CI 不需要服务端 `.env`、阿里云 AK/SK、参与者 token 或业务数据库。npm token 只作为 Gitee Go 的秘密变量 `NPM_TOKEN` 配置，不能提交到仓库。

## 发布

先将代码和配置提交到 Gitee，再给需要发布的提交创建版本标签。例如：

```bash
git tag v0.1.1
git push origin v0.1.1
```

也可以从 Gitee 页面创建 `v0.1.1` 标签。实际发布版本由标签决定：`v0.1.1` 构建出的二进制显示 `0.1.1`。CI 使用 `go run ./cmd/trace-dock-release --ci`，不会递增或回写 `release.json`；重跑同一标签的构建不会改变版本。

本地开发仍用 `make release`，其版本读取 `release.json`，全部产物构建成功后按 `auto_increment_patch` 决定是否递增。CI 的标签优先级可避免本地配置已自动递增为下一版后，重新构建旧标签时使用错误版本。

## 流水线行为

1. `go test ./...` 和 `go vet ./...`。
2. 以 `CGO_ENABLED=0` 构建五个平台的独立二进制。
3. 使用 Go 标准库生成四个 `.tar.gz` 和一个 `.zip`；使用 nFPM Go 库生成 Linux amd64/arm64 的 `.deb/.rpm`；使用 go-macos-pkg Go 库生成 macOS Intel/Apple Silicon 的 `.pkg`，包括 BOM、Payload 和安装元数据；同时生成 npm 入口包和五个 `os/cpu` 平台包。原生安装包只安装 CLI 可执行文件，安装和系统卸载流程均不读取或修改用户目录。
4. 写入 `RELEASE.json` 和 `SHA256SUMS`，逐个核对校验值，并启动 Linux x64 二进制检查版本。
5. 构建工具把已验证的标签写入官方参数文件 `GITEE_PARAMS`，通过 `TRACE_DOCK_RELEASE_TAG` 传给后续发布阶段。
6. `release@gitee` 使用该标签创建发行版，上传五个二进制、五个压缩包、六个原生安装包、npm tarballs、`npm/publish.json`、`npm/publish.cjs`、`SHA256SUMS` 和 `RELEASE.json`。Release 描述来自仓库中的 `RELEASE-NOTES.md`。

`allowUpdate: false` 会拒绝覆盖同名发行版。已发布版本应创建新标签；构建失败且尚未创建发行版时可以直接重跑。Windows 当前使用 `.zip/.exe`，尚未实现 `.msi`。

macOS `.pkg` 生成是跨平台的纯 Go 实现，因此 Linux CI 也能产出。当前没有 Apple Developer ID 证书和公证配置，产物是未签名、未公证包；生成与 payload 校验不等于已通过 macOS 原生安装或 Gatekeeper 验收。正式公开发行的签名、公证步骤需在提供证书与 App Store Connect 配置后接入。说明见 [Apple 官方文档](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)。

本地验证覆盖 Go 测试、交叉编译、归档和原生安装包的 payload/路径/版本/架构及校验值。Linux `.deb` 另外通过 `dpkg-deb` 解析、提取验证；`.rpm` 和 `.pkg` 用 Go 读取库解包核对。Gitee 控制台启用、插件权限、制品传递及最终 Release 上传需要在该私有化实例内执行首轮流水线才能验证；原生系统安装仍需各平台验收。

配置语法依据：[触发事件](https://help.gitee.com/gitee-go/pipeline/trigger/)、[参数设置](https://help.gitee.com/gitee-go/pipeline/parameter)、[Gitee Release 插件](https://help.gitee.com/enterprise/pipeline/plugin/gitee-release)。

## npm 发布

`release.json` 中配置 npm scope 和 registry。首次发布需要在 Gitee Go 的秘密变量中配置 `NPM_TOKEN`，不要将 token 提交到仓库。构建阶段会生成 npm 入口包和五个平台包，发布阶段可在 Gitee Go 的 release 后续步骤中执行：

```bash
node dist/npm/publish.cjs
```

`publish.cjs` 会校验所有 npm tarball 的 SHA-512；同版本已经存在且字节一致时跳过，存在但字节不同则失败。npm 入口包没有 install/postinstall 脚本，用户安装时通过 `optionalDependencies` 和 `os/cpu` 选择平台二进制。npm 包使用 `bin` 字段提供 `trace-dock` 命令。[npm package.json 文档](https://docs.npmjs.com/cli/v11/configuring-npm/package-json/)

用户安装和卸载：

```bash
npm install -g @trace-dock/cli
trace-dock
npm uninstall -g @trace-dock/cli
```

发布 scope 公共包需要 `npm publish --access public` 所需权限；这里由 `NPM_TOKEN` 对应的 npm 账号承担。[npm scoped package 文档](https://docs.npmjs.com/creating-and-publishing-scoped-public-packages/)
