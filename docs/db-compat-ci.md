# 数据库兼容 CI（db-compat）· 用历史版本自己的库验证升级

> **总原则**：升级测试的对象必须是**真实历史库**，不是「我以为的历史库」。任何一代的表结构，
> 只能由那一代的 `models.Init` 亲手建出来；手写的旧 DDL 只能证明「按那份 DDL 能升级」，
> 证明不了「按真实旧库能升级」。
>
> 本文结论标注出处：`[码]` = 读源码/工作流得到，`[测]` = 本机实测（2026-10-21，
> Windows + Git Bash + go1.26.6），`[推]` = 推理，未在真实 runner 上实测。

---

## 一、为什么不用「手写一份旧 DDL」（[码]）

`tools/versiondb/main.go:3-6` 把理由写在文件头上，两条：

1. `testdata/upstream_schema.sql` 那份快照只是 **master 这一代**的结构；
2. 更老的版本形态完全不同——**v0.1.0 的 `models` 表还带着 `io_log` 列、连 `configs` 表都还没有**。
   手写 DDL 永远只能证明「按我以为的旧结构能升级」。

所以把方向换过来：**让那一代自己的代码把库建出来**。`tools/versiondb/main.go:12-14,71-82` 的做法
（每一步都不碰当前工作区，也就不受当前 checkout 状态影响）：

| # | 动作 | 为什么 |
|---|---|---|
| 1 | `git archive <tag>` 导出该 tag 源码到临时目录 | 不依赖工作区是否干净 |
| 2 | 塞一个探针包进去，探针只调用**它自己**的 `models.Init` | 探针不 import 当前代码，新结构混不进旧库 |
| 3 | 在那个旧模块里 `go run` 探针（`GOWORK=off`、`GOFLAGS=-mod=mod`，main.go:117-119） | 旧模块用自己的 go.mod，不被外层 go.work 拽走 |
| 4 | 留下建好的 `.db` | 交给当前代码升级 |

旧代际的 `Init` 签名不一样（`tools/versiondb/main.go:84-85`：v0.5.8 及更早是
`Init(path)`，之后是 `Init(ctx, path)`），所以 `probeSource`（main.go:143-153）按签名挑探针模板——
**为了测旧版本不需要改旧版本源码**。签名不认识时直接报错：
`models/init.go 里没找到已知的 Init 签名，需要给 tools/versiondb 补一个模板`。

升级侧的 6 条契约写在 `models/compat_db_test.go:22-42`（`TestUpgradeFromReleaseDB`，line 43）：
integrity_check、行/值保留、老列不删、当前列/索引补齐、二次 Init 幂等、升级后可写。
未设置 `LLMIO_COMPAT_DB` 时该测试 **skip**（line 46）——所以 CI 里必须显式传环境变量，
传漏了它就是"永远绿"。

---

## 二、工作流结构（[码]）

`.github/workflows/db-compat.yml`，两个 job。

| 触发 | 条件 |
|---|---|
| `pull_request` | 改动落在 `models/**`、`tools/versiondb/**`、`go.mod`、`go.sum`、本工作流自身 |
| `workflow_dispatch` | 布尔输入 `all`：`true` = 跑全部 151 个 tag，默认只跑代表（50 个） |
| `schedule` | 每周一 03:17 UTC 兜底（代码没动也可能因依赖抖动而变红） |

`permissions: contents: read`、`concurrency: <workflow>-<ref>` + `cancel-in-progress`，
写法与 `test.yml` 一致；矩阵几十个 job，连推两次不能让两轮叠着跑。

**discover**（`ubuntu-latest`，10 分钟上限）：`fetch-depth: 0` 克隆 →
`git fetch --force --prune --tags origin` 兜底 → 分组，输出 `matrix` / `count` / `total`。

**compat**（`ubuntu-latest`，20 分钟上限，`fail-fast: false`，`max-parallel: 4`，
`name: compat / <ref>`）：每个代表 tag 一个 job，三步——生成该版本的库 → 用当前代码升级并断言
→ 失败时上传现场（库文件 + 完整测试日志，保留 7 天）。

为什么必须 `fetch-depth: 0`：compat job 里 `git archive <tag>` 需要 tag 对象和它指向的提交，
浅克隆里两者都不保证在。`test.yml` 那份 checkout 不带 `fetch-depth`，**不能照抄**。

两处「不这么写就永远绿」的细节：

- 生成库前 `git fetch --force origin refs/tags/$REF:refs/tags/$REF`：tag 不在本地时 `git archive` 会扑空；
- 测试用 `set -o pipefail` 再 `| tee`：否则退出码取的是 tee 的，测试失败传不出去。

---

## 三、矩阵怎么分组（[码][测]）

151 个 tag 全跑没必要：大量 tag 的 `models/` 目录树**完全一样**，建出来的库也完全一样。

分组键 = 三个 OID 都相同：

```
git rev-parse <tag>:models   # models/ 目录树哈希 —— 决定建出什么表
git rev-parse <tag>:go.mod   # 依赖版本（GORM / sqlite 驱动）—— 决定 AutoMigrate 行为
git rev-parse <tag>:go.sum   # 同上；老 tag 可能没有，用 none 占位
```

键相同即同组；组内只留**版本号最高**的 tag 当代表（按 `sort -V` 排序，不是字典序：
`v0.8.9 < v0.8.25`）。

实测（[测]，`git cat-file --batch-check` 与真实脚本两路对过）：

| 分组键 | 组数 |
|---|---|
| 只用 `models/` 树 | 44 |
| `models/` 树 + `go.mod` | **50** |
| `models/` 树 + `go.mod` + `go.sum` | **50** |

即：`go.sum` 不额外拆组，`go.mod` 多拆 6 组（同一份 models 代码、不同依赖版本）。
CI 实际跑 **50 个 job**（tag 总数 151）。

同组等价性举例（[测]）：v0.1.0 组的 8 个 tag
`v0.1.0 v0.1.1 v0.1.2 v0.1.3 v0.1.4 v0.2.0 v0.2.1 v0.2.2` 共享同一组键，代表 v0.2.2；
`git diff --name-only v0.1.0 v0.2.2` 共 14 个文件，**没有一个落在 `models/` 下**——
所以用 v0.2.2 代表 v0.1.0 的库是合法的。

当前 50 个代表（[测]，`sort -V` 序）：

```
v0.2.2 v0.2.3 v0.3.0 v0.3.2 v0.3.4 v0.3.5 v0.3.7 v0.3.8 v0.4.6 v0.4.12 v0.4.15
v0.5.2 v0.5.6 v0.5.8 v0.5.11 v0.5.12 v0.5.17 v0.5.20 v0.5.22 v0.5.30 v0.5.31 v0.5.32
v0.6.3 v0.6.5 v0.6.7 v0.6.19 v0.6.22 v0.6.24 v0.6.25 v0.6.27 v0.6.30 v0.6.33
v0.7.8 v0.7.9 v0.7.10
v0.8.3 v0.8.4 v0.8.6 v0.8.7 v0.8.8 v0.8.12 v0.8.15 v0.8.16 v0.8.19 v0.8.20 v0.8.22 v0.8.24
v0.9.3 v0.9.5 v0.9.7
```

`workflow_dispatch` 勾 `all` 时跳过分组直接跑 151 个——分组逻辑被改坏时的兜底。

本机全量实测（[测] 2026-10-21，Windows + go1.26.6）：151 个 tag **逐个**建库 + 用当前代码升级，
一个不跳（与 `workflow_dispatch` 勾 `all` 时同款跑法）：

| 指标 | 结果 |
|---|---|
| 结果 | **151/151 全绿**，0 失败 |
| 建库耗时 | 2.5–4.3 s / 版本（含旧模块首次编译，依赖走本机模块缓存） |
| 升级断言耗时 | 2.2–4.8 s / 版本 |
| 旧库形态 | 4 张表 ×34、5 张表 ×32、6 张表 ×1、7 张表 ×55、8 张表 ×19、10 张表 ×10 个 tag；从 4 张表到 10 张表全部收敛到当前 10 张表 |
| 典型输出 | `[v0.2.2] 旧库 4 张表 / 8 行 → 升级后 10 张表 / 10 行`；`[v0.9.7] 旧库 10 张表 / 22 行 → 升级后 10 张表 / 22 行` |

为什么还要跑 151 个：下面的 50 个代表是按「同组等价」推出来的（[推]），而 CI 默认只跑这 50 个。
把 151 个全跑一遍，是为了证明代表法没漏——结果 151/151 与只跑代表的 51/51 一致，
没有哪个只在被跳过的 tag 上才炸的升级路径。

本地看分组（不用起 CI）：把 YAML 里 `discover` 的 `run` 段落原样存成 `discover.sh`，
`bash discover.sh` 会打印 `151 个 tag → 50 组` 和 `matrix={...}`；`ALL=true bash discover.sh`
走全量分支。脚本读 `${GITHUB_OUTPUT:-}` / `${GITHUB_STEP_SUMMARY:-/dev/null}`，
本地没这两个变量只是不写文件。

---

## 四、本地复现单个版本（[测]）

两条命令（`models/compat_db_test.go:39-42` 里写的也是这两条）：

```bash
# 1) 用那个版本的代码建库
go run ./tools/versiondb -ref v0.2.2 -out /tmp/db-compat/v0.2.2.db

# 2) 用当前代码升级并断言
LLMIO_COMPAT_DB=/tmp/db-compat/v0.2.2.db LLMIO_COMPAT_TAG=v0.2.2 \
  go test ./models -run TestUpgradeFromReleaseDB -count=1 -v
```

PowerShell 版（本仓库主要在 Windows 上开发）：

```powershell
go run ./tools/versiondb -ref v0.2.2 -out "$env:TEMP\db-compat\v0.2.2.db"
$env:LLMIO_COMPAT_DB = "$env:TEMP\db-compat\v0.2.2.db"
$env:LLMIO_COMPAT_TAG = "v0.2.2"
go test ./models -run TestUpgradeFromReleaseDB -count=1 -v
```

实测（[测] 2026-10-21，Windows，go1.26.6）：

```
versiondb: v0.2.2 → .../db-compat/v0.2.2.db（49152 字节，2.066s）
--- PASS: TestUpgradeFromReleaseDB (0.29s)
    compat_db_test.go:120: [v0.2.2] 旧库 4 张表 / 8 行 → 升级后 10 张表 / 10 行；…
ok  github.com/atopos31/llmio/models  1.477s
```

排障开关（`tools/versiondb/main.go:32-36`）：`-sql` 把该版本建出的 `sqlite_master` DDL 打到
stdout（看"那一代到底建了什么"，不用打开库）；`-keep -work <dir>` 保留临时目录（看探针源码、
旧模块的 go.mod、确认真正跑的是哪段代码）。

**没有 `LLMIO_COMPAT_DB` 时这个测试 skip**，本地忘了传就是"什么都没跑但 ok"。

---

## 五、某个版本红了：真 bug 还是测试过严（[码]）

先看**哪一步**红，这两类完全不同：

| 红的步骤 | 含义 | 是升级问题吗 |
|---|---|---|
| 生成该版本自己的库 | 那一代代码在 runner 上编译/建库失败 | **不是**，与升级无关 |
| 用当前代码升级并断言 | 升级路径真出问题，或断言过严 | 是，看下面的断言 |

生成步骤常见原因：旧 tag 的依赖已从模块代理消失、`go.sum` 缺条目且 `-mod=mod` 补不上、
探针签名不认识（报"需要给 tools/versiondb 补一个模板"）、该 tag 不在本地（`git archive` 扑空）。
这些红跟当前分支的改动无关，要修的是生成器/环境，不是 `models/`。

升级步骤按断言认领（错误文案 → 结论；行号指 `models/compat_db_test.go`）：

| 断言 | 报错文案 | 结论 |
|---|---|---|
| integrity_check（:449） | `升级后库文件不完整：…` | **真 bug**：升级把库写坏了 |
| 行/值保留（:477、:497） | `表 %s 的行 rowid=%d 在升级后不见了` / `…列 %s 的值被改写` | **真 bug**：丢行 / 改值 |
| 老列不删（:471） | `升级删掉了列 %s.%s（老列必须留在表上）` | **真 bug**：AutoMigrate 把老列删了 |
| 表不消失（:461） | `升级后表 %s 消失了` | **真 bug** |
| 结构收敛（:517） | `升级后缺表 %s` / `升级后表 %s 缺列 %s（AutoMigrate 没补上）` / `升级后缺索引 %s` | **真 bug**：新结构没建出来 |
| 类型亲和性（:532） | `列 %s 的类型亲和性不对` | 先确认是否**有意**改类型；否则真 bug |
| 二次 Init 幂等（:90） | `二次启动改动了数据（Init 必须幂等）` | **真 bug** |
| 升级后可写（:93-111） | `升级后新列读回不一致` / `升级后写入 ChatLog 失败` | **真 bug** |

测试**已经刻意放宽**的地方（这些红不算"测试过严"，别往这边推）：

- 列类型只比**亲和性**（`compatAffinity`，:237）：`varchar(255)` 与 `text` 都是 TEXT，
  `integer` 与 `bigint` 都是 INTEGER，同类不算红；
- `NULL` 与空串**允许**被兼容回填改写（:485-489，`status NULL → true`、`strategy '' → 默认值` 这类）；
- `updated_at` **豁免**（:490-493）：回填走 GORM `Update`，会给被回填行盖新时间戳，那是记账不是损坏；
- 老库独有的"化石列"只统计、只打日志（`compatLegacyOnlyColumns`，:553），不断言、不因它红。

所以"测试过严"只剩两种合法边界：

1. **契约本身变了**：某次升级**有意**改写某个非空历史值（不是 NULL/空串、不是 updated_at）。
   那要维护者明确决定改契约，并把决定写进 `models/compat_db_test.go:33` 的注释——
   不能因为它红就把断言删了。
2. **塞数据阶段失败**：`compatSeedRows`（:261）按列的**类型**挑值（不按列名），只对 `deleted_at` 特判，
   报错文案是 `往历史版本库的 %s 塞第 %d 行失败`。这三条是**测试侧**的坑，不是升级 bug，
   都已经踩过并写进了代码注释（:261-270、:313-323）：

   - `INTEGER` 取 `100+row`（:316-317）：`0` 会被 `ensureModelDisplayOrder` 当成"未设置"改写；
   - `NUMERIC` 只能取 `0/1`（:318-323）：老版本把 bool 列（`status`/`breaker`/`io_log`…）声明成
     `numeric`，塞文本或 `100` 会让当前代码的 bool 字段扫描失败——
     `sql: Scan error on column index 10, name "breaker": sql/driver: couldn't convert "…" into type bool`。
     那是"塞进去的数据不像旧版本自己写的"，不是升级路径的问题（实测于 v0.6.7 起的代际）；
   - `BLOB` 必须走 `database/sql`（:263-267）：GORM 的 `Exec` 会把 `[]byte` 参数当成一组值展开，
     `VALUES (?, ?)` 被写成 `VALUES (98,108,111,…)`，SQLite 报 `SQL logic error: 25 values for 2 columns`。
     BLOB 列只在 v0.8.24 及以后才出现（`blocks`/`block_groups`），所以这个坑直到那一代才炸。

真红了的排查顺序：① 本地按 §四 复现；② `PRAGMA integrity_check` + 对比 `sqlite_master` 与同代码
新建库的 DDL；③ 再判断是升级逻辑还是断言。

---

## 六、新增一个发布版本要做什么（通常什么都不用）

打好 tag 推上去即可，下一次 PR（动了 `models/**` 等路径）或每周定时任务会自动带上它：

- `models/` 目录树没变 → 落进已有组，**不增加 job**；
- `models/` 或 `go.mod` 变了 → 自动成为新组的代表，矩阵多一个 job。

只有三种情况要动手：

1. **`models.Init` 签名又变了**（`tools/versiondb/main.go:143-153` 不认识）→ 给 `probeSource` 补一个模板，
   并跑 `go test ./tools/versiondb`（那里钉住了三个已知签名、解包越界防护、`tail` 截断）；
2. **想立刻验证新 tag**：`workflow_dispatch` 勾 `all=true` 跑全量；
3. **历史 tag 已经编译不出来**（依赖下架）→ 目前没有跳过名单，见 §七。

---

## 七、已知边界（[码][推]）

- **分组键有意不含 `consts/` 与 `pkg/sqlitex/`**：`models/init.go` 会 import 这两个包，但它们不决定表结构。
  建库用的是该 tag 自己的 `models.Init`，而 `AutoMigrate` 的表结构只由 `models/` 里的 struct 定义决定；
  `consts/` 只影响兼容回填写进去的**取值**，`pkg/sqlitex/` 只影响连接层（受保护驱动、`_txlock=immediate`）。
  升级侧的代码永远是当前分支，与 tag 分组无关。所以同组内这两个包不同、库结构仍然一样。
  例外：哪天 `models/` 之外出现建表代码（例如把迁移拆到 `migrations/`），必须把那条路径加进分组键。
- **没有跳过名单**：某个历史 tag 因依赖下架编译不出来时，这条工作流会一直红。这是有意保留的——
  红本身是信号（要么修 GOPROXY/缓存，要么确认该依赖真的取不到了），加白名单只会把问题藏起来。
- **fork PR 不受影响**：`checkout` 的 `origin` 永远指向**运行本工作流的仓库**（fork PR 时是 base 仓库），
  tag 也在那里，所以 discover 与 `git archive` 都能拿到 tag。唯一会红的情况是 base 仓库自己没有任何 v* tag。
- **会拉低 Go 覆盖率门禁约 0.7 个百分点**：`tools/versiondb` 新增 109 条语句、单测覆盖 26 条（23.9%），
  全仓 `go tool cover -func` 口径 72.8% → **72.1%**（[测] 2026-10-21，阈值 70%，仍达标；`test.yml` 的
  门禁命令没变，仍是 `go test -coverprofile=coverage.out ./...` + `go tool cover -func` 取 total）。
  想让数字回去就得给它补测试，或者把 `tools/` 从 `coverage.out` 里排除——后者会把工具代码变成盲区，不建议。
- **只验证 SQLite + `ubuntu-latest`**：`glebarez/sqlite` 纯 Go 驱动；macOS / Windows runner 没跑。
- **每个矩阵 job 全量克隆**：仓库 `.git` 实测 9.9 MB，克隆成本可忽略，换来 `git archive` 一定可用。
- **耗时是估算**（[推]）：单版本约 2~4 分钟（下载旧依赖 + 编译两代代码），`max-parallel: 4` 下
  50 个 job 粗估 25~50 分钟；未在真实 runner 上实测。
- **这套 CI 的两根支柱是 `tools/versiondb` 与 `models/compat_db_test.go`**，改它们之前先读 §一。
