# KPCCSSL 文档库

本目录收录 KPCCSSL 的使用、运维、开发与架构文档。根目录的 [`README.md`](../README.md) 只负责项目介绍，详细内容从这里开始。

## 快速上手

| 文档 | 内容 |
|---|---|
| [安装部署](guide/installation.md) | Docker Compose、systemd、端口与镜像构建 |
| [配置参考](guide/configuration.md) | YAML 配置文件、环境变量、数据目录布局 |
| [命令行与 Make](guide/cli.md) | `kpccssl` 子命令、`make` 目标 |

## 运维

| 文档 | 内容 |
|---|---|
| [运行手册](operations/runbook.md) | 首次部署、反向代理、备份恢复、升级回滚、常见错误、诊断 |
| [安全说明](operations/security.md) | 密钥加密、私钥下载、Webhook 防护、单写者约束、漏洞报告 |

## 开发

| 文档 | 内容 |
|---|---|
| [本地开发](guide/development.md) | 依赖、fake ACME、调试、测试与构建 |
| [CI 与发布](contributing/ci-release.md) | GitHub Actions、镜像标签、发布流程、本地复现 CI |
| [架构说明](architecture.md) | 设计目标、模块划分、数据模型、关键流程与工程约定 |
| [架构决策记录](adr/) | 执行器契约、密钥存储、单写者、任务调度等 ADR |
| [POC 记录](poc/) | acme.sh + Pebble 验证记录 |

## API

- [OpenAPI 契约](openapi.yaml)（前端类型由 `npm run openapi` 生成）

## 许可

- [许可证与 acme.sh](reference/licensing.md)
