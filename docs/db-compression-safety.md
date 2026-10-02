# 数据库压缩 · 兼容与中断（数据安全设计）

> **总原则**：压缩是**纯优化**。任何一条路径上「压不了 / 判不准 / 中断了」，
> 唯一允许的结果是**照原样存明文**。因为压缩本身丢掉一个字节，就是失败。
>
> 本文所有结论都标了出处：`[码]` = 读源码/读调用点得到，`[测]` = 本机实测，
> `[推]` = 推理（未经实测，实施前必须补测）。

---

## 一、兼容面：只有 5 个接触点 + 1 个前端契约（[码]）

| # | 位置 | 动作 |
|---|---|---|
| 1 | `service/chat.go:296` | `Create(&ChatIO{Input: string(before.raw), LogId: ...})` |
| 2 | `service/chat.go:314` | `Updates(models.ChatIO{OutputUnion: *output})` |
| 3 | `handler/api.go:901` | `gorm.G[ChatIO](DB).Where("log_id = ?").First(ctx)` ← **唯一的读** |
| 4 | `handler/api.go:1056` | `Unscoped().Where("log_id IN (...)").Delete(&ChatIO{})` |
| 5 | `service/log_cleanup.go:58-60` | 同上的清理任务版（在事务里） |

全仓库**没有任何生产代码对 `chat_ios` 发裸 SQL**（只有 `models/migrate_test.go`）[码]。
`service/process.go` 里 6 处 `OfString/OfStringArray` 赋值都发生在**内存结构上、写库之前**，
不是"从库里读出来再用"[码]。**接触面就这么大，这是本方案可控的根本原因。**

前端契约（`webui/src/routes/log-chat.tsx:816,860`）：`OfStringArray` 必须是**真数组**——
它要 `.length` 和逐 chunk `.map()` 渲染[码]。`webui/src/lib/api.ts:674-675` 类型为
`string | null` / `string[] | null`。

### 1.1 三条硬约束

**C1 —— 不新增列、不改列类型。**

实测当前声明类型[测]：

```
PRAGMA table_info(chat_ios)
  input           TEXT
  of_string       TEXT
  of_string_array TEXT

typeof 分布：('text','text','null')×3593  ('text','text','text')×8890   —— 无一个 blob
```

GORM 的 AutoMigrate 在 SQLite 上改列类型 = 建新表 / 拷数据 / 删旧表 / 改名。
**在 7 GB 表上这就是灾难**。所以：

- 三个 body 字段一律钉 `gorm:"type:text"`，让 AutoMigrate 对它们**无事可做**；
- **放弃 `compress_level` 列**（计划 v2 原设计）——见 §三.1，判形态只靠帧头，完全不需要它；
- 新增的块表是 `CREATE TABLE`，安全。

**C2 —— `OfStringArray` 对前端必须仍是 `string[]`。**

Go 侧 `OfStringArray []string` 不能改成 `[]byte`：`service/process.go` 有 6 处
`append(output.OfStringArray, ...)`，`handler/api.go:922` 直接把它塞进 JSON 返回[码]。
改类型会同时打断编译**和**前端。

**C3 —— 合法明文必须能判成明文。**

见 §1.2。

### 1.2 判帧规则（读路径的唯一依据）

```
00 4C 43 5A | ver | type | dictID | rawLen | ...
```

**为什么合法明文不会误判**：RFC 8259 规定 JSON 字符串之外唯一允许的控制字符是
`\x20 \x09 \x0A \x0D`，`0x00` 在任何位置都不合法 ⇒ 合法 JSON 首字节不可能是 `0x00`[码]。
`of_string_array` 的明文是 JSON 数组，首字节 `[`；`of_string`/`input` 是 JSON 对象，首字节 `{`。

判定的**全部门槛**：首 4 字节 = magic **且** 版本号已知 **且** `rawLen` 与解压结果一致。
四道全过才当帧。

| 情形 | 行为 |
|---|---|
| 空 / 短于 16 字节 / 首字节非 `0x00` / magic 不符 / 版本未知 | **原样返回明文** |
| 四道全过，但解压失败 | **明确报错**，不静默回退（宁可报错，不可返回垃圾） |

### 1.3 四个陷阱

#### 陷阱 A —— `Updates` 会把钩子写回的 `input` 一起 UPDATE 掉（**已从源码确认**）

`callbacks/callbacks.go` 的 Update 回调注册顺序[码]：

```
:68  gorm:before_update   ← 跑 BeforeSave
:70  gorm:update          ← 里面调 ConvertToAssignments（callbacks/update.go:74），才构造 SET 子句
```

`ConvertToAssignments` 对 struct 是**按非零字段**构造 SET[码]。于是：

> `service/chat.go:314` 传的是 `models.ChatIO{OutputUnion: *output}`，其中 `Input` 是**空串**。
> 如果 `BeforeSave` 不特判空串、把空串也"压缩"成非空值写回 `c.Input`，
> GORM 随后就会把 `input` 加进 SET 子句 —— **把这一行已经存好的请求体覆盖掉**。

即：**钩子只要不特判空串，写日志的每一步都在悄悄破坏请求体。**

守则：`BeforeSave` 第一行 `if c.Input == "" { return nil }`；且**任何情况下都不许把非空
`Input` 赋给一个原本为空的 `Input`**。配套守卫测试见 §四.2。

#### 陷阱 B —— 二进制帧塞不进 `[]string`（`serializer:json` 会丢数据）

`models/model.go:146-149`：

```go
type OutputUnion struct {
	OfString      string
	OfStringArray []string `gorm:"serializer:json"`
}
```

`encoding/json` 的 `Marshal` 对**非 UTF-8 字节会替换成 U+FFFD**[码]。
zstd 帧几乎必然含非法 UTF-8 序列 ⇒ 若把帧当字符串塞进这个 json 序列化字段，**字节被就地破坏**。

⇒ `of_string_array` 必须走**自定义 serializer**：`Value` 直接返回 `[]byte`，
由 database/sql 作为 BLOB 参数写入，**绕开 `json.Marshal`**；
`Scan` 拿到的既可能是 `[]byte`（新帧）也可能是 `string`（旧明文 JSON），两种都处理。

**这里修正了计划 v2 的一处**：T/U 报告否决 serializer 的两条理由是
「接口没有 DB 句柄」和「`Value` 会被调两次」[测]。但这两条**只对"要在 serializer 里读写块表"成立**：

| | `input`（要读写块表） | `of_string` / `of_string_array`（纯值变换） |
|---|---|---|
| 需要 DB 句柄？ | 需要 | **不需要** |
| `Value` 被调两次有害？ | 有害（副作用重复执行） | **无害（幂等，只多花一点 CPU）** |
| 结论 | **走模型钩子** | **走 serializer** |

⇒ **按列选接入点**，不是"一律走钩子"。这让 `of_string_array` 的形态问题迎刃而解。

#### 陷阱 C —— 读路径不许依赖任何标志位

`compress_level` 之类的标志一旦与实际字节不一致（中断、半写、手改），
**"标志说已压缩、字节还是明文"** 是良性的（判定回退明文）；
**"标志说明文、字节已是帧"** 就是读不出。所以读路径**只认帧头**，标志位只用于迁移记账。
⇒ 不变量 INV-2（§2.2）。

#### 陷阱 D —— `RecordLog` 没有外层事务

`service/chat.go:292-319` 的 `recordFunc` 里，`Create`(:296)、`ChatLog.Updates`(:310)、
`ChatIO.Updates`(:314) 是**三条独立语句、三次独立提交**[码]。GORM 默认
`SkipDefaultTransaction=false`，会把每条语句各自包一个事务。

推论：
- **这不引入新风险**——"请求体在、响应体空"这个中间态今天就存在，本方案必须容忍它（它容忍）。
- **它保证了 INV-1 在写路径上自动成立**：钩子里的块写入用的是同一个 `tx`，
  与行数据在**同一个语句事务**里提交。崩溃不可能留下"行在、块不在"。

### 1.4 降级三级（**这是数据安全的核心**）

压缩后的行**旧二进制读不了**（它会把帧字节当明文吐给前端）。所以降级必须有路：

| 级别 | 动作 | 数据操作 | 恢复时间 |
|---|---|---|---|
| **L1** | `DB_COMPRESS=false` | **零**。只停写，读仍然两种形态都认 | 重启即生效 |
| **L2** | `decompress`（全部还原成明文） | 分批改数据 | 见下 |
| **L3** | 备份恢复 | 整库替换 | 分钟级 |

**L2 的设计**（`POST /api/logs/compression/rollback` + 命令行 `llmio -decompress`）：

- 分批、事务内、可续（水位）、**幂等**（已是明文就跳过，判定同 §1.2）；
- 与迁移**共用同一套批处理骨架**，不另写一份；
- 完成后再降级二进制就是安全的。

> **`decompress` 不是"以后再说"的功能。它是 Phase 1 的交付物**，理由有两条：
> ① 它是 L2 降级的唯一实现；② **它就是最强的验证器**——
> "压缩整库 → decompress 整库 → 与原库逐字节比对"本身就是一次全库往返验证。

### 1.5 备份门槛

迁移**拒绝原地开跑**，除非满足其一：

- 同目录存在 `<db>.bak` 且其 `mtime >= db` 的 `mtime`（[测] 复制备份在本机可行，
  但连接打开时 `rename` 会被 Windows 拒绝，所以脚本不能 rename，只能 copy）；
- 显式 `--allow-in-place`（记进审计日志）。

启动时把备份的 SHA256 与库大小写进 `Config`（`compress_backup` 键），迁移状态页可见。

---

## 二、中断处理

### 2.1 中断点清单（穷举，逐个给结果与恢复动作）

| # | 中断位置 | 结果 | 恢复 |
|---|---|---|---|
| 1 | 写请求：`Create(input+块)` 语句中途 | 单语句事务回滚，**没有半行** | 客户端重试，无需动作 |
| 2 | 写请求：`:296` 已成、`:314` 前 | `input` 在、`output` 空。**这是今天就有的中间态** | 无（容忍） |
| 3 | 迁移批次中途 | 整批回滚（含水位），**库里无一行动过** | 重启后重跑同一批 |
| 4 | 迁移批次已提交、水位未落 | 重扫同一批；`input` 已判为帧 ⇒ 跳过 | 幂等，无需动作 |
| 5 | GC 中途 | 删除本身幂等；宽限期保证不误删在途块 | 下一轮重跑 |
| 6 | VACUUM 中途 | SQLite 回滚日志恢复原状；`auto_vacuum` 可能仍为 0 | 状态标 `interrupted`，**最多重试一次**，之后转人工 |
| 7 | 磁盘满 | 语句失败 ⇒ 批次回滚 ⇒ 水位不动 | 腾出空间后续跑 |
| 8 | `kill -9`（任意时刻） | 同 1–7，靠 SQLite 的原子提交 | 同上 |

**关键**：全部 8 种情况里，**没有一种会留下"行与块不一致"**。这靠的是 INV-1，不是靠运气。

### 2.2 三条不变量（任何后续优化都不许破）

**INV-1 · 行与块同事务。**
一次写入里，行数据与它引用的块必须在**同一个事务**中提交。
**禁止**为了吞吐把块表另开连接或另开事务——那正是 T 方案失败的原因
（`database is locked (5)`，卡 5 秒；`maxOpen=1` 自死锁）[测]。

**INV-2 · 读只认帧头。**
读路径不依赖任何标志位、不依赖列类型、不依赖行数统计。标志位不一致最多导致
"重复压一次"，**永不导致"读不出"**。

**INV-3 · 提交前自检。**
任何编码在提交前，必须**解回来与原文逐字节比对**，不一致就**存明文并计数**。
成本：470 KiB 的行按实测解码 1128 MB/s ≈ **0.4 ms**[测]，买的是"压缩 bug 不可能变成数据损坏"。

### 2.3 水位与幂等

- 水位 `last_id` 与该批的 UPDATE **写在同一个事务里**。即便落后，也只是重扫一批。
- 候选过滤：`id > last_id AND id <= maxID(快照) AND updated_at <= now - quiesce`
  —— 在途行（正在被写的那一行）不碰。
- 跳过并计数：空 `input`、已判为帧、解压/编码自检失败。
- 起始 `maxID` 快照排除了迁移期间新产生的行，它们由写路径直接以新形态落库。

### 2.4 与清理任务的互斥（含多实例）

- **进程内**：`service` 包级 `maintenanceMu`；`CleanLogsByDays` 用 `TryLock`，抢不到就跳过本轮。
- **进程间（多实例）没有互斥**。分析[推]：
  - 清理的批量 `DELETE` 与迁移的 `UPDATE` 由 SQLite 串行化；迁移的 UPDATE 命中 0 行即无害；
  - GC 的宽限期覆盖跨进程可见性（只回收"不在任何存活行引用里**且**早于宽限期"的块）。
  - **但这是推理，必须实测**（§四.6 多实例用例）。在实测通过前，把"多实例同时开迁移"
    在文档里标为不支持。

### 2.5 不阻塞启动

计划 v2 把 VACUUM 放在 `models.Init` 末尾、`router.Run` 之前。**改掉**：

- 理由：7 GB 库实测 VACUUM **62.9 s** 且持排他写锁[测]。放在启动路径上意味着
  "启动 → 63 秒不可用"；反复被 kill 就**永远起不来**。
- 改成：VACUUM 归入 Phase 5 的**迁移任务**，后台执行，由同一个 API 触发；
  启动路径**只读状态、不写数据**。
- 新库（0 字节）在 `CREATE TABLE` 之前设 pragma 即可，零成本，仍然放启动路径。
- `DB_VACUUM=true` 的既有语义（`models/init.go:82-87`）保留不动，避免改变现有行为。

---

## 三、这批约束推翻/改动了计划 v2 的哪几条

| # | 计划 v2 | 改为 | 依据 |
|---|---|---|---|
| 1 | `ChatIO` 加 `CompressLevel` 列 | **不加任何列**，判形态靠帧头，进度靠水位 | C1；7 GB 表上 AutoMigrate 改列 = 灾难 |
| 2 | 三列都走模型钩子 | **`input` 走钩子；`of_string`/`of_string_array` 走 serializer** | 陷阱 B；纯值变换不需要 DB 句柄，serializer 的幂等缺点不适用 |
| 3 | VACUUM 在 `models.Init` 后、`router.Run` 前 | **后台迁移任务里** | §2.5；63 s 持锁不能压启动路径 |
| 4 | `decompress` 属 Phase 6 | **提到 Phase 1** | §1.4；它是 L2 降级的唯一实现，且是最强的验证器 |
| 5 | （未提） | 新增：**迁移前备份门槛** | §1.5 |
| 6 | （未提） | 新增：**INV-1/2/3 与四类守卫测试** | §2.2 / §四 |

---

## 四、验证（数据安全专项，按危害排序）

1. **陷阱 A 守卫（最高危）**：建一行已知 `input` → 走 `Updates(OutputUnion)` 路径
   （照抄 `chat.go:314` 的写法）→ 断言 `input` **逐字节不变**。
   反例断言：故意去掉空串特判，必须能测出被覆盖（证明这个测试真的抓得住）。
2. **帧判定对抗样本**：明文首字节 `0x00`、以 `00 4C 43 5A` 开头但版本非法、
   以 `00 4C 43 5A 01` 开头但载荷非 zstd、截断帧、空体、`nil` 与 `[]` 的语义保持。
   全部必须"当明文返回"或"明确报错"，**不得静默返回垃圾**。
3. **陷阱 B 回归**：`of_string_array` 存入含非法 UTF-8 的帧 → 读回**逐字节相等**
   （这一条在改 serializer 之前**必然失败**，是回归护栏）。
4. **AutoMigrate 守卫（真库副本）**：对 7 GB 副本跑一次启动 → 断言
   `PRAGMA table_info(chat_ios)` 与启动前**完全一致**、库文件大小不变、启动耗时 < 10 s。
5. **中断注入**：迁移跑到第 N 批时 `kill -9` → 重启续跑 → 完成后
   **整库逐行与原库比对**（照 `verify.exe -mode=verify` 的两遍法：第二遍按哈希回填比对）。
6. **幂等**：迁移连跑三次，断言第二次、第三次"0 行改动"。
7. **回滚闭环**：压缩整库 → `decompress` 整库 → 与原库**逐行 SHA256 一致**。
8. **多实例**：两个进程同时写 + 一个跑清理 + 一个跑迁移，跑 30 分钟，结束后无悬空引用。
9. **并发**：编解码过 `-race`；`gorm.G[T]` 泛型 API 下钩子必须触发
   （复用现成探针 `probe/hooks_generics_test.go`[测]）。

---

## 四之二、回执（哪些已经验过了）

上面那张表是**待验清单**，不是成绩单。截至阶段 2 结束，实际拿到的证据如下。
没列进来的条目就是**还没验**，一条都不许当成已通过。

| §四 条目 | 状态 | 证据 |
|---|---|---|
| 2 帧判定对抗样本 | ✅ | `pkg/compress` 单测，语句覆盖率 100%，含故障注入分支 |
| 3 陷阱 B 回归 | ✅ | `TestChatIOCompress_RoundTripThroughFrames`：帧写进库后 `DecompressBytes` 仍与原字节相等；改序列化器之前此条必然失败 |
| 4 AutoMigrate 守卫（单库） | ✅ | `TestChatIOCompress_AutoMigrateLeavesChatIOsAlone`；另用变异测试坐实过它抓得住——把 `type:text` 改成 `type:varchar(255)` 即刻建表重建 |
| 4 AutoMigrate 守卫（真库副本） | ✅ | 7217.5 MiB 副本上跑 `Init`：耗时 0.01 s，`chat_ios` 的 DDL / `table_info` / 索引**一字不变** |
| 9 并发（`-race`） | ✅ | 全仓 `go test -race ./...` 通过。此前"本机没有 cgo/gcc"的缺口已补（MinGW-w64 16.2.0） |
| 1 陷阱 A 守卫 | ⚠️ **只覆盖了一半** | 现在 `input` 还是明文列，测到的只是 GORM 的"零值不进 SET"。真正的护栏要等阶段 3 换钩子接入 |
| 5 中断注入 / 6 幂等 / 7 回滚闭环 / 8 多实例 | ❌ 未做 | 属于迁移与块表的验证，随阶段 4–6 一起来 |
| 9 泛型 API 触发钩子 | ❌ 未做 | 阶段 3 的第一件事 |

**真机回执（生产库副本）**：

- 逐行核对 **12,483 行**的 `input` / `of_string` / `of_string_array`，与原始字节比对
  **全部通过**；其中 3593 行的 `of_string_array` 只有 JSON 文本的空白形态差异，语义完全相同。
- 取最大的 200 条真实响应体做往返：**382.25 MiB → 5.99 MiB（63.8x）**。

**踩到过一个坑，值得记下来**：第一次用的副本（`D:\llmio-test\llmio.db`）**自身是坏的**
（scp 传输所致），`PRAGMA integrity_check` 都跑不完，读到大半行时随机报
`database disk image is malformed (11)`。那个报错**完全看不出**是"库坏了"还是"压缩代码读错了"，
而这两件事的处置方式天差地别。所以真机验收的**第 0 步永远是先验副本自身的完整性**
（`TestCompressSerializer_RealDatabaseIntegrity`）——先修库，再谈压缩。

---

## 五、诚实标注

- `[测]` 的条目：`table_info`/`typeof` 分布、VACUUM 62.9 s、`Value` 被调两次、
  T2 的 5 s 锁等待、块表 GC 226 ms、泛型 API 触发钩子——均出自
  `D:\llmio-test\report-integ.md`、`report-dedup.md` 与本文件记录的本机实测。
- `[推]` 的条目：**多实例并发（§2.4）**、**VACUUM 中断后的 pragma 状态（§2.1 #6）**。
  两条都列进了 §四 的测试清单，实测通过前不得当成结论。
- 本文只覆盖**兼容与中断**。压缩率、分组大小、读放大等性能结论见
  `docs/` 下其余报告与计划文件，不在本文重复。
