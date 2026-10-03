# 数据库压缩 · 阶段 6：响应体两列的原地回填（真机回执）

## 〇、一句话结论

**三列都进迁移之后，那份 1.47 GiB 的库落到 126.10 MiB（8.4%）；响应体两列自己压掉 39.2 倍，
12,483 行三列逐一还原、回滚闭环全通。** 顺带修掉一个让这件事在真机上根本跑不起来的缺口：
「已完成」之后再点开始，水位停在表尾，一行都扫不到。

---

## 一、缺口是什么

阶段 3～5 的迁移只做了 `input` 一列。`of_string` / `of_string_array` 从设计之初就挂了
`gorm:"serializer:compress"`，**新写入**的行确实会落成帧——但历史行是明文，而迁移里
一个字都没提这两列（`SELECT id, input` / `UPDATE chat_ios SET input = ?`）。

后果在真机上很具体：

- 库文件 1.47 GiB，其中 `of_string_array` 一列的明文占 1.36 GiB（12,441 行）；
- 界面「实际占用」只报 62 MB，因为它的口径是「请求体列 + 分块组表 + 块表索引」——
  **根本没算响应体那两列**；
- 而迁移状态早就写着 `done`。看起来一切都好。

每一处（SELECT、UPDATE、候选判据、字节统计）各自写了一遍 `input`，漏一列**不会产生任何
编译错误**。这次把它们收进一个数组：

```go
var chatIOColumns = [colCount]string{"input", "of_string", "of_string_array"}
```

扫描目标要分两轮 append（三列值、再三列 `typeof`）——第一版交错着 append，16 条 service
测试同时红在 `sql: Scan error on column index 2, name "of_string"`。

### 1.1 一处不显眼的坑：`length()` 数到 NUL 为止

补这一段是为了记住它，不是为了留着它——**现在这三列一个 `length()` 都没有了**（见八）。

候选判据当时写的是 `length(input) > 0`，于是 **NUL 打头的行被判成空而漏出候选集**——
SQLite 的 `length()` 对 TEXT 数到第一个 NUL 为止，而帧 magic 的首字节恰好是 `\x00`。
当时的修法是全部改成 `length(CAST(列 AS BLOB)) > 0`，并留了一条回归用例
（`TestLogCompress_NulLeadingBodyIsStillACandidate`，现在仍然留着，判据换成
`typeof` 之后它照样成立）。

那个修法是对的，但它把成本带进来了：`length(CAST(...))` 要**把载荷读出来**。
真机上一次 4.24 秒的长读就足以把整站打成 500，根因与破法见第八节。

---

## 二、顺带修掉的水位缺口

前端「开始迁移」固定传 `full: false`。而 `input` 已经迁完的库，水位停在表尾 ⇒ 续跑的候选
查询一行都扫不到 ⇒ **立刻 done**，响应体那 1.36 GiB 永远迁不动。

修法在 handler 上：`full := req.Full || service.ShouldRescanFromZero(ctx)`。

判据本身（「状态是 done 就是整表重扫」）落在 service，但**不能**放进 `runCompressMode`——
调度器每 30 秒拿 `full=false` 推一次（done 状态也推），那条规则放进去就变成每 30 秒一次
全表重扫。

---

## 三、真机回执

源库：`D:\llmio-test\work-old\db\llmio.db`（1.47 GiB，12,483 行，`input` 列已迁完、
响应体两列仍是明文——这是生产升级路径上的实际形状）。测试在它旁边复制一份工作库再动，
源库以 `PRAGMA query_only` 只读打开。
跑法：`LLMIO_REAL_DB_COPY=<副本> go test ./service/ -run RealDatabaseOutputColumns -v -timeout 180m`

| 阶段 | 读数 |
|---|---|
| 迁移前 | `input` 3.37 MiB（帧 11,946 行 / 明文 15 行）｜响应体两列 **1.37 GiB**（12,483 行明文）｜组 891 个 / 56.87 MiB |
| 迁移 | **13.6 s**：扫 12,483 行 / 改 12,440 行 / 跳过 43 行，1.37 GiB ⇒ **35.77 MiB** |
| 归一化 | 迁移后与回滚后各断言一次：三列里**零长度的 TEXT 为 0**（真机上 `of_string` 原本有 8,932 行是空串） |
| 残留 | 43 段 / **8.45 KiB**（最长 615 字节）——全是压不动的短响应体 |
| 压缩比 | **39.2×** |
| 核对 | **12,483 行三列逐一相等**（走生产读路径，与源库逐行比对） |
| 文件 | VACUUM 1.20 s：**1.47 GiB ⇒ 126.10 MiB（8.4%）** |
| 回滚 | 32.6 s，还原 12,472 行；三列全回明文；**再次逐行相等**；空串仍为 0 |

数字与首轮一致（首轮 12.1 s / 改 12,398 / 跳过 54；差的那 42 行正是空串归一化带来的——
那些行原先只有 `of_string` 一个空串、别的列都没有货，旧的判据把它们整个跳过了）。

### 3.1 「改 12,398 行、跳过 54 行」与「残留 43 段」是同一件事

`Skipped` 的定义是「这一行所有待动的列都压不动」。残留的 43 段正是这种行：`of_string`
只有 19～615 字节，`of_string_array` 是 NULL——短文本压完比原文还长，`PackColumnValue`
按规则 1（压不了就存明文）让它留在明文里。

**这是设计，不是漏迁。** 第一版验收断言写的是「零明文行」，于是一件完全正确的事把它判红了。
现在改成用**生产那一个函数**逐行复核：

```go
if models.PackColumnValue(raw) != nil {
    t.Fatalf("行 %d 的 %s 有 %d 字节明文，而且它**压得动**——这一行被漏掉了", ...)
}
```

外加一条不依赖压缩代码的量级断言（残留不得超过原明文的 1%），两条合起来才既认得出
「压不动」、又挡得住「`PackColumnValue` 自己有 bug 于是什么都压不动」。

### 3.2 验收方法本身踩的两个坑

「源库那一侧」不能拿裸 SQL 读来的字节当基线，两个坑都要绕：

1. **源库的 `input` 列早就是帧了。** 拿列里字节跟明文比必然不等——而那个「不等」恰恰是
   压缩生效的证据。第一版红在 `行 1 的 input 还原不一致：178 字节 → 229 字节`。
2. **其中还有块引用序列。** 80 字节上下那些是 type 2 的引用帧，只有 `models.UnpackBody`
   解得开（要查块表）；`compress.DecompressBytes` 只认逐行帧，遇到引用序列会把那 80 字节
   原样当明文还回来。第二版红在 `行 3：80 字节 → 112,434 字节`。

现在两侧都走生产读路径（`openSourceModel`：单连接 + `query_only` + `ConnMaxLifetime(0)`，
`query_only` 挂在连接上，换一条连接就没了），各用**自己的块表**解包——钩子用的是查询时
那个 `tx`。查 id 列表后每批 64 行：整库三列明文合起来 7 GB，一次性 `Find` 会把它们全拽进内存。

---

## 四、三列的接入点不同

这一点在 `rewriteBodies` 里必须显式分开，喂错函数的后果不是报错而是**解错**：

| 列 | 接入点 | 为什么 |
|---|---|---|
| `input` | 模型钩子（`BeforeCreate` / `AfterFind`）+ `PackBodies` / `UnpackBody` | 要读写块表（`chat_io_blocks`） |
| `of_string` / `of_string_array` | 序列化器（`gorm:"serializer:compress"`）+ `PackColumnValue` / `UnpackColumnValue` | 纯值变换，值进值出 |

`PackColumnValue` / `UnpackColumnValue` 是序列化器与迁移**共用**的那一份编解码。返回值只有
三种含义，必须分得很开：帧/明文 = 编解码成功；`nil` = **这一列不用动**；`error` = 是我们造的
帧但解不开，调用方要停下来。把后两者混起来是静默损坏。

落库的值统一交 `models.BodyBytes`：明文落 TEXT、帧落 BLOB。回滚这条路尤其不能写错——把明文
按 BLOB 写回去，读路径**照样读得出来**，于是这个错会一直躺着，直到下一次迁移把它当成已迁过
而漏掉。`assertColumnsPlain` 就是钉这一条的。

### 4.1 只写真正变化的列

逐列判断、逐列发 UPDATE（`setColumn`），而不是一条语句写三列：这条语料一行 470 KiB，把没变的
列一起写回去意味着凭空重写一遍它的 BLOB——纯粹的 IO 与 WAL 放大。

---

## 五、界面口径

- `pending_rows` / `framed_rows` 是**三列并集**口径（任一行任一列没迁就算没迁）。
- 「实际占用」= **库文件在磁盘上的字节数**（`file_size`）。阶段 6 起初为它新增过一个
  `output_column_bytes`（响应体两列此刻真实占的字节），后来连同 `input_column_bytes`
  一起删了——那个数只能靠读载荷算，而它按秒轮询，正是第八节的根因。`file_size` 更便宜
  （一次 `os.Stat`）也更全（块表索引、freelist 都在里面），要修的那个「少报 1.4 GiB」
  由它直接回答。
- 三语文案同步改写（`stored` / `stored_hint` / `ratio_hint`，删掉 `file_size` /
  `file_size_hint` 两个词条）。

---

## 六、诚实的边界

1. **126.10 MiB 是这份库的实测**，不是通例。它由三块构成：分块组表 56.87 MiB + 响应体两列
   35.77 MiB + 块表索引等。响应体那部分能压多少取决于语料——这份库里 `of_string_array`
   是高度重复的 SSE 分片，39.2× 偏乐观。
2. **回滚比迁移慢**（32.6 s vs 13.6 s）：回滚要把 1.37 GiB 明文写回去，而迁移只写 35.77 MiB。
   方向如此，不必优化。
3. **43 段残留明文会一直留在库里**，每轮全表重扫都会再碰它们一次。开销 8.45 KiB，忽略不计。
4. **`bytes_total` 对已迁库是新旧混合的口径**：它在起跑时按三列明文现算，所以对一个
   `input` 已迁完的库，`bytes_total` 只等于响应体两列的明文（本例 1,471,413,138）。
   历史行上留下的旧值不代表现状。
5. **回滚之后文件不会自己缩**（7.12 GiB）——那是 freelist 里的页，要回收得另外走 VACUUM。
   这与前面几个阶段一致。
6. **真机验收只在这一个驱动（glebarez/sqlite）+ 这一份库上量过。** 换驱动或换块大小，
   数都要重新量。

---

## 七、空值不再占用 TEXT 形态

### 7.1 立这条不变量的理由

三列里从此只有三种形态，与 `typeof` 一一对应：

| 列里存的 | `typeof` | 含义 |
|---|---|---|
| 非空明文 | `text` | 还没迁 |
| 帧 | `blob` | 迁过了 |
| 空 | `null` | 无事可做 |

写入侧两处一起改：序列化器（`models/serializer.go` 的 `Value`）与 `models.BodyBytes.Value`
遇到零长度都返回 `nil` 而不是零长度字符串。**读路径完全不受影响**——`Scan(NULL)` 把字段
归零，字符串列读出来仍然是 `""`；`of_string_array` 的 `[]` 与 `null` 本来就走不同的分支
（`[]` 序列化成 2 字节的 `"[]"`，是合法明文）。

### 7.2 历史行怎么办：迁移顺手归一化

改写入侧不解决存量。真机上 `of_string` 有 **8,932 行是空串**（流式响应，正文在
`of_string_array` 里），在旧的「`length(...) > 0`」判据下它们不是候选，会被永远跳过。

迁移的 `packColumn` 对它们做一件事：**这一列此刻是零字节的 TEXT 就写 NULL**。它读得到
载荷，顺手做掉，代价为零。回滚那条路同样：空值写 NULL 回去。

于是这条不变量在存量上也成立，而且**只成立一次**——迁完就收敛，不会每轮重扫都碰它们。
`assertNoEmptyText` 钉住这一点，迁移后与回滚后各断言一次。

### 7.3 不需要额外处理的两种形态

`of_string_array` 的 `[]` 与 `input` 的空串都**没有**实际出现：前者是 2 字节的 `"[]"`，
是合法明文；后者实测 0 行。所以归一化只有一处落点，不必为它们写特例。

---

## 八、线上故障：状态接口的 4.24 秒把整站打成 500

### 8.1 现象

用户在验证实例上发一条普通聊天请求：

```
{"code":500,"message":"SQL logic error: cannot start a transaction within a transaction (1)"}
```

### 8.2 根因

库跑在 `journal_mode=delete`（回滚日志）下，**读事务挡写**。而状态页每 2 秒轮询一次的
`ReadDBStats` 里，当时带着为「实际占用」新增的那句：

```sql
sum(length(CAST(input AS BLOB))
  + length(CAST(of_string AS BLOB))
  + length(CAST(of_string_array AS BLOB)))
```

三列此刻都是明文，7 GB 载荷要一页页读出来。真机回执：回滚后的 7.12 GiB 库上
**4.24 秒**（冷缓存 5.27 秒）。于是每一次轮询，并发进来的聊天写请求都在写锁上等满
5 秒 `busy_timeout`，然后报 `SQLITE_BUSY`——整站 500。那句 `cannot start a transaction
within a transaction` 是次要症状。

### 8.3 破法：判据只剩 `typeof`

`typeof` 读的是记录头，**不碰载荷**。三列六个判据全部退化成
`typeof(列) = 'text'` / `= 'blob'`，状态页那句里一个 `length()` 都不许有。这条能立住，
靠的正是第七节那条不变量——没有它就必须量长度才能把「空」从「明文」里摘出去。

`service/db_stats.go` 顶部把这条写成了硬规矩：**这一句里只许出现 `typeof`**。

### 8.4 真机回执：读的代价，以及它把写请求拖住多久

源库 `D:\llmio-test\work-old\work-phase6\output.db`（7.12 GiB，12,483 行，三列全是明文
——回滚之后的形状），复制一份可写副本再动。
跑法：`LLMIO_REAL_DB_COPY=<源库> go test ./service/ -run RealDatabaseStatsLatency -v -timeout 30m`

| | 旧口径 `length(CAST(...))` 求和 | 新口径 `typeof` 并集 |
|---|---|---|
| 读一次 | **5.25 秒** | **28 毫秒**（快 188×） |
| 写请求最长等待 | **5.018 秒**（等满 `busy_timeout`），3 次里 3 次失败 | **56 毫秒**，100 次全部成功 |

同一台机器、同一份库、写请求同样的节奏（每 50ms 一次真实 UPDATE），只有读在变。
空载时写请求最长 2 毫秒。

这条测试量的是**写请求被拖住多久**，而不是读有多快——一个 80 毫秒的读也挡写 80 毫秒，
只是挡不死人。所以它用一个后台 goroutine 真写真提交，记下最长的一次；判据是
「新口径下一次都不许失败，且旧口径必须差一个数量级」。

验证实例上（7.66 GiB 库）复现同一件事：状态接口从 **1.07 秒**降到 **37～43 毫秒**，
一边把它按 150ms 打满、一边发真实写请求，写请求稳定在 2～22 毫秒、无一失败。

### 8.5 顺带删掉的东西

`DBStats` 上的 `input_column_bytes` / `output_column_bytes` 两个字段（含前端类型、
i18n 词条、两条用例）。界面要的那个数由 `file_size` 回答，见第五节。
