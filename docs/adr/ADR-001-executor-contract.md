# ADR-001: acme.sh executor contract

- 状态：Accepted
- 日期：2026-11-01

## 背景

KPCCSSL 需要调用 acme.sh 完成 ACME 协议交互，但必须避免把 Web 输入变成 shell 注入，并保证可测试、可替换。

## 决策

1. `internal/acme/acmesh` 是唯一允许 `exec.CommandContext` 的包；其余代码不得直接创建外部进程。
2. 所有参数由 `args.go` 的纯函数生成并带 golden tests。Web 层不拼接 argv。
3. 只暴露白名单参数：`--issue`、`--renew`、`--revoke`、`--install-cert`、`--list`、`--register-account`，以及固定的 `--home`、`--server`、`--keylength`、`--dns`、`--webroot`、`--standalone`、`--alpn`、`--httpport`、`--tlsport`、`--preferred-chain`、`--challenge-alias`、`--dnssleep`、`--accountconf`。
4. 禁止 `--reloadcmd`、`--pre-hook`、`--post-hook`、`--renew-hook`、`--deploy-hook`、`--output-insecure`、`--upgrade`、`--auto-upgrade`。
5. DNS 凭据通过进程环境变量注入，不进入 argv；每个任务创建 0600 的临时 `--accountconf`，任务结束删除。
6. 进程使用独立进程组，超时后向整个进程组发送 SIGKILL；退出码 0 成功，2 在 renew 场景视为 skipped。
7. 不解析自由文本作为最终状态；成功安装后使用 x509 解析证书，`--list --listraw` 仅用于对账。
8. acme.sh 版本随镜像固定，禁止运行时升级。

## 后果

- 每新增一个 acme.sh 参数都必须修改 `args.go` 并补测试，这是有意的摩擦。
- EAB HMAC 目前仍通过 `--eab-kid` / `--eab-hmac-key` 传给 acme.sh（上游仅支持 CLI）；注册成功后从 acme.sh CA 配置中清除。数据库中的 EAB 保持加密。
- 任务日志原样落盘，SSE 推送前统一脱敏；完整日志仅 admin 可下载。
