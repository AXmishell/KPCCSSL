# KPCCSSL

自托管的 Web 证书签发与管理工具：Go 后端 + React SPA + SQLite，ACME 引擎使用固定版本的 [acme.sh](https://github.com/acmesh-official/acme.sh)。

> 当前状态：**Phase 0–4 的 MVP 主链路已实现**（认证、CA/DNS/证书管理、DNS-01 签发、任务队列、实时日志、续期、吊销、部署、通知、审计、下载）。真实 acme.sh + Pebble 的 HTTP-01 已验证；真实 DNS provider 的 staging 签发（P0-10）仍需在目标环境完成，见 [PROJECT.md](PROJECT.md)。

## 能力

- ACME 账户：Let's Encrypt / ZeroSSL / Google / SSL.com / 自定义目录，staging 标记，EAB 加密存储。
- DNS 凭据：Cloudflare、阿里云、DNSPod、Route 53、GoDaddy；字段白名单 + AES-256-GCM 加密。
- 证书：主域名 + SAN、泛域名、ec-256/ec-384/rsa-2048/3072/4096；DNS-01 / HTTP-01 webroot / standalone / TLS-ALPN-01。
- 异步任务：持久化队列、幂等键、重试退避、超时、进程组终止、SSE 实时日志（Last-Event-ID）。
- 生命周期：启动对账、每日错峰续期、失败保留旧证书、吊销、事件时间线。
- 部署：本地文件（白名单 + 原子写 + 备份）、固定 reload 模板、HTTPS Webhook（SSRF 防护 + HMAC）。
- 通知：SMTP、Webhook、Telegram；事件订阅、去重、重试。
- 安全：Argon2id、HttpOnly 会话、CSRF 双提交 + Origin、RBAC、可选 TOTP、全量脱敏日志、审计。
- 前端：React 19 + Vite + Tailwind + TanStack Query + react-hook-form/Zod + i18next（zh-CN/en）+ 暗色模式。

## 快速开始（Docker）

```bash
cd deploy
mkdir -p data && sudo chown -R 10001:10001 data   # 容器以非 root UID 10001 运行
docker compose up -d --build
docker compose logs -f kpccssl     # 查看一次性 setup token
```

打开 <http://localhost:8080>，用日志中的 setup token 初始化管理员。

中国网络构建镜像时可覆盖 Alpine/Go 镜像源：

```bash
docker build --build-arg APK_MIRROR=mirrors.aliyun.com \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -f deploy/Dockerfile -t kpccssl:local .
```

acme.sh 版本通过 `--build-arg ACME_VERSION=3.1.6` 固定。

HTTP-01 standalone / TLS-ALPN 需要额外映射端口并在防火墙放行：

```bash
# docker-compose.yml 中取消注释
- "80:8081"
- "443:8443"
```

## 本地开发

依赖：Go 1.26+、Node.js 22+、npm。使用 fake ACME 引擎可以在没有 acme.sh 和真实 DNS 凭据的情况下跑通 UI。

```bash
make deps
make dev          # API :8080 + Vite :5173，fake ACME，数据写入 ./.data
```

打开 <http://localhost:5173>。setup token 打印在 API 日志中，也写入 `./.data/setup-token`。

切换真实 acme.sh：

```bash
KPCC_FAKE_ACME=0 KPCC_ACME_BIN=/opt/acme.sh/acme.sh make dev-api
```

构建单二进制（前端静态嵌入）：

```bash
make build
./bin/kpccssl doctor
./bin/kpccssl serve
```

## 配置

| 变量 | 默认值 | 说明 |
|---|---|---|
| `KPCC_LISTEN` | `:8080` | 面板/API 监听地址 |
| `KPCC_DATA_DIR` | `/data` | 数据根目录 |
| `KPCC_DB_PATH` | `${DATA}/kpccssl.db` | SQLite 路径 |
| `KPCC_MASTER_KEY_FILE` | `${DATA}/master.key` | 主密钥文件（0600） |
| `KPCC_MASTER_KEY` | 空 | 直接提供 base64/hex 主密钥；文件优先 |
| `KPCC_ACME_BIN` | `/opt/acme.sh/acme.sh` | acme.sh 路径 |
| `KPCC_ACME_HOME` | `${DATA}/acme` | acme.sh 状态目录 |
| `KPCC_CERT_DIR` | `${DATA}/certs` | 证书版本目录 |
| `KPCC_LOG_DIR` | `${DATA}/logs` | 任务日志目录 |
| `KPCC_JOB_CONCURRENCY` | `1` | worker 数；acme.sh 仍由 flock 串行 |
| `KPCC_JOB_TIMEOUT` | `30m` | 单任务超时 |
| `KPCC_BASE_URL` | 空 | 反向代理后的外部 URL |
| `KPCC_TRUSTED_PROXIES` | 空 | 信任的代理 CIDR，逗号分隔 |
| `KPCC_SETUP_TOKEN` | 自动生成 | 首次初始化 token；不设则打印/写入数据目录 |
| `KPCC_LOG_LEVEL` | `info` | debug/info/warn/error |
| `KPCC_RETENTION_DAYS` | `30` | 任务日志保留天数 |
| `KPCC_HTTP_PORT` | `8081` | HTTP-01 standalone 端口 |
| `KPCC_TLS_PORT` | `8443` | TLS-ALPN 端口 |
| `KPCC_DEPLOY_ROOTS` | 空 | 本地文件部署允许根目录，`:` 分隔 |
| `KPCC_WEBROOT_ROOTS` | `/var/www:${DATA}/webroot` | HTTP-01 webroot 允许根目录，`:` 分隔 |
| `KPCC_DEV` | `false` | 关闭 Secure cookie / 允许 http CA URL，仅本地 |
| `KPCC_FAKE_ACME` | `false` | 使用内置 fake 引擎，仅开发/E2E |

## 数据布局

```text
/data
├── kpccssl.db
├── master.key                 # 0600，必须与 DB 分开备份
├── acme/                      # acme.sh --home
├── certs/<cert-id>/
│   ├── revisions/<revision-id>/
│   └── current -> revisions/<revision-id>
├── logs/jobs/<job-id>.log
├── deploy/                    # 默认允许的本地部署根目录
└── tmp/jobs/                  # 每任务临时 accountconf，启动清理
```

## 开发命令

```bash
make deps        # go mod + npm ci
make dev         # API + Vite
make test        # Go + Vitest
make lint        # go vet + tsc
make e2e         # Playwright（fake ACME，完整签发/下载流程）
make build       # 前端 + Go 单二进制
make docker
make migrate-up
make scan
```

API 契约见 [docs/openapi.yaml](docs/openapi.yaml)，运行/备份/升级见 [docs/runbook.md](docs/runbook.md)，实施计划见 [PROJECT.md](PROJECT.md)。

## 安全要点

- 不提供任意 shell hook；外部命令只能是 acme.sh 白名单参数或固定 reload argv。
- DNS 凭据、EAB、SMTP/Webhook 密钥、TOTP secret 使用 AES-256-GCM 加密，AAD 绑定记录与字段。
- 每次任务使用临时 `--accountconf`，任务后删除；并清理 acme.sh 配置中的 `SAVED_*` 与 `CA_EAB_*` 残留。
- 私钥下载需要 operator/admin + `X-Confirm-Private-Key: yes`，并写审计；viewer 不可下载私钥。
- Webhook 仅 HTTPS，拒绝私网/回环地址，并在连接时再次校验（防 DNS rebinding）。
- 单实例单写者；不要对同一 `/data/acme` 启动多个副本。

发现安全问题请勿公开提交 issue，先联系维护者。

## acme.sh 与许可证

- 默认镜像固定 acme.sh `3.1.6`（构建参数 `ACME_VERSION`），禁用自动升级。
- KPCCSSL 与 acme.sh 均使用 GPLv3；镜像内附 `/licenses/KPCCSSL-LICENSE`、`/licenses/acme.sh-LICENSE.md` 与源码获取方式。分发时需遵守 GPLv3（包含对应源码）。

## License

GNU General Public License v3.0，见 [LICENSE](LICENSE)。
