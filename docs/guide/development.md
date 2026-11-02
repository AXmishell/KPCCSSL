# 本地开发

## 依赖

- Go 1.26+
- Node.js 22+、npm
- 可选：`/opt/acme.sh/acme.sh`（真实签发时使用）

## 启动开发环境

```bash
make deps
make dev
```

- API：<http://localhost:8080>
- Vite：<http://localhost:5173>（`/api` 代理到 Go）
- 使用 fake ACME 引擎，不需要 acme.sh 或真实 DNS 凭据
- 数据写入 `./.data`，setup token 打印在 API 日志中，也写入 `./.data/setup-token`

### 使用真实 acme.sh

```bash
KPCC_FAKE_ACME=0 KPCC_ACME_BIN=/opt/acme.sh/acme.sh make dev-api
```

### 使用 YAML 配置

```bash
cp kpccssl.example.yaml kpccssl.yaml
make dev-api
```

或显式指定：

```bash
./bin/kpccssl serve --config ./kpccssl.yaml
```

配置字段见[配置参考](configuration.md)。

## 构建

```bash
make build            # 前端构建 + Go 单二进制（含嵌入 SPA）
./bin/kpccssl doctor
./bin/kpccssl serve
```

只重建 Go 二进制、复用现有前端产物：

```bash
CGO_ENABLED=0 go build -o bin/kpccssl ./cmd/kpccssl
```

## 测试与检查

```bash
make test             # Go + Vitest
make lint             # gofmt / go vet / tsc
make e2e              # Playwright（fake ACME，完整签发/下载流程）
make scan             # govulncheck + npm audit
make ci               # lint + test + build
```

单独运行：

```bash
go test ./...
cd web && npm test
cd web && npm run typecheck
cd web && npx playwright test
```

## 目录结构

```text
cmd/kpccssl/          CLI 入口（serve/worker/migrate/doctor/admin）
internal/             后端模块（api、service、store、acme、deploy…）
migrations/           goose 迁移，随二进制嵌入
web/                  React SPA（构建产物输出到 internal/webui/dist）
deploy/               Dockerfile、compose、systemd unit
docs/                 文档库
```

更多设计细节见[架构说明](../architecture.md)。
