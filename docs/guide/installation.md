# 安装部署

KPCCSSL 以单个 Go 二进制运行，前端静态资源已嵌入。推荐使用 Docker Compose；也可以直接用二进制 + systemd 部署。

## 环境要求

- Docker 24+ / Docker Compose v2，或
- Linux + 已安装的 `kpccssl` 二进制
- 一个可用的 ACME CA 账户（Let's Encrypt、ZeroSSL、Google Trust Services、SSL.com、Actalis 或自建 ACME）
- 使用 DNS-01 时需要对应 DNS 服务商的 API 凭据
- 使用 HTTP-01 / TLS-ALPN-01 时需要公网可达的 80 / 443 端口

## Docker Compose（推荐）

```bash
cd deploy
mkdir -p data
sudo chown -R 10001:10001 data    # 容器以非 root UID 10001 运行
docker compose up -d --build
docker compose logs -f kpccssl    # 日志中会打印一次性 setup token
```

打开 <http://localhost:8080>，使用日志中的 setup token 初始化管理员。token 也会写入 `data/setup-token`。

### 端口

默认只映射面板端口：

```yaml
ports:
  - "8080:8080"   # 面板/API
```

需要 HTTP-01 standalone 或 TLS-ALPN-01 时，取消 `deploy/docker-compose.yml` 中对应注释，并在防火墙放行：

```yaml
- "80:8081"      # HTTP-01 standalone
- "443:8443"     # TLS-ALPN-01
```

如果面板位于 Nginx/Caddy 之后，不要把 8080 直接暴露到公网，参见[反向代理](../operations/runbook.md#2-反向代理)。

### 中国网络构建

```bash
docker build --build-arg APK_MIRROR=mirrors.aliyun.com \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -f deploy/Dockerfile -t kpccssl:local .
```

acme.sh 版本通过构建参数固定，默认 `3.1.6`：

```bash
docker build --build-arg ACME_VERSION=3.1.6 -f deploy/Dockerfile -t kpccssl:local .
```

### 配置文件

除了 Compose 的 `environment:`，也可以挂载 YAML 配置：

```bash
cp kpccssl.example.yaml deploy/data/kpccssl.yaml
chmod 600 deploy/data/kpccssl.yaml
```

然后在 `deploy/docker-compose.yml` 中加入：

```yaml
environment:
  KPCC_CONFIG: "/data/kpccssl.yaml"
```

完整字段见[配置参考](configuration.md)。

## systemd（二进制部署）

仓库提供 `deploy/systemd/kpccssl.service` 作为模板：

```bash
sudo useradd --system --home /var/lib/kpccssl --shell /usr/sbin/nologin kpccssl
sudo install -m 0755 kpccssl /usr/local/bin/kpccssl
sudo install -d -o kpccssl -g kpccssl -m 0700 /var/lib/kpccssl
sudo cp deploy/systemd/kpccssl.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now kpccssl
```

默认配置：

- 数据目录：`/var/lib/kpccssl`
- 监听：`127.0.0.1:8080`（配合反向代理）
- acme.sh：`/opt/acme.sh/acme.sh`

如需 YAML 配置，在 service 中增加：

```ini
Environment=KPCC_CONFIG=/etc/kpccssl/config.yaml
```

## 首次初始化

1. 从日志或 `<data_dir>/setup-token` 读取一次性 token。
2. 打开面板创建管理员。
3. 在「CA 账户」添加 CA 并注册账户；邮箱默认使用当前登录用户，可修改。
4. 在「DNS 凭据」添加服务商凭据（建议最小权限）。
5. 新建证书，选择 CA、域名、验证方式并签发。

后续的备份、升级、回滚与故障排查见[运行手册](../operations/runbook.md)。
