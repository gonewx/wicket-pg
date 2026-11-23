# wicket-pg 架构文档

> 面向读者的任务：理解本仓如何用 PostgreSQL 实现 wicket 的存储端口，改动时知道往哪个文件改、遵循什么不变量。设计期完整论述见 `_bmad-output/planning-artifacts/architecture/architecture-wicket-pg-2026-08-07/ARCHITECTURE-SPINE.md`。

## 一、架构模式：端口与适配器（六边形）

```mermaid
flowchart LR
    subgraph wicket["wicket（端口定义方，只读依赖 v0.10.0）"]
        P1["storage 端口族<br/>AuthorizationCodeStore 等九口<br/>+ ClientOriginLookup"]
        P2["session.Store"]
        P3["keymgmt.RecordStore"]
        S["契约套件 4.0.0<br/>storagetest / sessiontest / keymgmttest"]
        E["哨兵错误<br/>ErrNotFound 等"]
    end

    subgraph adapter["wicket-pg（本仓）"]
        subgraph store["store/ 单包适配器"]
            B["baseStore 共享基座<br/>pool + logger + codec"]
            A1["AuthorizationCodeStore"]
            A2["RefreshTokenStore"]
            A3["其余九 store"]
            C["payloadCodec<br/>版本容器编码"]
            ERR["mapDuplicateErr / mapReadErr<br/>SQLSTATE→哨兵映射"]
        end
        M["migrations/<br/>go:embed SQL + Up/Down"]
    end

    subgraph host["宿主（wicket 组合根）"]
        H["注入 store 并调用 ConformsTo() 校验"]
    end

    P1 --> A1 & A2 & A3
    P2 --> A2
    P3 --> A3
    B --> A1 & A2 & A3
    C --> B
    ERR --> B
    S -->|"契约套件工厂"| store
    M --> H
    A1 & A2 & A3 --> H
```

**依赖方向**：宿主 → 本仓适配器 → wicket 端口接口。本仓是平级适配器仓：`require` wicket 已发布版本，不复制其内部实现；wicket 不实施本仓任何代码。

## 二、技术栈与关键决策

| 决策 | 选择 | 理由（要点） |
|---|---|---|
| 驱动 | `pgx/v5` 原生接口（`pgxpool.Pool`） | 不经 `database/sql` 抽象层（spec 决策） |
| 序列化 | 版本容器单列 `payload jsonb`（`{"version":1,"dataProtected":false,"payload":"<base64>"}`） | model 演进时 schema 不必跟着动；`store/codec.go` |
| 真实列 | 只落查询路径与守卫所需最小集 | 句柄主键、subject/client/type 过滤列、按条件移除的 `client_id` / `session_id` 定位列（000004）、生命周期列、版本守卫列、keymgmt 唯一性列；其余进 payload。例外：PAR 表的可见列只写不读（000005）；DCR 表的可见列只写不读、revision 以列为准（000006）；在线刷新令牌按 session 移除以 `session_id` 定位、事务内锁行后在 Go 侧解码 payload 判定 `offline_access`，不落 scope 列（见 schema 设计记录例外 11） |
| 迁移 | go:embed SQL + 自研幂等记账 `schema_migrations` | 不引入外部迁移库、不依赖宿主装 CLI；文件名沿 golang-migrate 惯例便于未来换工具 |
| 过期判定 | **禁止** SQL 内 `expires_at > now()` | 适配器用数据库时钟、核心用注入 Clock，集群时钟漂移下结论分裂；清理入口只回收空间 |
| 清理形态 | 显式入口 `RemoveExpired(ctx, cutoff time.Time) (int, error)` | 调用方驱动的一次性回收、返回计数；不起后台 goroutine |

## 三、store 包结构

`store/` 是单包适配器：十一个公开 store 类型 + 未导出共享基座。

### 共享基座（`store/base.go`）

```go
type baseStore struct {
    pool   *pgxpool.Pool  // 宿主所有，构造器不创建不关闭
    logger *slog.Logger   // nil 回退 slog.Default()
    codec  *payloadCodec  // 无状态 JSON 版本容器编解码
}
```

**构造器形态**（十一个 store 完全一致）：

```go
func NewXxxStore(pool *pgxpool.Pool, logger *slog.Logger) *XxxStore
```

构造器只组装基座；连接池生命周期属于宿主，所有 store 均未实现 `io.Closer`。

### 错误映射（`store/errors.go`）

统一两条规则，基座下所有 store 共用：

| 底层错误 | 映射结果 |
|---|---|
| SQLSTATE 23505（唯一约束冲突） | 端口族重复哨兵（`storage.ErrDuplicateHandle` / `keymgmt.ErrDuplicateKey`） |
| `pgx.ErrNoRows` | 端口族缺失哨兵（`storage.ErrNotFound`） |
| 其它基础设施错误 | `fmt.Errorf("%w", err)` 包装，`errors.Is` 可判定 |

**禁止返回 `(nil, nil)`**：单记录读不存在返哨兵错误，基础设施故障返包装错误。列表方法返回空非 nil slice（`[]*Xxx{}`），不返哨兵。

### 版本容器（`store/codec.go`）

```json
{"version":1,"dataProtected":false,"payload":"<base64(JSON model)>"}
```

`[]byte` model 字段会被 base64 双重编码（encoding/json 一次 + 本层一次），往返无损。适配器只写 version 1。

## 四、数据架构

12 张表（`schema_migrations` + 11 张业务表）、25 个显式索引。**每张表、每个索引都能指名对应的端口方法**——这是硬性维护约定（AC-2），完整对应表见 [schema-design-record.md](./schema-design-record.md)，总览见 [data-models.md](./data-models.md)。

```mermaid
erDiagram
    authorization_codes ||--o{ "payload jsonb" : ""
    refresh_tokens ||--o{ "payload jsonb" : ""
    reference_tokens ||--o{ "payload jsonb" : ""
    user_consents ||--o{ "payload jsonb" : ""
    persisted_grants ||--o{ "payload jsonb" : ""
    device_codes ||--o{ "payload jsonb" : ""
    backchannel_auth_requests ||--o{ "payload jsonb" : ""
    pushed_authorization_requests ||--o{ "payload jsonb" : ""
    client_registrations ||--o{ "payload jsonb" : ""
    sessions ||--o{ "payload jsonb" : ""
    key_records ||--o{ "payload jsonb" : ""
```

要点：
- **单点取用**：`handle` / `session_id` / `key` / `client_id`（`client_registrations`）单列主键。
- **批量吊销**：`persisted_grants` 落 `subject_id`/`session_id`/`client_id`/`type` 四过滤真实列；`sessions.subject_id`（迁移 000003）、`device_codes.user_code`（迁移 000002）为后补维度列。
- **按条件移除定位列（迁移 000004）**：`authorization_codes` / `refresh_tokens` / `reference_tokens` 落 `client_id` + `session_id`，`device_codes` 落 `session_id`，`user_consents.client_id` 单建索引；写入时取自模型（引用令牌取 `sid` claim，与 `Token.SessionId()` 同口径），`UpdateRefreshToken` / `UpdateByUserCode` 同语句刷新；7 个新增定位列空值存 NULL（`user_consents.client_id` 是 000001 既有的 `NOT NULL` 列，空 ClientId 存空串，不适用此规则）；存量行由 000004 同事务回填。
- **原子取出（AD-12）**：`TakeAuthorizationCode` 单语句 `DELETE ... WHERE handle = $1 RETURNING payload`，`TakePushedAuthorizationRequest` 单语句 `DELETE ... WHERE key = $1 RETURNING payload`；0 行返 `ErrNotFound`，不开显式事务、不先 SELECT 再 DELETE。
- **推送授权请求（迁移 000005）**：`pushed_authorization_requests` 以 `key`（只经 `storage.PushedAuthorizationRequestKey` 生成）为主键；`client_id` 与三个时间列写入时复制、只写不读，模型只从 payload 解码（`timestamptz` 只精确到微秒）；回收列 `retention_expiration` 为 `NOT NULL`，`RemoveExpired` 写作 `DELETE ... WHERE retention_expiration < $1`。
- **客户端注册（迁移 000006）**：`client_registrations` 以 `client_id` 为主键，墓碑与活记录同行（删除置 `removed = true`，`payload` 与注册访问令牌摘要置 NULL，`allowed_cors_origins` 置 `'{}'`），所以注册是一条普通 INSERT，主键冲突同时拒绝已注册与已墓碑；更新与删除是 `WHERE client_id = $1 AND NOT removed AND revision = $2` 的单语句条件 UPDATE，0 行后复查区分 `ErrNotFound` 与 `ErrVersionConflict`；`revision` 只从列读，摘要、注册时间与 origins 三列写入时复制、只写不读；origin 查找走 `allowed_cors_origins @> ARRAY[$1]::text[]` 与 GIN 索引。本表无清理方法。
- **按条件移除（AD-13）**：空参数直接返回 `(0, nil)`、不发 SQL；否则单语句 `DELETE ... WHERE <定位列> = $1`，返回 `RowsAffected`。
- **清理**：`expires_at` 列（session 特例叫 `expires`）+ 对应索引，这两类列为 NULL 表示永不过期；PAR 的回收列 `retention_expiration` 为 `NOT NULL`，没有 NULL 形态，零值照常回收。
- **版本守卫**：`refresh_tokens` 与 `key_records` 有 `version bigint` 列，`client_registrations` 有同义的 `revision bigint` 列（AD-3）。
- **唯一性**：`key_records` 部分唯一索引（`phase <> 'retired'`）；`device_codes.user_code` 唯一索引双职责（读路径 + 重复拒绝）。

## 五、migrations 包

- `//go:embed *.sql` 嵌入全部迁移，`Up(ctx, pool)` 按版本顺序逐文件在**单连接**上执行（保持 search_path），每文件一个事务，记账 `schema_migrations(version, applied_at)`；重复调用幂等。
- `Down(ctx, pool)` 逆序回滚；未迁移过的库是 no-op。
- DDL 不写 schema 名，全部相对连接当前 search_path——宿主与测试工厂可钉专属 schema（e2e 已验证 `public` 无泄漏）。

## 六、验证架构（三组测试，见 development-guide.md）

```mermaid
flowchart LR
    L["tests/lineage<br/>血缘门禁 ×3"] --> G["CI lineage-gates job"]
    C["tests/conformance<br/>十一套件入口 + 隔离 schema 工厂"] --> G2["CI conformance job<br/>postgres:15 service"]
    E["tests/e2e<br/>真实库 e2e"] --> G2
    U["store/*_test.go<br/>单测（基座/编解码/错误映射）"] --> G2
    G --> G2
```

- **血缘门禁**：原创作权（文件头）、零上游血缘标注、中性提交信息——先跑并 gate 住一切构建。
- **契约套件**：十一个入口分别调用 wicket 三套件；工厂每次调用创建全新空 store（隔离 schema + 专属 pool），并行子测试互不污染；MUST 全绿，MAY 级逐条留档。
- **合规凭证**：每个 store 暴露 `ConformsTo() string`，返回所属套件的 `SuiteVersion` 常量（grant 族 → `storagetest`，session → `sessiontest`，keymgmt → `keymgmttest`）；wicket v0.10.0 下十一个均为 `4.0.0`。

## 七、关键不变量（改动红线）

1. 首参 `context.Context`；不在适配器内自造 `context.Background()`/`TODO()`，不自设超时。
2. 日志走注入 `*slog.Logger`；**凭据物料（令牌、句柄、密钥、secret）禁止落任何级别日志**，只记存在性与标识符前缀。
3. 默认 insert-only；句柄碰撞返 `ErrDuplicateHandle`；`expectedVersion` 冲突返 `ErrVersionConflict`。
4. 迁移与清理**不起后台 goroutine**。
5. SQL 永不出现时钟函数；`RemoveExpired` 统一形态 `DELETE ... WHERE <列> IS NOT NULL AND <列> < $cutoff`（PAR 的 `retention_expiration` 为 `NOT NULL` 列，写作 `DELETE ... WHERE retention_expiration < $cutoff`）。
6. 文件首行 `// Copyright 2026 Decker.`；任何文件不得出现血缘标注（门禁转红）。
7. 十一个 store 均暴露单值签名 `ConformsTo() string`。
8. 写入（Store）与 `UpdateRefreshToken` / `UpdateByUserCode` 在同一语句内写入 / 刷新定位列，不得漂移；空值存 NULL（`user_consents.client_id` 除外，该列 `NOT NULL`）。
9. `Remove*ByClient` / `*BySession` 空参数在 Go 侧短路返回 `(0, nil)`、不发 SQL。
10. `TakeAuthorizationCode` 与 `TakePushedAuthorizationRequest` 为单语句 `DELETE ... RETURNING`，禁止先 SELECT 再 DELETE、不开显式事务。
11. `UpdateClientRegistration` / `RemoveClientRegistration` 为单语句条件 UPDATE（`NOT removed AND revision = $2`），禁止先查后写、不开显式事务；墓碑不残留 payload 与注册访问令牌摘要。

## 八、依赖与演进

- 依赖树全部是已发布版本（wicket v0.10.0、pgx v5.10.0、yaml.v3 测试用），无 go.work 搭桥。
- 已识别风险（deferred-work 留档）：并发 `Up` 无 advisory lock（多实例并发启动 PK 冲突）；`schema_migrations` 表名与 golang-migrate 默认表同名冲突。`go 1.27rc1` directive 对 dependabot/gopls 的生态摩擦已消除（Go 1.27.0 发布后两处去 rc，2026-08-21）。
