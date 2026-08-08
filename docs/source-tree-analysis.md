# wicket-pg 源码树分析

> 面向读者的任务：一眼看清每个目录干什么、入口在哪、改动落到哪个文件。

## 一、目录总览

```
wicket-pg/
├── store/                       # ★ 适配器实现（核心交付物）
│   ├── base.go                  #   共享基座 baseStore（pool/logger/codec）
│   ├── codec.go                 #   payloadCodec：版本容器 JSON 编解码
│   ├── errors.go                #   mapDuplicateErr / mapReadErr：SQLSTATE→哨兵
│   ├── authorization_code.go    #   AuthorizationCodeStore（授权码）
│   ├── refresh_token.go         #   RefreshTokenStore（刷新令牌，版本守卫）
│   ├── reference_token.go       #   ReferenceTokenStore（引用令牌）
│   ├── user_consent.go          #   UserConsentStore（用户同意，复合键 upsert）
│   ├── persisted_grant.go       #   PersistedGrantStore（持久化 grant，多维过滤）
│   ├── device_flow.go           #   DeviceFlowStore（设备流，双码）
│   ├── backchannel_authentication_request.go  # BackchannelAuthenticationRequestStore（CIBA）
│   ├── session.go               #   SessionStore（会话，text[] 原子追加）
│   ├── key_records.go           #   KeyRecordStore（密钥记录，部分唯一索引）
│   └── *_test.go                #   单测（基座/编解码/错误映射/ConformsTo）
├── migrations/                  # ★ 迁移入口
│   ├── migrations.go            #   Up / Down（go:embed *.sql，幂等记账）
│   ├── 000001_init.up.sql       #   初始 schema：9 业务表 + 记账表 + 13 索引
│   ├── 000001_init.down.sql
│   ├── 000002_device_flow_user_code.up.sql   # 设备流 user_code 列 + 唯一索引
│   ├── 000002_device_flow_user_code.down.sql
│   ├── 000003_session_subject_id.up.sql      # 会话 subject_id 列 + 索引
│   ├── 000003_session_subject_id.down.sql
│   └── *_audit_test.go          #   时钟审计 / schema 记录三方一致审计
├── tests/
│   ├── lineage/                 # ★ 血缘门禁（CI 第一个 job）
│   │   ├── lineage_test.go      #   三项门禁（版权头/零标注/中性提交）
│   │   └── discrimination_test.go # 判别力探针（故意制造违规验证门禁会红）
│   ├── conformance/             # ★ 契约套件接入点（CI conformance job）
│   │   ├── factory.go           #   隔离 schema 工厂（每次全新空 store）
│   │   ├── grant_fixtures_test.go  # 七个 grant 族套件入口
│   │   ├── session_fixture_test.go # session 套件入口
│   │   ├── keymgmt_fixture_test.go # keymgmt 套件入口
│   │   └── *_test.go            #   套件外的补充断言（独立拷贝语义等）
│   └── e2e/                     #   真实 PostgreSQL e2e（17 个文件，76 用例）
│       ├── migrations_e2e_test.go        # 迁移生命周期/幂等/回滚/search_path
│       ├── schema_record_e2e_test.go     # schema 契约列级断言
│       ├── ci_job_e2e_test.go            # CI 配置静态契约（yaml 解析）
│       ├── readme_quickstart_e2e_test.go # README 快速开始示例可编译性
│       └── {store}_e2e_test.go           # 各 store 语义 e2e
├── internal/lineage/            # 预留（空）
├── docs/                        # 本知识库（index 见 index.md）
├── .github/workflows/ci.yml     # CI：lineage-gates → build、conformance
├── go.mod / go.sum
├── LICENSE                      # MIT
├── PROVENANCE.md                # 独立创作声明
└── README.md                    # 用户快速开始
```

## 二、关键目录职责与入口

| 路径 | 职责 | 入口点 |
|---|---|---|
| `store/` | 九个 store 适配器，共享基座 | `NewXxxStore(pool, logger)` 九个构造器 |
| `migrations/` | schema DDL + 幂等迁移 | `migrations.Up(ctx, pool)` / `Down(ctx, pool)` |
| `tests/lineage/` | 血缘门禁（零第三方依赖） | `go test ./tests/lineage/...` |
| `tests/conformance/` | 契约套件接入（需真实 PG） | `go test ./tests/conformance/...` |
| `tests/e2e/` | 语义/配置 e2e（需真实 PG） | `go test ./tests/e2e/...` |
| `internal/lineage/` | 预留实现落点 | — |

## 三、文件组织规律

- **每 store 一个文件**，命名 `snake_case` 即表名转写；构造器形态全仓统一。
- **测试就近放置**：单测放同包（`store/`、`migrations/` 内 `*_test.go`），需要真实数据库的放 `tests/` 下外部测试包（`package xxx_test` 或 `package e2e_test`）。
- **门禁自证**：`discrimination_test.go` 故意构造违规（缺版权头、带上游标注）验证门禁判别力，本身也是门禁的一部分。
- **迁移与 store 三方一致**：`migrations/schema_record_audit_test.go` 审计「设计记录 ↔ 迁移 SQL ↔ store 源码」一致；`clock_audit_test.go` 审计 SQL 无时钟函数。

## 四、测试总量与分布（123 个测试函数）

| 包 | 数量 |
|---|---|
| `store/` 单测 | 16 |
| `migrations/` 审计 | 5 |
| `tests/conformance/` | 19（含 9 个套件入口 + 补充断言） |
| `tests/e2e/` | 76 |
| `tests/lineage/` | 6（3 门禁 + 3 探针） |

## 五、构建与测试命令

```bash
GOWORK=off go build ./...            # 构建
GOWORK=off go test ./...             # 全量单测（e2e/conformance 无库则跳过）
GOWORK=off go test ./tests/lineage/...     # 血缘门禁（无需数据库）
WICKET_PG_TEST_DATABASE_URL=... GOWORK=off go test ./tests/conformance/...  # 契约套件（需 PG 15）
WICKET_PG_TEST_DATABASE_URL=... GOWORK=off go test ./tests/e2e/...          # e2e（需 PG 15）
```
