# wicket-pg 数据模型

> 面向读者的任务：快速了解数据库有哪些表、每张表干什么、schema 怎么演进。**逐表逐索引与端口方法的完整对应关系见 [schema-design-record.md](./schema-design-record.md)**（维护 schema 必须同步更新那份记录）。

## 一、Schema 形态概览

- **12 张表**：1 张记账表（`schema_migrations`）+ 11 张业务表。
- **25 个显式索引**（含 2 个唯一索引、1 个 GIN 索引）+ 各表主键索引与复合主键。
- **业务载荷单列存储**：每张表都有 `payload jsonb`（`client_registrations` 的墓碑行为 NULL），存版本容器 `{"version":1,"dataProtected":false,"payload":"<base64>"}`——模型字段结构不摊进 schema，wicket 侧 model 演进时本仓 schema 不必动。
- **真实列最小集**：只落查询路径与守卫需要的列——句柄主键、过滤维度列、按条件移除的定位列（000004）、生命周期列、版本守卫列、唯一性列。例外：`pushed_authorization_requests` 的 `client_id` 与三个时间列按端口的「可见字段」落列，只写不读，模型只从 payload 解码（见 schema-design-record 例外 9）；`client_registrations` 的注册访问令牌摘要、注册时间与 `allowed_cors_origins` 同样只写不读，但 `revision` 只从列读（见例外 10）。

## 二、业务表清单（11 张）

| 表 | 主键/唯一 | 真实列（除 payload） | 对应端口族 | 备注 |
|---|---|---|---|---|
| `authorization_codes` | `handle` PK | `client_id`, `session_id`（可空，000004）, `expires_at` | 授权码 | 句柄取用 + 原子取出（`TakeAuthorizationCode`）+ 按 client / session 移除 + 过期清理 |
| `refresh_tokens` | `handle` PK | `client_id`, `session_id`（可空，000004）, `expires_at`, `version` | 刷新令牌 | `version` 版本守卫（expectedVersion）；`UpdateRefreshToken` 同语句刷新定位列 |
| `reference_tokens` | `handle` PK | `client_id`, `session_id`（可空，000004，`session_id` 取 `sid` claim）, `expires_at` | 引用令牌 | 按 client / session 移除走定位列；按 subject + client 对吊销仍走 payload 扫描（subject 不落列，有意为之） |
| `user_consents` | `(subject_id, client_id)` 复合 PK | `expires_at` | 用户同意 | 自然键 upsert（ON CONFLICT）；按 client 移除走 000004 新增的 `client_id` 单列索引 |
| `persisted_grants` | `key` PK | `subject_id`, `session_id`, `client_id`, `type`, `expires_at` | 持久化 grant | 四维过滤真实列 + 批量吊销 |
| `device_codes` | `handle` PK + `user_code` 唯一 | `user_code`, `session_id`（可空，000004）, `expires_at` | 设备流 | 双码（device/user）双读路径；`UpdateByUserCode` 同语句刷新 `session_id` |
| `backchannel_auth_requests` | `handle` PK | `expires_at` | CIBA 后通道 | ExpirationTime 零值存 NULL |
| `pushed_authorization_requests` | `key` PK | `client_id`（可空）, `creation_time`, `entry_expiration`, `retention_expiration`（000005） | 推送授权请求（PAR，可选端口） | insert-only；读取不消费；`TakePushedAuthorizationRequest` 单语句原子取出；按 `retention_expiration` 回收；可见列只写不读 |
| `client_registrations` | `client_id` PK | `revision`, `removed`, `registration_access_token_digest`（墓碑为 NULL）, `registration_time`, `allowed_cors_origins text[]`（000006） | 客户端注册（DCR）+ origin 查找 | 注册为普通 INSERT，墓碑同行占住主键；更新 / 删除为单语句 revision 守卫 UPDATE；origin 精确查找走 GIN；无清理方法 |
| `sessions` | `session_id` PK | `client_ids text[]`, `subject_id`（可空）, `expires` | 会话 | `expires` 列名例外（非 `expires_at`） |
| `key_records` | `handle` PK + `public_id` 部分唯一 | `public_id`, `phase`, `version` | 密钥管理 | 无清理方法；唯一性限 `phase <> 'retired'` |

## 三、索引职责速查（25 个显式索引）

| 索引 | 支撑的路径 |
|---|---|
| `idx_*_expires_at` ×7 + `idx_sessions_expires` + `idx_pushed_authorization_requests_retention_expiration`（000005） | 各表 `RemoveExpired` / `DeleteExpired` 清理路径 |
| `idx_persisted_grants_subject_id` / `_session_id` / `_client_id` / `_type` | `GetAll` / `RemoveAll` 四维过滤（含 `= ANY(...)`） |
| `idx_key_records_public_id_unique`（部分唯一） | `Create` 唯一性守卫（retired 可复用 public_id） |
| `idx_device_codes_user_code`（唯一） | `FindByUserCode` / `UpdateByUserCode` 读路径 + insert 重复拒绝（双职责） |
| `idx_sessions_subject_id` | `GetSessionsBySubjectID` / `DeleteSessionsBySubjectID`（可空列，NULL 永不命中是正确语义） |
| `idx_{authorization_codes,refresh_tokens,reference_tokens}_client_id` / `_session_id`（×6）+ `idx_device_codes_session_id` + `idx_user_consents_client_id`（000004） | 8 个 `Remove*ByClient` / `*BySession` 按条件移除（`DELETE ... WHERE <定位列> = $1`；空参数在 Go 侧短路返回 `(0, nil)`、不发 SQL） |
| `idx_client_registrations_allowed_cors_origins`（GIN，000006） | `FindClientsByAllowedOrigin`（`allowed_cors_origins @> ARRAY[$1]::text[] AND NOT removed`） |

## 四、设计约束（读写路径推导）

1. **schema 从端口读写访问模式推导**，不从 wicket model 字段清单映射：句柄取用→句柄主键；subject 批量吊销→复合索引；过期清理→`expires_at` 索引。
2. **过期判定在核心侧**：清理 SQL 统一形态 `DELETE ... WHERE <列> IS NOT NULL AND <列> < $cutoff`，cutoff 由调用方传入，SQL 永不出现时钟函数；`expires_at` 为 NULL 表示永不过期、永不清理。`pushed_authorization_requests` 的回收列 `retention_expiration` 为 `NOT NULL`，清理写作 `DELETE ... WHERE retention_expiration < $cutoff`，零值照常回收。
3. **零迁移纪律**：任何迁移变更必须同步更新 `schema-design-record.md`（三方一致：记录 ↔ 迁移 SQL ↔ store 源码）。

## 五、迁移策略

- **形态**：`go:embed` 嵌入 SQL 文件（`000001_init.up.sql` / `.down.sql` 惯例，golang-migrate 兼容），由适配器暴露 `migrations.Up(ctx, pool)` / `Down(ctx, pool)` 供宿主调用；不引入外部迁移库、不依赖宿主安装 CLI。
- **幂等**：`schema_migrations(version, applied_at)` 记账，重复 `Up` 跳过已应用版本；`Down` 逆序回滚全部已应用迁移（会删除所有表和数据，含 `000001`；不能用来单独撤销 000004，见 [deployment-guide.md](./deployment-guide.md) 第六节）。
- **search_path 中立**：DDL 不写 schema 名，全部相对连接当前 search_path——宿主与测试工厂可钉专属 schema 隔离。
- **执行语义**：每文件一个事务，单连接执行（所有语句落在该连接当前 search_path）；**不起后台 goroutine**。
- **版本历史**：`000001_init`（初始 9 表 + 记账表 + 13 索引）→ `000002_device_flow_user_code`（user_code 列 + 唯一索引）→ `000003_session_subject_id`（subject_id 列 + 索引）→ `000004_scoped_removal_columns`（7 个定位列 + 8 个索引，同事务从 payload 回填存量行）→ `000005_pushed_authorization_requests`（新表 + 回收索引，不动既有表、无回填）→ `000006_client_registrations`（新表 + origin GIN 索引，不动既有表、无回填）。

## 六、已知风险（deferred-work 留档）

- 并发 `Up` 无 advisory lock：多实例并发启动可能 PK 冲突（幂等仅串行成立）。
- `schema_migrations` 表名与 golang-migrate 默认表同名：共用库宿主 `CREATE TABLE IF NOT EXISTS` 静默 no-op 后 INSERT 缺列失败。
- 000004 回填只在迁移事务内执行一次：执行前须停掉所有旧版本写入方；payload 含 `\u0000` 会让整个事务回滚。升级步骤、NUL 预检与回退见 [deployment-guide.md](./deployment-guide.md) 第六节。
