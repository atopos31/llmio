# 线上故障回执：COMMIT 撞锁 → 泄漏事务 → 整库死锁

> 2026-10-17 19:11–19:40（Asia/Shanghai），生产实例 `129.211.169.240:7070`
> （容器 `llmio`，镜像 `atopos31/llmio:latest`，库 `/root/db/llmio.db`，535 MiB）。
> 本文是根因、证据、修复与验收的记录，对应分支 `fix/compress-busy`。

## 一、现象（用户侧）

1. 聊天请求的 IO 记录报错：
   `compress: input 入块表失败: database is locked (5) (SQLITE_BUSY)`
   ——响应其实已经正常返回，但日志被标成 error。
2. 接口整片 500：
   `database is locked (5) (SQLITE_BUSY)`；
   以及 `SQL logic error: cannot start a transaction within a transaction (1)`。
3. 聊天日志大量停在 `running`（界面永远显示"在途"），结束时的 UPDATE 全部失败。
4. 只有**重启进程**能恢复；重启后偶发一段时间又复发。

## 二、现场证据

- 死锁期间 `/root/db/` 下常驻 `llmio.db-journal`，且容器进程持有它的 fd
  （`/proc/1/fd`）——说明 SQLite 侧有一个**一直没结束的写事务**。
- 向进程发 SIGQUIT 抓 goroutine 栈：**没有任何 goroutine 在做数据库操作**。
  锁的持有者不是某段卡住的代码，而是一条已经回到连接池、却仍带着未结束事务的连接。
- 重启后 journal 被 SQLite 自动回滚、文件消失，服务立刻恢复。
- 同一份库副本上用原生驱动可稳定复现（见 `pkg/sqlitex` 的对照测试）：
  写者 COMMIT 撞 `SQLITE_BUSY` 后，同一连接池的下一次 `Begin()` 报
  `cannot start a transaction within a transaction (1)`，且**别的池的读也被锁住**，
  直到进程退出。

## 三、根因链

1. 库跑在 `journal_mode=delete`（回滚日志）。COMMIT 要 EXCLUSIVE 锁；只要别的连接
   持有 SHARED（一次普通查询就算），它就得等，等满 `busy_timeout`（驱动默认 5000 ms）
   后返回 `SQLITE_BUSY`。**SQLite 的语义是：COMMIT 返回 BUSY 时事务仍然开着。**
2. Go 的 `database/sql` 在 `Tx.Commit` 出错时只把连接放回池子：
   `tx.close(err) → putConn(err)`，只有 `driver.ErrBadConn` 才会 Close，
   其它错误（BUSY 不是 ErrBadConn）**原样回池**，并且**不会**调用
   `driver.Tx.Rollback`（见 GOROOT `database/sql/sql.go` 的 `Tx.Commit` 与 `putConn`）。
3. 于是连接带着未结束的写事务回到空闲池：RESERVED/PENDING 锁永不释放。
   - 所有读在拿 SHARED 时等满 5 秒 → `database is locked (5) (SQLITE_BUSY)`；
   - 所有写失败（包括 compress 的入块表）；
   - 复用这条连接的下一次 `BEGIN` → `cannot start a transaction within a transaction (1)`。
4. 直到进程重启，SQLite 用残留的 `-journal` 回滚，锁才释放。

这正是阶段 6 文档里那句"次要症状"的**完整机理**：当时它由一次 4.24 秒的长读触发；
这次没有长读，触发它的只是并发读写下的 COMMIT 争锁。**只要 COMMIT 可能失败，
这个泄漏路径就一直在**。

## 四、修复

### 4.1 事务收尾（根因修复）：`pkg/sqlitex`

在 glebarez 驱动外面包一层受保护驱动：

- `Commit()` 失败 → 立即 `driver.Tx.Rollback()`；再不行就发一条 SQL `ROLLBACK`；
  仍收尾不掉 → 返回 `driver.ErrBadConn`，让 `database/sql` **丢弃**这条连接
  （进程内关闭 fd，fcntl 锁由内核释放）。
- `Rollback()` 失败走同一套兜底与丢弃逻辑。
- 其余可选接口（`BeginTx`/`ExecContext`/`QueryContext`/`PrepareContext`/
  `Ping`/`ResetSession`/`IsValid`/`CheckNamedValue`）逐个转发，不退化成
  "每条语句 Prepare 一次"。
- 注意：`BeginTx` 在底层驱动未实现 `ConnBeginTx` 时必须**自己回退到 `Begin()`**，
  不能返回 `driver.ErrSkip`——`database/sql` 在这一条路径上不会替你回退。

`models.Init` 改用 `sqlitex.DriverName` 打开库。

### 4.2 减少撞锁：`_txlock=immediate`

DSN 追加 `_txlock=immediate`。deferred 事务是"先拿 SHARED、写时再升级 RESERVED"，
两个写者同时升级会互等，SQLite 为避免死锁**直接返回 BUSY（不等待、不走 busy_timeout）**；
BEGIN IMMEDIATE 在事务开头就拿到 RESERVED，并发写者改由 busy_timeout 排队。
读路径不受影响（GORM 单条查询不开事务）。

### 4.3 写入韧性：`service/chat.go`

- `createChatIO`：第一次正常写（可能走块表），撞锁后**退回明文**（`SkipCompress`，
  不写块表）重试；仍失败只记日志——**IO 记录是观测性功能，不再把成功的聊天标成 error**。
- `SaveChatLog`（响应前同步执行）与结束时的 UPDATE 撞锁重试，日志不再永远停在 running。
- `models.ChatIO.SkipCompress` 是 `gorm:"-"` 的非持久字段，只影响当次写入形态；
  读路径仍只认帧头。

### 4.4 重启收尾：`models.ReconcileInterruptedLogs`

启动时把上次进程留下的 `status=running` 日志标成 error（"服务重启：该请求在记录结束前
被中断"）。只在启动路径调用——那一刻没有在途请求，这是事实而不是猜测。

## 五、验证

- `pkg/sqlitex` 对照测试（真驱动 + 假驱动）：
  - 原生驱动：COMMIT 撞 BUSY 后下一次 Begin 报
    `cannot start a transaction within a transaction`（稳定复现，即线上现象）；
  - 受保护驱动：COMMIT 失败后，写者池可继续开事务、**别的池的读立即成功**、
    撞锁那笔整笔回滚；Rollback 也失败时连接被丢弃（`opens/closes` 计数为证）。
- `go test ./...` 全绿；`go vet ./...` 通过。
- 真机部署（二进制热替换，见下）：`/api/version=v0.9.5-fixbusy`，
  启动日志 `已收尾上次进程中断的日志 rows=3`；部署后 10 分钟内
  `SQLITE_BUSY` / `cannot start a transaction` / `record log error` 计数均为 0；
  `/api/logs?status=running` 为 0 条；`/root/db` 无残留 `-journal`。

## 六、部署与回滚（2026-10-17 真机）

- 新二进制：`v0.9.5-fixbusy`（linux/amd64，`CGO_ENABLED=0`，静态）。
- 旧二进制归档：`/root/llmio-deploy/backup/llmio-old-20261017-194025`
- 库备份：`/root/db-backup/llmio.db.20261017-194025`
- 交换方式：`docker stop llmio` → `docker cp <new> llmio:/app/llmio` →
  `docker start llmio`（容器文件系统在 restart 后保留）。
- 回滚：`docker stop llmio && docker cp <old> llmio:/app/llmio && docker start llmio`
- **注意**：这是热替换，不是镜像发布。`docker compose up` 重建容器、或 watchtower
  拉到新的 `atopos31/llmio:latest` 时，修复会丢。要长期生效需把分支合入并发布新镜像。

## 七、剩下的风险

1. `journal_mode=delete` 下"读挡写"的固有代价没变：写事务仍可能等满 busy_timeout 失败，
   只是不再泄漏事务、不再整库死锁。要根治并发读写最稳的是 WAL，但会牵动备份、
   文件大小口径与回收策略，需要单独立项。
2. 入块表 / 写日志仍可能偶发失败（此时会降级为明文或只丢 IO 记录），但请求本身
   不会再被标成 error。
3. 热替换的二进制不随镜像升级，见第六节。
