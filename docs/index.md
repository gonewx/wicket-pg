# wicket-pg 项目文档索引

> 本文档是 wicket-pg 的知识库入口：AI agent 与维护者都从这里开始。生成于 2026-08-08 全量文档更新。

## 项目速览

- **类型:** Go 库（独立单包项目，monolith）
- **语言:** Go 1.27rc1
- **模块:** `github.com/gonewx/wicket-pg`
- **架构:** 端口与适配器（六边形）——wicket 定义端口与契约套件，本仓是平级适配器仓
- **数据库:** PostgreSQL 15+（`pgx/v5` 原生驱动，不经 `database/sql`）
- **状态:** 实现完成，v0.1.0 / v0.1.1 已发布（推荐 v0.1.1）

### 快速参考

| 项 | 值 |
|---|---|
| 最新发布 | `v0.1.1`（v0.1.0 因分发层分裂不推荐引用） |
| 关键依赖 | `github.com/gonewx/wicket v0.1.2`、`github.com/jackc/pgx/v5 v5.10.0`、`gopkg.in/yaml.v3 v3.0.1`（仅测试） |
| 入口点 | `store.NewXxxStore(pool, logger)` × 9、`migrations.Up/Down(ctx, pool)` |
| 测试总量 | 123 个测试函数（门禁 + 契约套件 + e2e） |
| 门禁 | 三项血缘门禁（原创作权 / 零血缘标注 / 中性提交信息） |

## 生成的文档

- [项目概览](./project-overview.md) — 目的、状态、技术栈、仓库结构
- [架构文档](./architecture.md) — 端口与适配器设计、store 基座、错误映射、schema 哲学
- [源码树分析](./source-tree-analysis.md) — 带注释的目录结构与职责
- [数据模型](./data-models.md) — 10 张表 / 15 个索引的职责总览与迁移策略
- [开发指南](./development-guide.md) — 环境搭建、构建、测试命令
- [部署与 CI 指南](./deployment-guide.md) — CI 三 job 管线、发布流程
- [贡献指南](./contribution-guide.md) — 血缘门禁、提交约定、代码规范
- [API 契约](./api-contracts.md) — 九个 store 构造器与方法的完整签名、迁移入口、错误哨兵

## 既有文档

- [Schema 设计记录](./schema-design-record.md) — 逐表逐索引指名对应端口方法（维护 schema 必读）
- [初始化与交接记录](./HANDOFF.md) — 骨架期交接说明（历史文档，已由当前文档集取代）
- [README](../README.md) — 用户快速开始（安装 / 迁移 / 注入）
- [PROVENANCE](../PROVENANCE.md) — 独立创作声明（零上游血缘）

## 设计期产物（只读参考）

- [架构脊柱](_bmad-output 侧) — `_bmad-output/planning-artifacts/architecture/architecture-wicket-pg-2026-08-07/ARCHITECTURE-SPINE.md`
- [正式契约 SPEC](_bmad-output 侧) — `_bmad-output/specs/spec-pg-storage-adapter/SPEC.md`（含 port-contract / schema-design / conventions / lineage-gates / preconditions 五个 companion）
- [PRD](_bmad-output 侧) — `_bmad-output/planning-artifacts/prds/prd-wicket-pg-2026-08-07/prd.md`

## 快速开始（简述）

```bash
# 本仓开发：任何 go 命令必须 GOWORK=off（外部 go.work 不含本模块）
GOWORK=off go build ./...
GOWORK=off go test ./tests/lineage/...

# 宿主接入：见 README.md 或 development-guide.md
GOWORK=off go get github.com/gonewx/wicket-pg@v0.1.1
```

对 AI agent：**写任何代码前先读 `_bmad-output/project-context.md`**（24 条本仓特有规则）与 [贡献指南](./contribution-guide.md)（门禁红线）。最常踩的三个坑：词表必须字符串拼接、文件头版权行、凭据禁止落日志。

---

_由 bmad-document-project 工作流生成 · 深扫（deep）级别 · 2026-08-08_
