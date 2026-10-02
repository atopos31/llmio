package models

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/atopos31/llmio/pkg/compress"
	"github.com/atopos31/llmio/pkg/env"
	"gorm.io/gorm"
)

// 本文件是 `chat_ios.input` 这一列的接入层：写的时候分块进块表、留一串引用，
// 读的时候按引用还原。它和 `of_string` / `of_string_array` 那两列走的是
// **两条不同的路**，这一点是刻意的：
//
//   - 那两列是纯值变换，GORM 的 serializer 就够（`serializer.go`）。
//   - `input` 要额外读写块表，**必须拿到 DB 句柄**才能把块写进同一个事务。
//     serializer 的接口里没有句柄，另开连接会撞事务锁——实测卡满 5 秒的
//     `BUSY_TIMEOUT` 才报错，`maxOpen=1` 时直接自死锁。所以只能走模型钩子。
//
// 三条硬规则与 serializer 一致，顺序即优先级：
//  1. **压不了就存明文**。压缩是纯优化，丢一个字节就是失败。
//  2. 明文写成 TEXT（string），只有帧写成 BLOB（[]byte）。
//  3. 读只认帧头：不是帧就当明文，是帧但解不开就**报错**。

// DefaultBlockCacheBytes 是组缓存的默认容量。
//
// 32 MiB 是 Phase 0 实测选的：顺序浏览（日志界面真正在做的事）命中率 98.7%、
// P50 延迟归零。但要清楚它保的是顺序浏览——随机访问时组表本身就有 57–64 MiB，
// 32 MiB 只覆盖得了一半。
const DefaultBlockCacheBytes = 32 << 20

// inputBlockMin 是走块表的最小 body。
//
// 低于它的 body 直接逐行压成一个帧：分块器对 4 KiB 以下的输入本来也只切出
// 一块，走块表要多付一条引用序列的固定开销加一行块表记录，比它省下来的还多。
// 阈值取 BlockChunkAvg 是同一件事的两面——比平均块还小的 body 分不出块来。
const inputBlockMin = BlockChunkAvg

// blockStore 是包级单例。
//
// 必须是单例：组缓存的价值全在**跨请求复用**上。每个请求各建一个，
// 缓存就永远命中不了，等于白解压一遍。
var blockStore = NewBlockStore(DefaultBlockCacheBytes)

// compressionEnabled 是总开关。默认开。
//
// 关掉它**只影响新写入的行**：已经压过的行照常读得回来，因为读路径只认
// 帧头，不认任何配置。这正是"帧自描述"换来的回退能力——运维事故当场把开关
// 关掉即可，不需要反向迁移，也不需要重建库。
func compressionEnabled() bool {
	return env.GetWithDefault("DB_COMPRESS", true)
}

// BodyBytes 是 body 列的存储形态。
//
// 它存在的唯一理由是**让"库里是 TEXT 还是 BLOB"由内容自己决定**：明文写成
// TEXT、帧写成 BLOB。这条不变量很值钱——迁移时一句 `WHERE typeof(input)='text'`
// 就能挑出还没迁的行，而 typeof 只读记录的类型头、不读载荷，全表扫也很便宜。
// 丢掉它的话，就只能把每一行的载荷前几个字节都读出来比对。
type BodyBytes []byte

var (
	_ driver.Valuer = BodyBytes(nil)
	_ sql.Scanner   = (*BodyBytes)(nil)
)

// Value 把内存里的字节按"是帧还是明文"分别落成 BLOB / TEXT。
//
// 注意它**不做压缩**：压不压、怎么压是钩子的决定，这里只管按已经定好的形态
// 落库。两件事混在一起的话，"值可能被取两次"就不再是幂等的了。
func (b BodyBytes) Value() (driver.Value, error) {
	if b == nil {
		return nil, nil
	}
	if compress.LooksLikeFrame(b) {
		return []byte(b), nil
	}
	return string(b), nil
}

// Scan 收驱动给的字节。三种形态都要认：BLOB（帧）、TEXT（历史明文）、NULL。
func (b *BodyBytes) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*b = nil
	case []byte:
		*b = append((*b)[:0], v...)
	case string:
		*b = append((*b)[:0], v...)
	default:
		return fmt.Errorf("compress: input 列拿到驱动类型 %T，只认 text / blob / null", src)
	}
	return nil
}

// String 只为日志与调试。它不参与任何存储决策。
func (b BodyBytes) String() string { return string(b) }

// ── 钩子 ──────────────────────────────────────────────────────────────────

// BeforeCreate 在插入前把 body 换成"引用序列"或"逐行帧"。
//
// **必须是 BeforeCreate，不能是 BeforeSave。** 写路径的第二半是
// `service/chat.go` 里的
//
//	Updates(ctx, models.ChatIO{OutputUnion: *output})
//
// 它只为补上响应体。BeforeSave 在这里也会触发，而钩子拿到的 Input 是零值——
// 若把"零值"当成"这份 body 没有可去重的内容"写回去，就会**把已经存好的请求体
// 覆盖掉**。GORM 的 Updates(struct) 靠零值跳过恰好挡住了这一下，但那是
// GORM 的实现细节，不该拿来做数据安全的前提。BeforeCreate 只在插入时触发，
// 这一类问题从定义上就不存在。
func (c *ChatIO) BeforeCreate(tx *gorm.DB) error {
	return c.packBody(tx)
}

// AfterFind 在查出后把引用还原成原始 body。
//
// 历史明文行不经任何处理直接放行，所以迁移可以随时中断、分批做，不需要
// 一次性切换。
func (c *ChatIO) AfterFind(tx *gorm.DB) error {
	return c.unpackBody(tx)
}

// packBody 是写路径。
func (c *ChatIO) packBody(tx *gorm.DB) error {
	plain := c.Input
	// 空值不动：保持 NULL/TEXT 与压缩前一致，别为了"顺便压一下"改变历史行的
	// typeof 分布。已经是帧也不动——Value 可能被调第二次（GORM 的慢查询日志
	// 会对 driver.Valuer 再取一次值），幂等要求"同一份输入必得同一份输出"。
	if len(plain) == 0 || compress.LooksLikeFrame(plain) {
		return nil
	}
	if !compressionEnabled() {
		return nil
	}
	ctx := tx.Statement.Context

	if len(plain) >= inputBlockMin {
		refs, err := blockStore.Put(ctx, tx, plain)
		if err != nil {
			return fmt.Errorf("compress: input 入块表失败: %w", err)
		}
		if len(refs) > 0 {
			if frame := EncodeRefs(refs); frame != nil {
				c.Input = frame
				return nil
			}
			// 引用序列过不了自检。此时块已经写进本事务了，但这一行仍要存下来——
			// 丢 body 比留几个孤儿块严重得多。孤儿由块表 GC 兜底回收。
			// 真走到这里说明压缩代码本身有问题，不是数据有问题。
		}
	}

	frame, ok := compress.EncodeVerified(plain, compress.TypeRowFrame, compress.LevelWrite)
	if !ok || len(frame) >= len(plain) {
		return nil // 压不动就存明文
	}
	c.Input = frame
	return nil
}

// unpackBody 是读路径。
func (c *ChatIO) unpackBody(tx *gorm.DB) error {
	stored := c.Input
	if len(stored) == 0 {
		return nil
	}
	f, err := compress.Unmarshal(stored)
	if errors.Is(err, compress.ErrNotFrame) {
		return nil // 历史明文，原样放行
	}
	if err != nil {
		// 帧头合法但我们解不了（未知编解码器）——报错，绝不退回明文。
		return fmt.Errorf("compress: chat_ios.input 的帧头不可信: %w", err)
	}
	// 块表查询要用一个**干净的 statement**：此刻外层 Find 的 statement 还在
	// 飞行中，直接复用会把它冲掉。Session(NewDB) 保留同一个 ConnPool 与
	// context（所以事务里读得到自己的未提交数据），只换 statement。
	db := tx.Session(&gorm.Session{NewDB: true})

	switch f.Type {
	case compress.TypeBlockRefs:
		refs, err := DecodeRefsFrame(f)
		if err != nil {
			return fmt.Errorf("compress: chat_ios.input 的引用序列坏了: %w", err)
		}
		plain, err := blockStore.Get(tx.Statement.Context, db, refs)
		if err != nil {
			return err
		}
		c.Input = plain
		return nil
	case compress.TypeRowFrame:
		plain, err := compress.Decompress(f)
		if err != nil {
			return fmt.Errorf("compress: chat_ios.input 解压失败: %w", err)
		}
		c.Input = plain
		return nil
	default:
		return fmt.Errorf("compress: chat_ios.input 里装着 %s 帧，这一列不装这种", f.Type)
	}
}
