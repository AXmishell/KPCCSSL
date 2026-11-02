# 命令行与 Make

## `kpccssl`

```text
kpccssl serve           启动 API + SPA（默认命令）
kpccssl worker          仅运行任务与续期调度
kpccssl migrate         应用数据库迁移
kpccssl doctor          检查运行环境
kpccssl admin reset-password --email <email>
kpccssl version
```

`serve` / `worker` / `migrate` / `doctor` / `admin` 均支持：

```text
--config <file>          YAML 配置文件（默认 $KPCC_CONFIG 或 ./kpccssl.yaml）
```

示例：

```bash
kpccssl serve --config /etc/kpccssl/config.yaml
kpccssl doctor --config /etc/kpccssl/config.yaml
kpccssl admin reset-password --email admin@example.com
```

说明：

- 不带子命令时等价于 `serve`。
- `migrate --down` 被有意禁用；回滚请恢复数据库备份。
- 启动时自动执行向前迁移。

## `make`

| 目标 | 说明 |
|---|---|
| `make deps` | 安装 Go 与 npm 依赖 |
| `make web` | 构建前端到 `internal/webui/dist` |
| `make build` | 构建 `bin/kpccssl`（嵌入前端） |
| `make dev` | 同时启动 API（fake ACME）与 Vite |
| `make dev-api` | 仅启动 API |
| `make dev-web` | 仅启动 Vite |
| `make test` | Go + 前端测试 |
| `make lint` | gofmt / go vet / TypeScript |
| `make ci` | lint + test + build |
| `make e2e` | Playwright 端到端测试 |
| `make docker` | 构建容器镜像 |
| `make docker-run` | 运行容器（挂载 `kpccssl-data`） |
| `make migrate-up` | 构建并执行迁移 |
| `make sqlc` | 重新生成 sqlc 代码（可选） |
| `make scan` | govulncheck + npm audit |
| `make clean` | 删除 `bin`、`.data`、`node_modules` |

常用变量：

```bash
VERSION=v0.1.0 make build
```

构建会注入 `main.version` 与 `main.commit`。
