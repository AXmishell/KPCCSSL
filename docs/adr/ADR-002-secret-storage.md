# ADR-002: 凭据加密存储

- 状态：Accepted
- 日期：2026-11-01

## 决策

- 主密钥 32 字节，默认保存在 `${KPCC_DATA_DIR}/master.key`，权限 0600；也可通过 `KPCC_MASTER_KEY` 提供（文件优先）。
- 算法 AES-256-GCM。
- 密文格式：`v1:<base64(nonce || ciphertext || tag)>`，版本前缀用于密钥轮换。
- AAD 绑定逻辑位置：`table:record-id:field`，例如 `dns_credentials:01J...:config`。把密文搬到其他记录或字段会导致解密失败。
- 数据库同时保存 `key_version`，解密时按密文前缀选择密钥。
- 备份要求：`master.key` 与数据库分开保存；没有主密钥时数据库中的 DNS 凭据、EAB、SMTP/Webhook 密钥、TOTP secret 均不可恢复。
- 明文只存在于进程内存和必要的外部进程环境变量中；API 不返回任何密文字段或明文。

## 未做

- 尚未实现自动密钥轮换 CLI；`secret.Box.AddKey` 已支持多版本解密，后续可增加 `admin rotate-key`。
- 尚未接入 KMS/HSM。
