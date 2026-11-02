# ADR-007: 数据访问层手写 SQL

- 状态：Accepted
- 日期：2026-11-01

## 背景

早期基线选择 `sqlc + goose`。实际实现中，证书列表需要动态筛选（status/search/cursor）、任务领取依赖 `UPDATE ... RETURNING`、部分更新用乐观锁，sqlc 生成代码会引入较多一次性查询变体。

## 决策

- 迁移仍使用 `goose`，迁移文件嵌入二进制。
- 所有 SQL 集中在 `internal/store`，每个方法对应一条命名查询；handler/service 不拼 SQL。
- 不引入 ORM。
- 保留 `make sqlc` 目标，后续可在查询稳定后逐步迁移到 sqlc；迁移时必须保持事务与 `RETURNING` 语义。

## 后果

- 需要人工保证列与 scan 顺序一致；store 集成测试覆盖关键路径（用户、证书乐观锁、任务领取、凭据引用）。
- 迁移到 sqlc 是纯内部重构，不影响 API 契约。
