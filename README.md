# Trace Dock

Trace Dock 是一个交互式命令行工具，用来选择 Codex 或 Claude Code 的历史会话，检查本机 workspace，并把你授权的内容上传到 Trace Dock 服务端。

它适合需要提交任务轨迹、复现材料或项目快照的场景。工具会先展示发现结果，再分别请求“读取本机数据”和“上传到服务端”的确认；未确认前不会读取 workspace 内容或上传文件。

## 开始使用

### 1. 安装

使用 npm 全局安装。需要 Node.js 18 或更高版本，Go 和 Python 不需要安装。

```bash
npm install -g @pjlab/trace-dock
```

### 2. 运行

直接运行会进入交互式流程：

```bash
trace-dock
```

如果服务端地址不是默认地址，可以在命令行指定：

```bash
trace-dock --server https://collector.example.com
```

你需要准备：

1. Trace Dock 服务端地址。
2. 服务端发放的邀请代码或个人 token。
3. 本机已经存在的 Codex 或 Claude Code 会话数据。

运行过程中会依次完成身份验证、选择工具、发现会话、选择上传范围、本机采集确认、上传确认和服务端校验。服务端返回完成状态并保存本地回执后，任务才算完成。

### 3. 选择会话并上传

界面中的“全部上传”只表示全部符合条件的会话，仍会遵守 workspace 目录授权、敏感信息排除和大文件规则。

选择“部分上传”后，使用键盘操作：

| 按键 | 作用 |
| --- | --- |
| `↑` / `↓` | 移动当前会话 |
| `空格` | 选择或取消当前会话 |
| `A` | 全选或取消全选 |
| `/` | 按会话 ID 或 workspace 路径搜索 |
| `D` | 查看当前会话详情 |
| `PgUp` / `PgDn` | 翻页 |
| `Enter` | 确认当前选择 |
| `Ctrl+C` | 取消当前操作 |

会话列表中的“normal / abnormal”只描述轨迹是否观察到完成、错误或中断证据，不代表敏感信息检测结果。`abnormal` 会话仍可以选择和上传。

workspace 会根据目录情况显示为：

- `snapshot`：授权后读取符合规则的项目文件并生成快照。
- `path_only`：只记录 workspace 路径，不读取目录内容。

轨迹和 workspace 都会在本机进行敏感信息检查。命中的文件不会上传，源文件也不会被修改。大文件通常只记录路径，具体排除原因会显示在确认页和结果页。

## 配置

### 服务端地址

服务端地址的优先级从高到低为：

1. `--server`
2. `TRACE_DOCK_SERVER`
3. `$TRACE_DOCK_HOME/config.json` 中的 `server_url`
4. 编译时默认地址

例如：

```bash
export TRACE_DOCK_SERVER=https://collector.example.com
trace-dock
```

生产环境必须使用 HTTPS。仅在测试环境使用 HTTP，并显式打开开关：

```bash
./dist/trace-dock --allow-http --server http://127.0.0.1:8080
```

也可以设置 `TRACE_DOCK_ALLOW_HTTP=1`。HTTP 会让 token 和上传控制请求以明文传输，不要用于生产服务。

### 本地数据目录

Trace Dock 默认把配置、设备 ID、任务状态、本地归档和完成回执保存在：

- Linux / macOS：`~/.trace-dock`
- Windows：`%LOCALAPPDATA%\trace-dock`

可以通过 `TRACE_DOCK_HOME` 改变位置：

```bash
TRACE_DOCK_HOME=/data/trace-dock trace-dock
```

任务中断后再次运行并通过同一身份验证，可以恢复未完成任务。成功任务的回执位于：

```text
$TRACE_DOCK_HOME/jobs/<JOB_ID>/receipt.json
```

回执和任务目录包含任务摘要与本地状态，不要把其中可能含有服务端对象路径的文件公开转发。

### Codex / Claude 数据目录

通常不需要配置，Trace Dock 会读取默认目录：

- Codex：`~/.codex/sessions`
- Claude Code：`~/.claude/projects`

如果数据在其他磁盘、容器、WSL 或自定义目录中，请显式指定：

```bash
trace-dock --codex-home /data/codex
trace-dock --claude-home /data/claude/projects
```

也可以使用环境变量：

| 用途 | 命令行参数 | 环境变量 | 配置字段 |
| --- | --- | --- | --- |
| Codex 数据目录 | `--codex-home` | `TRACE_DOCK_CODEX_HOME` 或 `CODEX_HOME` | `codex_home` |
| Claude Code 数据目录 | `--claude-home` | `TRACE_DOCK_CLAUDE_HOME` 或 `CLAUDE_CONFIG_DIR` | `claude_home` |

优先级为命令行参数、`TRACE_DOCK_*_HOME`、`config.json`、工具原生环境变量和默认目录。参数可以填写工具数据根目录，也可以填写末级 `sessions` / `projects` 目录。

### config.json

配置文件位于 `$TRACE_DOCK_HOME/config.json`。只保存服务地址和本地目录，不保存 token：

```json
{
  "server_url": "https://collector.example.com",
  "codex_home": "/data/codex",
  "claude_home": "/data/claude"
}
```

`codex_home` 和 `claude_home` 都可以省略。

## 使用示例

### 连接本地测试服务

本地服务使用 HTTP 时，需要显式允许明文 HTTP：

```bash
./dist/trace-dock \
  --allow-http \
  --server http://127.0.0.1:8080
```

启动后按提示输入邀请代码，选择 `Codex` 或 `Claude`，在会话列表中用 `↑` / `↓` 移动、空格勾选，最后按 `Enter` 确认。

### 指定另一份 Codex 数据

```bash
trace-dock \
  --server https://collector.example.com \
  --codex-home /mnt/shared/codex
```

如果传入的是 Codex 数据根目录，工具会自动查找其中的 `sessions`；也可以直接传入 `/mnt/shared/codex/sessions`。

### 将 Trace Dock 数据放到指定位置

```bash
TRACE_DOCK_HOME=/data/trace-dock \
trace-dock --server https://collector.example.com
```

这样配置文件、任务状态和回执都会保存到 `/data/trace-dock`。

## 常用命令

```bash
trace-dock --help
trace-dock --version
trace-dock --server https://collector.example.com
trace-dock --allow-http --server http://127.0.0.1:8080
```

卸载 npm 安装：

```bash
# npm 安装：先清理 Trace Dock 本地数据，再移除 npm 包
trace-dock uninstall --data
npm uninstall -g @pjlab/trace-dock
```

`trace-dock uninstall --data` 会在两次确认后删除 Trace Dock 的本地配置、设备 ID、任务和归档，但保留 npm 管理的 CLI。它不会删除 workspace、源代码、服务端数据或 OSS 对象。

## 常见问题

### 提示“需要交互终端”

采集需要身份确认和上传确认，不能在没有 TTY 的后台管道中运行。请在终端、Windows Terminal 或支持交互输入的 SSH 会话中启动。

### 没有发现会话

确认当前终端用户能读取会话目录，并用 `--codex-home` 或 `--claude-home` 指向实际落盘目录。桌面客户端、另一用户、另一 WSL 环境和容器中的数据不会自动出现在当前用户的默认目录。

### HTTP 地址被拒绝

测试服务必须同时指定 `--allow-http`：

```bash
trace-dock --allow-http --server http://127.0.0.1:8080
```

生产服务请改用 HTTPS。

### 上传中断了

重新运行 `trace-dock`，通过同一身份验证后选择恢复未完成任务。已完成的本地归档会复用，不会因为恢复而自动发现新的会话。

## 开发与发布

维护者常用命令：

```bash
make build       # 构建当前平台二进制
make test        # go test ./... 和 go vet ./...
make release     # 构建全部平台发行包
```

发布参数在 [release.json](release.json)，发布流程说明见 [GitHub-CI-发布.md](docs/GitHub-CI-发布.md)。

服务端部署、API 契约和实现验收属于维护者文档：

- [API-MVP.md](docs/API-MVP.md)
- [CLI-MVP-技术方案.md](docs/CLI-MVP-技术方案.md)
- [MVP-实现与验收.md](docs/MVP-实现与验收.md)
