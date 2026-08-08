# wicket-pg 数据模型

> 面向读者的任务：快速了解数据库有哪些表、每张表干什么、schema 怎么演进。**逐表逐索引与端口方法的完整对应关系见 [schema-design-record.md](./schema-design-record.md)**（维护 schema 必须同步更新那份记录）。

## 一、Schema 形态概览

- **10 张表**：1 张记账表（`schema_migrations`）+ 9 张业务表。
- **15 个显式索引**（含 2 个唯一索引）+ 各表主键索引与复合主键。
- **业务载荷单列存储**：每张表都有 `payload jsonb`，存版本容器 `{"version":1,"dataProtected":false,"payload":"<base64>"}`——模型字段结构不摊进 schema，wicket 侧 model 演进时本仓 schema 不必动。
- **真实列最小集**：只落查询路径与守卫需要的列——句柄主键、过滤维度列、生命周期列、版本守卫列、唯一性列。

## 二、业务表清单（9 张）

| 表 | 主键/唯一 | 真实列（除 payload） | 对应端口族 | 备注 |
|---|---|---|---|---|
| `authorization_codes` | `handle` PK | `expires_at` | 授权码 | 纯句柄取用 + 过期清理 |
| `refresh_tokens` | `handle` PK | `expires_at`, `version` | 刷新令牌 | `version` 版本守卫（expectedVersion） |
| `reference_tokens` | `handle` PK | `expires_at` | 引用令牌 | 批量吊销走 payload 扫描（无过滤真实列，有意为之） |
| `user_consents` | `(subject_id, client_id)` 复合 PK | `expires_at` | 用户同意 | 自然键 upsert（ON CONFLICT） |
| `persisted_grants` | `key` PK | `subject_id`, `session_id`, `client_id`, `type`, `expires_at` | 持久化 grant | 四维过滤真实列 + 批量吊销 |
| `device_codes` | `handle` PK + `user_code` 唯一 | `user_code`, `expires_at` | 设备流 | 双码（device/user）双读路径 |
| `backchannel_auth_requests` | `handle` PK | `expires_at` | CIBA 后通道 | ExpirationTime 零值存 NULL |
| `sessions` | `session_id` PK | `client_ids text[]`, `subject_id`（可空）, `expires` | 会话 | `expires` 列名例外（非 `expires_at`） |
| `key_records` | `handle` PK + `public_id` 部分唯一 | `public_id`, `phase`, `version` | 密钥管理 | 无清理方法；唯一性限 `phase <> 'retired'` |

## 三、索引职责速查（15 个显式索引）

| 索引 | 支撑的路径 |
|---|---|
| `idx_*_expires_at` ×7 + `idx_sessions_expires` | 各表 `RemoveExpired` / `DeleteExpired` 清理路径 |
| `idx_persisted_grants_subject_id` / `_session_id` / `_client_id` / `_type` | `GetAll` / `RemoveAll` 四维过滤（含 `= ANY(...)`） |
| `idx_key_records_public_id_unique`（部分唯一） | `Create` 唯一性守卫（retired 可复用 public_id） |
| `idx_device_codes_user_code`（唯一） | `FindByUserCode` / `UpdateByUserCode` 读路径 + insert 重复拒绝（双职责） |
| `idx_sessions_subject_id` | `GetSessionsBySubjectID` / `DeleteSessionsBySubjectID`（可空列，NULL 永不命中是正确语义） |

## 四、设计约束（读写路径推导）

1. **schema 从端口读写访问模式推导**，不从 wicket model 字段清单映射：句柄取用→句柄主键；subject 批量吊销→复合索引；过期清理→`expires_at` 索引。
2. **过期判定在核心侧**：清理 SQL 统一形态 `DELETE ... WHERE <列> IS NOT NULL AND <列> < $cutoff`，cutoff 由调用方传入，SQL 永不出现时钟函数；`expires_at` 为 NULL 表示永不过期、永不清理。
3. **零迁移纪律**：任何迁移变更必须同步更新 `schema-design-record.md`（三方一致：记录 ↔ 迁移 SQL ↔ store 源码）。

## 五、迁移策略

- **形态**：`go:embed` 嵌入 SQL 文件（`000001_init.up.sql` / `.down.sql` 惯例，golang-migrate 兼容），由适配器暴露 `migrations.Up(ctx, pool)` / `Down(ctx, pool)` 供宿主调用；不引入外部迁移库、不依赖宿主安装 CLI。
- **幂等**：`schema_migrations(version, applied_at)` 记账，重复 `Up` 跳过已应用版本；`Down` 逆序回滚。
- **search_path 中立**：DDL 不写 schema 名，全部相对连接当前 search_path——宿主与测试工厂可钉专属 schema 隔离。
- **执行语义**：每文件一个事务，单连接执行（所有语句落在该连接当前 search_path）；**不起后台 goroutine**。
- **版本历史**：`000001_init`（初始 9 表 + 记账表 + 13 索引）→ `000002_device_flow_user_code`（user_code 列 + 唯一索引）→ `000003_session_subject_id`（subject_id 列 + 索引）。

## 六、已知风险（deferred-work 留档）

- 并发 `Up` 无 advisory lock：多实例并发启动可能 PK 冲突（幂等仅串行成立）。
- `schema_migrations` 表名与 golang-migrate 默认表同名：共用库宿主 `CREATE TABLE IF NOT EXISTS` 静默 no-op 后 INSERT 缺列失败。
