# 安全说明

## 设计约束

- **单实例单写者**：不要对同一份 `/data/acme` 和 SQLite 启动多个副本；并发写 acme.sh 状态会破坏证书与账户。
- **不提供任意 shell hook**：外部命令只能是 acme.sh 的白名单参数，或部署模块内置的固定 reload argv。
- **最小权限部署**：本地文件部署目标必须位于 `KPCC_DEPLOY_ROOTS` 白名单内；HTTP-01 webroot 同样受 `KPCC_WEBROOT_ROOTS` 限制。
- **Webhook 仅 HTTPS**：拒绝私网/回环地址，并在实际连接时再次校验，防止 DNS rebinding。
- **审计**：登录、CA/DNS/证书操作、部署、私钥下载都会写入审计日志。

## 凭据与密钥

- DNS 凭据、EAB HMAC、SMTP/Webhook/Telegram 密钥、TOTP secret 使用 AES-256-GCM 加密，AAD 绑定记录与字段。
- 主密钥位于 `<data_dir>/master.key`（0600），或通过 `KPCC_MASTER_KEY` 注入。
- 没有 `master.key` 时，上述加密数据全部无法解密；备份时必须单独离线保存，并与数据库分开存放。
- 每次任务使用临时 `--accountconf`，任务结束后删除；并清理 acme.sh 配置中的 `SAVED_*`、`CA_EAB_*` 残留。
- 日志默认脱敏，不输出密钥、token、密码等敏感字段。

## 认证与下载

- 密码使用 Argon2id 哈希；会话 Cookie 为 HttpOnly + Secure（开发模式除外），生产使用 `__Host-` 前缀。
- CSRF 使用双提交 Cookie + Origin 校验。
- 角色分为 viewer / operator / admin；viewer 不可下载私钥。
- 私钥下载需要 operator/admin，并带 `X-Confirm-Private-Key: yes`，同时写入审计。
- 可选 TOTP 双因素与恢复码。

## 供应链

- 镜像固定 acme.sh 版本（构建参数 `ACME_VERSION`，默认 `3.1.6`），禁止运行时 `--upgrade` / `--auto-upgrade`。
- CI 运行 govulncheck、gosec(high/high)、npm audit 与 Trivy。
- 发布产物提供 SHA-256；镜像发布到 `ghcr.io/axmishell/kpccssl`。

## 部署检查清单

- [ ] 面板位于 HTTPS 反向代理之后，8080 不直接暴露公网
- [ ] `KPCC_BASE_URL`、`KPCC_TRUSTED_PROXIES` 已正确设置
- [ ] `master.key` 权限为 0600，且已离线备份
- [ ] 数据目录与备份介质权限受限
- [ ] `kpccssl doctor` 全部通过
- [ ] 服务器启用 NTP，时钟偏差在 5 分钟内
- [ ] DNS 凭据使用最小权限 token，并定期轮换

## 漏洞报告

请勿公开提交安全 issue。请通过仓库维护者提供的私密渠道联系，并附上复现步骤、影响范围与版本信息。
