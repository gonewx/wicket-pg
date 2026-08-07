# wicket-pg Schema 设计记录

本文件是 wicket-pg 的 schema 设计记录（FR-5 / AD-4）：**逐表逐索引指名对应的端口方法**。维护者打开本文件即可追踪每一张表、每一个索引承载了哪条读写/清理路径；任何迁移 SQL 变更都应能在本记录中追到对应的读路径或清理路径。

**盘点基线：** 迁移 `migrations/000001_init.up.sql`、`migrations/000002_device_flow_user_code.up.sql`、`migrations/000003_session_subject_id.up.sql` 与 `store/` 九个 store 实现（2026-08-08 逐行核对）。

---

## 一、推导原则

- **schema 从端口的读写访问模式推导，不从 model 字段清单映射。** 按句柄单点取用 → 句柄列为主键；按 subject 批量吊销 → `subject_id` 相关复合索引；按过期时间清理 → `expires_at` 索引。表名、实体划分与列集合均由此独立推导。
- **序列化载荷以单列存储**：业务模型序列化进 `payload jsonb`（版本容器形状 `{"version":1,"dataProtected":false,"payload":"<base64>"}`，见 `store/codec.go`），不把 model 字段结构摊进 schema——model 演进时 schema 不必跟着动。
- **列名 snake_case**：Go 侧命名的 snake_case 转写（SQL 惯例）。
- **过期判定在核心侧**：清理 SQL 统一形态 `DELETE ... WHERE <列> IS NOT NULL AND <列> < $cutoff`，cutoff 由调用方传入，SQL 永不出现时钟函数。
- **端口契约六要素**贯穿所有方法语义：insert-only + `ErrDuplicateHandle`、`expectedVersion` + `ErrVersionConflict`、清理返回计数、过期判定在核心侧。

---

## 二、表清单

### 记账表（非端口表）

| 表 | 真实列 | 主键/唯一约束 | 承载的路径 |
|---|---|---|---|
| `schema_migrations` | `version text NOT NULL`, `applied_at timestamptz NOT NULL` | `version` PK | 非端口——迁移运行器（`migrations/migrations.go` `Up`/`Down`）的幂等记账（AD-8），不经任何 store 访问 |

### 业务表

| 表 | 真实列 | 主键/唯一约束 | 承载的端口方法 | 读写模式推导依据 |
|---|---|---|---|---|
| `authorization_codes` | `handle text PK`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK | `StoreAuthorizationCode`（insert-only）/ `GetAuthorizationCode` / `RemoveAuthorizationCode` / `RemoveExpired` | 按句柄单点取用；过期清理 |
| `refresh_tokens` | `handle text PK`, `expires_at timestamptz`（可空）, `version bigint NOT NULL`, `payload jsonb NOT NULL` | `handle` PK | `StoreRefreshToken`（insert-only）/ `GetRefreshToken` / `UpdateRefreshToken`（`expectedVersion` 版本守卫）/ `RemoveRefreshToken` / `RemoveExpired` | 句柄取用 + 版本守卫列（AD-3，仅此表与 `key_records` 有 `version` 列） |
| `reference_tokens` | `handle text PK`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK | `StoreReferenceToken`（insert-only）/ `GetReferenceToken` / `RemoveReferenceToken` / `RemoveReferenceTokens` / `RemoveExpired` | 句柄取用；批量吊销**扫描 payload**（无过滤真实列，见例外 1） |
| `user_consents` | `subject_id text NOT NULL`, `client_id text NOT NULL`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `(subject_id, client_id)` 复合 PK | `StoreUserConsent`（`ON CONFLICT (subject_id, client_id)` upsert）/ `GetUserConsent` / `GetAllUserConsents` / `RemoveUserConsent` / `RemoveExpired` | 自然键 upsert（AD-10）；按 subject 批量取用走复合 PK 前缀（见例外 6） |
| `persisted_grants` | `key text PK`, `subject_id text NOT NULL`, `session_id text NOT NULL`, `client_id text NOT NULL`, `type text NOT NULL`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `key` PK | `Store`（`ON CONFLICT (key)` upsert）/ `Get` / `GetAll`（多维过滤）/ `Remove` / `RemoveAll`（批量吊销）/ `RemoveExpired` | 四个过滤维度（subject/session/client/type）落真实列（AD-4），`ClientIds`/`Types` 用 `= ANY(...)` |
| `device_codes` | `handle text PK`, `user_code text NOT NULL`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK + `user_code` 唯一索引 | `StoreDeviceAuthorization`（insert-only，双码重复拒绝）/ `FindByDeviceCode` / `FindByUserCode` / `UpdateByUserCode` / `RemoveByDeviceCode` / `RemoveExpired` | 双码（device code + user code）双读路径；`user_code` 唯一索引同时承担写路径重复拒绝（见例外 5） |
| `backchannel_auth_requests` | `handle text PK`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK | `StoreBackchannelAuthenticationRequest`（insert-only）/ `FindBackchannelAuthenticationRequest` / `UpdateBackchannelAuthenticationRequest` / `RemoveBackchannelAuthenticationRequest` / `RemoveExpired` | 句柄取用；`ExpirationTime` 零值存 NULL（AD-5） |
| `sessions` | `session_id text PK`, `client_ids text[] NOT NULL`, `subject_id text`（可空，迁移 000003 增）, `expires timestamptz`（可空）, `payload jsonb NOT NULL` | `session_id` PK | `GetSession` / `CreateSession` / `UpdateSession` / `DeleteSession` / `GetSessionsBySubjectID` / `DeleteSessionsBySubjectID` / `AddClientID` / `DeleteExpired` | `AddClientID` 对 `client_ids` text[] 原子追加去重（AD-6）；批量吊销按 subject；清理走 **`expires` 列**（不是 `expires_at`，见例外 4） |
| `key_records` | `handle text PK`, `public_id text NOT NULL`, `phase text NOT NULL`, `version bigint NOT NULL`, `payload jsonb NOT NULL` | `handle` PK + 部分唯一索引 | `Get` / `Create`（insert-only，双唯一约束）/ `Update`（`expectedVersion` 版本守卫）/ `Delete` / `List` | 唯一性列（AD-7）；版本守卫（AD-3）；**无清理方法**（见例外 2） |

---

## 三、索引清单（15 个显式索引）

每个索引逐一指名支撑的读路径或清理路径及其方法名；索引与路径是双向核对关系——索引存在必有真实路径，真实路径列举在表清单中。

| 索引 | 定义 | 支撑的路径（方法） | 路径类型 |
|---|---|---|---|
| `idx_authorization_codes_expires_at` | `authorization_codes (expires_at)` | `AuthorizationCodeStore.RemoveExpired` | 清理路径（`DELETE ... WHERE expires_at IS NOT NULL AND expires_at < $cutoff`） |
| `idx_refresh_tokens_expires_at` | `refresh_tokens (expires_at)` | `RefreshTokenStore.RemoveExpired` | 清理路径 |
| `idx_reference_tokens_expires_at` | `reference_tokens (expires_at)` | `ReferenceTokenStore.RemoveExpired` | 清理路径 |
| `idx_user_consents_expires_at` | `user_consents (expires_at)` | `UserConsentStore.RemoveExpired` | 清理路径 |
| `idx_persisted_grants_expires_at` | `persisted_grants (expires_at)` | `PersistedGrantStore.RemoveExpired` | 清理路径 |
| `idx_device_codes_expires_at` | `device_codes (expires_at)` | `DeviceFlowStore.RemoveExpired` | 清理路径 |
| `idx_backchannel_auth_requests_expires_at` | `backchannel_auth_requests (expires_at)` | `BackchannelAuthenticationRequestStore.RemoveExpired` | 清理路径 |
| `idx_sessions_expires` | `sessions (expires)` | `SessionStore.DeleteExpired` | 清理路径（**`expires` 列**，session 例外，见例外 4） |
| `idx_persisted_grants_subject_id` | `persisted_grants (subject_id, client_id, type)` | `PersistedGrantStore.GetAll` / `RemoveAll` 的 `SubjectId` 过滤（`subject_id = $n`） | 读路径 + 批量吊销路径 |
| `idx_persisted_grants_session_id` | `persisted_grants (session_id)` | `GetAll` / `RemoveAll` 的 `SessionId` 过滤（`session_id = $n`） | 读路径 + 批量吊销路径 |
| `idx_persisted_grants_client_id` | `persisted_grants (client_id, type)` | `GetAll` / `RemoveAll` 的 `ClientId` / `ClientIds`（`client_id = $n` / `client_id = ANY($n)`）过滤 | 读路径 + 批量吊销路径 |
| `idx_persisted_grants_type` | `persisted_grants (type)` | `GetAll` / `RemoveAll` 的 `Type` / `Types`（`type = $n` / `type = ANY($n)`）过滤 | 读路径 + 批量吊销路径 |
| `idx_key_records_public_id_unique` | `UNIQUE key_records (public_id) WHERE phase <> 'retired'` | `KeyRecordStore.Create` 唯一性约束（SQLSTATE 23505 → `keymgmt.ErrDuplicateKey`）；非 retired 记录 `public_id` 唯一是**写路径守卫**（AD-7 部分唯一索引，retired 记录可复用 public_id） | 写路径约束（部分唯一索引） |
| `idx_device_codes_user_code` | `UNIQUE device_codes (user_code)`（迁移 000002 增） | `DeviceFlowStore.FindByUserCode` / `UpdateByUserCode`（读/更新路径，`WHERE user_code = $1`）+ `StoreDeviceAuthorization` 重复拒绝（SQLSTATE 23505 → `storage.ErrDuplicateHandle`） | 读路径 + 写路径约束（双职责，见例外 5） |
| `idx_sessions_subject_id` | `sessions (subject_id)`（迁移 000003 增，可空列） | `SessionStore.GetSessionsBySubjectID` / `DeleteSessionsBySubjectID`（`WHERE subject_id = $1`） | 读路径 + 批量吊销路径 |

### 复合主键与单列主键（非显式索引名，同样指名）

- 各表 `handle` / `session_id` / `key` 单列 PK 索引支撑全部单点取用读路径（`Get*`/`FindBy*` 的 `WHERE <pk> = $1`）与 upsert 的 `ON CONFLICT` 目标（AD-10）。
- `user_consents` 复合 PK `(subject_id, client_id)` 支撑 `GetUserConsent`（全对匹配 `WHERE subject_id = $1 AND client_id = $2`）、`GetAllUserConsents`（`subject_id` 前缀匹配 `WHERE subject_id = $1`）与 `StoreUserConsent` 的 `ON CONFLICT (subject_id, client_id)` 目标。

---

## 四、例外与设计决策

1. **`reference_tokens` 无 subject/client 过滤列，批量吊销走 payload 扫描**：`RemoveReferenceTokens(ctx, subjectId, clientId)` 全表 `SELECT handle, payload` 后按模型字段匹配，再 `DELETE ... WHERE handle = ANY($1)`（`store/reference_token.go`）——有意的设计决策：表不落过滤真实列（AD-4 真实列最小集），该路径**不需要索引**。AC-2 的「每个索引都能对上路径」不应被误读成「每条路径都要有索引」。
2. **`key_records` 没有 `expires_at` 列、没有清理方法**：密钥退役由核心侧协调（`store/key_records.go` 无 `RemoveExpired`/`DeleteExpired`），是九个 store 中唯一无清理入口的。不为它虚构清理路径。
3. **`sessions.subject_id` 可空**：空 subject 存 NULL，永不命中 `WHERE subject_id = $1`（`store/session.go`，镜像内存语义）——`idx_sessions_subject_id` 对 NULL 无命中是正确语义，不是缺陷。
4. **`sessions.expires` 列名例外**：清理列是 `expires` 不是 `expires_at`（`store/session.go` `DeleteExpired`）——统一清理形态中「session 为 `expires` 列」的特例（AD-5）。
5. **`device_codes.user_code` 唯一索引双职责**：既支撑 `FindByUserCode`/`UpdateByUserCode` 读路径，又承担 insert-only 的重复拒绝（SQLSTATE 23505 → `storage.ErrDuplicateHandle`）——索引条目须同时指名两类路径。
6. **`user_consents` 无独立 subject 索引**：`GetAllUserConsents` 走复合 PK 的前缀匹配，无需额外索引——反向核对时不要误报「缺索引」。

---

## 五、维护约定

- **零迁移纪律**：本记录只读盘点迁移 SQL 与 store 实现；任何迁移变更（新增表/列/索引）都必须同步更新本记录，指明新对象的对应端口方法，否则 AC-2 的「索引必须对上真实路径」无法核验。
- **三方一致**：记录中的表名/列名/索引名/方法名与 `migrations/*.up.sql`、`store/*.go` 逐字一致，不出现臆造的对象名。
- **盘点核对**：迁移 SQL 中每个对象（表、显式索引、复合主键）在记录中有条目；记录中每个条目在迁移 SQL 与 store 源码中真实存在。
