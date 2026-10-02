package models

import (
	"log/slog"

	"github.com/atopos31/llmio/pkg/env"
	"gorm.io/gorm"
)

// 本文件是**存储层**：`auto_vacuum` 与增量回收。它和压缩迁移是两件事，
// 但只有在一起才完整——
//
//	迁移把 470 KiB 的明文行换成几十字节的引用帧，省下的是**页**；
//	而 SQLite 默认（auto_vacuum=0）只把页还进 freelist，**文件一字节都不会缩**。
//	实测：一份 7.05 GiB 的库迁完，文件仍是 7.05 GiB，直到 VACUUM。
//
// 所以"压缩省了 5.6 GiB"这句话在 auto_vacuum=0 下只对**页**成立，对**文件**不成立。

// auto_vacuum 的三个取值就是 SQLite PRAGMA 的定义，别改。
const (
	// AutoVacuumNone 是 SQLite 的默认值：文件只会涨，freelist 里的页永不归还。
	AutoVacuumNone = 0
	// AutoVacuumFull 每次提交都整理并截断。**对 llmio 是错的**：每个请求都写一行
	// 日志，等于每个请求都付一次整库整理的代价。
	AutoVacuumFull = 1
	// AutoVacuumIncremental 维护 ptrmap，只在被要求时截断。这是要的那个。
	AutoVacuumIncremental = 2
)

// StorageRebuildEnv 控制"既有库要不要在启动时重建以启用增量回收"。
//
//	off  —— 一概不碰 auto_vacuum
//	auto —— （默认）只在**空库**上设。既有库一个字节都不动
//	on   —— 既有库也在启动时重建转换（一次 VACUUM，分钟级、要 2× 库大小的空闲）
//
// 默认 auto 而不是 on，是被"最老的库"这件事逼出来的：一份 7 GiB 的老库启动时
// 突然做起 60 秒的 VACUUM，既占 2× 磁盘又让服务晚 60 秒可用，而它可能只是想
// 重启一下。**转换是一次运维动作，不是一次启动副作用。**
const StorageRebuildEnv = "DB_AUTO_VACUUM_REBUILD"

// DBVacuumEnv 是既有的"启动时 VACUUM"开关，语义保持不变。
const DBVacuumEnv = "DB_VACUUM"

// StorageRebuildMode 读出重建模式，非法值一律当 auto（保守）。
func StorageRebuildMode() string {
	switch v := env.GetWithDefault(StorageRebuildEnv, "auto"); v {
	case "off", "on":
		return v
	default:
		// 拼错的值（"true"、"yes"…）也走这里。默认必须是**边界最小的那个**：
		// 一个拼错的环境变量不该让 7 GiB 的老库在启动时重建。
		return "auto"
	}
}

// ReadAutoVacuum 读当前库的 auto_vacuum。
func ReadAutoVacuum(db *gorm.DB) (int64, error) {
	var v int64
	if err := db.Raw(`PRAGMA auto_vacuum`).Row().Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// PrepareEmptyStorage 只对**空库**设 auto_vacuum=INCREMENTAL。
//
// 为什么它必须在 AutoMigrate **之前**被调用：pragma 要在第一张表建出来之前
// 生效，ptrmap 才会跟着表一起长出来；表建完再设就晚了。
//
// 而"晚了"这件事比想象的严重得多，实测过：**非空库上设这一句会被静默忽略**
// ——同一条连接上立刻读回来还是 0，换一条连接读也是 0，连文件头都没写。
// 也就是说，它没有任何"设错了"的风险，只是没生效而已；真正能转换的只有
// VACUUM（见 service.PrepareStorage）。这条实测同时是**老库的安全保证**：
// 就算这段代码哪天被误放到既有库上，也只是白跑一句 SQL。
//
// 返回值只用于日志与测试，启动流程不因它失败——存储层的优化没有资格
// 让服务起不来。
func PrepareEmptyStorage(db *gorm.DB) (applied bool, err error) {
	if StorageRebuildMode() == "off" {
		return false, nil
	}

	var pages int64
	if err := db.Raw(`PRAGMA page_count`).Row().Scan(&pages); err != nil {
		return false, err
	}
	// 有页就说明这是既有库。**一个字节都不碰**——老库的迁移路径必须原样不动。
	if pages > 0 {
		return false, nil
	}
	if err := db.Exec(`PRAGMA auto_vacuum = 2`).Error; err != nil {
		return false, err
	}
	slog.Info("storage: 空库已设 auto_vacuum=INCREMENTAL", "pages", pages)
	return true, nil
}
