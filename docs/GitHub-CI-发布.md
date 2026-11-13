# GitHub Actions 发布

发布工作流位于 `.github/workflows/release.yml`，在 `v*` tag push 后运行。Go 发布程序进一步限制为唯一规范 `vX.Y.Z` tag。

## 首次设置

1. 在 npm 创建有权发布 `release.json` 中 `@pjlab/trace-dock` 的 granular access token。需要 publish 权限及 npm 要求的 2FA 处理设置；勿将 token 提交到仓库。
2. 在 GitHub 仓库进入 **Settings → Environments → New environment**，创建名称为 `trace-dock-env` 的环境。
3. 进入 `trace-dock-env` 环境，在 **Environment secrets** 中新增名为 `NPM_TOKEN` 的 secret，值为 npm token。发布 job 已声明 `environment: trace-dock-env`，所以只有该 job 能读取这个 secret。
4. 确认 `.github/workflows/release.yml` 已提交，并给工作流启用 `contents: write` 权限。该权限由 workflow 声明，供 `gh release create` 上传发行版资产。
5. 检查 `release.json` 中默认服务端 HTTPS 地址及 npm scope/package 名称；scope 必须由该 npm 账号或组织拥有。

## 发布流程

在要发布的提交上打版本 tag 并推到 GitHub：

```bash
git tag v0.1.3
git push origin v0.1.3
```

Workflow 分为两个 job：

1. `build` 在 GitHub 托管的 Ubuntu runner 上执行 Go 全量测试和 vet，启用 Node 20 执行 npm 安装集成测试，用 Go 发布器构建 Linux/macOS/Windows 二进制、`.deb`/`.rpm`/`.pkg`、便携压缩包、npm 包和校验清单，再上传为 GitHub Actions artifact。
2. `publish` 下载完全相同的构建 artifact，用 `NPM_TOKEN` 执行 `npm view`/`npm publish --ignore-scripts`，再用 GitHub 自带 `GITHUB_TOKEN` 创建 GitHub Release 并附加全部 `dist/` 文件。

Release 版本直接取 tag，例如 `v0.1.3` 生成 CLI 版本 `0.1.3`。CI 不修改或递增 `release.json`；版本更新由下一次要发布的 tag 决定。主包及平台包的 tarball SHA-512 在发布前全部验证；同版本已存在且字节一致时跳过，字节不同则终止。GitHub Release 不覆盖现有同 tag 发行版。

GitHub 上无需导入流水线 YAML，也不需要仓库内保存 PAT。`NPM_TOKEN` 是 `trace-dock-env` 环境中的唯一发布 secret；`GITHUB_TOKEN` 由 Actions 自动提供。发布资产包含二进制、原生包、压缩包、`SHA256SUMS`、`RELEASE.json` 和生成的 npm tarballs。

GitHub Actions workflow 与 npm scoped 包权限参考：[GitHub Actions permissions](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)、[npm scoped public packages](https://docs.npmjs.com/creating-and-publishing-scoped-public-packages/)。
