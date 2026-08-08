# wicket-pg 开发指南

> 面向读者的任务：把本仓跑起来、跑测试、做日常开发。**最重要的一条：所有 go 命令都必须 `GOWORK=off`**，原因见下文「常见坑」。

## 一、前置条件

| 依赖 | 要求 | 说明 |
|---|---|---|
| Go | 1.27+（`go.mod` 为 `go 1.27rc1`，稳定工具链需 GOTOOLCHAIN 自动下载或手动装 1.27） | CI pin `1.27.0-rc.1` |
| PostgreSQL | 15+（跑契约套件与 e2e 才需要） | 单测与门禁不需要数据库 |
| 无 | — | 开发零外部工具：迁移不依赖 CLI，门禁只用标准库 |

## 二、环境搭建

```bash
cd /mnt/disk0/project/auth/wicket-pg
GOWORK=off go mod download
```

**为什么必须 `GOWORK=off`**：外部 `/mnt/disk0/project/auth/wicket/go.work` 是当前生效的 `GOWORK` 且不含本模块；直接 `go test` 会报 `directory prefix tests/lineage does not contain modules listed in go.work`——错误信息指向不明，容易误判成路径写错。

## 三、构建

```bash
GOWORK=off go build ./...
```

## 四、测试

### 4.1 血缘门禁（不需要数据库，CI 第一道门）

```bash
GOWORK=off go test ./tests/lineage/...
```

### 4.2 全量单测（不需要数据库）

```bash
GOWORK=off go test ./...
```

`tests/conformance/` 与 `tests/e2e/` 在 `WICKET_PG_TEST_DATABASE_URL` 未设置时会 `t.Skip`，所以无库环境跑 `./...` 也安全。

### 4.3 契约套件与 e2e（需要真实 PostgreSQL 15）

```bash
# 本地起一个 postgres:15（示例）
docker run -d --name wicket-pg-test \
  -e POSTGRES_DB=wicket_pg_test -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -p 5432:5432 postgres:15

export WICKET_PG_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/wicket_pg_test'

GOWORK=off go test ./tests/conformance/...
GOWORK=off go test ./tests/e2e/...
```

注意：

- 契约套件工厂每次调用创建**全新隔离 schema**（专属 pool），套件在并行子测试中调用工厂，状态绝不跨用例泄漏。
- MUST 级用例必须全绿；MAY 级默认跳过（`WithMay(true)` 开启）——本仓会话套件**不带** `WithMay`（其 `LazyExpiryReclaimsOnRead` MAY 项不可实现，story 1.11 确认）。
- 三组套件（grant 族 / session / keymgmt）**不共用夹具**，各有独立 fixture。

### 4.4 本地等价验证（无 Docker 时的降级方案）

如果无法跑容器，CI 绿的前提条件是：门禁绿（4.1）+ 构建绿（三）+ 契约套件绿（4.3）。无法本地复验契约套件时，push 后到 Actions 页确认三个 job 全绿即可（见 deployment-guide.md）。

## 五、日常开发任务

| 任务 | 做法 |
|---|---|
| 新增一个 store 方法 | 改对应 `store/<name>.go`，单测就近加同包 `*_test.go`；涉及真实 SQL 语义的另加 `tests/e2e/<name>_e2e_test.go` |
| 改 schema | 新增 `migrations/NNNNNN_xxx.up.sql` + `.down.sql`；**同步更新 `docs/schema-design-record.md`**（三方一致审计 `schema_record_audit_test.go` 会查） |
| 改 CI | `tests/e2e/ci_job_e2e_test.go` 静态断言 CI 配置形态，改 `ci.yml` 需同步更新该测试 |
| 改 README 示例 | `tests/e2e/readme_quickstart_e2e_test.go` 校验快速开始代码可编译 |
| 改门禁词表 | ⚠️ 见「致命陷阱」——词表必须字符串拼接写，否则门禁扫自己源码会红 |

## 六、提交前检查

1. `GOWORK=off go build ./...` 通过
2. `GOWORK=off go test ./...` 通过（单测与门禁）
3. 契约套件/e2e 本地有库则跑；无库则 push 后确认 CI
4. 每个 `.go` 文件首行是 `// Copyright 2026 Decker.`（门禁会查）
5. commit message 保持功能中性（门禁会扫全量历史）——详见 contribution-guide.md

## 七、常见坑

| 症状 | 原因与解法 |
|---|---|
| `directory prefix tests/lineage does not contain modules listed in go.work` | 没加 `GOWORK=off` |
| 门禁红：词表命中自身源码 | 词表用了字面量；必须字符串拼接（`"Due" + "nde"`） |
| 契约套件/ e2e 全部 skip | `WICKET_PG_TEST_DATABASE_URL` 未设置 |
| `gh` 命令失败 | 环境有全局代理；用 `NO_PROXY="*" HTTP_PROXY="" HTTPS_PROXY="" gh <cmd>` |

## 八、已留档的技术债（deferred-work）

- 门禁探针子进程无显式超时；Windows 下 `go test -c` 产物无 `.exe` 后缀（CI 为 Linux，潜伏问题）。
- 迁移并发 `Up` 无 advisory lock。
- `go 1.27rc1` directive 对 dependabot/gopls 的摩擦（Go 1.27.0 发布后自愈；届时 `go.mod` 与 `ci.yml` 两处同步去 rc）。
