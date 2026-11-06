# ADR-006: 结构化部署目标

- 状态：Accepted
- 日期：2026-11-01

## 背景

acme.sh 的 `--reloadcmd` / hook 等价于任意 shell 执行。把它暴露给 Web 用户会把面板漏洞升级为宿主机 RCE。

## 决策

> 后续扩展（SSH、宝塔/1Panel、云 CDN/WAF）见 [ADR-008](ADR-008-deployment-targets.md)，仍遵守本文的“无自由 shell”约束。

部署只允许三种结构化目标：

1. `local_file`：在 `KPCC_DEPLOY_ROOTS` 允许根目录内写文件。路径经过绝对路径、`..`、EvalSymlinks 检查；同目录临时文件 + fsync + rename 原子替换；可选保留 `.bak`。
2. `reload`：只允许固定 argv 模板：
   - `nginx -s reload`
   - `systemctl reload nginx`
   - `systemctl reload apache2`
   - `apachectl graceful`
3. `webhook`：仅 HTTPS；解析目标地址并拒绝回环/私网/链路本地/组播地址；在 DialContext 时再次校验，防御 DNS rebinding；HMAC-SHA256 签名；不跟随重定向。

不存在自由 shell 参数、环境变量或自定义命令。

## 后果

- 自定义部署需要用户自行写外部 webhook 接收器，或等待 Agent/插件机制。
- `local_file` 目标目录必须预先存在且在允许根目录内，否则创建部署配置时即被拒绝。
