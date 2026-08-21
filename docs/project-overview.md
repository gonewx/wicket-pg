# wicket-pg 项目概览

## 一句话定位

wicket-pg 是 [wicket](https://github.com/gonewx/wicket)（OAuth 授权服务器）的 **PostgreSQL 存储适配器**：宿主把本仓的 store 实现注入 wicket 公开的存储端口，即可把授权码、令牌、会话、密钥记录等授权状态持久化到 PostgreSQL，替换默认内存存储。

## 项目性质与边界

- **独立项目**：自备 LICENSE（MIT）、PROVENANCE（独立创作声明）、血缘门禁、migrations 与 CI；不是 wicket 的子目录。
- **独立创作**：实现依赖 wicket 的**公开 API 契约**（端口接口、哨兵错误、契约套件），但不复制、不改编其实现；schema 从端口读写模式独立推导，不照搬上游表结构与实体划分。
- **平级依赖**：通过 `require github.com/gonewx/wicket v0.1.4` 接入，不用 go.work 搭桥。

## 当前状态

| 项 | 状态 |
|---|---|
| 九个 store 实现 | ✅ 完成（七个 grant 存储族 + 会话 + 密钥记录） |
| 迁移入口 | ✅ 完成（`migrations.Up` / `Down`，go:embed SQL，幂等） |
| 契约套件接入 | ✅ 三组套件全部 MUST 级用例绿（storagetest / sessiontest / keymgmttest） |
| 血缘门禁 | ✅ 三项全绿，判别力经探针自证 |
| CI | ✅ 三 job（lineage-gates → build、conformance）合并阻塞 |
| 发布 | ✅ `v0.1.0`、`v0.1.1`、`v0.1.2`、`v0.1.3` tag 已打（**推荐 v0.1.3**，首个不携带 rc directive 的版本；v0.1.0 因分发层分裂不推荐引用） |

## 技术栈

| 类别 | 技术 | 版本 | 说明 |
|---|---|---|---|
| 语言 | Go | `1.27`（CI pin `1.27.0`） | directive 是对下游的语言版本承诺（不带补丁号），CI pin 是本仓构建环境（取精确补丁版）；rc 已于 Go 1.27.0 发布后去除，v0.1.3 起 tag 不携带 rc directive |
| 核心依赖 | wicket | v0.1.4 | 端口接口、哨兵错误、契约套件入口，只读依赖 |
| 数据库驱动 | pgx/v5 | v5.10.0 | 原生 `pgxpool.Pool` 接口，不经 `database/sql` |
| 测试依赖 | yaml.v3 | v3.0.1 | 仅 `tests/e2e/ci_job_e2e_test.go`（CI 配置静态断言） |
| 数据库 | PostgreSQL | 15+ | CI 用 `postgres:15` service 容器 |

## 架构类型

端口与适配器（六边形）：`wicket/storage`、`wicket/session`、`wicket/keymgmt` 定义端口，本仓 `store/` 单包实现全部适配器，宿主组合根注入。内部为「单包适配器 + 真实列最小集 / payload 单列」形态。

## 仓库结构（简述，详见 source-tree-analysis.md）

```
wicket-pg/
  store/          九个 store 适配器 + 未导出共享基座
  migrations/     go:embed SQL + Up/Down 入口
  tests/          门禁 / 契约套件接入 / e2e 三组
  docs/           本知识库
  .github/        CI
```

## 文档地图

- 用户入口：[README](../README.md)（安装 / 迁移 / 注入）
- 架构：[architecture.md](./architecture.md)
- 数据模型：[data-models.md](./data-models.md) + [schema-design-record.md](./schema-design-record.md)
- 开发：[development-guide.md](./development-guide.md)
- 贡献红线：[contribution-guide.md](./contribution-guide.md)
- 完整索引：[index.md](./index.md)
