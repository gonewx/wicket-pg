# wicket-pg API 契约

> 面向读者的任务：宿主集成时知道要注入什么、能调用什么；维护者知道公共面形态。本仓是 Go 库，无 HTTP API——「API 契约」指三个公开包 `store`、`migrations` 与 `tests/conformance` 的公共面。所有签名以源码为准（本文件 2026-11-21 按 wicket v0.8.0 依赖核对）。

## 一、包与依赖

| 包 | 路径 | 职责 |
|---|---|---|
| `store` | `github.com/gonewx/wicket-pg/store` | 十个 PostgreSQL 适配器 |
| `migrations` | `github.com/gonewx/wicket-pg/migrations` | schema 迁移入口 |
| `tests/conformance` | `github.com/gonewx/wicket-pg/tests/conformance` | 契约套件接入（测试用，宿主一般不需要） |

依赖：`github.com/gonewx/wicket v0.8.0`（三套契约套件 `SuiteVersion` 均为 `3.0.0`；`TakeAuthorizationCode` 与 7 个 ByClient / BySession 方法自 wicket v0.5.0 起进入端口，`RemoveDeviceCodesBySession` 与可选端口 `storage.PushedAuthorizationRequestStore` 自 v0.8.0 起）、`github.com/jackc/pgx/v5 v5.10.0`。

## 二、store 包

### 构造器（十个，形态统一）

```go
func NewAuthorizationCodeStore(pool *pgxpool.Pool, logger *slog.Logger) *AuthorizationCodeStore
func NewRefreshTokenStore(pool *pgxpool.Pool, logger *slog.Logger) *RefreshTokenStore
func NewReferenceTokenStore(pool *pgxpool.Pool, logger *slog.Logger) *ReferenceTokenStore
func NewUserConsentStore(pool *pgxpool.Pool, logger *slog.Logger) *UserConsentStore
func NewPersistedGrantStore(pool *pgxpool.Pool, logger *slog.Logger) *PersistedGrantStore
func NewDeviceFlowStore(pool *pgxpool.Pool, logger *slog.Logger) *DeviceFlowStore
func NewBackchannelAuthenticationRequestStore(pool *pgxpool.Pool, logger *slog.Logger) *BackchannelAuthenticationRequestStore
func NewPushedAuthorizationRequestStore(pool *pgxpool.Pool, logger *slog.Logger) *PushedAuthorizationRequestStore
func NewSessionStore(pool *pgxpool.Pool, logger *slog.Logger) *SessionStore
func NewKeyRecordStore(pool *pgxpool.Pool, logger *slog.Logger) *KeyRecordStore
```

- `pool` 为宿主所有：构造器不创建、不关闭，store 不实现 `io.Closer`。
- `logger` 可 nil，回退 `slog.Default()`。

### 授权码（`storage.AuthorizationCodeStore`）

```go
func (s *AuthorizationCodeStore) StoreAuthorizationCode(ctx context.Context, handle string, code *models.AuthorizationCode) error  // insert-only
func (s *AuthorizationCodeStore) GetAuthorizationCode(ctx context.Context, handle string) (*models.AuthorizationCode, error)
func (s *AuthorizationCodeStore) RemoveAuthorizationCode(ctx context.Context, handle string) error
func (s *AuthorizationCodeStore) TakeAuthorizationCode(ctx context.Context, handle string) (*models.AuthorizationCode, error)  // 原子取出（AD-12），未命中返 ErrNotFound
func (s *AuthorizationCodeStore) RemoveAuthorizationCodesByClient(ctx context.Context, clientId string) (int, error)  // 按条件移除（AD-13），返回删除条数
func (s *AuthorizationCodeStore) RemoveAuthorizationCodesBySession(ctx context.Context, sessionId string) (int, error)
func (s *AuthorizationCodeStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *AuthorizationCodeStore) ConformsTo() string
```

### 刷新令牌（`storage.RefreshTokenStore`，版本守卫）

```go
func (s *RefreshTokenStore) StoreRefreshToken(ctx context.Context, handle string, token *models.RefreshToken) error  // insert-only
func (s *RefreshTokenStore) GetRefreshToken(ctx context.Context, handle string) (*models.RefreshToken, error)
func (s *RefreshTokenStore) UpdateRefreshToken(ctx context.Context, handle string, token *models.RefreshToken, expectedVersion int) error  // 版本冲突返 ErrVersionConflict
func (s *RefreshTokenStore) RemoveRefreshToken(ctx context.Context, handle string) error
func (s *RefreshTokenStore) RemoveRefreshTokensByClient(ctx context.Context, clientId string) (int, error)  // AD-13
func (s *RefreshTokenStore) RemoveRefreshTokensBySession(ctx context.Context, sessionId string) (int, error)
func (s *RefreshTokenStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *RefreshTokenStore) ConformsTo() string
```

### 引用令牌（`storage.ReferenceTokenStore`）

```go
func (s *ReferenceTokenStore) StoreReferenceToken(ctx context.Context, handle string, token *models.Token) error  // insert-only
func (s *ReferenceTokenStore) GetReferenceToken(ctx context.Context, handle string) (*models.Token, error)
func (s *ReferenceTokenStore) RemoveReferenceToken(ctx context.Context, handle string) error
func (s *ReferenceTokenStore) RemoveReferenceTokens(ctx context.Context, subjectId, clientId string) error  // 按 subject + client 对吊销（payload 扫描）
func (s *ReferenceTokenStore) RemoveReferenceTokensByClient(ctx context.Context, clientId string) (int, error)  // AD-13
func (s *ReferenceTokenStore) RemoveReferenceTokensBySession(ctx context.Context, sessionId string) (int, error)  // session 取 Token.SessionId()（sid claim）
func (s *ReferenceTokenStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *ReferenceTokenStore) ConformsTo() string
```

### 用户同意（`storage.UserConsentStore`，自然键 upsert）

```go
func (s *UserConsentStore) StoreUserConsent(ctx context.Context, consent *models.Consent) error  // ON CONFLICT (subject_id, client_id) upsert
func (s *UserConsentStore) GetUserConsent(ctx context.Context, subjectId, clientId string) (*models.Consent, error)
func (s *UserConsentStore) GetAllUserConsents(ctx context.Context, subjectId string) ([]*models.Consent, error)
func (s *UserConsentStore) RemoveUserConsent(ctx context.Context, subjectId, clientId string) error
func (s *UserConsentStore) RemoveUserConsentsByClient(ctx context.Context, clientId string) (int, error)  // AD-13
func (s *UserConsentStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *UserConsentStore) ConformsTo() string
```

### 持久化 grant（`storage.PersistedGrantStore`，四维过滤）

```go
func (s *PersistedGrantStore) Store(ctx context.Context, grant *models.PersistedGrant) error  // ON CONFLICT (key) upsert
func (s *PersistedGrantStore) Get(ctx context.Context, key string) (*models.PersistedGrant, error)
func (s *PersistedGrantStore) GetAll(ctx context.Context, filter *storage.PersistedGrantFilter) ([]*models.PersistedGrant, error)  // subject/session/client/type 过滤
func (s *PersistedGrantStore) Remove(ctx context.Context, key string) error
func (s *PersistedGrantStore) RemoveAll(ctx context.Context, filter *storage.PersistedGrantFilter) error
func (s *PersistedGrantStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *PersistedGrantStore) ConformsTo() string
```

### 设备流（`storage.DeviceFlowStore`，双码）

```go
func (s *DeviceFlowStore) StoreDeviceAuthorization(ctx context.Context, deviceCode string, userCode string, data *models.DeviceCode) error  // insert-only，双码重复拒绝
func (s *DeviceFlowStore) FindByDeviceCode(ctx context.Context, deviceCode string) (*models.DeviceCode, error)
func (s *DeviceFlowStore) FindByUserCode(ctx context.Context, userCode string) (*models.DeviceCode, error)
func (s *DeviceFlowStore) UpdateByUserCode(ctx context.Context, userCode string, data *models.DeviceCode) error
func (s *DeviceFlowStore) RemoveByDeviceCode(ctx context.Context, deviceCode string) error
func (s *DeviceFlowStore) RemoveDeviceCodesBySession(ctx context.Context, sessionId string) (int, error)  // AD-13；一行承载双码，计数一次
func (s *DeviceFlowStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *DeviceFlowStore) ConformsTo() string
```

### 后通道认证请求（`storage.BackchannelAuthenticationRequestStore`，CIBA）

```go
func (s *BackchannelAuthenticationRequestStore) StoreBackchannelAuthenticationRequest(ctx context.Context, requestID string, data *models.BackchannelAuthenticationRequest) error  // insert-only
func (s *BackchannelAuthenticationRequestStore) FindBackchannelAuthenticationRequest(ctx context.Context, requestID string) (*models.BackchannelAuthenticationRequest, error)
func (s *BackchannelAuthenticationRequestStore) UpdateBackchannelAuthenticationRequest(ctx context.Context, requestID string, data *models.BackchannelAuthenticationRequest) error
func (s *BackchannelAuthenticationRequestStore) RemoveBackchannelAuthenticationRequest(ctx context.Context, requestID string) error
func (s *BackchannelAuthenticationRequestStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *BackchannelAuthenticationRequestStore) ConformsTo() string
```

### 推送授权请求（`storage.PushedAuthorizationRequestStore`，PAR，可选端口）

宿主开启 wicket 的 PAR 特性时须经 `ServerConfig.SetPushedAuthorizationRequestStore`（或 `SetPushedAuthorizationRequestStoreProvider`）显式注入此端口；未开启 PAR 的宿主不需要构造它。

```go
func (s *PushedAuthorizationRequestStore) StorePushedAuthorizationRequest(ctx context.Context, key string, request *models.PushedAuthorizationRequest) error  // insert-only
func (s *PushedAuthorizationRequestStore) GetPushedAuthorizationRequest(ctx context.Context, key string) (*models.PushedAuthorizationRequest, error)   // 读取不消费
func (s *PushedAuthorizationRequestStore) TakePushedAuthorizationRequest(ctx context.Context, key string) (*models.PushedAuthorizationRequest, error)  // 原子取出（AD-12），未命中返 ErrNotFound
func (s *PushedAuthorizationRequestStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)  // 只看 RetentionExpiration
func (s *PushedAuthorizationRequestStore) ConformsTo() string
```

- `key` 只来自 `storage.PushedAuthorizationRequestKey`，store 不生成、不拼接键，也看不到 request_uri。
- 读取与取出均不判断过期；`RemoveExpired` 删除 `RetentionExpiration` 严格早于 cutoff 的记录，`EntryExpiration` 已过但 `RetentionExpiration` 未过的记录保留；零值 `RetentionExpiration` 被任何晚于零时刻的 cutoff 回收。
- 模型只从 payload 解码；`client_id` 与三个时间列只写不读（见 [schema-design-record.md](./schema-design-record.md) 例外 9）。

### 会话（`session.Store`）

```go
func (s *SessionStore) GetSession(ctx context.Context, sessionID string) (*session.Record, error)
func (s *SessionStore) CreateSession(ctx context.Context, rec *session.Record) error
func (s *SessionStore) UpdateSession(ctx context.Context, rec *session.Record) error
func (s *SessionStore) DeleteSession(ctx context.Context, sessionID string) error
func (s *SessionStore) GetSessionsBySubjectID(ctx context.Context, subjectID string) ([]*session.Record, error)
func (s *SessionStore) DeleteSessionsBySubjectID(ctx context.Context, subjectID string) (int, error)
func (s *SessionStore) AddClientID(ctx context.Context, sessionID, clientID string) error  // text[] 原子追加去重
func (s *SessionStore) DeleteExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *SessionStore) ConformsTo() string
```

### 密钥记录（`keymgmt.RecordStore`，版本守卫）

```go
func (s *KeyRecordStore) Get(ctx context.Context, handle string) (*keymgmt.Record, error)
func (s *KeyRecordStore) Create(ctx context.Context, record *keymgmt.Record) error  // insert-only，双唯一约束
func (s *KeyRecordStore) Update(ctx context.Context, record *keymgmt.Record, expectedVersion uint64) error  // 版本冲突返 ErrVersionConflict
func (s *KeyRecordStore) Delete(ctx context.Context, handle string) error
func (s *KeyRecordStore) List(ctx context.Context) ([]*keymgmt.Record, error)
func (s *KeyRecordStore) ConformsTo() string  // 唯一无清理方法（密钥退役由核心协调）
```

## 三、migrations 包

```go
func Up(ctx context.Context, pool *pgxpool.Pool) error    // 按版本顺序应用全部未应用迁移，幂等
func Down(ctx context.Context, pool *pgxpool.Pool) error  // 逆序回滚已应用迁移，未迁移库为 no-op
```

- **`Down` 是全量拆除**：逆序回滚全部已应用迁移，包括 `000001`（DROP 全部表），会删除所有表和数据；不能用来单独撤销 000004，生产库不要调用。回退与手工重做 000004 见 [deployment-guide.md](./deployment-guide.md) 第六节。

- 全部 SQL 经 `go:embed` 内嵌，无需宿主安装 CLI 或迁移工具。
- 每文件一个事务、单连接执行（保持 search_path）；记账于 `schema_migrations`。

## 四、错误语义（哨兵全部来自 wicket，本仓不重新定义）

| 场景 | 返回 | 来源 |
|---|---|---|
| 记录不存在（单点读） | `storage.ErrNotFound`（等） | `storage` 包 |
| 句柄/键重复（insert-only） | `storage.ErrDuplicateHandle` / `keymgmt.ErrDuplicateKey` | 端口族 |
| 版本冲突（expectedVersion） | `storage.ErrVersionConflict` | `storage` 包 |
| 基础设施故障 | `fmt.Errorf("%w", err)` 包装 | 本仓 |

契约细节：

- 单记录读**不返 `(nil, nil)`**。
- 列表方法空结果返**空非 nil slice**（`[]*Xxx{}`），不返哨兵错误。
- 错误判定用 `errors.Is`（哨兵是 `%w` 链上的一环）。
- **原子取出（AD-12）**：`TakeAuthorizationCode` 用单语句 `DELETE FROM authorization_codes WHERE handle = $1 RETURNING payload` 完成定位、删除与返回，不开显式事务、不先 SELECT 再 DELETE；0 行返回 `storage.ErrNotFound`；不判断过期；并发取同一句柄只有一个成功。DELETE 已提交后 payload 解码失败时返回包装后的解码错误（不是 `ErrNotFound`），码不恢复；ctx 取消或连接中断等传输失败同理——任何非 `ErrNotFound` 错误都可能意味着码已被消费，此时重试会得到 `ErrNotFound`。
- **PAR 原子取出（AD-12）**：`TakePushedAuthorizationRequest` 用单语句 `DELETE FROM pushed_authorization_requests WHERE key = $1 RETURNING payload`，其余语义同上：0 行返回 `storage.ErrNotFound`；不判断过期；并发取同一键只有一个成功；DELETE 已提交后的解码或传输错误都可能意味着请求已被消费，请求不恢复。
- **按条件移除（AD-13）**：8 个 `Remove*ByClient` / `*BySession` 方法在参数为空串时直接返回 `(0, nil)`、不发 SQL（`user_consents.client_id` 为 `NOT NULL` 列，空 ClientId 以空串存储，空参数若下发会误删）；否则执行单语句 `DELETE ... WHERE <定位列> = $1`，返回 `RowsAffected`，零匹配返回 `(0, nil)`。定位列由迁移 000004 增加并回填存量行，见 [schema-design-record.md](./schema-design-record.md) 例外 7、8。`PersistedGrantStore` 无新增方法，按 client / session 移除沿用 `RemoveAll` 过滤。

## 五、合规凭证（宿主装配期校验）

十个 store 均暴露 `ConformsTo() string`，返回所属套件版本：

| Store | 值 |
|---|---|
| 八个 grant 族 store（含 `PushedAuthorizationRequestStore`） | `storagetest.SuiteVersion`（`"3.0.0"`） |
| `SessionStore` | `sessiontest.SuiteVersion`（`"3.0.0"`） |
| `KeyRecordStore` | `keymgmttest.SuiteVersion`（`"3.0.0"`） |

返回值直接取自套件常量，不硬编码；wicket v0.8.0 下三套件同步为 `3.0.0`（wicket 自 v0.3.0 起让三套件版本同步递进）。宿主在装配期分别与对应套件的 `SuiteVersion` 比对。注意：Go 在一次构建中只选定一个 wicket 版本，store 返回的常量与宿主比对用的常量来自同一个包，因此该比对在同一构建内恒成立；返回值的可信度来自本仓的交付纪律——依赖升级、方法补齐与套件全绿原子交付（AD-11 修订），声称的版本即本仓已通过的套件版本。

## 六、宿主集成最小示例（完整见 README.md）

```go
pool, _ := pgxpool.New(ctx, dsn)          // 宿主所有，宿主负责 Close
_ = migrations.Up(ctx, pool)              // 幂等

logger := slog.Default()
authCodeStore := store.NewAuthorizationCodeStore(pool, logger)
// ... 其余九个构造器（PushedAuthorizationRequestStore 仅在开启 PAR 时需要）
if authCodeStore.ConformsTo() != storagetest.SuiteVersion { /* 策略由宿主决定 */ }
```

## 七、变化时的维护责任

- 任何公共签名变更：同步更新本文件与 README 示例。
- 任何迁移变更：同步更新 `docs/schema-design-record.md`（审计测试强制三方一致）。
- 新增 store 方法：保持六要素契约（ctx 首参、哨兵语义、清理形态、版本守卫、无时钟 SQL、不返 `(nil,nil)`）。
