# CLI / server MVP JSON 契约 v1

所有 API 请求使用 `Authorization: Bearer <个人 token>` 和 JSON；响应不包含原始 token。CLI 拒绝重定向，生产 API/OSS 使用 HTTPS。只有显式本地开发模式允许回环 HTTP。服务端仅接收控制字段和 manifest 摘要，文件正文由 CLI 直传 OSS。

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
	"object_prefix":"trajectory/token=<token>/dt=2026-11-12/",
  "rules":{
    "classification_version":"1.0.0",
    "secret_version":"1.0.0",
    "large_file_version":"1.0.0",
    "large_file_threshold_bytes":104857600,
    "whitelist":[".git/**","*.analysis.json"],
    "blacklist":["datasets/**","checkpoints/**","*.pt","*.pth","*.safetensors","*.zip"],
    "secret_patterns":[]
  }
}
```

实际黑名单以服务端 `trace_dock_server/rules.json` 为准。客户端只支持已实现的 1.0.0 分类/扫描/大文件规则版本；未知版本停止流程。附加密钥规则使用 Go RE2 语法；内置扫描规则始终生效。

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
{"endpoint":"https://oss-cn-hangzhou.aliyuncs.com","bucket":"bucket-name","prefix":"trajectory/token=<token>/dt=2026-11-12/","region":"cn-hangzhou","access_key_id":"temporary","access_key_secret":"temporary","security_token":"temporary","expiration":"2026-11-12T11:05:00Z","path_style":false}
```

STS 只在内存中使用，权限限于当前 Job 前缀。CLI 使用阿里云官方 OSS Go SDK v2 的 `Client` 和 `Uploader`，由 SDK 负责签名、重试和 multipart/checkpoint；服务端使用官方 OSS Python SDK v2 校验对象。CLI 仍在本地保存 manifest/artifact SHA-256，服务端最终读取对象并重新计算 SHA-256，不信任客户端元数据。

`POST /v1/collections/{job_id}/complete` 请求：

```json
{"manifest_key":"trajectory/token=<token>/dt=2026-11-12/manifest.json","manifest_sha256":"64 lowercase hex","manifest_size_bytes":12345}
```

服务端从 OSS 校验 manifest、逐对象流式 SHA-256/大小、选择范围、规则、会话统计及排除/大文件清单行数。成功返回 receipt：`job_id`、`participant_id`、`state=completed`、`verification=sha256_verified`、`manifest_key`、`manifest_sha256`、`uploaded_bytes`、`verified_object_count`、`completed_at`、`summary`。重复提交相同摘要返回同一回执，不重复计数；不同摘要返回 409。manifest 不包含自身摘要，receipt 覆盖整个冻结 manifest 及其中所有对象的最终校验结果。

错误响应为 `{"code":"stable_error_code"}`；401 认证失败，404 非本人/不存在任务，409 状态或幂等冲突，422 manifest/对象校验失败，429 限速（`Retry-After`），503 临时不可用。错误不回显凭据或文件内容。`GET /healthz` 供负载均衡健康检查。
