# MVP 实现与验收记录

实现依据为 `CLI-MVP-技术方案.md` 与 RFC-01。CLI 入口 `cmd/trace-dock`，服务端独立仓库 `../trace-dock-server`。两端通过 `API-MVP.md` 约定的 v1 JSON 接口交互。

已实现默认逐项问答、隐藏 token、身份核对、单工具选择、可搜索分页的根会话多选、默认拒绝的本机采集与最终上传确认。发现与采集分离；本地登记选择和时间后才进入 workspace 正文读取。恢复沿用原 Job、已登记源文件、冻结规则及已确认归档。Codex 未知/混合 schema 禁止选择；Claude 子 agent 依据明确字段或目录关联，支持深层子 agent；resume/fork 保留最长版本与 aliases。

workspace 使用项目证据、所有者与路径边界进行保守分类，不用 Git 仓库数量否定已锚定项目内的子模块。unknown、跨人、产物目录默认只记录路径。纯 Go 扫描规则覆盖凭据类型、常见 token/密码/私钥以及本次个人 token；扫描失败或文件持续变化时排除。确定性 tar.zst 保存普通文件、空目录、必要执行权限和安全的目录内链接。`.git/` 接受密钥扫描但绕过大文件排除。归档不修改源文件。

服务端两张表，预登记身份 HMAC 摘要、创建幂等、任务归属与状态约束、冻结规则、延迟 STS、实际 OSS SHA-256 校验、JSONL 字段/行数校验和幂等回执已实现。普通 API 超时 45 秒，完成校验允许 30 分钟。OSS 对象键统一为 `trajectory/token=<token>/dt=<yyyy-MM-dd>/`，由服务端创建任务时冻结并返回。真实云账号和服务地址由部署环境提供。

| 自动验证 | 结果 |
| --- | --- |
| Go 单测与 `go vet ./...` | 通过；覆盖 schema、损坏 JSONL、Windows 路径语义、alias/子 agent、分类、规则优先级、扫描、归档、恢复范围与对象跳过 |
| Python 服务端 unittest | 通过；覆盖无效/撤销身份、跨人任务隔离、并发创建幂等、单工具约束、授权与 STS 门槛、范围冻结、摘要校验与完成幂等 |
| CGO_ENABLED=0 交叉编译 | Linux amd64/arm64、macOS amd64/arm64、Windows amd64，生成裸二进制、deb/rpm/pkg、tar.gz/zip 和 SHA256SUMS |
| Linux 启动检查 | `--help`、`--version` 通过 |

系统预装 Go 1.27.4 的标准库含冲突文件，项目的 Makefile 与跨平台发布工具固定使用独立的 Go 1.26.5 工具链，未修改系统 Go。纯 Go macOS 安装包库要求该版本。首次执行会由 Go 自动下载工具链；已缓存后可直接复用。`GOTOOLCHAIN` 可显式覆盖为兼容版本。

上线前仍需提供生产域名、预登记身份、PostgreSQL、OSS bucket/RAM role 和两台 API 节点，执行真实 STS 直传、跨节点幂等/故障切换，以及 macOS、Windows 原生完整交互验收。CI 使用 GitHub Actions 在 Linux 上测试和交叉编译；交叉编译与解包检查不能代替原生安装运行验证。macOS pkg 的签名和公证仍待配置。

本版不自动生成题目或倒推目录。Codex 缺少起点基线时只归档；Claude 的 Edit old/new 证据仅能成为候选，Write 覆盖或未证明的 shell/工具写操作保持 unknown/archive_only；compacted、损坏或异常终止不计为有效候选。所有未经异机文件树比对的会话 `reconstructable_count=0`。执行 `make acceptance MANIFEST=/path/to/manifest.json` 可从 manifest 复核统计；同时提供 `BASELINE`、`RESTORED`、`SESSION` 时，会在异机比较人工独立恢复结果与可信起始基线，输出验证证据，不改写原始 manifest。没有可信基线时，不能宣布可重建验收通过。
