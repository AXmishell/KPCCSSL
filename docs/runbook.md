# KPCCSSL 运行手册

> 面向单机 Docker / systemd 部署。所有路径以 `KPCC_DATA_DIR=/data` 为例。

## 1. 首次部署

```bash
cd deploy
mkdir -p data && sudo chown -R 10001:10001 data
docker compose up -d --build
docker compose logs -f kpccssl
```

1. 从日志或 `data/setup-token` 读取一次性 setup token。
2. 浏览器打开 `http://<host>:8080`，创建管理员。
3. 在「CA 账户」添加 Let's Encrypt 并点击「注册账户」。测试时可手动填写 staging 目录 URL。
4. 在「DNS 凭据」添加 provider token（建议最小权限）。
5. 新建证书 → 选择 CA/域名/DNS 凭据 → 签发。
6. 需要 HTTP-01 standalone / TLS-ALPN 时，取消 compose 中的 80/443 端口注释并放行防火墙。

### 可选：YAML 配置文件

除 `docker-compose.yml` 的 `environment:` 外，也可以使用 YAML 文件（优先级：环境变量 > 文件 > 默认值）：

```bash
cp kpccssl.example.yaml deploy/data/kpccssl.yaml
chmod 600 deploy/data/kpccssl.yaml
```

在 `deploy/docker-compose.yml` 的 `environment:` 中加入：

```yaml
KPCC_CONFIG: "/data/kpccssl.yaml"
```

`--config <file>` 与 `kpccssl doctor --config <file>` 对非容器部署同样适用。未知字段会报错；文件缺失时启动失败（自动发现的 `kpccssl.yaml` 除外）。

## 2. 反向代理

面板应由 Nginx/Caddy 终止 TLS，转发到 `127.0.0.1:8080`。设置 `KPCC_BASE_URL`、`KPCC_TRUSTED_PROXIES`。

Nginx 关键配置：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;          # SSE
    proxy_read_timeout 3600s;     # 任务日志
}
```

安全头由应用输出（CSP、HSTS、X-Frame-Options 等）；若反代重复添加，注意不要放宽 CSP。

## 3. 备份

必须备份四项，且主密钥与数据库分开保存：

```bash
# 1) 一致性 DB 备份（有 sqlite3 时）
sqlite3 /data/kpccssl.db ".backup '/backup/kpccssl.db'"
# 或停服后直接复制 kpccssl.db（WAL 模式下需连同 -wal/-shm 一起停服复制）

# 2) acme.sh 状态 + 证书版本
tar czf /backup/acme-certs.tgz -C /data acme certs

# 3) 主密钥：单独离线保存
cp /data/master.key /secure-offline/master.key
```

`/data/logs` 与 `/data/tmp` 可不备份。没有 `master.key` 时，DNS 凭据/EAB/SMTP/Webhook/TOTP 全部无法解密。

## 4. 恢复

顺序不可颠倒：

1. 停服。
2. 恢复 `master.key` 到 `/data/master.key`，权限 0600，属主 `10001:10001`。
3. 恢复数据库到 `/data/kpccssl.db`。
4. 恢复 `/data/acme` 与 `/data/certs`。
5. 启动服务（自动执行迁移）。
6. 运行 `kpccssl doctor`。
7. 等待启动对账 `sync` 任务；确认证书状态与 acme.sh `--listraw` 一致。
8. 手动续期一张测试证书验证链路。

## 5. 升级与回滚

1. 备份（见上）。
2. 拉取新镜像；启动时自动执行向前迁移。
3. 检查 `/api/v1/system/health` 与 `doctor`。
4. 手动续期一张测试证书。
5. 失败时回滚镜像 **并恢复升级前的 DB 快照**（数据库迁移不自动回滚）。

acme.sh 升级只能通过新镜像并跑契约测试；禁止在运行中执行 `--upgrade`/`--auto-upgrade`。

## 6. 常见错误

| 错误码 | 排查 |
|---|---|
| `E_DNS_AUTH` | provider token 权限/区域错误；检查 DNS 凭据的「最近错误」，轮换 token 后更新凭据 |
| `E_DNS_PROPAGATION` | TXT 未生效；增大 `dns_sleep`、检查权威 DNS、代理/CDN 是否拦截 |
| `E_HTTP_CHALLENGE` | 80 端口未映射、反代拦截 `/.well-known/acme-challenge`、webroot 权限 |
| `E_PORT_IN_USE` | 8081/8443 被占用；检查容器端口映射与 acme.sh 进程 |
| `E_RATE_LIMIT` | CA 限流；等待窗口结束，人工重试，必要时换 staging |
| `E_CA_REJECTED` | 域名/CSR/账户被拒；查看任务日志中的 ACME problem 详情 |
| `E_NETWORK` | 出站 DNS/HTTPS 失败；检查容器 DNS、防火墙、代理 |
| `E_TIMEOUT` | 任务超过 `KPCC_JOB_TIMEOUT`；检查 DNS/网络，不要盲目并发 |
| `E_CERT_MISSING` | DB 有记录但 acme home 无状态；恢复 `/data/acme` 或重新签发 |
| `E_INTERRUPTED` | 进程重启导致任务中断；确认无重复订单后重新触发 |

## 7. 诊断

```bash
kpccssl doctor                 # 目录权限、DB、acme.sh 版本、账号配置残留
kpccssl admin reset-password --email admin@example.com
curl -s http://127.0.0.1:8080/api/v1/system/health
```

`doctor` 检查：

- 数据/acme/cert/log/tmp 目录存在且可写；
- `master.key` 权限不宽于 0600；
- acme.sh 可执行且能输出版本；
- 主 `account.conf` 不含 `SAVED_*` 凭据；
- 服务器时间（ACME 对时钟偏差敏感，建议 NTP）。

## 8. 安全注意事项

- 单实例单写者，不要对同一 `/data/acme` 启动多个副本。
- 私钥下载需要 operator/admin 且二次确认，会写审计；viewer 不可下载。
- 本地文件部署必须位于 `KPCC_DEPLOY_ROOTS` 内。
- Webhook 仅 HTTPS，拒绝私网/回环地址。
- 备份介质需加密；主密钥离线保存。

## 9. 许可证

KPCCSSL 与 acme.sh 均为 GPLv3。分发镜像时需提供 `/licenses/KPCCSSL-LICENSE`、`/licenses/acme.sh-LICENSE.md` 及对应源码获取方式（见构建参数 `ACME_VERSION` 的 GitHub tag）。
