# KPCCSSL

[![CI](https://github.com/AXmishell/KPCCSSL/actions/workflows/ci.yml/badge.svg)](https://github.com/AXmishell/KPCCSSL/actions/workflows/ci.yml)
[![Release](https://github.com/AXmishell/KPCCSSL/actions/workflows/release.yml/badge.svg)](https://github.com/AXmishell/KPCCSSL/actions/workflows/release.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

自托管的 ACME 证书签发与管理面板：Go 后端 + React SPA + SQLite，ACME 引擎使用固定版本的 [acme.sh](https://github.com/acmesh-official/acme.sh)。

KPCCSSL 面向希望自己掌控证书生命周期的团队：在同一个界面里管理 CA 账户、DNS 凭据、证书签发与续期、部署和通知，而不必把私钥和 DNS API 凭据交给第三方 SaaS。

> 当前状态：MVP 主链路已完成（认证、CA/DNS/证书管理、DNS-01 签发、任务队列、实时日志、续期、吊销、部署、通知、审计、下载）。

## 功能

- **ACME 账户**：Let's Encrypt、ZeroSSL、Google Trust Services、SSL.com（RSA/ECC）、Actalis 及任意 RFC 8555 目录；预设从内置 acme.sh 自动同步，EAB 加密存储。
- **DNS 凭据**：自动同步 acme.sh `dnsapi` 的 190+ 服务商，字段白名单 + AES-256-GCM 加密。
- **证书**：主域名 + SAN、泛域名、EC/RSA 多种密钥类型；支持 DNS-01、HTTP-01 webroot/standalone、TLS-ALPN-01。
- **自动化**：持久化任务队列、幂等键、重试退避、SSE 实时日志、启动对账、每日错峰续期、失败保留旧证书。
- **流水线**：图形化节点连线编排 apply → deploy → verify → notify，支持成功/失败条件边、并行分支、定时触发、持久化运行与节点级日志。
- **部署**：本地文件（白名单 + 原子写 + 备份）、固定 reload 模板、HTTPS Webhook、SSH/SFTP、宝塔/1Panel 面板与站点、腾讯云 CDN/WAF/COS/EdgeOne、阿里云 CDN/DCDN/WAF/OSS、七牛云 CDN。
- **部署凭据**：云厂商 AK/SK、面板 API Key、SSH 密钥集中加密管理，部署目标通过 `credential_id` 引用，避免重复填写。
- **通知**：SMTP、Webhook、Telegram、飞书、钉钉、企业微信，支持事件订阅、去重与重试。
- **安全**：Argon2id、HttpOnly 会话、CSRF 防护、RBAC + 用户管理、可选 TOTP、私钥下载 step-up 二次认证、邮件找回密码与邮箱验证、全量脱敏日志与审计。
- **运维**：readiness、Prometheus 文本指标、主密钥版本轮换、acme.sh 校验和固定、崩溃后证书状态自动修复。
- **界面**：React 19 + Tailwind，支持中文/英文与浅色/深色主题。

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.26、chi、SQLite（modernc）、goose |
| 前端 | React 19、Vite、Tailwind CSS、TanStack Query、react-hook-form + Zod、i18next |
| ACME | acme.sh 3.1.6（镜像内固定，禁用自动升级） |
| 部署 | 单二进制（嵌入 SPA）、Docker、systemd |

## 快速开始

```bash
cd deploy
mkdir -p data && sudo chown -R 10001:10001 data
docker compose up -d --build
docker compose logs -f kpccssl    # 获取一次性 setup token
```

打开 <http://localhost:8080>，用日志中的 setup token 初始化管理员。

## 文档

| 文档 | 内容 |
|---|---|
| [文档库](docs/README.md) | 全部文档索引 |
| [安装部署](docs/guide/installation.md) | Docker Compose、systemd、端口与镜像构建 |
| [配置参考](docs/guide/configuration.md) | YAML 配置、环境变量、数据目录 |
| [运行手册](docs/operations/runbook.md) | 反向代理、备份恢复、升级回滚、故障排查 |
| [本地开发](docs/guide/development.md) | fake ACME、调试、测试与构建 |
| [架构说明](docs/architecture.md) | 模块划分、数据模型与关键流程 |
| [API 契约](docs/openapi.yaml) | OpenAPI 定义 |

## 许可证

GNU General Public License v3.0，见 [LICENSE](LICENSE)。acme.sh 同样使用 GPLv3，分发要求见[许可证说明](docs/reference/licensing.md)。
