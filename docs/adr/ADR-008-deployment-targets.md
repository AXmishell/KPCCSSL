# ADR-008: 扩展部署目标

- 状态：Accepted
- 日期：2026-11-06

## 背景

ADR-006 把部署限制为 `local_file`、`reload`、`webhook` 三类，以避免任意
shell 执行。实际使用中，用户需要把证书推送到远程主机、宝塔/1Panel 面板
以及云 CDN/WAF，否则只能在目标端自行实现 webhook 接收器。

## 决策

在保留 ADR-006 安全约束的前提下，新增以下结构化目标：

1. `ssh`：SFTP 上传 PEM 文件，随后只允许执行固定 reload 模板。
   - 默认要求 `host_key_sha256`；只有显式设置
     `insecure_ignore_host_key=true` 才跳过主机密钥校验。
   - 目录必须是绝对路径；文件名经过 `path.Clean` 校验，拒绝 `..` 和绝对路径。
   - 远程写入使用同目录临时文件 + `posix-rename`，私钥权限固定 `0600`。
   - 不接受任意前置/后置命令。
2. `baota_panel` / `baota_site`：调用宝塔面板 API。
   - 面板模式更新面板自身 SSL；站点模式先上传到证书库，再绑定一个或多个站点。
   - API key 使用面板签名算法，配置整体由 `secret.Box` 加密。
3. `1panel_panel` / `1panel_site`：调用 1Panel v1/v2 API。
   - 面板模式更新面板 SSL；站点模式更新指定网站的 HTTPS 配置。
4. `tencent_cdn` / `tencent_waf` / `tencent_cos` / `tencent_teo`：通过腾讯云
   SSL 服务上传证书，再按 `cdn` / `waf` / `cos` / `teo` 资源类型部署。
   COS 的实例 ID 使用 `region|bucket|domain` 形式。
5. `aliyun_cdn` / `aliyun_dcdn` / `aliyun_waf`：通过阿里云 RPC API 更新
   CDN/DCDN 证书；WAF 先创建证书，再复制现有 Listen/Redirect 配置并只替换
   证书 ID。
6. `aliyun_oss`：使用 OSS V1 签名调用 `PutBucketCnameWithCertificate`，
   将证书绑定到 Bucket 的自定义域名。
7. `qiniu_cdn`：先上传证书到七牛证书库，再调用 `domain/<domain>/sslize`
   为加速域名启用 HTTPS。

同时新增 `POST /api/v1/deployments/test` 与
`POST /api/v1/deployments/{id}/test`，用于在不改变目标状态的情况下测试
凭据和连通性。

认证信息不再重复保存在每个部署目标里：云厂商 AK/SK、面板 API Key 和 SSH
主机密钥集中在 `deployment_credentials` 表，按 `tencent`、`aliyun`、`qiniu`、
`baota`、`1panel`、`ssh` 分类。部署目标的 config 只保存 `credential_id` 和
目标参数（域名、Bucket、站点、目录等），执行时按 `credential_id` 解密并合并
认证字段。凭据被目标引用时禁止删除。

## 安全约束

- 面板地址与云 API 凭据只允许管理员/操作员创建，配置整体加密。
- 面板地址可能是内网地址，因此不复用 Webhook 的“禁止私网”策略；改为
  由管理员显式配置目标地址，并提供 `ignore_tls` 开关用于自签证书。
- `ignore_tls`、`insecure_ignore_host_key` 属于高风险开关，UI 明确标注，
  审计日志记录部署目标类型与操作者。
- 云厂商凭据应使用最小权限策略；SSH 用户应只允许写入目标目录并执行
  固定 reload 命令。
- 仍不支持任意 shell、任意前置/后置命令或用户自定义 argv。

## 后果

- 部署目标类型从 3 种扩展到 17 种，`deployments.type` 的 CHECK 约束需要
  重建（迁移 `0012_more_targets_and_channels.sql`）。
- 新增 `deployment_credentials` 表和 `deployments.credential_id` 外键
  （迁移 `0013_deployment_credentials.sql`）；旧目标仍可使用原来的内联
  凭据，不会因升级失效。
- 通知渠道新增 `feishu`、`dingtalk`、`wecom`，同样通过迁移扩展
  `notification_channels.type` 的 CHECK 约束。机器人 URL 只允许官方
  HTTPS 域名，避免通知渠道被用作 SSRF 探测。
- SSH 与云厂商适配器需要额外的网络依赖；失败信息会写入
  `deployment_runs.message`，并触发 `deployment.failed` 通知。
- 面板/云 API 的版本差异仍需在适配器内处理，新增厂商应按同样的
  `Validate` / `Test` / `Apply` 结构扩展。
