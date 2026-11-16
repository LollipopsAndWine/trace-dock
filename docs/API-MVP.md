# CLI / server MVP JSON 契约 v1

所有 API 请求使用 `Authorization: Bearer <个人 token>` 和 JSON；身份验证响应不返回 token。CLI 拒绝重定向，生产 API/OSS 使用 HTTPS。只有显式本地开发模式允许回环 HTTP。服务端仅接收控制字段和 manifest 摘要，文件正文由 CLI 直传 OSS。当前创建任务返回的 object_prefix 包含 token 原文，该值会进入 OSS object key 和 manifest；这与 token 不应进入 OSS 的安全目标冲突，属于待解决的设计问题。

仅支持当前按 Job 隔离的对象格式。旧格式任务直接清理，不提供恢复或迁移；当前格式任务仍支持中断恢复。

`POST /v1/identity/verify` 请求 `{}`，响应：

```json
{"participant_id":"pseudonymous-id","masked_email":"p***@example.com"}
```

`POST /v1/collections` 必须携带 `Idempotency-Key`，值与 `client_operation_id` 相同。CLI 在发请求前持久化 operation ID。

```json
{"client_operation_id":"random-id","device_id":"random-device-id","machine_name":"hostname","tool":"claude","cli_version":"0.1.0"}
```

响应包含 `job_id` 和 `rules`，不包含 STS。`tool` 仅接受 `codex` 或 `claude`。相同参与者和 operation ID 返回同一任务及同一份冻结规则；创建参数不同返回 409。

```json
{
  "job_id":"random-job-id",
	"object_prefix":"trajectory/token=<token>/dt=2026-11-12/job=<job-id>/",
  "rules":{
    "classification_version":"1.0.0",
    "secret_version":"2.2.0",
    "large_file_version":"1.2.0",
    "large_file_threshold_bytes":1073741824,
    "whitelist":[".git/**","*.analysis.json"],
    "blacklist":["datasets/**","checkpoints/**","*.pt","*.pth","*.safetensors","*.ckpt","*.onnx","*.gguf","pytorch_model*.bin","*.parquet","*.arrow"],
    "secret_patterns":[]
  }
}
```

实际黑名单以服务端 `trace_dock_server/rules.json` 为准。客户端创建任务时支持分类规则 `1.0.0`、大文件规则 `1.2.0`、密钥规则 `2.2.0`；未知版本停止流程。大文件默认阈值为 1 GiB（1,073,741,824 bytes），仅在文件严格超过阈值时按体积排除，白名单和黑名单优先于体积判断。普通压缩包及泛化 .bin 不按后缀排除；Python site-packages/dist-packages 直接子文件的 .pth 不作为模型权重，仍接受体积检测。排除清单分别使用 large_file_type、large_file_size 记录主要原因；大文件 JSONL 行仍只含 relative_path、remote_path。密钥检测使用 CLI 内嵌的 guiji 第二层 Gitleaks 高置信度规则和明文凭据兜底，`secret_patterns` 必须为空，不加载服务端附加正则。更新服务端规则后须重启服务，已有任务的冻结规则不会变更；使用旧版密钥或大文件规则的任务需重新创建。

`PATCH /v1/collections/{job_id}` 的三个主要请求：

```json
{"discovered_count":12}
```

```json
{"state":"collecting","discovered_count":12,"selection_mode":"partial","selected_count":2,"selection_sha256":"64 lowercase hex","consented_at":"2026-11-12T10:00:00Z"}
```

```json
{"state":"uploading","upload_confirmed_at":"2026-11-12T10:05:00Z"}
```

打包后先 PATCH `{"state":"awaiting_upload_confirmation"}`。选择摘要是根 session ID 排序后、紧凑 JSON 数组 UTF-8 的 SHA-256。选择与授权时间一旦登记即冻结；后续更新不能扩展数量或替换摘要。客户端不能上报已校验字节数或把任务标为 completed。中断可 PATCH `resumable`，拒绝本机采集可 PATCH `cancelled`。

`POST /v1/collections/{job_id}/credentials` 请求 `{}`。必须已确认上传，响应：

```json
{"endpoint":"https://oss-cn-hangzhou.aliyuncs.com","bucket":"bucket-name","prefix":"trajectory/token=<token>/dt=2026-11-12/job=<job-id>/","region":"cn-hangzhou","access_key_id":"temporary","access_key_secret":"temporary","security_token":"temporary","expiration":"2026-11-12T11:05:00Z","path_style":false}
```

STS 只在内存中使用，权限限于当前 Job 前缀。CLI 使用阿里云官方 OSS Go SDK v2 的 `Client` 和 `Uploader`，由 SDK 负责签名、重试、multipart/checkpoint 和 CRC64；服务端使用官方 OSS Python SDK v2 读取对象元数据。CLI 在 manifest 中保存对象大小和 CRC64；服务端只下载小型 manifest 正文，随后通过 `HeadObject` 比较对象大小和 `x-oss-hash-crc64ecma`，不下载轨迹、workspace 或清单对象正文。

`POST /v1/collections/{job_id}/complete` 请求：

```json
{"manifest_key":"trajectory/token=<token>/dt=2026-11-12/job=<job-id>/manifest.json","manifest_crc64":"0-18446744073709551615","manifest_size_bytes":12345}
```

服务端从 OSS 校验 manifest CRC64、对象大小和 CRC64，并校验选择范围、规则、会话统计及 manifest 中的对象索引。成功返回 receipt：`job_id`、`participant_id`、`state=completed`、`verification=crc64_verified`、`manifest_key`、`manifest_crc64`、`uploaded_bytes`、`verified_object_count`、`completed_at`、`summary`。重复提交相同摘要返回同一回执，不重复计数；不同摘要返回 409。manifest 不包含自身摘要，receipt 覆盖整个冻结 manifest 及其中所有对象的最终校验结果。

错误响应为 `{"code":"stable_error_code"}`；401 认证失败，404 非本人/不存在任务，409 状态或幂等冲突，422 manifest/对象校验失败，429 限速（`Retry-After`），503 临时不可用。错误不回显凭据或文件内容。`GET /healthz` 供负载均衡健康检查。
