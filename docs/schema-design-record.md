# wicket-pg Schema 设计记录

本文件是 wicket-pg 的 schema 设计记录（FR-5 / AD-4）：**逐表逐索引指名对应的端口方法**。维护者打开本文件即可追踪每一张表、每一个索引承载了哪条读写/清理路径；任何迁移 SQL 变更都应能在本记录中追到对应的读路径或清理路径。

**盘点基线：** 迁移 `migrations/000001_init.up.sql`、`migrations/000002_device_flow_user_code.up.sql`、`migrations/000003_session_subject_id.up.sql`、`migrations/000004_scoped_removal_columns.up.sql`、`migrations/000005_pushed_authorization_requests.up.sql`、`migrations/000006_client_registrations.up.sql` 与 `store/` 十一个 store 实现（2026-08-08 逐行核对；2026-11-21 补入 000004 及 wicket v0.8.0 新增端口方法；同日补入 000005 与 `PushedAuthorizationRequestStore`，以及 000006 与 `ClientRegistrationStore`；2026-11-22 补入 wicket v0.10.0 新增的 `RefreshTokenStore.RemoveOnlineRefreshTokensBySession`，无新迁移）。

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
| `authorization_codes` | `handle text PK`, `client_id text`（可空，000004 增）, `session_id text`（可空，000004 增）, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK | `StoreAuthorizationCode`（insert-only，写定位列）/ `GetAuthorizationCode` / `TakeAuthorizationCode`（`DELETE ... WHERE handle = $1 RETURNING payload` 单语句原子取出）/ `RemoveAuthorizationCode` / `RemoveAuthorizationCodesByClient` / `RemoveAuthorizationCodesBySession` / `RemoveExpired` | 按句柄单点取用；按句柄原子取出（`TakeAuthorizationCode` 单语句 `DELETE ... RETURNING`，AD-12）；过期清理；按 client / session 批量移除的定位列（见例外 7） |
| `refresh_tokens` | `handle text PK`, `client_id text`（可空，000004 增）, `session_id text`（可空，000004 增）, `expires_at timestamptz`（可空）, `version bigint NOT NULL`, `payload jsonb NOT NULL` | `handle` PK | `StoreRefreshToken`（insert-only，写定位列）/ `GetRefreshToken` / `UpdateRefreshToken`（`expectedVersion` 版本守卫，同语句刷新定位列）/ `RemoveRefreshToken` / `RemoveRefreshTokensByClient` / `RemoveRefreshTokensBySession` / `RemoveExpired` | 句柄取用 + 版本守卫列（AD-3，仅此表与 `key_records` 有 `version` 列）；定位列（见例外 7） |
| `reference_tokens` | `handle text PK`, `client_id text`（可空，000004 增）, `session_id text`（可空，000004 增，取 `sid` claim）, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK | `StoreReferenceToken`（insert-only，写定位列）/ `GetReferenceToken` / `RemoveReferenceToken` / `RemoveReferenceTokens` / `RemoveReferenceTokensByClient` / `RemoveReferenceTokensBySession` / `RemoveExpired` | 句柄取用；仅 `RemoveReferenceTokens`（按 subject + client 对吊销）**扫描 payload**（subject 无真实列，见例外 1）；`RemoveReferenceTokensByClient` / `RemoveReferenceTokensBySession` 走 `client_id` / `session_id` 定位列及其索引（见例外 7） |
| `user_consents` | `subject_id text NOT NULL`, `client_id text NOT NULL`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `(subject_id, client_id)` 复合 PK + `client_id` 单列索引（000004 增） | `StoreUserConsent`（`ON CONFLICT (subject_id, client_id)` upsert）/ `GetUserConsent` / `GetAllUserConsents` / `RemoveUserConsent` / `RemoveUserConsentsByClient` / `RemoveExpired` | 自然键 upsert（AD-10）；按 subject 批量取用走复合 PK 前缀（见例外 6）；按 client 批量移除走单列索引（见例外 7） |
| `persisted_grants` | `key text PK`, `subject_id text NOT NULL`, `session_id text NOT NULL`, `client_id text NOT NULL`, `type text NOT NULL`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `key` PK | `Store`（`ON CONFLICT (key)` upsert）/ `Get` / `GetAll`（多维过滤）/ `Remove` / `RemoveAll`（批量吊销）/ `RemoveExpired` | 四个过滤维度（subject/session/client/type）落真实列（AD-4），`ClientIds`/`Types` 用 `= ANY(...)` |
| `device_codes` | `handle text PK`, `user_code text NOT NULL`, `session_id text`（可空，000004 增）, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK + `user_code` 唯一索引 | `StoreDeviceAuthorization`（insert-only，双码重复拒绝，写 `session_id`）/ `FindByDeviceCode` / `FindByUserCode` / `UpdateByUserCode`（审批写入 session，同语句刷新 `session_id`）/ `RemoveByDeviceCode` / `RemoveDeviceCodesBySession`（一行承载双码，计数一次）/ `RemoveExpired` | 双码（device code + user code）双读路径；`user_code` 唯一索引同时承担写路径重复拒绝（见例外 5）；`session_id` 定位列（无 `client_id`，见例外 7） |
| `backchannel_auth_requests` | `handle text PK`, `expires_at timestamptz`（可空）, `payload jsonb NOT NULL` | `handle` PK | `StoreBackchannelAuthenticationRequest`（insert-only）/ `FindBackchannelAuthenticationRequest` / `UpdateBackchannelAuthenticationRequest` / `RemoveBackchannelAuthenticationRequest` / `RemoveExpired` | 句柄取用；`ExpirationTime` 零值存 NULL（AD-5） |
| `pushed_authorization_requests` | `key text PK`, `client_id text`（可空）, `creation_time timestamptz NOT NULL`, `entry_expiration timestamptz NOT NULL`, `retention_expiration timestamptz NOT NULL`, `payload jsonb NOT NULL`（迁移 000005 增） | `key` PK | `StorePushedAuthorizationRequest`（insert-only，写可见列）/ `GetPushedAuthorizationRequest`（读取不消费）/ `TakePushedAuthorizationRequest`（`DELETE ... WHERE key = $1 RETURNING payload` 单语句原子取出）/ `RemoveExpired`（按 `retention_expiration` 回收） | 按键单点取用与原子取出（AD-12）；键只经 `storage.PushedAuthorizationRequestKey` 生成；回收只看 `RetentionExpiration`；四个可见列只写不读（见例外 9） |
| `client_registrations` | `client_id text PK`, `revision bigint NOT NULL`, `removed boolean NOT NULL DEFAULT false`, `registration_access_token_digest text`（墓碑为 NULL）, `registration_time timestamptz NOT NULL`, `allowed_cors_origins text[] NOT NULL`, `payload jsonb`（墓碑为 NULL）（迁移 000006 增）；CHECK `removed = (payload IS NULL)` 与 `removed = (registration_access_token_digest IS NULL)` | `client_id` PK | `ClientRegistrationStore.RegisterClient`（普通 INSERT，主键冲突同时拒绝已注册与已墓碑，SQLSTATE 23505 → `storage.ErrDuplicateHandle`）/ `ClientRegistrationStore.FindClientRegistration` / `ClientRegistrationStore.FindClientById` / `ClientRegistrationStore.UpdateClientRegistration`（`UPDATE ... WHERE client_id = $1 AND NOT removed AND revision = $2` 单语句 revision 守卫，同语句 `revision = revision + 1` 并刷新可见列）/ `ClientRegistrationStore.RemoveClientRegistration`（同形条件 UPDATE 置墓碑）/ `ClientRegistrationStore.FindClientsByAllowedOrigin`（端口 `storage.ClientOriginLookup`，`allowed_cors_origins @> ARRAY[$1]::text[]`） | 按 client id 单点取用；revision 守卫（AD-3）；墓碑与活记录同行；origin 精确查找走 `text[]` 列加 GIN 索引；**无清理方法**（见例外 2、例外 10） |
| `sessions` | `session_id text PK`, `client_ids text[] NOT NULL`, `subject_id text`（可空，迁移 000003 增）, `expires timestamptz`（可空）, `payload jsonb NOT NULL` | `session_id` PK | `GetSession` / `CreateSession` / `UpdateSession` / `DeleteSession` / `GetSessionsBySubjectID` / `DeleteSessionsBySubjectID` / `AddClientID` / `DeleteExpired` | `AddClientID` 对 `client_ids` text[] 原子追加去重（AD-6）；批量吊销按 subject；清理走 **`expires` 列**（不是 `expires_at`，见例外 4） |
| `key_records` | `handle text PK`, `public_id text NOT NULL`, `phase text NOT NULL`, `version bigint NOT NULL`, `payload jsonb NOT NULL` | `handle` PK + 部分唯一索引 | `Get` / `Create`（insert-only，双唯一约束）/ `Update`（`expectedVersion` 版本守卫）/ `Delete` / `List` | 唯一性列（AD-7）；版本守卫（AD-3）；**无清理方法**（见例外 2） |

---

## 三、索引清单（25 个显式索引）

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
| `idx_authorization_codes_client_id` | `authorization_codes (client_id)`（迁移 000004 增，可空列） | `AuthorizationCodeStore.RemoveAuthorizationCodesByClient`（端口 `storage.AuthorizationCodeStore`，wicket v0.5.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE client_id = $1` | 批量移除路径 |
| `idx_authorization_codes_session_id` | `authorization_codes (session_id)`（迁移 000004 增，可空列） | `AuthorizationCodeStore.RemoveAuthorizationCodesBySession`（端口 `storage.AuthorizationCodeStore`，wicket v0.5.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE session_id = $1` | 批量移除路径 |
| `idx_refresh_tokens_client_id` | `refresh_tokens (client_id)`（迁移 000004 增，可空列） | `RefreshTokenStore.RemoveRefreshTokensByClient`（端口 `storage.RefreshTokenStore`，wicket v0.5.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE client_id = $1` | 批量移除路径 |
| `idx_refresh_tokens_session_id` | `refresh_tokens (session_id)`（迁移 000004 增，可空列） | `RefreshTokenStore.RemoveRefreshTokensBySession`（端口 `storage.RefreshTokenStore`，wicket v0.5.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE session_id = $1`；`RefreshTokenStore.RemoveOnlineRefreshTokensBySession`（端口 `storage.RefreshTokenStore`，wicket v0.10.0 起，见例外 11）：事务内 `SELECT handle, payload ... WHERE session_id = $1 FOR UPDATE`，Go 侧判定后 `DELETE ... WHERE handle = ANY($1) AND session_id = $2` | 批量移除路径 |
| `idx_reference_tokens_client_id` | `reference_tokens (client_id)`（迁移 000004 增，可空列） | `ReferenceTokenStore.RemoveReferenceTokensByClient`（端口 `storage.ReferenceTokenStore`，wicket v0.5.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE client_id = $1` | 批量移除路径 |
| `idx_reference_tokens_session_id` | `reference_tokens (session_id)`（迁移 000004 增，可空列） | `ReferenceTokenStore.RemoveReferenceTokensBySession`（端口 `storage.ReferenceTokenStore`，wicket v0.5.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE session_id = $1` | 批量移除路径 |
| `idx_device_codes_session_id` | `device_codes (session_id)`（迁移 000004 增，可空列） | `DeviceFlowStore.RemoveDeviceCodesBySession`（端口 `storage.DeviceFlowStore`，wicket v0.8.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE session_id = $1` | 批量移除路径 |
| `idx_user_consents_client_id` | `user_consents (client_id)`（迁移 000004 增索引；列为 000001 既有的 `NOT NULL` 列） | `UserConsentStore.RemoveUserConsentsByClient`（端口 `storage.UserConsentStore`，wicket v0.5.0 起；Story 4.2 实现，见例外 8）：`DELETE ... WHERE client_id = $1` | 批量移除路径 |
| `idx_pushed_authorization_requests_retention_expiration` | `pushed_authorization_requests (retention_expiration)`（迁移 000005 增） | `PushedAuthorizationRequestStore.RemoveExpired`（端口 `storage.PushedAuthorizationRequestStore`，wicket v0.8.0 起；Story 5.1 实现）：`DELETE ... WHERE retention_expiration < $1` | 清理路径（`NOT NULL` 列，无 `IS NOT NULL` 守卫，见例外 9） |
| `idx_client_registrations_allowed_cors_origins` | `client_registrations USING gin (allowed_cors_origins)`（迁移 000006 增） | `ClientRegistrationStore.FindClientsByAllowedOrigin`（端口 `storage.ClientOriginLookup`，wicket v0.5.0 起；Story 5.2 实现）：`SELECT ... WHERE allowed_cors_origins @> ARRAY[$1]::text[] AND NOT removed` | 读路径（GIN 数组包含，见例外 10） |

### 复合主键与单列主键（非显式索引名，同样指名）

- 各表 `handle` / `session_id` / `key` 单列 PK 索引支撑全部单点取用读路径（`Get*`/`FindBy*` 的 `WHERE <pk> = $1`）与 upsert 的 `ON CONFLICT` 目标（AD-10）。
- `authorization_codes` 的 `handle` PK 还支撑 `AuthorizationCodeStore.TakeAuthorizationCode` 的原子取出路径：`DELETE ... WHERE handle = $1 RETURNING payload` 单语句完成定位、删除与返回（AD-12），不先 SELECT 再 DELETE、不开显式事务。
- `pushed_authorization_requests` 的 `key` PK 支撑 `PushedAuthorizationRequestStore.GetPushedAuthorizationRequest`（`WHERE key = $1`）、`PushedAuthorizationRequestStore.TakePushedAuthorizationRequest` 的原子取出（`DELETE ... WHERE key = $1 RETURNING payload`，AD-12）与 `PushedAuthorizationRequestStore.StorePushedAuthorizationRequest` 的重复拒绝（SQLSTATE 23505 → `storage.ErrDuplicateHandle`）。
- `client_registrations` 的 `client_id` PK 支撑 `ClientRegistrationStore.FindClientRegistration` / `ClientRegistrationStore.FindClientById`（`WHERE client_id = $1 AND NOT removed`）、`ClientRegistrationStore.UpdateClientRegistration` / `ClientRegistrationStore.RemoveClientRegistration` 的条件 UPDATE 与 0 行后的存在性复查（`SELECT removed ... WHERE client_id = $1`），以及 `ClientRegistrationStore.RegisterClient` 的重复拒绝（SQLSTATE 23505 → `storage.ErrDuplicateHandle`，墓碑行同样占住主键）。
- `user_consents` 复合 PK `(subject_id, client_id)` 支撑 `GetUserConsent`（全对匹配 `WHERE subject_id = $1 AND client_id = $2`）、`GetAllUserConsents`（`subject_id` 前缀匹配 `WHERE subject_id = $1`）与 `StoreUserConsent` 的 `ON CONFLICT (subject_id, client_id)` 目标。

---

## 四、例外与设计决策

1. **`reference_tokens` 无 subject 过滤列，仅按 subject + client 对吊销（`RemoveReferenceTokens`）走 payload 扫描**（按 client / 按 session 移除走定位列，见例外 7）：000004 增了 `client_id` / `session_id` 定位列（服务按条件移除，见例外 7），但 subject 仍不落列，`RemoveReferenceTokens(ctx, subjectId, clientId)` 仍全表 `SELECT handle, payload` 后按模型字段匹配，再 `DELETE ... WHERE handle = ANY($1)`（`store/reference_token.go`）——有意的设计决策：表不落过滤真实列（AD-4 真实列最小集），该路径**不需要索引**。AC-2 的「每个索引都能对上路径」不应被误读成「每条路径都要有索引」。
2. **`key_records` 没有 `expires_at` 列、没有清理方法**：密钥退役由核心侧协调（`store/key_records.go` 无 `RemoveExpired`/`DeleteExpired`），是十一个 store 中两个无清理入口的 store 之一，另一个是 `client_registrations`（注册客户端没有服务端生命周期，见例外 10）。不为它们虚构清理路径。
3. **`sessions.subject_id` 可空**：空 subject 存 NULL，永不命中 `WHERE subject_id = $1`（`store/session.go`，镜像内存语义）——`idx_sessions_subject_id` 对 NULL 无命中是正确语义，不是缺陷。
4. **`sessions.expires` 列名例外**：清理列是 `expires` 不是 `expires_at`（`store/session.go` `DeleteExpired`）——统一清理形态中「session 为 `expires` 列」的特例（AD-5）。
5. **`device_codes.user_code` 唯一索引双职责**：既支撑 `FindByUserCode`/`UpdateByUserCode` 读路径，又承担 insert-only 的重复拒绝（SQLSTATE 23505 → `storage.ErrDuplicateHandle`）——索引条目须同时指名两类路径。
6. **`user_consents` 无独立 subject 索引**：`GetAllUserConsents` 走复合 PK 的前缀匹配，无需额外索引——反向核对时不要误报「缺索引」。
7. **按条件移除的定位列（000004）**：`authorization_codes` / `refresh_tokens` / `reference_tokens` 落 `client_id` + `session_id`，`device_codes` 只落 `session_id`（设备流端口无按 client 移除，AD-4 真实列最小集），`user_consents` 已有 `client_id` 列但复合 PK 前导列为 `subject_id`，故为按 client 移除单建索引。值在写入时取自模型（引用令牌的 session 取 `Token.SessionId()`，即 `Type` 为 `sid` 的 claim 恰好一个、且其值为字符串时取该值；多于一个——不论值类型——或唯一一个不是字符串时为空），`UpdateRefreshToken` / `UpdateByUserCode` 同语句刷新，不残留陈旧值。**空值存 NULL**（沿例外 3 的 `sessions.subject_id` 先例）：NULL 不参与等值匹配，按空参数移除永不命中无 client / session 的记录。存量行由 000004 在同一迁移事务内从 payload 容器解码回填，回填语义与 Go 侧逐条一致（`tests/e2e/scoped_removal_columns_e2e_test.go` 直接列断言）。**部署顺序：执行 000004 之前须停掉所有旧版本写入方**——迁移后旧二进制的 INSERT 会留下 NULL 定位列、旧二进制的 `UpdateRefreshToken` / `UpdateByUserCode` 会留下陈旧值，回填只在迁移事务内执行一次，事后不会补齐，按条件移除会漏删这些行。
8. **4.1 → 4.2 过渡期已结束**：8 个定位列索引支撑的按条件移除方法已随 wicket v0.8.0 升级原子落地（Story 4.2），索引条目均为「store 类型名 + 点号 + 方法名」的完整引用，由静态盘点 `TestSchemaRecordStoreMethods` 强制核对方法存在。空参数在 Go 侧短路返回 `(0, nil)`、不发 SQL：定位列的 NULL 只是第二道防线，`user_consents.client_id` 为 `NOT NULL` 列、空 ClientId 以空串存储，`WHERE client_id = ''` 会命中，因此 Go 侧短路是正确性要求。

9. **`pushed_authorization_requests` 的可见列只写不读（000005）**：`client_id`、`creation_time`、`entry_expiration`、`retention_expiration` 四列在 `StorePushedAuthorizationRequest` 写入时从模型复制（空 ClientId 存 NULL），只服务运维查询和将来的索引；`Get` / `Take` **只**从 `payload` 解码模型，从不由列重建，因为 `timestamptz` 只精确到微秒，模型时间可能带纳秒。这是 AD-4「不把模型摊成 schema」的登记例外：列存在，但模型的唯一来源仍是 payload，模型演进时这些列不必跟着动。其余约定：
   - 整个模型（含不透明的 `Payload []byte`）经 payload codec 写入版本容器；`Payload` 可能含 NUL 与非法 UTF-8，只以 base64 形式进入 jsonb，不原样写入任何列。
   - 回收列沿用模型字段名 `retention_expiration`，不叫 `expires_at`：端口区分 `EntryExpiration`（入口有效期，由核心判定，不驱动回收）和 `RetentionExpiration`（入口有效期加交互往返宽限，回收只看它），叫 `expires_at` 会被误读为前者。
   - 回收 SQL 为 `DELETE ... WHERE retention_expiration < $1`：列为 `NOT NULL`，不写 `IS NOT NULL` 守卫，也不做零值转 NULL；零值 `RetentionExpiration` 照常存储，任何晚于零时刻的 cutoff 都会回收它。这是统一清理形态（第一节）在本表的变体，`migrations/clock_audit_test.go` 单独固定其形态。
   - 端口没有按 client 枚举或移除的路径，`client_id` 不建索引（AD-4 每个查询列须配索引，反之无查询路径的列不配索引）；键只经 `storage.PushedAuthorizationRequestKey` 生成，store 不生成、不拼接键。

10. **`client_registrations` 的可见列只写不读，revision 以列为准（000006）**：`registration_access_token_digest`、`registration_time`、`allowed_cors_origins` 三列在 `RegisterClient` / `UpdateClientRegistration` 写入时从记录复制，服务运维查询和 origin 查找索引；读路径只从 `payload` 解码客户端（`storage.DecodeClient`）、摘要和注册时间，不由这三列重建，因为 `timestamptz` 只精确到微秒，而注册时间可能带纳秒。与例外 9 的区别在于 **revision 以 `revision` 列为准**：payload 不含 revision，读取时 `Revision` 只取自该列，更新与删除的并发守卫也只看该列（AD-3）。其余约定：
   - payload 容器内是本仓未导出的结构体，含 `storage.EncodeClient` 产出的字节、`RegistrationAccessTokenDigest` 与 `RegistrationTime`；客户端字节按不透明字节存储，不摊成列。
   - **墓碑与活记录同行**：删除在同一条 UPDATE 中置 `removed = true`、`payload` 与摘要置 NULL、`allowed_cors_origins` 置 `'{}'`，只保留 `client_id`、`revision` 与 `registration_time`，不残留 secret 哈希和摘要。两条 CHECK 约束（`removed = (payload IS NULL)`、`removed = (registration_access_token_digest IS NULL)`）把墓碑形状固定在 schema 层。墓碑仍占住主键，所以注册只需一条 INSERT，主键冲突同时拒绝「已注册」和「已墓碑」。三个读取都带 `NOT removed`。
   - 更新与删除都是单语句条件 UPDATE（`WHERE client_id = $1 AND NOT removed AND revision = $2`），不先查后写、不开显式事务；0 行时复查 `SELECT removed ... WHERE client_id = $1`：无行或已墓碑返回 `storage.ErrNotFound`，活记录返回 `storage.ErrVersionConflict`。
   - nil `AllowedCorsOrigins` 写入前归一为空数组（pgx 会把 nil 切片写成 NULL，而列为 `NOT NULL`）。origin 查找用 `@>` 做 text 等值包含，大小写、端口和尾斜杠都精确匹配，不过滤 `Enabled`，未命中返回非 nil 空切片。
   - 端口不提供按摘要查找，也不提供无过滤枚举，所以摘要列不建索引；注册客户端没有服务端生命周期，本表无过期列、无清理方法。

11. **在线刷新令牌按 session 移除在 Go 侧判定 scope，不落 scope 列（wicket v0.10.0，无新迁移）**：`RefreshTokenStore.RemoveOnlineRefreshTokensBySession` 在一个事务内执行：`SELECT handle, payload FROM refresh_tokens WHERE session_id = $1 FOR UPDATE` 经 `idx_refresh_tokens_session_id` 定位并锁住该会话的行，逐行用 store codec 解码 payload，`AuthorizedScopes` 不含精确元素 `offline_access` 的句柄收集起来，再在同一事务内 `DELETE ... WHERE handle = ANY($1) AND session_id = $2`。约定与理由：
   - **精确元素匹配**：按切片元素逐个比较，`offline_access_extended` 不算离线；scope 为 nil、空切片或 payload 缺 `AuthorizedScopes` 键时视为在线并移除。
   - **不在 SQL 内解码 payload**：PostgreSQL 的 jsonb 拒绝 `\u0000` 转义，而 codec 能写入并读回含 NUL 的字符串（000004 的 NUL 预检即源于此）；SQL 侧 `::jsonb` 判定会让该会话只要有一个这样的令牌就整体报错、永远删不掉。Go 侧判定与 `GetRefreshToken` 用同一个 codec，读得出的令牌就判得出。
   - **不加 scope 列的理由**：新增 `offline_access` 之类的列须迁移回填，且迁移后旧二进制写入的行该列为 NULL，无论 NULL 被当作在线还是离线，都会误删离线令牌或漏删在线令牌；读 payload 对任何时期写入的行都能判定，升级只需 `go get`，无部署顺序要求。代价是逐行解码，但只扫一个会话的令牌（`session_id` 索引定位）。
   - **锁与原子性**：`FOR UPDATE` 阻止并发的 `UpdateRefreshToken` 在判定与删除之间改写 scope；DELETE 带 `session_id = $2` 作双保险。任一行解码失败或数据库出错都回滚事务、不删除任何行并返回错误。判定时刻之后才插入的同会话令牌不在本次范围内，与端口按调用时快照移除的语义一致。
   - 空 `sessionId` 在 Go 侧短路返回 `(0, nil)`、不发 SQL（同例外 8）；`session_id` 为 NULL 的行永不命中。

---

## 五、维护约定

- **零迁移纪律**：本记录只读盘点迁移 SQL 与 store 实现；任何迁移变更（新增表/列/索引）都必须同步更新本记录，指明新对象的对应端口方法，否则 AC-2 的「索引必须对上真实路径」无法核验。
- **三方一致**：记录中的表名/列名/索引名/方法名与 `migrations/*.up.sql`、`store/*.go` 逐字一致，不出现臆造的对象名。
- **盘点核对**：迁移 SQL 中每个对象（表、显式索引、复合主键）在记录中有条目；记录中每个条目在迁移 SQL 与 store 源码中真实存在。
