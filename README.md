# trace-dock CLI MVP

按 [MVP 技术方案](docs/CLI-MVP-技术方案.md) 实现的 Go CLI。运行 `trace-dock`，依次核对地址和身份、选择一种工具与根会话、授权本机采集、核对排除结果，再单独确认直传 OSS。个人 token 隐藏输入，仅在内存中使用。

CLI 仓库的开发、测试和发布统一使用 Go 1.26.5，`make build`、`make test` 和 `make release` 均只使用 Go。该版本满足纯 Go 的 macOS 安装包库要求；首次使用时 Go 会自动下载工具链，可通过 `GOTOOLCHAIN` 显式覆盖为兼容版本。发布二进制不需要 Go、Python 或 Node.js。`--help`、`--version` 不扫描用户数据。界面需要交互终端，方向键移动、空格选择、A 全选/取消、`/` 搜索、D 查看详情、PgUp/PgDn 翻页。授权和上传默认均为“否”。

在项目根目录执行 `make build` 生成当前平台的 `dist/trace-dock`；发布全部平台执行 `make release`。发布参数在根目录 [release.json](release.json) 中。如果遇到 `/usr/local/go/src/internal/strconv` 中的重复定义或缺失类型错误，说明系统 Go 标准库文件冲突；项目固定的工具链会绕过该系统安装，无需修改 `/usr/local/go`。

安装发布版时，下载适合系统和 CPU 架构的安装包并校验 `dist/SHA256SUMS`。Linux x64 的安装命令为：

```bash
# Debian / Ubuntu
sudo apt install ./trace-dock-linux-amd64.deb
# Fedora / RHEL
sudo dnf install ./trace-dock-linux-amd64.rpm
```

Linux ARM64 将文件名中的 `amd64` 改为 `arm64`。macOS 双击 `trace-dock-darwin-arm64.pkg`（Apple Silicon）或 `trace-dock-darwin-amd64.pkg`（Intel）安装到 `/usr/local/bin/trace-dock`。当前 `.pkg` 尚未配置 Apple Developer ID 签名和公证，可能出现系统安全提示，且需要在 macOS 上完成安装验收；正式公开发行前应接入签名、公证。Windows 暂提供 `.exe` 和 `.zip`，未提供 `.msi`。

也可下载便携二进制，重命名并放入 `PATH`。从源码安装：

```bash
mkdir -p "$HOME/.local/bin"
GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$HOME/.local/bin/trace-dock" ./cmd/trace-dock
chmod 0755 "$HOME/.local/bin/trace-dock"
```

运行 CLI：

```bash
trace-dock --server https://collector.example.com
```

便携二进制卸载：

```bash
trace-dock uninstall
```

该命令需要两次交互确认，删除当前 `trace-dock` 可执行文件及 CLI 专属的 `TRACE_DOCK_HOME`（配置、任务、归档、日志）。它不会删除该目录之外的用户文件、源代码、workspace、OSS 对象或服务端数据。数据目录含未知文件或符号链接时会拒绝卸载；Windows 会在 CLI 退出后由清理进程删除 `.exe`。

通过 `.deb` / `.rpm` 安装的程序使用 `sudo apt remove trace-dock` / `sudo dnf remove trace-dock` 卸载，以保持系统包数据库一致；包管理器卸载不清理用户的个人配置和任务数据。

配置优先级为 `--server`、`TRACE_DOCK_SERVER`、`$TRACE_DOCK_HOME/config.json`、编译时默认地址。仓库默认地址 `https://collector.example.com` 是占位值，发布时必须配置实际域名：

```bash
make release
```

打包前编辑 `release.json` 的 `version` 和 `default_server_url`，也可填写原生包的 `maintainer` 和 `license`（默认分别为 `Trace Dock maintainers`、`UNLICENSED`，不表示授予开源许可）。本地 `make release` 成功后自动递增 patch 版本，例如构建 `0.1.0` 后文件变为 `0.1.1`；设置 `auto_increment_patch` 为 `false` 可关闭自动递增。生成五个平台的裸二进制、Linux `.deb/.rpm`、macOS `.pkg`、Linux/macOS `.tar.gz`、Windows `.zip`、`dist/RELEASE.json` 和 `dist/SHA256SUMS`；全部使用 `CGO_ENABLED=0`。

Gitee Go 配置在 [.workflow/trace-dock-release.yml](.workflow/trace-dock-release.yml)。启用流水线后推送 `vX.Y.Z` 标签，会依次测试、构建、生成原生安装包和便携压缩包、校验并上传 Gitee Release。CI 版本取标签，不自动递增 `release.json`。当前使用私有 Gitee 实例，首次需在控制台导入 YAML 并配置执行环境和 Release 权限，具体见 [Gitee-CI-发布.md](docs/Gitee-CI-发布.md)。

| 设置 | 默认值 |
| --- | --- |
| `TRACE_DOCK_HOME` | Linux/macOS `~/.trace-dock`；Windows `%LOCALAPPDATA%/trace-dock` |
| `TRACE_DOCK_CODEX_HOME` | `~/.codex`，只发现其 `sessions/YYYY/MM/DD/rollout-*.jsonl` |
| `TRACE_DOCK_CLAUDE_HOME` | `~/.claude`，递归发现其 `projects/**/*.jsonl` |
| config.json | `{"server_url":"https://your-collector.example.com"}` |

任务目录包含 `job.json`、`selection.json`、`consent.json`、`manifest.json`、`exclusions.jsonl`、`large-files/`、`staging/`、`upload-state.json`、`logs.jsonl` 和成功后的 `receipt.json`。Unix 使用私有权限；Windows 使用当前用户 ACL。再次运行并验证同一身份后，可以恢复原 Job。恢复上传只读取原归档；恢复打包只处理已登记的源文件，不自动发现新增会话或子 agent。

workspace 自动分级。只有有项目证据且属于当前用户的目录可制作快照；home、parent、artifact、missing、unknown 和跨人目录只记录路径。轨迹与 workspace 在本机扫描密钥，命中时整文件排除，源文件不修改。`.git/` 绕过大文件规则但仍接受密钥扫描；大文件清单每行只有 `relative_path` 与 `remote_path`。符号链接只保留安全的目录内链接，不读取链接目标。Codex 缺少基线、压缩、未证明的写操作均不判为可重建；候选与异机验证通过数分开统计。

服务端位于 `../trace-dock-server`，使用其中的运行说明初始化身份、PostgreSQL 和 OSS。API JSON 契约见 [API-MVP.md](docs/API-MVP.md)。

OSS 上传由阿里云官方 OSS Go SDK v2 负责，服务端对象读取/校验由官方 OSS Python SDK v2 负责；CLI 不包含手写 OSS 签名或 multipart 协议实现。

对象键统一写入 `oss://<bucket>/trajectory/token=<token>/dt=<yyyy-MM-dd>/` 前缀；具体任务的 `object_prefix` 由服务端创建任务时返回，CLI 不自行推导日期或 bucket。

```bash
make test
make acceptance MANIFEST="$HOME/.trace-dock/jobs/JOB_ID/manifest.json"
```

有异机恢复结果时，可以追加 `BASELINE`、`RESTORED` 和 `SESSION` 三个参数进行文件树比较：

```bash
make acceptance \
  MANIFEST="$HOME/.trace-dock/jobs/JOB_ID/manifest.json" \
  BASELINE=/path/to/baseline \
  RESTORED=/path/to/restored \
  SESSION=SESSION_ID
```

生产服务地址和 OSS 必须使用 HTTPS；CLI 只允许在显式设置 `TRACE_DOCK_ALLOW_HTTP=1` 时访问回环 HTTP。

实际验证范围和上线前验收项见 [MVP-实现与验收.md](docs/MVP-实现与验收.md)。

