# wicket-pg 贡献指南

> 面向读者的任务：知道这个仓的硬性红线与代码约定，避免提交被门禁拦下。**这些规则不是建议，是门禁与审计测试强制执行的红线。**

## 一、血缘门禁（三项，CI 第一道门）

`tests/lineage/lineage_test.go` 全仓强制执行，任何违反都会让 `lineage-gates` job 红：

| 门禁 | 断言 |
|---|---|
| `TestFileHeadersAssertOriginalAuthorship` | 每个 `.go` 文件**首行**必须是 `// Copyright 2026 Decker.`（逐字符检查） |
| `TestNoUpstreamLineageMarkers` | 全仓零上游血缘标注（`Based on` / `Ported from` / `Derived from` / `Adapted from` / 上游厂商名与产品名） |
| `TestCommitMessagesStayNeutral` | 全部 commit message 无上游厂商名、无血缘分类标记、无隔离流程术语 |

### 三个致命陷阱

1. **词表必须字符串拼接写**。修改 `tests/lineage/lineage_test.go` 的禁用词表时，写成字面量（如 `"Derived from"`）会让门禁扫自己的源码时被自己的词表命中——首次运行就是这样红的。必须写 `"Due" + "nde"` 形态。
2. **不要给任何文件加血缘标注**。`// From:`、`// Based on:` 之类立刻转红。本仓是独立创作，文档与提交信息里也不要写「对齐某上游实现」。
3. **文件头两行形态**（沿用 `tests/lineage/lineage_test.go:1-2`）：

```go
// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.
```

## 二、提交约定

- **commit message 保持功能中性**。门禁扫**全量 git 历史**——git 历史一旦公开即不可撤回，写之前想清楚。
- 推荐形态（见本仓历史）：`story N-M: 简述`、`chore: ...`、`release: ...`。
- tag 为轻量 tag，门禁只扫 commit messages 不扫 tag——「tag message 必须中性」红线在门禁层面无执行覆盖，靠自觉。

## 三、代码规范（端口契约六要素）

实现或修改 store 时逐条满足：

1. **上下文** — 首参 `context.Context`；不自造 `context.Background()`/`TODO()`；超时由调用方经 ctx 决定，适配器不自设超时。
2. **缺失语义** — 记录不存在返端口哨兵错误（`storage.ErrNotFound`），基础设施故障返 `%w` 包装错误；**不返 `(nil, nil)`**。
3. **清理入口** — 显式 `RemoveExpired(ctx, cutoff) (int, error)` 返回回收计数；不做批处理服务式分解，**不起后台 goroutine**（迁移与清理都不许）。
4. **写入语义** — 默认 insert-only，句柄碰撞返 `ErrDuplicateHandle`（`key_records` 返 `keymgmt.ErrDuplicateKey`）；`user_consents` / `persisted_grants` 是 ON CONFLICT upsert（契约如此）。
5. **并发语义** — 读-改-写更新携 `expectedVersion`，冲突返 `ErrVersionConflict`（仅 `refresh_tokens` 与 `key_records` 有版本守卫列）。
6. **过期判定不在 SQL** — 禁止 `WHERE expires_at > now()`；清理 SQL 统一 `... < $cutoff`（cutoff 由调用方传）。

运维约束：

- **日志走注入的 `*slog.Logger`**，不自建 logger、不直写 stdout；nil 回退 `slog.Default()`。
- **凭据物料禁止落日志**：令牌值、句柄、密钥、client secret 不得出现在任何级别日志。只允许记录存在性与标识符前缀：

```go
logger.Debug("token lookup", "handle_prefix", handle[:8], "found", ok)  // ✅
logger.Debug("token lookup", "handle", handle)                          // ❌
```

- **driver 用 `pgx/v5` 原生接口**（`pgxpool.Pool`），不经 `database/sql`。

## 四、测试要求

- **门禁测试只依赖标准库**。`tests/lineage/` 下禁止任何第三方包（testify 也不行）——门禁必须能在没有依赖树的情况下跑。
- **契约套件工厂每次返回全新空 store**（隔离 schema + 专属 pool），套件在并行子测试中调用工厂；状态跨用例泄漏会互相污染。
- **三组套件不共用夹具**：grant 存储族（7 口）一套、session 一套、keymgmt 一套。
- **MUST 级全绿；MAY 级默认跳过**（`WithMay(true)` 开启）；未实现的 MAY 级要在文档逐条写明。本仓会话套件不带 `WithMay`（`LazyExpiryReclaimsOnRead` 不可实现，story 1.11 确认）。
- 测试包用 `_test` 后缀外部测试包形态。
- **story 收尾纪律**：置 done 前「全量回归绿 + commit 落库 + git status 与 File List 一致」三件套齐备。

## 五、代码组织

- **目录职责**：`store/` 适配器实现 · `migrations/` 嵌入 SQL · `tests/lineage/` 血缘门禁 · `tests/conformance/` 契约套件调用点 · `tests/e2e/` 真实库语义测试 · `internal/lineage/` 预留。
- **构造器形态**：`NewXxxStore(pool *pgxpool.Pool, logger *slog.Logger) *XxxStore`——只组装基座，不创建/关闭 pool。
- **合规凭证**：每个 store 暴露**单值签名** `ConformsTo() string`，返回所属套件的 `SuiteVersion`（grant 族 → `storagetest.SuiteVersion`；session → `sessiontest.SuiteVersion`；keymgmt → `keymgmttest.SuiteVersion`）。`store/conforms_to_test.go` 编译期断言签名。
- **迁移文件**：`NNNNNN_name.up.sql` / `.down.sql`（golang-migrate 兼容），`go:embed` 嵌入；改名/加表必须同步更新 `docs/schema-design-record.md`（`schema_record_audit_test.go` 三方一致审计）。
- **CI 配置**：改 `ci.yml` 必须同步更新 `tests/e2e/ci_job_e2e_test.go`（静态断言）；禁止把 `${{ github.event.* }}` 插进 `run:` 块。

## 六、贡献流程（BMad 工作流）

1. 改动通过 story 驱动：`create-story` → `dev-story` → code-review → 置 done。
2. 本地验证：见 development-guide.md 第四节（门禁 + 构建 + 契约套件三绿）。
3. 提交信息中性；push 后确认 CI 三 job 绿。
4. 涉及 schema / CI / README 的改动检查对应审计测试（schema_record / ci_job / readme_quickstart）。

## 七、发布纪律（与贡献相关）

- **Go 版本标记两处形态不同，改动须成对**：`go.mod` 的 `go 1.27` 是对下游的语言版本承诺（不带补丁号），`ci.yml` 的 `1.27.0` 是本仓构建环境 pin（取精确补丁版）。rc 已于 Go 1.27.0 发布后去除；`tests/e2e/ci_job_e2e_test.go` 分别精确比对两者，任何预发布值都不得通过。
- 文档引用版本只引 v0.1.3（首个不携带 rc directive 的版本；v0.1.0 因分发层分裂不可用，v0.1.1 / v0.1.2 可用但仍携带 rc directive）。
