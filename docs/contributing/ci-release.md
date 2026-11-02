# CI 与发布

## GitHub Actions

### `.github/workflows/ci.yml`

- Go：gofmt、`go vet`、`go test -race` + 覆盖率
- Web：TypeScript、Vitest、生产构建
- E2E：Playwright 完整签发/下载流程（fake ACME）
- Docker：构建镜像、容器启动健康检查、容器内 `doctor`
- 安全：govulncheck、gosec(high/high)、npm audit、Trivy
- Workflow lint：actionlint

### `.github/workflows/release.yml`

推送 `v*` tag 后：

- 构建 linux/amd64 + arm64 单二进制并生成 SHA-256
- 构建并推送多架构镜像到 `ghcr.io/axmishell/kpccssl`
- 自动创建 GitHub Release

支持的镜像标签：

```text
ghcr.io/axmishell/kpccssl:v0.1.0
ghcr.io/axmishell/kpccssl:0.1
ghcr.io/axmishell/kpccssl:latest
```

### `.github/dependabot.yml`

每周更新 Go、npm、GitHub Actions 与 Docker 依赖。

## 发布流程

```bash
git tag -a v0.1.0 -m "KPCCSSL v0.1.0"
git push origin v0.1.0
```

推送 tag 前确认：

- `main` 已推送且工作区干净
- `make ci` 通过
- `make e2e` 通过
- 版本号与 Release 说明一致

仓库需要允许 Actions 写入 Packages（默认 `GITHUB_TOKEN` 即可）。

## 本地复现 CI

```bash
make lint test build
make e2e
make scan
```

单独运行：

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
cd web && npm run build
cd web && npx playwright test
```
