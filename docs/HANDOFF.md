# wicket-pg 初始化与交接

> **状态（2026-08-08 更新）：本文件是骨架期（2026-07-31）的历史交接说明，内容已全部落地完成。** 实现、契约套件、CI、发布（v0.1.0/v0.1.1）均已收官；当前文档集以 `docs/index.md` 为入口（含架构 / API 契约 / 数据模型 / 开发 / 部署 / 贡献指南）。下文保留历史背景，仅作追溯参考，不要再按「待开工」理解。
>
> **补记（2026-08-21）：** 下文「二、你要执行的初始化步骤」中提到的「1.27 正式版发布后两处同步去 rc」已完成——`go.mod` 现为 `go 1.27`（语言版本承诺），`ci.yml` 现 pin `1.27.0`（构建环境），v0.1.3 是首个不携带 rc directive 的发布版本。已发布的 v0.1.0–v0.1.2 因 tag 不可变而永久携带 rc directive。

本文件是 wicket-pg 仓的交接说明：当前骨架交付了什么、你要执行哪些初始化步骤、哪些工作被前置条件挡住、解除后从哪里继续。

状态：**骨架就绪，适配器实现待开工**。前置条件已于 2026-08-07 实测解除（wicket v0.1.1 已发布，端口形态固定），可以施工。

正式契约见 `_bmad-output/specs/spec-pg-storage-adapter/`（SPEC.md 加五个 companion）。本文件保留背景与操作细节。

---

## 一、当前骨架交付物

```
wicket-pg/
  LICENSE                       MIT
  PROVENANCE.md                 独立创作声明（零上游血缘、依赖 wicket 公开端口但不复制实现）
  README.md                     占位，待实现落地后补快速开始
  .gitignore                    与 wicket 同口径（含 go.work、工具目录）
  go.mod                        module github.com/gonewx/wicket-pg（暂无 wicket require）
  .github/workflows/ci.yml      门禁 CI：血缘门禁先跑并 gate 住 build/test
  tests/lineage/lineage_test.go 三项血缘门禁，仅依赖标准库
  store/                        （空）适配器实现落点
  migrations/                   （空）schema 落点
  internal/lineage/             （空）预留
  docs/HANDOFF.md               本文件
```

### 三项血缘门禁（AD-14 要求移植）

| 门禁 | 断言 |
|---|---|
| `TestFileHeadersAssertOriginalAuthorship` | 每个 `.go` 文件首行含 `// Copyright 2026 Decker.` |
| `TestNoUpstreamLineageMarkers` | 全仓零上游标注（`Based on` / `Ported from` / `Derived from` / `Adapted from` / 上游厂商名与产品名） |
| `TestCommitMessagesStayNeutral` | commit message 无上游厂商名、无血缘分类标记、无隔离流程术语 |

**判别力已自证**：两枚临时探针（缺版权头的文件、带上游标注的文件）各自使门禁转红，移除后复绿。门禁不是恒绿装饰。

**两处实现细节，改动时别踩**：

1. 禁用词表用字符串拼接写（`"Due" + "nde"`）。否则门禁扫自己的源码时会被自己的词表命中——首次运行就是这样红的。
2. 本地跑必须 `GOWORK=off`。`/mnt/disk0/project/auth/wicket/go.work` 是当前生效的 `GOWORK`，它不含 wicket-pg，直接 `go test` 会报 `directory prefix tests/lineage does not contain modules listed in go.work`。

```bash
cd /mnt/disk0/project/auth/wicket-pg
GOWORK=off go test ./tests/lineage/...
```

---

## 二、你要执行的初始化步骤

骨架尚未 `git init`，也未建远端。以下命令请你自己跑（涉及远端与凭据）。

### 1. 本地仓初始化

```bash
cd /mnt/disk0/project/auth/wicket-pg
git init
git add .
git commit -m "chore: initialize repository skeleton with lineage gates"
```

提交信息保持功能中性——`TestCommitMessagesStayNeutral` 会扫它，且 git 历史一旦公开即不可撤回。

### 2. 建远端（参照 godi 的形态）

```bash
NO_PROXY="*" HTTP_PROXY="" HTTPS_PROXY="" gh repo create gonewx/wicket-pg \
  --public \
  --description "PostgreSQL storage adapter for wicket" \
  --source . \
  --remote origin \
  --push
```

`gh` 必须排除代理，否则连接失败。

### 3. 确认门禁在 CI 上跑通

推送后到 Actions 页确认两个 job（`lineage-gates` → `build`）均绿。

注意 `ci.yml` 的 Go 版本当前 pin 在 `1.27.0-rc.1`，与 `go.mod` 的 `go 1.27rc1` 对应。**1.27 正式版发布后，两处要同步改掉**——这与 wicket 侧 Story 9.8 的「首个 tag 不得携带 rc directive」是同一件事。

---

## 三、前置条件：已解除

原先挡住实现的端口形态问题已全部落地。2026-08-07 实测 wicket 状态：

| 前置 | 实测 |
|---|---|
| wicket semver tag | `v0.1.0`、`v0.1.1` 已发布 |
| 端口首参 `context.Context` | 已到位（`storage/interfaces.go`、`storage/consent_message_store.go`） |
| 哨兵错误 | `ErrNotFound` / `ErrDuplicateHandle` / `ErrVersionConflict` 均已定义于 `storage` 包 |
| 句柄参数化 / `expectedVersion` | 已实施，如 `UpdateRefreshToken(ctx, handle, token, expectedVersion int)` |
| `storage/storagetest` 契约套件 | 存在，七个入口，`SuiteVersion = "1.0.0"` |
| session / keymgmt 独立套件 | `session/sessiontest`、`keymgmt/keymgmttest` 均存在，各 `SuiteVersion = "1.0.0"` |

签名不会再破坏性变更，第 1 步接依赖可立即执行。

**留档：曾经的阻塞理由。** 端口形态未定时写实现，等 ctx 首参、哨兵错误、句柄参数与版本参数落地就要把每个方法签名重写一遍——这正是「适配器仓一出生即干净的 require 形态、无迁移负担」要避免的事。骨架与门禁当时不受此阻塞（只依赖 AD-14，不依赖端口形态），所以先行交付没有白做。

---

## 四、续做路线

第 1 步接依赖可立即执行。按序做：

### 第 1 步：接依赖

```bash
cd /mnt/disk0/project/auth/wicket-pg
GOWORK=off go get github.com/gonewx/wicket@v0.1.1   # 或取用时最新的 v0.1.x
GOWORK=off go get github.com/jackc/pgx/v5
```

driver 用 `pgx/v5` 原生接口，不经 `database/sql` 抽象层。最低支持 PostgreSQL 15。

### 第 2 步：设计 schema（先设计，后写代码）

**schema 从端口的读写访问模式推导，不从 wicket 内部 model 的字段清单映射。** 依据是各端口方法实际需要的查询路径：

- 按句柄单点取用 → 句柄列为主键
- 按 subject 批量吊销 → `subject_id` 相关复合索引
- 按过期时间清理 → `expires_at` 索引

留一份设计记录，说明**每张表、每个索引对应哪条端口方法**。列名用 Go 侧命名的 snake_case 转写遵从 SQL 惯例；但不要照搬上游 EntityFramework 的表名、实体划分与列集合。

序列化载荷沿用版本容器形态（`Version` / `DataProtected` / `Payload`）**以单列存储**，不要把 model 字段结构摊进 schema——这样 wicket 侧 model 演进时适配器 schema 不必跟着动。

migrations 以 `go:embed` 嵌入 SQL 文件，由适配器暴露迁移入口供宿主调用；不引入外部迁移库、不依赖宿主安装 CLI。文件名沿 `000001_init.up.sql` / `.down.sql` 惯例（与 golang-migrate 兼容，便于未来平滑换工具）。

### 第 3 步：实现适配器

实现 wicket 的九个 store 端口，逐条满足契约六要素：

1. **上下文** — 首参 `context.Context`，不自造 `context.Background()`；超时由调用方经 ctx 决定，适配器不自设超时覆盖
2. **缺失语义** — 记录不存在返哨兵错误，基础设施故障返包装后的底层错误；**不返 `(nil, nil)`**
3. **清理入口** — 提供显式过期清理；命名与分解取「调用方驱动的一次性回收、返回回收计数」，**不做批处理服务式分解**
4. **写入语义** — 默认 insert-only，句柄碰撞返 `ErrDuplicateHandle`
5. **并发语义** — 读-改-写更新携 `expectedVersion`，冲突返 `ErrVersionConflict`
6. **过期判定** — **禁止**在 SQL 里加 `expires_at > now()` 之类过滤。理由：适配器用数据库时钟、核心用注入的 Clock，集群时钟漂移下两者结论不同；且过期记录对上层不可见会让「信任 store 已过滤」与「上层自行判定」两种假设静默分裂。过期判定一律在核心侧，清理入口只回收空间。

另外两条运维约束：日志走注入的 `*slog.Logger`，不自建 logger 也不直写 stdout；凭据物料（令牌值、句柄、密钥、client secret）**禁止**落任何级别的日志，只记存在性与标识符前缀。

需实现的九个 store：

| 端口 | 套件入口 |
|---|---|
| `storage.AuthorizationCodeStore` | `storagetest.RunAuthorizationCodeStoreSuite` |
| `storage.RefreshTokenStore` | `storagetest.RunRefreshTokenStoreSuite` |
| `storage.ReferenceTokenStore` | `storagetest.RunReferenceTokenStoreSuite` |
| `storage.UserConsentStore` | `storagetest.RunUserConsentStoreSuite` |
| `storage.PersistedGrantStore` | `storagetest.RunPersistedGrantStoreSuite` |
| `storage.DeviceFlowStore` | `storagetest.RunDeviceFlowStoreSuite` |
| `storage.BackchannelAuthenticationRequestStore` | `storagetest.RunBackchannelAuthenticationRequestStoreSuite` |
| `session.Store` | `sessiontest.RunSessionStoreSuite` |
| `keymgmt.RecordStore` | `keymgmttest.RunRecordStoreSuite` |

### 第 4 步：接契约套件并暴露合规凭证

```go
// tests/conformance/ 下分别调用九个套件入口
storagetest.RunAuthorizationCodeStoreSuite(t, newAuthzCodeStore, opts...)
storagetest.RunRefreshTokenStoreSuite(t, newRefreshTokenStore, opts...)
// ... 其余七个入口
```

三组（grant 存储族、session、keymgmt）不共用夹具——session 与 keymgmt 在 wicket 侧有其自身的契约论证。

容器/夹具由本仓自备（套件只依赖标准库 `testing`）。全部 MUST 级用例必须绿；MAY 级若未实现，在文档里写明。

每个 store 实现暴露 `ConformsTo() string`，返回其对应套件的 `SuiteVersion`（三套均为 `"1.0.0"`）：

```go
// 示例
func (s *AuthorizationCodeStore) ConformsTo() string {
	return storagetest.SuiteVersion
}
```

宿主在装配期分别调用每个 store 的 `ConformsTo()`，比对不匹配时自行决定失败策略。

### 第 5 步：CI 补齐并发版

- `ci.yml` 增加契约套件执行步骤，用 `services: postgres: image: postgres:15`，作为合并阻塞条件
- 打 v0.1.0 tag（与 wicket v0.1.x 同期）
- 回 wicket 仓给 `docs/storage-persistence-guide.md` 补一节「PostgreSQL 适配器」，含安装指令与注入示例

---

## 五、边界

- wicket 仓**不实施**本仓的任何代码、schema 或门禁；wicket 侧义务止于「移交物完备且可被独立验证」
- 本仓是独立项目，自备 LICENSE / PROVENANCE / 门禁 / migrations
- 本仓为独立创作，无上游血缘：**不要**给任何文件加 `// From:` 之类的血缘标注（门禁会红），也不要在文档或提交信息里写「对齐某上游实现」

---

**交接日期：** 2026-07-31  
**骨架验证：** 三项门禁全绿；判别力经两枚探针自证（各自转红、移除后复绿）
