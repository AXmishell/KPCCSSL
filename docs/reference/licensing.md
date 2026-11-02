# 许可证与 acme.sh

## KPCCSSL

KPCCSSL 使用 **GNU General Public License v3.0**，完整文本见根目录 [`LICENSE`](../../LICENSE)。

## acme.sh

ACME 执行引擎使用 [acme.sh](https://github.com/acmesh-official/acme.sh)（GPLv3）：

- 默认镜像固定 `3.1.6`，可通过构建参数 `ACME_VERSION` 覆盖；
- 禁止在运行中执行 `--upgrade` / `--auto-upgrade`，升级必须通过新镜像并完成契约测试；
- acme.sh 与 `dnsapi/` 位于镜像的 `/opt/acme.sh`，只读使用；
- 运行状态与账户数据位于 `KPCC_ACME_HOME`（默认 `<data_dir>/acme`）。

## 分发要求

镜像内附带：

- `/licenses/KPCCSSL-LICENSE`
- `/licenses/acme.sh-LICENSE.md`
- `/licenses/README.txt`（说明 GPLv3 与源码获取方式）

对外分发镜像或二进制时，需要同时提供 GPLv3 对应的完整源码，以及所用 acme.sh 版本的源码获取方式（例如对应 GitHub tag）。
