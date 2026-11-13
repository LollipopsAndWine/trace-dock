trace-dock CLI 发行版由 GitHub Actions 构建并校验。

附件中的 `RELEASE.json` 记录本次版本、Git 标签、源码提交和默认服务地址；`SHA256SUMS` 覆盖全部二进制、原生安装包、压缩包和版本记录。

| 平台 | 原生安装包 | 便携包 |
| --- | --- | --- |
| Linux Intel/AMD x64 | trace-dock-linux-amd64.deb / .rpm | trace-dock-linux-amd64.tar.gz |
| Linux ARM64 | trace-dock-linux-arm64.deb / .rpm | trace-dock-linux-arm64.tar.gz |
| macOS Intel | trace-dock-darwin-amd64.pkg | trace-dock-darwin-amd64.tar.gz |
| macOS Apple Silicon | trace-dock-darwin-arm64.pkg | trace-dock-darwin-arm64.tar.gz |
| Windows x64 | 暂无 MSI | trace-dock-windows-amd64.zip |

Linux/macOS 压缩包内含可执行文件 `trace-dock`，Windows 压缩包内含 `trace-dock.exe`。解压并放入 PATH 后运行 `trace-dock --version`，再运行 `trace-dock` 开始采集。参与者无需安装 Go、Python 或 Node.js。

Ubuntu/Debian 执行 `sudo apt install ./trace-dock-linux-amd64.deb`；Fedora/RHEL 执行 `sudo dnf install ./trace-dock-linux-amd64.rpm`，Linux 原生包安装到 `/usr/bin/trace-dock`。macOS 双击对应 `.pkg`，安装到 `/usr/local/bin/trace-dock`。安装包不包含配置或用户数据，也不带读写用户目录的安装/卸载脚本。

当前 macOS 安装包尚未进行 Apple Developer ID 签名和公证，可能出现系统安全提示，需在 macOS 上完成原生安装验收。

更改默认服务地址可使用 `trace-dock --server https://你的服务域名`，或编辑用户目录内的 `config.json`。便携安装卸载使用 `trace-dock uninstall`；通过 deb/rpm 安装时，使用 `sudo apt remove trace-dock` / `sudo dnf remove trace-dock` 维护系统包记录，用户配置和任务数据予以保留。

Node.js/npm 用户也可以执行 `npm install -g @pjlab/trace-dock` 安装。npm 包会按当前平台选择一个原生 Go 二进制；不要使用 `--omit=optional`，否则平台二进制不会安装。
