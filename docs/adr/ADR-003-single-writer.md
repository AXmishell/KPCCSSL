# ADR-003: 单写者与串行执行

- 状态：Accepted
- 日期：2026-11-01

## 背景

acme.sh 的账户状态、CA 目录和域名配置共享同一个 `--home`。上游没有跨进程的文件锁，多个 acme.sh 并发写同一 home 会互相覆盖。

## 决策

1. 单实例、单写者部署；SQLite 使用 WAL，连接池上限 1。
2. 所有 acme.sh 调用在 `<acme-home>/.lock` 上持有 `flock(LOCK_EX)`；获取锁时轮询并在上下文取消时退出。
3. 默认 `KPCC_JOB_CONCURRENCY=1`；即使调大，flock 仍会把 acme.sh 执行串行化。调大只对部署/通知等非 acme 任务有意义。
4. 每个证书的 issue/renew/revoke 通过“同证书同类型仅一个 queued/running job”合并。
5. 启动时把所有 `running` job 标记为 `interrupted`，不自动重放；由用户或调度器重新触发。

## 后果

- 不支持多副本。需要横向扩展时应引入 Controller/Agent 或按证书分片，而不是共享 `/data/acme`。
- 外部进程异常退出后可能留下 flock 文件，但内核会在进程退出时释放锁。
