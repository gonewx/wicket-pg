# wicket-pg API 契约

> 面向读者的任务：宿主集成时知道要注入什么、能调用什么；维护者知道公共面形态。本仓是 Go 库，无 HTTP API——「API 契约」指三个公开包 `store`、`migrations` 与 `tests/conformance` 的公共面。所有签名以源码为准（本文件 2026-08-08 核对）。

## 一、包与依赖

| 包 | 路径 | 职责 |
|---|---|---|
| `store` | `github.com/gonewx/wicket-pg/store` | 九个 PostgreSQL 适配器 |
| `migrations` | `github.com/gonewx/wicket-pg/migrations` | schema 迁移入口 |
| `tests/conformance` | `github.com/gonewx/wicket-pg/tests/conformance` | 契约套件接入（测试用，宿主一般不需要） |

依赖：`github.com/gonewx/wicket v0.1.2`（端口、哨兵错误、套件）、`github.com/jackc/pgx/v5 v5.10.0`。

## 二、store 包

### 构造器（九个，形态统一）

```go
func NewAuthorizationCodeStore(pool *pgxpool.Pool, logger *slog.Logger) *AuthorizationCodeStore
func NewRefreshTokenStore(pool *pgxpool.Pool, logger *slog.Logger) *RefreshTokenStore
func NewReferenceTokenStore(pool *pgxpool.Pool, logger *slog.Logger) *ReferenceTokenStore
func NewUserConsentStore(pool *pgxpool.Pool, logger *slog.Logger) *UserConsentStore
func NewPersistedGrantStore(pool *pgxpool.Pool, logger *slog.Logger) *PersistedGrantStore
func NewDeviceFlowStore(pool *pgxpool.Pool, logger *slog.Logger) *DeviceFlowStore
func NewBackchannelAuthenticationRequestStore(pool *pgxpool.Pool, logger *slog.Logger) *BackchannelAuthenticationRequestStore
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
func (s *AuthorizationCodeStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *AuthorizationCodeStore) ConformsTo() string
```

### 刷新令牌（`storage.RefreshTokenStore`，版本守卫）

```go
func (s *RefreshTokenStore) StoreRefreshToken(ctx context.Context, handle string, token *models.RefreshToken) error  // insert-only
func (s *RefreshTokenStore) GetRefreshToken(ctx context.Context, handle string) (*models.RefreshToken, error)
func (s *RefreshTokenStore) UpdateRefreshToken(ctx context.Context, handle string, token *models.RefreshToken, expectedVersion int) error  // 版本冲突返 ErrVersionConflict
func (s *RefreshTokenStore) RemoveRefreshToken(ctx context.Context, handle string) error
func (s *RefreshTokenStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *RefreshTokenStore) ConformsTo() string
```

### 引用令牌（`storage.ReferenceTokenStore`）

```go
func (s *ReferenceTokenStore) StoreReferenceToken(ctx context.Context, handle string, token *models.Token) error  // insert-only
func (s *ReferenceTokenStore) GetReferenceToken(ctx context.Context, handle string) (*models.Token, error)
func (s *ReferenceTokenStore) RemoveReferenceToken(ctx context.Context, handle string) error
func (s *ReferenceTokenStore) RemoveReferenceTokens(ctx context.Context, subjectId, clientId string) error  // 批量吊销（payload 扫描）
func (s *ReferenceTokenStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error)
func (s *ReferenceTokenStore) ConformsTo() string
```

### 用户同意（`storage.UserConsentStore`，自然键 upsert）

```go
func (s *UserConsentStore) StoreUserConsent(ctx context.Context, consent *models.Consent) error  // ON CONFLICT (subject_id, client_id) upsert
func (s *UserConsentStore) GetUserConsent(ctx context.Context, subjectId, clientId string) (*models.Consent, error)
func (s *UserConsentStore) GetAllUserConsents(ctx context.Context, subjectId string) ([]*models.Consent, error)
func (s *UserConsentStore) RemoveUserConsent(ctx context.Context, subjectId, clientId string) error
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

## 五、合规凭证（宿主装配期校验）

九个 store 均暴露 `ConformsTo() string`，返回所属套件版本：

| Store | 值 |
|---|---|
| 七个 grant 族 store | `storagetest.SuiteVersion`（`"1.0.0"`） |
| `SessionStore` | `sessiontest.SuiteVersion` |
| `KeyRecordStore` | `keymgmttest.SuiteVersion` |

宿主在装配期分别调用比对；套件独立升级时返回值随之变化（已知风险：仅靠版本串相等校验，任一套件独立升级即静默失效——deferred-work 留档）。

## 六、宿主集成最小示例（完整见 README.md）

```go
pool, _ := pgxpool.New(ctx, dsn)          // 宿主所有，宿主负责 Close
_ = migrations.Up(ctx, pool)              // 幂等

logger := slog.Default()
authCodeStore := store.NewAuthorizationCodeStore(pool, logger)
// ... 其余八个构造器
if authCodeStore.ConformsTo() != storagetest.SuiteVersion { /* 策略由宿主决定 */ }
```

## 七、变化时的维护责任

- 任何公共签名变更：同步更新本文件与 README 示例。
- 任何迁移变更：同步更新 `docs/schema-design-record.md`（审计测试强制三方一致）。
- 新增 store 方法：保持六要素契约（ctx 首参、哨兵语义、清理形态、版本守卫、无时钟 SQL、不返 `(nil,nil)`）。
