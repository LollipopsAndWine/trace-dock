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
- CLI 通过 HTTPS Authorization header 发送 token；不在命令行参数、任务目录、日志、manifest 或 OSS 中保存原文。
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
| 本机采集授权 | 展示选中的会话/workspace 范围；默认“否” | 用户确认后才读取所选 workspace |
| 本机扫描与打包 | 先在本地逐会话登记源路径和时间，再只处理选中项；按 RFC-01 自动分级 cwd、本机扫描密钥、生成归档及 manifest | 显示实际归档体积与排除清单 |
| 最终上传确认 | 显示选中会话、子 agent、workspace 快照、体积、排除数量及 manifest 摘要；默认“否” | “是”后为已登记 Job 申请 OSS 临时凭证 |
| 上传与结果 | CLI 直传 OSS，服务端校验大小和 SHA-256 | 显示 Job ID、成功/失败数量和 receipt 路径 |

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

本简约方案只减少初版功能模块，不更改 RFC-01 的数据识别、分类、排除、去重和有效性规则。以下规则对发现数据和用户选中的数据一致生效；MVP 可以少做看板和后台功能，但不能用简化规则改变采集结果。

**发现与解析**

- Codex 只扫描 `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`。旧版按顶层 `type=response_item` 识别 `apply_patch` / `exec_command`；新版按顶层 `payload.type=item_completed` / `custom_tool_call` 识别 `exec`，并保留可能存在的 ordinal / thread_name。两个 schema 分支独立解析，不猜测格式。
- Codex 未知或混合 schema 的会话标记为 unsupported，记录路径和原因，不允许选择为采集项，也不上传原始轨迹。
- Claude 从 `~/.claude/projects` 递归发现所有 JSONL，必须包括 `<session-id>/subagents/agent-*.jsonl`。路径只从轨迹事件的 cwd 字段取得；projects 编码目录会损失非 ASCII 路径信息，不可用于还原路径。
- Claude 轨迹按 RFC-01 当前 schema 解析；JSONL 损坏标记对应 session 为 invalid，无法识别的结构记录跳过原因，不猜测字段含义。
- Claude 子 agent 依据明确事件关联或 `<session-id>/subagents/agent-*.jsonl` 目录结构关联到父会话。无证据时不按相似路径猜测。
- 每个被采集的根会话记录参与者、机器名、工具、workspace/cwd、轨迹路径、会话结束时间、采集时间和二者秒差；目录顶层 mtime 只作参考，不能证明子目录未变化。
- Codex ID 使用 rollout 文件名中的 UUID；Claude ID 使用主轨迹文件名。resume/fork 按明确根会话归并，依次按事件数、文件大小、结束时间选择最长版本，其余 ID 记为 alias；不跨工具归并。

**可逆性与有效性**

- Codex 轨迹只有 unified diff；没有起始基线时标记 not_reconstructable/archive_only。出现 compacted 事件的会话标记不可逆，只归档。任何会话不得仅凭工具调用记录推断可逆。
- Claude 可逆性判定必须包含主轨迹和全部子 agent。Edit 需有完整 old/new 证据；Write 覆盖旧文件但没有旧内容证据时不能判为可逆。
- sed -i、重定向、cp、git checkout 等 shell 写操作若无法从轨迹完整证明或重放，标记 unknown/archive_only。只有异机 verifier 比对文件树通过，才能最终标为 reconstructable。
- 中断、超时、报错或终止状态不完整的会话标为 invalid；仍可归档，但不作为题目候选。MVP UI 展示状态，不因选中而把 invalid 会话改成 valid。
- JSONL 损坏只将对应 session 标为 invalid，不中止同一批其他会话的采集。

MVP 不生成评测题，也不在默认交互中自动倒推目录。规则初判有希望倒推的会话仅记录候选依据，`reversibility` 仍为 unknown；只有异机 verifier 比对通过的会话才记为 reconstructable。试采验收必须抽样验证候选；失败时先修规则再扩大采集，未验证的会话不得计入已验证可重建数。该验收可以通过独立脚本人工执行，不要求开发看板或新的 CLI 问答。

**workspace、路径与大文件**

- 自动分类保持 RFC-01：单一正常项目目录可快照；包含多个独立项目仓库的上层目录属于 parent、cwd 等于用户 home 时属于 home、训练日志/checkpoint/数据集等产物目录属于 artifact；missing/unknown 默认只记路径、不存内容。artifact 标为非代码会话。不能仅凭 `.git` 数量将单一项目中的子模块误判为 parent，无法安全判断时归为 unknown。
- Claude projects 目录名不可用于 cwd 还原。保存 cwd 原值和规范化值；Windows 路径比较忽略大小写，Linux/macOS 保留大小写语义。
- workspace 归档不跟随目录外符号链接；归档成员路径必须是安全相对路径。`.git/`（包括 pack）不因大文件规则出列；密钥命中仍按整文件排除处理。
- workspace 快照使用确定性 tar.zst：文件按相对路径排序，归一化非必要的 mtime、uid、gid 等元数据；采集不删除、移动或修改源文件。
- 大文件规则与 RFC-02 共用同一版本配置，严格按白名单 → 黑名单 → 配置化体积阈值执行。白名单优先；黑名单和阈值命中的文件从快照出列，但不改动源文件。体积阈值从规则配置读取，不写死在 CLI。
- RFC-01 的大文件 JSONL 每项只含 `relative_path` 和 `remote_path`。每份清单按其 workspace artifact ID 使用独立 OSS 对象键，并由 manifest 关联到对应快照；不要在清单行增加 session ID、大小、哈希或规则原因。remote_path 优先从轨迹中的 wget/curl/aws s3 cp/scp 命令提取；否则用 `<machine_name>:<absolute_path>` 表示带机器名的本机绝对路径；未知记 null 并统计。大文件白名单不能绕过密钥扫描。

**密钥与本机边界**

- 在源机器扫描将上传的轨迹及选中 workspace 文件，至少覆盖 token、密码、私钥和常见凭据；命中密钥时整个文件排除并记录，不原地脱敏或替换，secret 值不得进入日志、错误消息、manifest 或 API 请求。
- 扫描器异常时对该文件 fail-closed。发现结束后，未选会话不再进入正文采集、密钥扫描或打包；拒绝采集时不扫描或打包 workspace。
- 不扫描或复制整个 `~/.claude` / `~/.codex`；不读取或复制 Claude `.credentials.json`、shell-snapshots、memories，或 Codex `logs_2.sqlite` 及其 `-wal` / `-shm` 文件。
- 跨人 workspace 路径必须在清单中明确警示。参与者 token 不代表其拥有他人目录；无额外授权时该 workspace 只记路径，不读取、不快照。
- 缺少允许采集的 workspace 快照、属于非代码会话或唯一轨迹因密钥被排除时，只保留可安全归档的内容，不作为当前题目候选。

MVP 发现阶段需要在本机读取候选轨迹 JSONL 以识别 schema、提取 cwd 和会话元数据；允许查看分类所需的目录元数据，但不读取 workspace 文件内容。后续轨迹正文采集、密钥扫描、workspace 文件读取和打包仅处理用户明确选中的根会话及其关联子 agent。未选会话不上传路径或正文，只在本机保留当前选择所需元数据；服务端仅接收发现总数。

### 4.3 manifest 与登记口径

任务登记与逐会话登记分两步：选择工具后先创建服务端任务；用户授权后，再在本地逐会话登记以下字段，之后才读取所选 workspace 文件并生成归档。这样既遵守 RFC-01“先登记，再采集”，也不会提前上传未选会话的路径。会话结束时间未知时记 null 和原因，不能用采集时间补造结束时间或把秒差写成 0。

| 层级 | 必须记录的内容 |
| --- | --- |
| 任务 | manifest_schema、job_id、participant_id、device_id、machine_name、CLI/adapter 版本、分类/密钥/大文件规则版本、授权和最终上传确认时间、选择范围及摘要 |
| 每个根会话及子 agent | tool、原生 session_id、root/parent ID、aliases、schema、原轨迹路径、cwd 原值/规范化值、存在性及分类、结束时间、采集时间、collection_delay_seconds、有效性、可逆性及依据、归档资格或跳过原因 |
| 采集对象 | artifact_id、kind、关联会话/workspace、对象键、字节数、SHA-256 和校验结果；大文件行仍只含 relative_path、remote_path |
| 汇总 | 按参与者、工具统计选中会话数、workspace 仍在数、可逆候选数、已验证可重建数、只归档数、无效数、排除数，以及远端路径缺失数/大文件总数和缺失比例 |

逐会话登记、原始证据和汇总存入本地 manifest；授权上传后存入 OSS。候选数和已验证数分开统计；大文件总数为 0 时缺失比例记 null。服务端持有邮箱映射，manifest 只用 participant_id，不写邮箱或 token。MVP 不开发看板，但须能通过 receipt 和验收脚本从 manifest 复核这些统计。

会话汇总以去重后的选中根会话计数，子 agent 单列数量；多个根会话共享快照不减少会话数。有效性与归档资格分别统计，invalid 可以同时属于 archive_only，两者不能相加作为总会话数。本地任务目录、文件权限和 OSS 对象键沿用完整方案 4.3、9.3；扫描期间文件变化时重新哈希并记录 changed_during_collect，不把变化前后的内容当作同一份已校验快照。

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
| manifest_sha256 | bytea | 最终 manifest 的 SHA-256 |
| created_at | timestamptz | NOT NULL；任务创建时间 |
| consented_at | timestamptz | 用户授权本机采集的时间 |
| upload_confirmed_at | timestamptz | 用户确认最终上传范围的时间 |
| completed_at | timestamptz | 所有对象校验通过时间 |

collections 不存文件正文、每会话行、排除项明细或上传分片明细：这些内容分别在 OSS manifest/JSONL 和客户端本地 upload-state 中。MVP 不建独立 artifacts、sessions、exclusions、large_files、审计事件或幂等表。

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

- 新 token 匹配唯一预登记邮箱；无效或撤销 token 不能创建任务，token 不出现在日志或 OSS。
- 单步交互：当前问题确认前不出现下一问题；身份、采集范围和最终上传均需用户明确确认。
- 每次运行只接受一个工具；Codex 与 Claude 的混合模式不可选择或通过 API 绕过。
- 全部上传和部分多选路径都正常；未选会话不读取 workspace 内容、不打包、不上传。
- 根会话选择自动包含关联子 agent；共享 workspace 在同一 job 只采集一次。
- cwd 自动分级，密钥命中文件在源机整体排除；最终上传范围与用户选择一致。
- 服务端任务登记和本地逐会话登记都先于正文采集；manifest 完整记录人、机器、工具、源路径、结束/采集时间和秒差。
- 覆盖 Codex 两种 schema、未知版本跳过、Claude 深层子 agent、cwd 字段取值及 Windows 路径规范化、resume/fork 保留最长版本。
- `.git/` 不因大文件规则出列，黑名单/体积阈值命中项从快照出列；清单每行只有两个规定字段，远端路径按 RFC-01 优先级获取。
- compacted、缺少基线、无证据的 shell 写操作不误判为可重建；中断/超时/报错只归档并标 invalid。抽样异机比对通过再扩大采集，统计可从 manifest 复核。
- 最终上传确认前不签发 STS、不上传对象；确认后直传 OSS，服务端校验对象 SHA-256。
- 网络中断后同一 job 可以恢复；恢复不重复计入已校验对象，也不扩大已确认范围。
- Linux、macOS、Windows 均验证对应二进制可启动并运行完整交互；交叉编译设置 CGO_ENABLED=0，产物不依赖目标机 Go/Python 运行时。

## 8. 暂不实现

MVP 不做统计看板、后台参与者管理页面、题库生成、人工标注、独立会话/artifact 数据表、独立审计事件表、离线后异机补传、自动更新器或 Codex + Claude 混合采集模式。参与者邮箱-token 对由运营侧预先准备并导入 participants 表。
