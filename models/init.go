package models

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/atopos31/llmio/consts"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

// DBPath 是当前库文件的位置。压缩状态页要报库文件大小与 freelist，
// 前者只能从文件系统量（SQLite 的 page_count*page_size 不含 WAL 与页头开销）。
var DBPath string

func Init(ctx context.Context, path string) {
	if err := ensureDBFile(path); err != nil {
		panic(err)
	}
	db, err := gorm.Open(sqlite.Open(path))
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

	// `DB_VACUUM` 的启动期 VACUUM **搬去 service.PrepareStorage 了**（main 里、
	// 监听端口之前调用）。搬家的理由是它原先 `panic(err)`，而且连磁盘够不够都
	// 没看过：VACUUM 要一份和库等大的副本，在 7 GiB 的库上磁盘不够就是启动失败。
	// 现在它与 auto_vacuum 转换合并成一次动作、动手前预检、失败只跳过。
	// 顺带解掉一个分层问题：`diskFree` 在 service 包里，models 不能反向依赖它。
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
