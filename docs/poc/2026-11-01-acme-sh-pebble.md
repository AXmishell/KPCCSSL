# PoC: acme.sh + Pebble (HTTP-01, 2026-11-01)

目标：验证真实 acme.sh 3.1.6 与 `--accountconf`、`--listraw`、`--install-cert` 契约（P0-09）。

环境：

- acme.sh **3.1.6**（官方 tag）
- Pebble 2.5.2（`ghcr.io/letsencrypt/pebble:2.5.2`，host network）
- 域名：`poc.127.0.0.1.nip.io`（公网通配 DNS，解析到 127.0.0.1）
- HTTP-01 webroot 由 `python3 -m http.server 5002` 提供（Pebble 配置的验证端口为 5002）
- Pebble 自签证书，因此 PoC 使用 `--insecure`；生产执行器禁止该参数

## 1. 临时 accountconf

`/tmp/acme-poc-job/account.conf`（0600）：

```text
ACCOUNT_EMAIL='poc@example.com'
AUTO_UPGRADE='0'
```

注册：

```bash
/opt/acme.sh/acme.sh --register-account \
  --home /tmp/acme-poc --server https://localhost:14000/dir \
  --insecure --accountconf /tmp/acme-poc-job/account.conf --no-color
# => Registered
```

## 2. 签发

```bash
/opt/acme.sh/acme.sh --issue -d poc.127.0.0.1.nip.io \
  --webroot /tmp/acme-webroot --home /tmp/acme-poc \
  --server https://localhost:14000/dir --insecure \
  --accountconf /tmp/acme-poc-job/account.conf --keylength ec-256 --no-color
# exit 0, Cert success.
```

## 3. `--listraw`（真实输出）

```text
Main_Domain|KeyLength|SAN_Domains|Profile|CA|Created|Renew
poc.127.0.0.1.nip.io|"ec-256"|no||https://localhost:14000/dir|2026-11-01T06:10:03Z|2026-11-30T06:10:03Z
```

契约要点（已回写解析器）：

- 表头字段是 `SAN_Domains`，不是 `SAN`。
- `KeyLength` 带引号，需要去除。
- 无 SAN 时该列是 `no`，不能当作域名。
- CA 列是完整 directory URL。

对应测试：`internal/acme/acmesh/accountconf_test.go` 的 `TestParseListRaw`。

## 4. `--install-cert`

```bash
/opt/acme.sh/acme.sh --install-cert --home /tmp/acme-poc \
  -d poc.127.0.0.1.nip.io --ecc \
  --key-file /tmp/acme-stage/privkey.pem \
  --cert-file /tmp/acme-stage/cert.pem \
  --ca-file /tmp/acme-stage/chain.pem \
  --fullchain-file /tmp/acme-stage/fullchain.pem --no-color
```

结果：

- `cert.pem` / `chain.pem` / `fullchain.pem` 0644，`privkey.pem` 0600。
- `openssl x509 -pubkey` 与 `openssl pkey -pubout` 的 SHA-256 一致，证书与私钥匹配。
- SAN 为 `DNS:poc.127.0.0.1.nip.io`。

## 4.1 续期

```bash
/opt/acme.sh/acme.sh --renew --home /tmp/acme-poc \
  -d poc.127.0.0.1.nip.io --ecc --force \
  --accountconf /tmp/acme-poc-job/account.conf --insecure --no-color
# exit 0，证书重新签发并覆盖
```

说明：生产执行器不会传 `--insecure`；Pebble 使用自签证书，仅 PoC 需要。`--accountconf` 在 renew 路径同样被接受。

## 5. 凭据残留

- 临时 `account.conf` 无 `SAVED_*`。
- 域名配置只包含 `Le_Webroot`、`Le_API`、`Le_Keylength`，无 `SAVED_*`。
- 应用在执行器结束后还会调用 `ScrubSavedCredentials` 与 `ScrubEAB` 做防御性清理。

## 6. 未覆盖

- 真实 DNS provider 的 DNS-01 签发（P0-10）仍未执行，需要 Cloudflare/阿里云/DNSPod 等测试凭据。
- 生产模式下的 `--server` + 公网 CA、真实 ACME 限流、ARI 行为。
