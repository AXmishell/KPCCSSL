# ADR-004: 任务调度与重试扩展

- 状态：Accepted
- 日期：2026-11-01

## 决策

在早期设计基线的 `jobs` 表上增加 `available_at INTEGER NOT NULL DEFAULT 0` 与索引 `idx_jobs_available(status, available_at)`。

- `ClaimNext` 只领取 `status='queued' AND available_at <= now` 的任务。
- 失败重试原地复用同一行：`attempts` 已由 claim 递增，设置新的 `available_at` 后回到 queued，保留同一 job id 与日志文件。
- 重试退避：issue 1m/10m/1h；renew 1h/6h/24h/24h；deploy/register/sync 1m/5m/15m；revoke 不重试。
- 限流、域名非法、证书缺失等错误不自动重试。
- `Idempotency-Key` 唯一；重复请求在业务层先查已有 job，返回同一 job 与 Location。
- 调度器使用 `available_at` 加入 0–10 分钟 jitter，避免同一时刻集中续期。

## 后果

- 重启后待重试任务保留计划时间。
- 任务日志按 job id 追加，重试会继续写同一个文件，SSE 通过行号续传。
