# trace-dock CLI MVP 技术方案

**状态**：提案  
**规则依据**：[RFC-01-存量workspace与轨迹回收.md](./RFC-01-存量workspace与轨迹回收.md)  
**关联完整方案**：[CLI-存量workspace与轨迹回收技术方案.md](./CLI-存量workspace与轨迹回收技术方案.md)  
**实现范围**：只实现完整方案的“4.1 默认交互”闭环。

RFC-01 是处理规则的规范来源；完整方案和本方案按同一规则采集。MVP 仅缩减命令、看板、管理界面和数据库拆分等实现范围，不简化解析、授权、分类、排除、去重或验收规则。规则冲突时以 RFC-01 为准，先修订技术方案再开发。

## 1. MVP 目标

参与者安装工具后，在终端运行 `trace-dock`，按逐项问答完成：

1. 核对预填的默认服务地址（可按需修改），输入本人 token；服务器通过 token 识别预先登记的邮箱。
2. 单选采集 Codex 或 Claude；一次运行只采集一种工具。
3. 浏览发现的会话及 workspace 分类，选择全部或部分会话。
4. 确认授权 CLI 在本机读取所选 workspace 并执行密钥扫描。
5. 查看最终待上传范围、体积和排除清单，再单独确认上传。
6. CLI 将选中数据直传 OSS，显示校验结果和采集编号。

每次只显示当前需要回答的问题。用户确认后，已完成选择收起为结果摘要，再显示下一步。MVP 不建设统计看板、题库、人工标注或后台管理界面。

## 2. 语言与运行方式

- **CLI：Go**，命令入口为 `trace-dock`；使用逐项选择界面，支持方向键、空格多选和回车确认。
- **服务端：Python 3.12+**，提供 HTTPS API；沿用既定的两台无状态 API 节点、负载均衡、PostgreSQL 和 OSS。
- CLI 使用标准库和纯 Go 依赖，设置 `CGO_ENABLED=0`，在一台构建机交叉编译 Windows x64、macOS arm64/x64、Linux x64 产物；Linux arm64 可同时交叉编译或在首发后加入。
- 向参与者发布对应平台的独立二进制和 SHA-256 校验值；参与者不需要安装 Go 或 Python。
- 轨迹、workspace、密钥扫描、路径分类和 OSS 直传都在参与者本机执行；服务端不接收文件正文。

CLI 与服务端按职责选择语言：Go CLI 提供单文件部署、低内存占用和易并发的本机扫描；Python 服务端复用团队现有后端技术栈。两端通过版本化 HTTPS API 和 JSON manifest 交互。

CLI 内置默认服务地址。用户可通过配置文件修改默认值，也可在启动时临时覆盖。跨平台产物由一台 CI 构建机交叉编译；用户下载对应系统的二进制即可。

配置文件沿用完整方案的 `$TRACE_DOCK_HOME/config.json`；默认任务根目录为 Linux/macOS `~/.trace-dock`、Windows `%LOCALAPPDATA%/trace-dock`。配置只保存服务地址等非敏感设置，优先级为命令行、环境变量、配置文件、内置默认值。

轨迹位置新增 `--codex-home` / `--claude-home` 参数及配置字段 `codex_home` / `claude_home`。位置优先级为命令行参数、`TRACE_DOCK_CODEX_HOME` / `TRACE_DOCK_CLAUDE_HOME`、配置文件、原生 `CODEX_HOME` / `CLAUDE_CONFIG_DIR`、默认 `~/.codex` / `~/.claude`。允许填写数据根目录或末级 sessions/projects，找不到轨迹时可在界面手动指定；不根据程序安装目录猜测数据位置，不递归扫描整个磁盘。实际路径写入界面摘要；轨迹 data home 作为私有状态边界，在发现、恢复和打包登记时持续禁止成为 workspace 快照。

```json
{"server_url": "https://collector.example.com"}
```

```bash
# 临时覆盖当前运行使用的地址
trace-dock --server https://collector-staging.example.com
```

首次启动显示预填的默认地址，用户可直接按 Enter 使用，或编辑本次地址；若编辑并选择保存，则更新配置文件。个人 token 不保存到配置文件。

## 3. 身份与 token

工具开发前，由运营人员准备每位参与者的**邮箱 + 个人 token**，并将对应关系预登记到服务端。CLI 中原“邀请代码”输入框用于填写该个人 token，不再另建邀请码流程。

- 运营侧用受控脚本为每个邮箱生成独立的高熵随机 token，导入时服务端只保存 HMAC 摘要；原始 token 仅在生成时显示一次，由运营人员安全交付给对应参与者。
- token 是随机高熵凭证，只能识别一个预登记邮箱；不是邮箱密码，也不用于登录邮箱。
- CLI 通过 HTTPS Authorization header 发送 token；不在命令行参数、任务目录或日志中保存 token。当前 OSS 前缀格式将 token 原文放入 object key，manifest.object_prefix 也包含该前缀；这与 RFC-01 没有明文冲突，但违反本技术方案原先“不在 OSS 中保存 token”的安全目标，仍需确定生产采用的前缀凭据策略。
- 默认服务地址随 CLI 发布配置提供；首次运行不要求用户输入地址。用户可修改配置文件或在本次运行中覆盖。
- 服务端只保存 `HMAC-SHA256(server_pepper, token)` 摘要，并建立唯一索引；原始 token 仅在准备名单中安全生成和发放。
- 认证成功后服务端返回 participant ID 和脱敏邮箱供参与者核对。无效、撤销或未知 token 均停止流程，不显示 token 是否与其他邮箱匹配。
- token 可单独撤销和重新签发。认证接口启用限速；日志只记录认证结果码，不记录 Authorization header。

## 4. 默认交互与范围选择

MVP 按顺序执行：

| 步骤 | CLI 行为 | 确认后的状态 |
| --- | --- | --- |
| 启动与身份识别 | 显示预填服务地址，允许本次修改或保存为新默认值；输入个人 token 并验证 | 服务地址已配置、身份已验证 |
| 选择工具 | 单选 Codex 或 Claude；一次运行只采集一种工具，不支持 Codex + Claude 混合采集 | 显示已选工具 |
| 登记任务 | 自动登记参与者、机器和工具，获取 Job ID 与版本化规则；此步没有额外问答 | 已登记，尚未采集或上传文件 |
| 本机发现 | 读取轨迹 JSONL 提取元数据，递归发现 Claude 子 agent；不读 workspace 文件内容 | 显示发现数量 |
| 选择范围 | 清单显示工具、session ID、结束时间、cwd、workspace 处理方式和子 agent 数；支持搜索、分页、全部上传或多选 | 显示选中数和未选中数 |
| 本机采集授权 | 展示选中的会话/workspace 范围；默认“是” | 用户确认后才读取所选 workspace |
| 本机扫描与打包 | 先在本地逐会话登记源路径和时间，再只处理选中项；按 RFC-01 自动分级 cwd、本机扫描密钥、生成归档及 manifest | 显示实际归档体积与排除清单 |
| 最终上传确认 | 显示选中会话、子 agent、workspace 快照、体积、排除数量及 manifest 摘要；默认“是” | “是”后为已登记 Job 申请 OSS 临时凭证 |
| 上传与结果 | CLI 直传 OSS，服务端通过 HeadObject 校验对象大小和 CRC64 | 显示 Job ID、成功/失败数量和 receipt 路径 |

### 4.1 workspace 自动分类

workspace 分级不是用户需要理解或选择的策略，由 CLI 根据 RFC-01 自动判定：

| cwd 分类 | MVP 处理 |
| --- | --- |
| 单一项目目录 | 按密钥规则扫描后制作快照 |
| home、包含多个仓库的 parent、artifact、missing | 只记录路径和分类原因，不制作快照 |
| unknown | 默认只记录路径 |

用户按根会话选择；选中 Claude 根会话时自动包括其子 agent。多个选中会话引用同一 workspace 时，在本次 job 中共享一份快照。未选会话的 workspace 内容不读取、不扫描、不打包、不上传；最终上传确认和中断恢复均不得扩大原选择范围。

密钥命中文件整个排除且不原地修改。MVP 只上传选中会话的轨迹、允许的 workspace 快照、manifest、排除清单和大文件清单。

### 4.2 RFC-01 处理规则（MVP 保持一致）

CLI 按当前采集目标发现轨迹、登记选择、扫描敏感信息并原样归档。schema 识别只服务于元数据提取和下游分析提示，不作为用户选择或轨迹上传的门槛。workspace 分类、敏感信息排除、明确关联去重和文件读取边界仍按以下规则执行。

**发现与解析**

- Codex 只扫描 `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`。旧版按顶层 `type=response_item` 识别 `apply_patch` / `exec_command`；新版支持 `response_item.payload.type=custom_tool_call` 和 `event_msg.payload.type=item_completed`，也支持无顶层 type 的独立 payload 形状，并保留可能存在的 ordinal / thread_name。新版文件中的这些封装事件可以与普通 `response_item` 消息共存，不能仅因同时出现这两类标记就判为混合格式。适配器 `1.1.0` 将格式识别与归档资格分开；无法明确分类的结构记录为 unknown 或 mixed，原始轨迹仍可供用户选择。
- Codex 未知或混合 schema 只记录格式标签及 evidence，不因格式而隐藏或排除会话。读取失败的文件不能归档；可读文件通过敏感信息检测后按原始字节上传，不解析重写正文。
- Claude 从 `~/.claude/projects` 递归发现所有 JSONL，必须包括 `<session-id>/subagents/agent-*.jsonl`。路径只从轨迹事件的 cwd 字段取得；projects 编码目录会损失非 ASCII 路径信息，不可用于还原路径。
- Claude 轨迹按已知字段尽力提取元数据；JSONL 损坏或缺少结束证据保守记录为 invalid，未知格式记录为 unknown，这些分析标签不阻止原文归档。不猜测无法提取的字段；缺少 cwd 时仅归档轨迹，不推断 workspace。
- Claude 子 agent 依据明确事件关联或 `<session-id>/subagents/agent-*.jsonl` 目录结构关联到父会话。无证据时不按相似路径猜测。
- 每个被采集的根会话记录参与者、机器名、工具、workspace/cwd、轨迹路径、会话结束时间、采集时间和二者秒差；目录顶层 mtime 只作参考，不能证明子目录未变化。
- Codex ID 优先使用 rollout 文件名中的 UUID；不含 UUID 时使用原文件路径的摘要生成稳定文件 ID 并注明来源。Claude ID 使用主轨迹文件名。resume/fork 按明确根会话归并，依次按事件数、文件大小、结束时间选择最长版本，其余 ID 记为 alias；不跨工具归并。

**可逆性与有效性**

有效性和敏感信息检测分别执行。发现阶段仅依据结束/错误记录给出 validity，并记录 `validity_reason`：`completion_observed`（结束证据充分且无错误）、`trajectory_parse_error`（JSONL 解析或读取异常）、`completed_with_recorded_errors`（有结束标记也有历史中断/错误）、`error_or_interruption_recorded`（记录了错误或中断）、`completion_not_observed`（未识别到结束）。当前整份日志的保守统计口径不因后续恢复而清除历史错误；D 详情解释原因，但这些标签不影响原文归档资格。Codex 结束证据为 task_complete 或 assistant final_answer，task_started 重新置为未结束；turn_aborted/error/timeout 记录错误。Claude 结束证据为 assistant stop_reason=end_turn，user 消息重新置为未结束；system error/turn_aborted、error、is_error、isInterrupted、max_tokens、内容块 is_error 和显式用户中断文本记录错误。敏感信息扫描发生在授权后，检测结果只影响文件是否出列，不参与发现时的 validity 判定。

- Codex 轨迹只有 unified diff；没有起始基线时标记 not_reconstructable/archive_only。出现 compacted 事件的会话标记不可逆，只归档。任何会话不得仅凭工具调用记录推断可逆。
- Claude 可逆性判定必须包含主轨迹和全部子 agent。Edit 需有完整 old/new 证据；Write 覆盖旧文件但没有旧内容证据时不能判为可逆。
- sed -i、重定向、cp、git checkout 等 shell 写操作若无法从轨迹完整证明或重放，标记 unknown/archive_only。只有异机 verifier 比对文件树通过，才能最终标为 reconstructable。
- 中断、超时、报错或终止状态不完整的会话标为 invalid；仍可归档，但不作为题目候选。MVP UI 展示状态，不因选中而把 invalid 会话改成 valid。
- JSONL 损坏只将对应 session 标为 invalid，不中止同一批其他会话的采集。

MVP 不生成评测题，也不在默认交互中自动倒推目录。规则初判有希望倒推的会话仅记录候选依据，`reversibility` 仍为 unknown；只有异机 verifier 比对通过的会话才记为 reconstructable。试采验收必须抽样验证候选；失败时先修规则再扩大采集，未验证的会话不得计入已验证可重建数。该验收可以通过独立脚本人工执行，不要求开发看板或新的 CLI 问答。

**workspace、路径与大文件**

- 自动分类保持 RFC-01 的保守原则：当前项目证据为 `.git`、`go.mod`、`pyproject.toml`、`package.json`、`Cargo.toml`、`pom.xml`、`CMakeLists.txt`、`setup.py`、`Makefile`；只有无项目锚点且至少两个直接子目录含 `.git` 才判为 parent。artifact 当前按 cwd 最后一级名称 `checkpoints`、`checkpoint`、`datasets`、`dataset`、`training_logs`、`artifacts`、`runs` 识别；其他训练/日志产物名可能落入 unknown/path_only，尚未覆盖 RFC-01 所有实际目录命名。home、parent、artifact、missing、unknown、跨所有者目录默认只记录路径。不能仅凭 `.git` 数量将单一项目中的子模块误判为 parent。
- Claude projects 目录名不可用于 cwd 还原。保存 cwd 原值和规范化值；Windows 路径比较忽略大小写，Linux/macOS 保留大小写语义。
- workspace 归档不跟随目录外符号链接；归档成员路径必须是安全相对路径。`.git/`（包括 pack）不因大文件规则出列；密钥命中仍按整文件排除处理。
- workspace 快照使用确定性 tar.zst：最多 4 个 worker 并行扫描/暂存文件，随后按原相对路径顺序排序写入归档，固定归档 mtime 并只保留必要权限；并行阶段不写最终 tar，因此不改变目录结构和成员顺序。轨迹文件在扫描复制阶段同步计算 CRC64，workspace 压缩输出阶段同步计算 CRC64，避免上传前再次完整读取大文件。采集不删除、移动或修改源文件。快照取自用户授权后的本次采集时刻，不是历史会话结束时的文件树；`collection_delay_seconds` 只能提示延迟，不能恢复期间发生的目录变化。扫描文件期间比较 inode、size、mtime 并最多重试一次，记录 `changed_during_collect`；这不是原子文件系统快照，metadata 恢复原值的并发修改仍可能无法识别。
- 大文件规则与 RFC-02 共用同一版本配置，严格按白名单 → 黑名单 → 配置化体积阈值执行。白名单优先；黑名单和阈值命中的文件从快照出列，但不改动源文件。体积阈值从规则配置读取，不写死在 CLI。
- 大文件 JSONL 每项只含 `relative_path` 和 `remote_path`。每份清单按 workspace artifact ID 单独存放，并由 manifest 关联快照；一个 Job 内共用 cwd 的会话共享该清单。当前默认规则见服务端 `rules.json`：大文件规则版本 `1.2.0`，阈值 1 GiB（1,073,741,824 bytes）；文件严格超过阈值时才按体积出列，恰好 1 GiB 不因体积出列。白名单为 `.git/**`、`*.analysis.json`，黑名单为 datasets/checkpoints 目录、*.pt、*.pth、*.safetensors、*.ckpt、*.onnx、*.gguf、pytorch_model*.bin、*.parquet、*.arrow。普通 zip/tar/gz/zst/7z 压缩包及泛化 *.bin 不按后缀出列；site-packages/dist-packages 直接子文件中的 .pth 是 Python 路径配置，不作为模型权重，但仍接受体积及敏感信息检测。其他目录的 .pth 仍按模型类型排除。白名单先于黑名单和阈值；`.git/` 特殊处理为始终不按体积出列。remote_path 从轨迹中可识别的 wget/curl/aws s3 cp/scp 命令提取，否则使用 `<machine_name>:<absolute_path>`，未知为 null。文件类型/目录规则与体积超限分别在 exclusions 中记为 large_file_type 和 large_file_size，类型/目录优先，计数互斥。确认、上传和完成页在大文件总数后附各原因数量，large-files JSONL 的行结构不增加字段。大文件命中密钥扫描时只进 exclusions，不进入 large-files 清单；白名单不能绕过密钥扫描。阈值调整只影响服务端重启后新建任务，已有任务沿用冻结值。

**密钥与本机边界**

- 在源机器扫描待上传的轨迹正文、将进入快照的 workspace 文件及命中大文件规则的文件正文。检测复用 `guiji` 第二层的 16 条 Gitleaks 高置信度规则，涵盖常见服务 token、JWT、完整 PEM 私钥块，并补充明文凭据兜底。兜底检查 password/api_key 等敏感字段的实际字符串值和 `os.getenv`、`getenv`、`os.environ.get` 的明文默认值，跳过空值、明确占位示例、变量引用及函数调用；不会把 `os.getenv`、`MILVUS_API_KEY` 或 `build_token()` 本身当作凭据值。规则集版本为 `2.1.0`，Gitleaks 快照为 CLI 内嵌的 `internal/core/secret-rules.json`。不启用字段名强制脱敏、PII/NLP、个人 token 原文匹配或服务端额外正则；不按 `.env`、`.npmrc`、`.pypirc` 文件名直接排除。检测只判断是否命中，命中时整文件不上传、不原地替换；secret 值不得进入日志、错误消息、manifest 或 API 请求。排除记录只含 path、reason 和可选 session/workspace ID，不含 hash 或 rule ID。格式匹配不验证凭据有效性，符合凭据形状的演示字符串仍可能命中；规则范围不代表覆盖所有凭据。
- 扫描器异常时对该文件 fail-closed。发现结束后，未选会话不再进入正文采集、密钥扫描或打包；拒绝采集时不扫描或打包 workspace。
- 不扫描或复制整个 `~/.claude` / `~/.codex`；不读取或复制 Claude `.credentials.json`、shell-snapshots、memories，或 Codex `logs_2.sqlite` 及其 `-wal` / `-shm` 文件。
- 跨人 workspace 路径必须在清单中明确警示。参与者 token 不代表其拥有他人目录；无额外授权时该 workspace 只记路径，不读取、不快照。
- 缺少允许采集的 workspace 快照、属于非代码会话或唯一轨迹因密钥被排除时，只保留可安全归档的内容，不作为当前题目候选。

当前实现的 artifact cwd 规则和密钥检测规则比 RFC-01 的自然语言范围更窄；两者是已实现的保守初版，不应据此宣称覆盖全部日志/产物目录或全部密钥形状。增加规则需更新版本化服务端 `rules.json` 并补充 fixture。

MVP 发现阶段需要在本机读取候选轨迹 JSONL 以识别 schema、提取 cwd 和会话元数据；允许查看分类所需的目录元数据，但不读取 workspace 文件内容。后续轨迹正文采集、密钥扫描、workspace 文件读取和打包仅处理用户明确选中的根会话及其关联子 agent。未选会话不上传路径或正文，只在本机保留当前选择所需元数据；服务端仅接收发现总数。

### 4.3 manifest 与登记口径

任务登记与逐会话登记分两步：选择工具后先创建服务端任务；用户授权后，再在本地逐会话登记以下字段，之后才读取所选 workspace 文件并生成归档。这样既遵守 RFC-01“先登记，再采集”，也不会提前上传未选会话的路径。会话结束时间未知时记 null 和原因，不能用采集时间补造结束时间或把秒差写成 0。

| 层级 | 必须记录的内容 |
| --- | --- |
| 任务 | manifest_schema、job_id、participant_id、device_id、machine_name、CLI/adapter 版本、分类/密钥/大文件规则版本、授权和最终上传确认时间、选择范围及摘要 |
| 每个根会话及子 agent | tool、原生 session_id、root/parent ID、aliases、schema、原轨迹路径、cwd 原值/规范化值、存在性及分类、结束时间、采集时间、collection_delay_seconds、有效性、可逆性及依据、归档资格或跳过原因 |
| 采集对象 | artifact_id、kind、关联会话/workspace、对象键、字节数、CRC64 和校验结果；大文件行仍只含 relative_path、remote_path |
| 汇总 | 按参与者、工具统计选中会话数、workspace 仍在数、可逆候选数、已验证可重建数、只归档数、无效数、排除数，以及远端路径缺失数/大文件总数和缺失比例 |

逐会话登记、原始证据和汇总存入本地 manifest；授权上传后存入 OSS。候选数和已验证数分开统计；大文件总数为 0 时缺失比例记 null。manifest 不包含邮箱字段，但 `object_prefix` 当前含参与者 token 原文，因此 manifest 和 OSS object key 均会间接暴露该 token；见 4.4 的未解决安全问题。MVP 不开发看板，但须能通过 receipt 和验收脚本从 manifest 复核这些统计。

会话汇总以去重后的选中根会话计数，子 agent 单列数量；多个根会话共享快照不减少会话数。manifest 的 `discovered_count` 记录全部发现数，`selectable_count` 记录发现时符合采集条件的主会话数；结果页以可选数减去选中数计算未选中数量，不能把文件读取或关联失败的跳过项算作未选中；未知 schema 本身不再产生跳过项。已上传主会话和子 agent 仅统计实际生成轨迹 artifact 的项，密钥排除文件数从 exclusions 的 `secret_detected` 行统计。有效性与归档资格分别统计，invalid 可以同时属于 archive_only，两者不能相加作为总会话数。本地任务目录、文件权限和 OSS 对象键沿用完整方案 4.3、9.3。当前 `changed_during_collect` 通过读取前后的文件身份、大小及修改时间检测并重试；manifest 的 CRC64 标识最终暂存对象的完整性，不证明原文件在整个采集区间保持不变。

### 4.4 OSS 对象结构

服务端为每个 Job 返回独立前缀。当前前缀格式为 `trajectory/token=<token>/dt=<yyyy-MM-dd>/job=<job-id>/`；日期来自 Job 创建日期，`job-id` 隔离同一参与者同一天创建的不同任务。STS 只允许写入当前 Job 前缀。

```text
trajectory/token=<token>/dt=<date>/job=<job-id>/
├── manifest.json
│   # 当前 Job 的总索引，含所选会话、artifact 对象键和校验摘要
├── exclusions.jsonl
│   # 当前 Job 的所有排除项，每行记录 path、reason 及可选的 session/workspace ID
├── sessions/
│   └── <tool>/
│       └── <session-id>/
│           └── trajectory.jsonl
│               # 此会话通过本机密钥扫描后的轨迹正文
└── workspaces/
    └── <workspace-artifact-id>/
        ├── workspace.tar.zst
        │   # 该 workspace 的一次确定性快照
        └── large-files.jsonl
            # 该快照中出列的大文件清单，每行仅有 relative_path、remote_path
```

只有生成 workspace 快照时才有对应的 workspace 目录；没有大文件时 `large-files.jsonl` 仍可为空。当前 workspace ID 为 `workspace-<SHA-256(规范化 cwd) 前24位>`。一个 Job 内多个会话使用相同规范化 cwd 时，共享同一个 workspace artifact：每个会话分别上传自己的 `trajectory.jsonl`，共享一份 `workspace.tar.zst` 和一份 `large-files.jsonl`，各会话通过 manifest 的 `workspace_artifact_id` 引用该快照。该快照代表采集时目录状态，不是每个会话结束时的历史快照。

`receipt.json` 仅写入本地 Job 目录，不作为 OSS 对象。`manifest.json` 和 `exclusions.jsonl` 是每个 Job 一份，不按日期跨 Job 共用。

服务端完成校验时只下载小型 `manifest.json`，随后按 manifest 中的对象键调用 OSS `HeadObject`，比较对象大小和 `x-oss-hash-crc64ecma`；轨迹、workspace、exclusions 和 large-files 对象正文不下载。服务端配置 `OSS_READ_ENDPOINT` 可单独指定读取对象元数据的 endpoint，返回给 CLI 的 `OSS_ENDPOINT` 仍为直传 endpoint。同地域 ECS 可设置 OSS 内网 endpoint；开发机或 VPC 外服务应留空。服务端对象校验不再使用并发下载。

当前实现把返回的 object prefix 原样写入 manifest，并包含在 OSS object key 中；因此该前缀中的 token 值也会出现在对象键和 manifest 正文。它与本节前文“token 不在 manifest 或 OSS 中保存”的要求冲突，是已知的隐私/凭据设计不一致，不能把“只在 key 中出现”视为未写入 OSS。部署前需要统一 token 保护要求和前缀命名策略。

仅支持当前按 Job 隔离的对象格式。旧格式任务直接清理，不提供恢复或迁移；当前格式任务仍支持中断恢复。

## 5. 最小数据表

MVP 只建两张表。会话选择明细和 artifact 清单保存在本地 manifest，并将最终 manifest 上传 OSS；数据库保存身份、任务状态和验收所需汇总，不为每个会话或文件新增数据库行。

### participants：预登记身份

| 字段 | PostgreSQL 类型 | 注释 |
| --- | --- | --- |
| participant_id | text | PK；伪名参与者 ID |
| email | text | NOT NULL、UNIQUE；预登记邮箱 |
| token_hash | bytea | NOT NULL、UNIQUE；个人 token 的服务端 pepper HMAC |
| status | text | NOT NULL；active / revoked |
| created_at | timestamptz | NOT NULL；预登记时间 |
| updated_at | timestamptz | NOT NULL；记录实际修改时间，数据库触发器自动维护；新增时等于 created_at |
| revoked_at | timestamptz | 可空；token 撤销时间 |

### collections：采集任务与汇总状态

| 字段 | PostgreSQL 类型 | 注释 |
| --- | --- | --- |
| job_id | text | PK；服务端生成的任务 ID |
| participant_id | text | NOT NULL；FK → participants.participant_id |
| device_id | text | NOT NULL；CLI 随机生成，不含硬件信息 |
| machine_name | text | NOT NULL；采集机器 hostname；按 RFC-01 随任务登记 |
| client_operation_id | text | NOT NULL；创建请求幂等 ID；与 participant_id 组合 UNIQUE |
| state | text | NOT NULL；created / collecting / awaiting_upload_confirmation / uploading / verifying / completed / resumable / failed / cancelled |
| tool | text | NOT NULL；本次选择的单一工具，CHECK codex / claude |
| selection_mode | text | 选择前可空；用户确认后为 all / partial |
| discovered_count | integer | NOT NULL DEFAULT 0；发现的根会话数 |
| selected_count | integer | NOT NULL DEFAULT 0；选中的根会话数 |
| workspace_exists_count | integer | NOT NULL DEFAULT 0；用户授权处理的选中会话中，采集时 cwd 存在数 |
| reconstructable_count | integer | NOT NULL DEFAULT 0；异机 verifier 比对通过的会话数；未验证候选不计入 |
| archive_only_count | integer | NOT NULL DEFAULT 0；选中会话中只归档数 |
| invalid_count | integer | NOT NULL DEFAULT 0；选中会话中无效数 |
| excluded_count | integer | NOT NULL DEFAULT 0；本机安全规则排除数 |
| large_file_missing_remote_path_count | integer | NOT NULL DEFAULT 0；大文件 remote path 缺失数 |
| uploaded_bytes | bigint | NOT NULL DEFAULT 0；服务端已校验字节数 |
| manifest_key | text | 最终 manifest 的 OSS 对象键 |
| manifest_crc64 | text | 最终 manifest 的 OSS CRC64 |
| created_at | timestamptz | NOT NULL；任务创建时间 |
| updated_at | timestamptz | NOT NULL；任务状态、授权、统计或校验元数据实际修改时间，数据库触发器自动维护 |
| consented_at | timestamptz | 用户授权本机采集的时间 |
| upload_confirmed_at | timestamptz | 用户确认最终上传范围的时间 |
| completed_at | timestamptz | 所有对象校验通过时间 |

collections 不存文件正文、每会话行、排除项明细或上传分片明细：这些内容分别在 OSS manifest/JSONL 和客户端本地 upload-state 中。MVP 不建独立 artifacts、sessions、exclusions、large_files、审计事件或幂等表。

两张表的 updated_at 均以 UTC 保存，空更新、写入相同值及幂等重试不刷新，事务回滚时一并回滚。服务端初始化会幂等补齐历史表字段，并按已有创建/撤销/授权/完成时间回填；没有记录过的历史修改时间无法还原。SQLite 开发库以带时区的 ISO 文本保存，PostgreSQL 使用 timestamptz。

## 6. 最小服务端 API

| 方法 | 路径 | MVP 用途 |
| --- | --- | --- |
| POST | /v1/identity/verify | 校验个人 token，返回 participant ID 和脱敏邮箱 |
| POST | /v1/collections | 选择工具后、采集前登记任务，返回 Job ID 和版本化规则；不签发 STS |
| POST | /v1/collections/{job_id}/credentials | 签发只允许写当前 Job 前缀的短期 OSS STS |
| PATCH | /v1/collections/{job_id} | 更新状态、选择/排除计数、已校验上传字节数及错误码 |
| POST | /v1/collections/{job_id}/complete | 提交 manifest 摘要；服务端校验 OSS 对象并完成任务 |

CLI 用 STS 将文件直接上传 OSS，API 节点不转发文件内容。STS 仅在进程内使用；过期后为同一 Job 重新申请，不改变 manifest 和选择范围。

创建任务使用持久化的 client_operation_id 作为 Idempotency-Key；服务端以 `(participant_id, client_operation_id)` 唯一约束保证重试返回同一个 Job，同键但工具、设备等创建参数不同则拒绝。后续接口按已认证的 participant_id 校验 Job 归属，只允许合法状态迁移；最终上传确认前拒绝签发 STS。complete 可重复提交相同 manifest 摘要，相同 Job 提交不同摘要时拒绝，服务端校验完成后再写入对象字节数和汇总，不能累加客户端的重复上报。

## 7. 验收标准

- 新 token 匹配唯一预登记邮箱；无效或撤销 token 不能创建任务，token 不出现在日志或 OSS（当前实现因 object_prefix 泄漏未通过该项验收）。
- 单步交互：当前问题确认前不出现下一问题；身份、采集范围和最终上传均需用户明确确认。
- 每次运行只接受一个工具；Codex 与 Claude 的混合模式不可选择或通过 API 绕过。
- 全部上传和部分多选路径都正常；未选会话不读取 workspace 内容、不打包、不上传。
- 根会话选择自动包含关联子 agent；共享 workspace 在同一 job 只采集一次。
- cwd 自动分级，密钥命中文件在源机整体排除；最终上传范围与用户选择一致。
- 服务端任务登记和本地逐会话登记都先于正文采集；manifest 完整记录人、机器、工具、源路径、结束/采集时间和秒差。
- 覆盖 Codex 已知格式元数据提取、Codex/Claude 未知及混合格式原文归档、损坏 JSONL 原始字节保留、未知格式敏感信息排除、Claude 深层子 agent、cwd 字段取值及 Windows 路径规范化、resume/fork 保留最长版本。
- `.git/` 不因大文件规则出列，黑名单/体积阈值命中项从快照出列；清单每行只有两个规定字段，远端路径按 RFC-01 优先级获取。
- compacted、缺少基线、无证据的 shell 写操作不误判为可重建；中断/超时/报错只归档并标 invalid。抽样异机比对通过再扩大采集，统计可从 manifest 复核。
- 最终上传确认前不签发 STS、不上传对象；确认后直传 OSS，服务端通过 HeadObject 校验对象大小和 CRC64，不下载大对象正文。
- 网络中断后同一 job 可以恢复；恢复不重复计入已校验对象，也不扩大已确认范围。
- Linux、macOS、Windows 均验证对应二进制可启动并运行完整交互；交叉编译设置 CGO_ENABLED=0，产物不依赖目标机 Go/Python 运行时。

## 8. 暂不实现

MVP 不做统计看板、后台参与者管理页面、题库生成、人工标注、独立会话/artifact 数据表、独立审计事件表、离线后异机补传、自动更新器或 Codex + Claude 混合采集模式。参与者邮箱-token 对由运营侧预先准备并导入 participants 表。
