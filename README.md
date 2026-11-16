# trace-dock CLI MVP

按 [MVP 技术方案](docs/CLI-MVP-技术方案.md) 实现的 Go CLI。运行 `trace-dock` 后进入逐步交互：每屏只展开当前问题，已确认的地址、身份、工具和范围收在顶部；发现清单支持全部上传或进入部分多选。授权本机采集与最终 OSS 上传分别确认，个人 token 隐藏输入。

CLI 仓库的开发、测试和发布统一使用 Go 1.26.5，`make build`、`make test` 和 `make release` 均只使用 Go。该版本满足纯 Go 的 macOS 安装包库要求；首次使用时 Go 会自动下载工具链，可通过 `GOTOOLCHAIN` 显式覆盖为兼容版本。发布二进制不需要 Go、Python 或 Node.js。`--help`、`--version` 不扫描用户数据。界面需要交互终端，方向键移动、空格选择、A 全选/取消、`/` 搜索、D 查看详情、PgUp/PgDn 翻页；阶段标题及选择状态按终端能力着色，发现的跳过项按原因汇总。采集流程中的“是/否”确认默认选中“是”，仍需按 Enter 明确确认；卸载删除确认默认“否”。

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

GitHub Actions 配置在 [.github/workflows/release.yml](.github/workflows/release.yml)。推送 `vX.Y.Z` 标签后，GitHub Actions 会自动测试、构建原生安装包和便携压缩包、发布 npm 包，并创建 GitHub Release。CI 版本取 Git tag，不自动递增 `release.json`。首次发布前，在 GitHub 仓库 Secrets 中配置 `NPM_TOKEN`；详细设置见 [GitHub 发布说明](docs/GitHub-CI-发布.md)。

| 设置 | 默认值 |
| --- | --- |
| `TRACE_DOCK_HOME` | Linux/macOS `~/.trace-dock`；Windows `%LOCALAPPDATA%/trace-dock` |
| `TRACE_DOCK_CODEX_HOME` / `--codex-home` | 显式指定 Codex 数据根目录或 sessions 目录；未指定时自动读取 `CODEX_HOME`，最后使用 `~/.codex` |
| `TRACE_DOCK_CLAUDE_HOME` / `--claude-home` | 显式指定 Claude Code 数据根目录或 projects 目录；未指定时自动读取 `CLAUDE_CONFIG_DIR`，最后使用 `~/.claude` |
| config.json | `{"server_url":"https://your-collector.example.com","codex_home":"/data/codex","claude_home":"/data/claude"}`；两个工具路径字段可省略 |

轨迹目录优先级为命令行参数、`TRACE_DOCK_*_HOME` 环境变量、config.json 的 `codex_home` / `claude_home`、工具原生环境变量、默认数据目录。可填写工具数据根目录或末级 sessions/projects，支持空格及 `~/`；每次只扫描当前选中的一个目录，目录不存在或未发现轨迹时允许手动输入其他数据目录。发现成功后界面显示实际扫描路径。更改程序安装目录不影响发现；本地桌面客户端/IDE 只要把轨迹保存在相同布局中即可发现。GUI 客户端的自定义环境不一定被终端继承，可用参数或配置显式指定；远端、云端、另一用户或另一 WSL 环境的数据需在实际落盘环境采集或指定已挂载路径。当前 Codex 仍只扫描 sessions/YYYY/MM/DD/rollout-*.jsonl，不自动扫描 archived_sessions 或压缩文件。

```bash
./dist/trace-dock --codex-home /data/codex
./dist/trace-dock --claude-home /data/claude/projects
```

列表中的“轨迹有效/无效”描述完成与错误证据，与敏感信息检测无关。按 D 查看详情可见具体 `validity_reason`；无效轨迹仍可选择和归档。当前采用整份日志的保守口径：存在 JSONL 解析/读取异常，或任意位置出现中断、超时、API/工具错误、max_tokens 等记录，或未识别到结束标记，都会计为 invalid；中途出错、后来完成仍可能计为 invalid，详情明确提示“存在完成标记，也记录过中断或报错”。敏感信息检测在用户授权采集后单独执行，命中才整文件排除。

任务目录包含 `job.json`、`selection.json`、`consent.json`、`manifest.json`、`exclusions.jsonl`、`large-files/`、`staging/`、`upload-state.json`、`logs.jsonl` 和成功后的 `receipt.json`。Unix 使用私有权限；Windows 使用当前用户 ACL。再次运行并验证同一身份后，可以恢复原 Job。恢复上传只读取原归档；恢复打包只处理已登记的源文件，不自动发现新增会话或子 agent。

仅支持当前按 Job 隔离的对象格式。旧格式任务直接清理，不提供恢复或迁移；当前格式任务仍支持中断恢复。

`receipt.json` 是服务端完成 OSS 对象校验后的本地回执，记录 Job 状态、manifest 摘要、已校验字节数、对象数和汇总统计，供之后核对任务完成情况。当前回执还含 OSS 对象前缀；服务端此前缀包含参与者 token，因此不要公开或转发该文件内容。屏幕显示的 `receipt: /.../receipt.json` 只是文件位置，不是错误或额外命令。

轨迹按用户选择和文件可读性归档，未知或混合 schema 不会阻止选择或上传。CLI 尽力提取时间、cwd、会话关系等列表元数据；提取不到的字段记为未知，不猜测 workspace。通过敏感信息检测的轨迹按原始字节暂存和上传，不转换格式或改写事件；未知格式仍接受同样的密钥扫描与 CRC64 校验。

workspace 自动分级。只有有项目证据且属于当前用户的目录可制作快照；home、parent、artifact、missing、unknown 和跨人目录只记录路径。轨迹与 workspace 在本机扫描密钥，命中时整文件排除，源文件不修改。`.git/` 绕过大文件规则但仍接受密钥扫描；大文件清单每行只有 `relative_path` 与 `remote_path`。符号链接只保留安全的目录内链接，不读取链接目标。Codex 缺少基线、压缩、未证明的写操作均不判为可重建；候选与异机验证通过数分开统计。

workspace 单文件的大文件默认阈值为 1 GiB（1,073,741,824 bytes），由服务端 `rules.json` 的 `large_file_threshold_bytes` 下发，大文件规则版本为 `1.2.0`。严格超过阈值才按体积排除；`.git/**` 和 `*.analysis.json` 白名单优先，datasets/checkpoints 目录及明确的模型/数据格式黑名单仍可使小文件出列。普通 `.zip/.tar/.gz/.zst/.7z` 压缩包和泛化 `.bin` 不再仅凭后缀出列，只有超过阈值或命中目录规则才出列；保留 `pytorch_model*.bin` 的模型类型规则。Python `site-packages` / `dist-packages` 直接子文件中的 `.pth` 按路径配置处理，不命中模型后缀规则，但仍接受体积和敏感信息检测；其他位置的 `.pth` 模型权重保持类型排除。调整规则后重启服务端，仅新建任务使用新阈值；已有任务沿用创建时冻结的规则。

密钥检测使用 `guiji` 项目第二层的 16 条 Gitleaks 高置信度规则，并补充明文凭据兜底。兜底检测 password/api_key 等敏感字段的字符串值，以及 `os.getenv`、`getenv`、`os.environ.get` 的明文默认值；空值、明确占位示例、变量引用和函数调用不会仅因字段名被排除。它支持轨迹中的多层 JSON 转义；只返回是否命中，不脱敏、不替换或修改源文件及上传内容。匹配凭据格式的演示 token 仍可能命中。不启用 PII/NLP、个人 token 原文匹配或服务端额外正则。规则嵌入 CLI，不需要安装 Gitleaks 或 Python。密钥规则版本为 `2.1.0`，服务端 `trace_dock_server/rules.json` 须使用相同的 `secret_version` 并保持 `secret_patterns: []`，更新后重启服务端。使用旧检测规则的任务需重新创建。

服务端位于 `../trace-dock-server`，使用其中的运行说明初始化身份、PostgreSQL 和 OSS。API JSON 契约见 [API-MVP.md](docs/API-MVP.md)。

OSS 上传由阿里云官方 OSS Go SDK v2 负责，服务端对象读取/校验由官方 OSS Python SDK v2 负责；CLI 不包含手写 OSS 签名或 multipart 协议实现。

上传确认页、上传页和结果页统一区分已选主会话、实际可上传/已上传的主会话轨迹、关联子 agent 轨迹及排除数量。workspace 快照按目录去重独立计数；轨迹被排除时，其独立扫描通过的 workspace 仍可能上传。确认页按上传内容、上传体积、排除情况三个区域分组，使用对齐的数量和体积行。最多展开三条未上传轨迹明细，每条分别标注项目目录名、完整会话 ID、轨迹排除原因和关联项目快照处理结果，ID 不截短，窄屏自动换行。全部排除记录单独汇总，明确覆盖本次所有选中轨迹和去重项目目录；敏感文件不上传、大文件只上传路径、其他目录或链接排除分别显示。大文件总数后注明“文件类型排除 X 项 · 体积超限 Y 项”；两项互斥，命中类型/目录规则时优先计入文件类型排除。完整明细保留在本地清单，大量选择时不逐条展开。预计上传合计是本次所选内容经过排除和压缩后的轨迹、快照、manifest 和各类清单总和，并显示体积分项；最终确认时间写入 manifest 后，精确上传总字节数以冻结的 upload-state 和回执为准。

上传页展示精简确认摘要、绿色传输进度条、已传/总大小、平均速度和预计剩余时间。传输结束后收起百分比进度条，切换为“服务端校验（尚未完成）”，持续刷新等待动画及已等待时间，明确提示保持终端开启；不以传输 100% 表示任务完成。服务端返回 completed 且本地保存回执后才显示完成结果。传输中 Ctrl+C 暂停会保留本地任务，可再次运行恢复。

校验通过后的结果页仅保留连接摘要与完成结果，列出 Job ID、已选/可选主会话数、已上传/已选主会话轨迹数、已上传/关联子 agent 轨迹数、未选中数量、workspace 快照数、密钥及其他排除数、发现/选中/无效主会话数、上传量、已校验对象数和完整 receipt 路径。所选会话因密钥等原因未生成轨迹时单独注明，不计入已上传主会话，也不计作未选中。

对象键写入 `oss://<bucket>/trajectory/token=<token>/dt=<yyyy-MM-dd>/job=<job-id>/`；该 Job 下包含一份 manifest 和 exclusions、每个会话各自的轨迹，以及按 workspace artifact 去重的快照和大文件清单。workspace 文件位于 `workspaces/<workspace-artifact-id>/`。前缀由服务端创建任务时返回，CLI 不自行推导日期或 bucket。注意当前 token 原文出现在 object key 和 manifest.object_prefix 中，与 token 不进入 OSS 的安全目标冲突。

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

