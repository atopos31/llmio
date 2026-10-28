package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/pkg/sqlitex"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// DBPath 是当前库文件的位置。压缩状态页要报库文件大小与 freelist，
// 前者只能从文件系统量（SQLite 的 page_count*page_size 不含 WAL 与页头开销）。
var DBPath string

func Init(ctx context.Context, path string) {
	if err := ensureDBFile(path); err != nil {
		panic(err)
	}
	// 受保护驱动：COMMIT 撞锁失败时必须把事务回滚掉再交还连接。
	// 原生 driver + database/sql 不会做这件事，泄漏的写事务会把整库锁死到重启
	// （2026-10-17 线上故障，见 pkg/sqlitex 的包注释）。
	sqlitex.Register()
	db, err := gorm.Open(sqlite.Dialector{
		DriverName: sqlitex.DriverName,
		DSN:        sqliteDSN(path),
	}, &gorm.Config{Logger: newGormLogger()})
	if err != nil {
		panic(err)
	}
	DB = db
	DBPath = path
	// 组缓存只按组 id 索引，而组 id 只在单个库文件内才有意义。走到这里说明
	// 进程要开始用（可能是另一个）库了，旧缓存一律作废——否则会读到别的库里
	// 同号的组，症状是"长度对得上、内容全错"，而且不报任何错。
	blockStore.Reset()
	// 存储层：空库要在**第一张表建出来之前**设 auto_vacuum，ptrmap 才会跟着
	// 表一起长出来（既有库在这里一个字节都不会动，见 PrepareEmptyStorage）。
	// 它失败只记日志：一项存储层的优化没资格让服务起不来。
	if _, err := PrepareEmptyStorage(db); err != nil {
		slog.Error("storage: 设 auto_vacuum 失败", "error", err)
	}
	if err := db.AutoMigrate(
		&Provider{},
		&Model{},
		&ModelWithProvider{},
		&ChatLog{},
		&ChatIO{},
		&Config{},
		&AuthKey{},
		&LogCleanupRecord{},
		// 块表只服务 chat_ios.input 一列（见 docs/db-compression-phase0.md）。
		// 两张都是新表，AutoMigrate 只做 CREATE TABLE，不会碰既有的 7 GB 大表。
		&Block{},
		&BlockGroup{},
	); err != nil {
		panic(err)
	}
	// 兼容性考虑
	if _, err := gorm.G[ModelWithProvider](DB).Where("status IS NULL").Update(ctx, "status", true); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ModelWithProvider](DB).Where("backup IS NULL").Update(ctx, "backup", false); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ModelWithProvider](DB).Where("customer_headers IS NULL").Updates(ctx, ModelWithProvider{
		CustomerHeaders: map[string]string{},
	}); err != nil {
		panic(err)
	}
	if _, err := gorm.G[Model](DB).Where("strategy = '' OR strategy IS NULL").Update(ctx, "strategy", consts.BalancerDefault); err != nil {
		panic(err)
	}
	if err := ensureModelDisplayOrder(ctx); err != nil {
		panic(err)
	}
	if _, err := gorm.G[Model](DB).Where("breaker IS NULL").Update(ctx, "breaker", false); err != nil {
		panic(err)
	}
	if _, err := gorm.G[AuthKey](DB).Where("io_log IS NULL").Update(ctx, "io_log", false); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ChatLog](DB).Where("auth_key_id IS NULL").Update(ctx, "auth_key_id", 0); err != nil {
		panic(err)
	}
	// 备用标记是后加的非指针 bool：AutoMigrate 加列不会给存量行填默认值，
	// 留 NULL 会导致日志列表扫描 bool 时报错（与 auth_key_id 同一类问题）
	if _, err := gorm.G[ChatLog](DB).Where("backup IS NULL").Update(ctx, "backup", false); err != nil {
		panic(err)
	}
	if err := ensureLogCleanupPolicyConfig(ctx); err != nil {
		panic(err)
	}
	if err := ensureModelAutofillPolicyConfig(ctx); err != nil {
		panic(err)
	}
	if err := MigratePeakTermsToAssociations(ctx); err != nil {
		panic(err)
	}
	zero := 0.0
	if _, err := gorm.G[ModelWithProvider](DB).Where("input_price IS NULL").Update(ctx, "input_price", &zero); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ModelWithProvider](DB).Where("cache_read_price IS NULL").Update(ctx, "cache_read_price", &zero); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ModelWithProvider](DB).Where("output_price IS NULL").Update(ctx, "output_price", &zero); err != nil {
		panic(err)
	}
	if _, err := gorm.G[ModelWithProvider](DB).Where("currency = '' OR currency IS NULL").Update(ctx, "currency", "CNY"); err != nil {
		panic(err)
	}
	// 放在 Init 的最后一步：它要建索引，真机上是一次秒级的全表读。
	// **失败只记日志**——一条索引没建上只是慢，不该让服务起不来。
	if err := EnsureChatIOLogIDIndex(ctx); err != nil {
		slog.Error("chat_ios.log_id 索引没建上（回填响应体将退化为全表扫描）", "error", err)
	}

	// `DB_VACUUM` 的启动期 VACUUM **搬去 service.PrepareStorage 了**（main 里、
	// 监听端口之前调用）。搬家的理由是它原先 `panic(err)`，而且连磁盘够不够都
	// 没看过：VACUUM 要一份和库等大的副本，在 7 GiB 的库上磁盘不够就是启动失败。
	// 现在它与 auto_vacuum 转换合并成一次动作、动手前预检、失败只跳过。
	// 顺带解掉一个分层问题：`diskFree` 在 service 包里，models 不能反向依赖它。
}

// chatIOLogIDIndexName / chatIOLogIDIndexSQL 是 chat_ios.log_id 的索引名与建法。
//
// **必须是普通索引，不能是 partial（WHERE deleted_at IS NULL）**：查询条件里
// 确实带着 `deleted_at IS NULL`，看上去 partial 更省空间，但 log_id 在
// chat_ios 里是**事实上的唯一键**（一个 log 一行 IO），行数不比表小多少；
// 而 partial 索引在这份语料上（软删占比极低）省不下什么，却让 `log_id = ?`
// 失去等值查找的唯一入口。普通索引在 `log_id = ?` 上是 SEARCH，
// deleted_at 退化为建索引后的行内过滤，代价是常数。
const (
	chatIOLogIDIndexName = "idx_chat_ios_log_id"
	chatIOLogIDIndexSQL  = `CREATE INDEX IF NOT EXISTS idx_chat_ios_log_id ON chat_ios (log_id)`
)

// EnsureChatIOLogIDIndex 给 chat_ios.log_id 建索引。可重复调用。
//
// **为什么它不是一条 `gorm:"index"` 标签。** 先纠正一个容易想当然的点：
// AutoMigrate 对索引走的是 HasIndex → CreateIndex（gorm v1.30 migrator），
// 不会触发 SQLite 的"建新表-拷数据-删旧表-改名"——那只发生在列类型 /
// 可空性 / 主键变化时。所以标签本身并不危险。真正的差别有两条：
//
//  1. 标签建索引失败会让 AutoMigrate 返回错误，而 Init 对迁移错误是 panic。
//     一条索引没建上只该让回填变慢，不该让服务起不来——这里失败只记日志。
//  2. 标签只能表达"名字存在就跳过"，管不了**同名但定义不同**的存量索引
//     （例如早先手工建的 partial 索引）：那种索引会被当成"已存在"直接放行，
//     线上那条 UPDATE 继续全表扫，而且没有任何告警。这里会核对索引列与
//     partial 标记，不符就删掉重建。
//
// 为什么它仍然在启动路径上：这条 UPDATE 跑在**每个开了 io_log 的请求**的
// 响应回填里（service.chat.updateChatIOOutput）。没有索引时 SQLite 会挑
// `idx_chat_ios_deleted_at`——而软删极少发生，那个索引命中整张表，
// 真机单次实测 **0.9 秒**（5895ms 那条 SLOW SQL 就来自这里），
// 有索引后是同一条语句 0.000 秒。把它留到"以后某次维护"就是让每个请求
// 继续等 0.9 秒换一次启动期 2.9 秒，这笔账不成立。
//
// 建索引期间持写锁。调用点在 Init 里（监听端口之前），此刻没有在途请求——
// 与 service.PrepareStorage 的 VACUUM 是同一条时序前提。
func EnsureChatIOLogIDIndex(ctx context.Context) error {
	ok, err := chatIOLogIDIndexMatches(ctx)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	// 走到这里只有两种情况：索引不存在，或同名但定义不对。后者必须删掉
	// 重建——`CREATE INDEX IF NOT EXISTS` 遇到同名索引会直接跳过。
	if err := DB.WithContext(ctx).Exec("DROP INDEX IF EXISTS " + chatIOLogIDIndexName).Error; err != nil {
		return err
	}
	return DB.WithContext(ctx).Exec(chatIOLogIDIndexSQL).Error
}

// chatIOLogIDIndexMatches 判断既有索引是不是我们想要的那条：
// 名字对、只索引 log_id 一列、且不是 partial。
//
// 只查名字是不够的：同名但列序不同（如 `(deleted_at, log_id)`）或带 WHERE
// 的索引对 `log_id = ? AND deleted_at IS NULL` 未必可用，而那种库上
// 查询不会报错、只会静默退化。
func chatIOLogIDIndexMatches(ctx context.Context) (bool, error) {
	var indexes []struct {
		Name    string
		Partial int
	}
	if err := DB.WithContext(ctx).Raw("PRAGMA index_list('chat_ios')").Scan(&indexes).Error; err != nil {
		return false, err
	}
	for _, idx := range indexes {
		if idx.Name != chatIOLogIDIndexName {
			continue
		}
		if idx.Partial != 0 {
			return false, nil
		}
		var cols []struct {
			Name string
		}
		if err := DB.WithContext(ctx).Raw("PRAGMA index_info('" + chatIOLogIDIndexName + "')").Scan(&cols).Error; err != nil {
			return false, err
		}
		return len(cols) == 1 && cols[0].Name == "log_id", nil
	}
	return false, nil
}

// newGormLogger 构造 SQL 日志。它与 gorm 默认值只差两处，但两处都是必要的：
//
//  1. **IgnoreRecordNotFoundError=true。** 默认是 false，于是每一次"按主键/唯一键
//     查一行、没查着"都会打一条 record not found + 慢 SQL 提示。而本仓有多处
//     **把缺行当成正常态**的读：策略配置缺行回默认值、按名字查模型未命中就走
//     回退、按 log_id 查 IO 但这条日志本来就没开 io_log。它们不是错误，
//     也没有调用方会把它们当错误处理，只是日志噪音——每分钟一到两条，
//     长期盖住真正要看的东西。
//
//     注意这只影响**日志**：错误照样原样返回给调用方，`errors.Is(err,
//     gorm.ErrRecordNotFound)` 的分支行为一个字节都没变。真正该看的是
//     "表不存在/库读不通"这类，它们不是 ErrRecordNotFound，照常打印。
//
//  2. **输出走 slog，不走 stdlib log。** 默认 logger 直接写 os.Stdout 并自带
//     时间戳，而本仓其余日志全是 slog（main.go、service 各包）。两套日志各打
//     一份格式，时间戳也对不齐。接进 slog 之后级别、时间戳、输出目标都统一。
//
// SlowThreshold 保持 gorm 默认的 200ms：慢 SQL 是**真的**要看见的东西，
// 它正是这次 chat_ios 那条 5895ms UPDATE 的发现路径，不该被一起关掉。
func newGormLogger() logger.Interface {
	return newGormLoggerWith(slogWriter{})
}

// newGormLoggerWith 与 newGormLogger 是**同一份配置**，只是输出目的地可换。
//
// 这样测试就能断言"这份配置自己的行为"，而不是在测试里再抄一份
// logger.Config——抄一份的话，改 newGormLogger 不会让任何用例变红。
func newGormLoggerWith(out logger.Writer) logger.Interface {
	return logger.New(out, logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// slogWriter 把 gorm logger 的每一行接到 slog.Warn。
//
// gorm 的日志行自带级别语义（它已经在 Trace 里按 Error/Warn/Info 分好了），
// 但 writer 接口只拿到字符串，分不出来。统一走 Warn 是刻意的：这个 logger
// 的 LogLevel 已经是 Warn，能到这里的只有"需要看一眼"的行。
type slogWriter struct{}

func (slogWriter) Printf(format string, args ...any) {
	slog.Warn(strings.TrimRight(fmt.Sprintf(format, args...), "\n"))
}

func ensureModelDisplayOrder(ctx context.Context) error {
	needAssign, err := gorm.G[Model](DB).
		Where("display_order = 0 OR display_order IS NULL").
		Order("id ASC").
		Find(ctx)
	if err != nil {
		return err
	}
	if len(needAssign) == 0 {
		return nil
	}

	var currentMax int
	if err := DB.Model(&Model{}).
		Select("COALESCE(MAX(display_order), 0)").
		Scan(&currentMax).Error; err != nil {
		return err
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		nextOrder := currentMax
		for _, model := range needAssign {
			nextOrder++
			if err := tx.WithContext(ctx).
				Model(&Model{}).
				Where("id = ?", model.ID).
				Update("display_order", nextOrder).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func ensureLogCleanupPolicyConfig(ctx context.Context) error {
	count, err := gorm.G[Config](DB).Where("key = ?", KeyLogCleanupPolicy).Count(ctx, "*")
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	defaultLogCleanupPolicy, err := json.Marshal(LogCleanupPolicy{
		Enabled:       false,
		RetentionDays: 30,
	})
	if err != nil {
		return err
	}

	config := Config{
		Key:   KeyLogCleanupPolicy,
		Value: string(defaultLogCleanupPolicy),
	}
	if err := gorm.G[Config](DB).Create(ctx, &config); err != nil {
		return err
	}

	return nil
}

// ensureModelAutofillPolicyConfig 建一行默认的自动填写策略。
//
// 与日志清理那次不同，这里写默认值不只是"省一次判空"：**Enabled 默认是开**，
// 而"没配过"与"配成开"在数据库里长得一模一样。落一行显式的配置，配置页
// 与弹窗读到的就是同一份事实，"当前状态：已启用"这句话才有出处。
//
// 注意它**不覆盖已存在的行**：用户关掉之后重启服务不该把它又打开。
func ensureModelAutofillPolicyConfig(ctx context.Context) error {
	count, err := gorm.G[Config](DB).Where("key = ?", KeyModelAutofillPolicy).Count(ctx, "*")
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	value, err := json.Marshal(ModelAutofillPolicy{
		Enabled:         true,
		Overwrite:       false,
		AllowDeprecated: false,
		Sources:         consts.ModelMetaDefaultSources,
	})
	if err != nil {
		return err
	}

	config := Config{
		Key:   KeyModelAutofillPolicy,
		Value: string(value),
	}
	return gorm.G[Config](DB).Create(ctx, &config)
}

// sqliteDSN 在库路径上追写驱动参数。
//
// _txlock=immediate：SQLite 的 deferred 事务是"先拿 SHARED、写的时候再升级成
// RESERVED"。两个并发写事务同时走到升级那一步时会互等，SQLite 为避免死锁直接
// 返回 SQLITE_BUSY（**不走 busy_timeout，也没有等待**），于是并发聊天请求的 IO
// 写入成片失败。BEGIN IMMEDIATE 在事务开头就把 RESERVED 拿到手，并发写者改由
// busy_timeout 排队，而不是撞死锁。读路径不受影响：GORM 的单条查询不开事务。
func sqliteDSN(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_txlock=immediate"
}

func ensureDBFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
