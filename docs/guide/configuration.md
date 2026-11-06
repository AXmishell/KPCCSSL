# 配置参考

配置来源优先级：

```
环境变量 > YAML 配置文件 > 内置默认值
```

环境变量可以覆盖 YAML 中的任意同名配置项。

## YAML 配置文件（可选）

完整示例见根目录的 [`kpccssl.example.yaml`](../../kpccssl.example.yaml)。

加载顺序：

1. `--config <file>` 命令行参数（文件不存在时报错）；
2. `$KPCC_CONFIG` 指定的文件（文件不存在时报错）；
3. 工作目录下的 `kpccssl.yaml` / `kpccssl.yml`（存在才加载）；
4. 以上都没有时，仅使用环境变量与默认值。

```bash
cp kpccssl.example.yaml kpccssl.yaml
chmod 600 kpccssl.yaml          # 可能包含 setup_token / master_key
./bin/kpccssl serve             # 自动读取 ./kpccssl.yaml
./bin/kpccssl serve --config /etc/kpccssl/config.yaml
./bin/kpccssl doctor --config /etc/kpccssl/config.yaml
```

最小示例：

```yaml
listen: ":8080"
data_dir: "/data"
setup_token: "optional-fixed-token"
log_level: "info"
job_concurrency: 1
job_timeout: "30m"
trusted_proxies:
  - "172.16.0.0/12"
deploy_roots:
  - "/etc/nginx/certs"
```

说明：

- `data_dir` 是其他路径的默认基准（`db_path`、`master_key_file`、`acme_home`、`cert_dir`、`log_dir`、`webroot_roots` 等）。
- 未知字段会直接报错，避免拼写错误被静默忽略。
- 所有字段均可省略；省略即使用默认值。
- YAML 中的 `master_key`、`setup_token` 属于敏感信息，建议权限 `0600` 且不要提交到版本库（`.gitignore` 已忽略根目录的 `kpccssl.yaml`）。
- Docker 部署时挂载文件并设置 `KPCC_CONFIG=/data/kpccssl.yaml`。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `KPCC_CONFIG` | 空 | YAML 配置文件路径 |
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

## 邮件服务（密码重置 / 邮箱验证）

在 Web 界面的“设置 → 邮件服务”配置 SMTP，保存后可用“发送测试邮件”验证：

| 设置键 | 说明 |
|---|---|
| `smtp_host` | SMTP 服务器地址 |
| `smtp_port` | 端口，默认 587 |
| `smtp_tls` | `starttls`（默认）、`tls` 或 `none` |
| `smtp_username` | 登录用户名，可留空 |
| `smtp_password` | 登录密码，AES-256-GCM 加密存储，界面显示为 `********` |
| `smtp_from` | 发件地址，例如 `no-reply@example.com` |

- 未配置发信时，找回密码和“发送重置邮件”会返回 `E_MAIL_NOT_CONFIGURED`。
- 重置链接默认 30 分钟有效，邮箱验证链接 24 小时有效，均为一次性令牌；使用后清空该用户全部会话。
- 生产环境请设置 `KPCC_BASE_URL`。未设置时会回退到请求的 Host 生成邮件链接。

## 数据布局

```text
/data
├── kpccssl.db
├── master.key                 # 0600，必须与 DB 分开备份
├── setup-token                # 首次初始化后删除
├── acme/                      # acme.sh --home
├── certs/<cert-id>/
│   ├── revisions/<revision-id>/
│   └── current -> revisions/<revision-id>
├── logs/jobs/<job-id>.log
├── deploy/                    # 默认允许的本地部署根目录
└── tmp/jobs/                  # 每任务临时 accountconf，启动清理
```

备份与恢复要求见[运行手册](../operations/runbook.md#3-备份)。

## 多实例

KPCCSSL 是**单实例单写者**：不要对同一份 `/data/acme` 与 SQLite 启动多个副本。需要高可用时请使用主备切换，而不是同时运行多个进程。
