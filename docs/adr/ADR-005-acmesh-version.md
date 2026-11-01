# ADR-005: acme.sh 版本与许可证

- 状态：Accepted
- 日期：2026-11-01

## 决策

- 镜像构建参数 `ACME_VERSION` 固定 acme.sh tag（当前默认 `3.1.6`），复制到 `/opt/acme.sh`，包含 `dnsapi/`、`deploy/`、`notify/`。
- 运行时禁止 `--upgrade` / `--auto-upgrade`；临时 accountconf 写入 `AUTO_UPGRADE='0'`。
- 升级必须走新镜像，并在 CI 中跑参数 golden tests 与 `--listraw` 解析测试。
- acme.sh 以 GPLv3 分发，镜像内附 `/licenses/acme.sh-LICENSE.md` 与源码获取说明；KPCCSSL 自身保持 MIT，通过独立进程调用。

## 后果

- 发行镜像需要同时提供 acme.sh 对应源码或书面获取方式。
- 任何上游输出格式变化都由契约测试提前暴露。
