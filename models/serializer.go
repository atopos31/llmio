package models

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/atopos31/llmio/pkg/compress"
	"gorm.io/gorm/schema"
)

// serializerCompress 是 body 列压缩序列化器的注册名（`gorm:"serializer:compress"`）。
const serializerCompress = "compress"

func init() {
	schema.RegisterSerializer(serializerCompress, compressSerializer{})
}

// compressSerializer 让 body 列在库里以压缩帧存放、在 Go 侧仍是原本的类型。
//
// 它只做**纯值变换**，不需要数据库句柄——这正是它与模型钩子的分工依据：
// input 列要读写块表，只能走钩子；这两列是值进值出，序列化器就够。
// 见 docs/db-compression-safety.md §1.3 陷阱 B。
//
// 三条硬规则，顺序即优先级：
//  1. **压不了就存明文**。压缩是纯优化，丢一个字节就是失败。
//  2. 明文写成 string（TEXT）、帧写成 []byte（BLOB）、**空值写成 NULL**。
//     三者与 `typeof` 一一对应，于是"这一列迁没迁过"是一个**只读记录头**就能
//     回答的问题（见下面 Value 里那段空值说明）。
//  3. 读的时候只认帧头：不是帧就当明文，是帧但解不开就**报错**。
//     退回明文等于把二进制垃圾当成用户数据吐出去。
type compressSerializer struct{}

// Value 把字段值编成帧；空值、压不动、自检不过都退回明文。
//
// 它必须幂等：GORM 默认 logger 在慢查询或报错时会对 driver.Valuer 再取一次值
// （logger/sql.go 的 ExplainSQL），副作用跑两遍在这里只是多花一点 CPU——
// 前提是"同一份输入必得同一份输出"，这条由 compress 的确定性保证。
func (compressSerializer) Value(ctx context.Context, field *schema.Field, dst reflect.Value, fieldValue interface{}) (interface{}, error) {
	plain, err := plainBytes(fieldValue)
	if err != nil {
		return nil, fmt.Errorf("compress: %s 列无法序列化: %w", field.DBName, err)
	}
	// 空值一律写 NULL。这不是"与压缩前保持一致"（压缩前字符串列存的是 `''`），
	// 而是刻意立的一条不变量：**这三列里 TEXT 只表示"非空明文"**，
	// 于是 `typeof` 一个信号就把明文 / 帧 / 空三者分开了。
	//
	// 为什么值得改存储形态：只要 TEXT 还兼着"空"这个含义，"挑出还没迁的行"
	// 就非得读一遍载荷去量长度不可。真机上 `of_string` 有 8,932 行是 `''`
	// （流式响应那一批：正文在 `of_string_array` 里），而那条带 `length()` 的
	// 判据在 7.6 GiB 的库上要跑 4.2 秒——`journal_mode=delete` 下读事务挡写，
	// 聊天请求等锁 5 秒超时，整站 500。判据换成纯 `typeof` 之后是 0.076 秒。
	//
	// 读路径不受影响：`Scan(NULL)` 把字段归零，字符串列读出来仍然是 `""`。
	if len(plain) == 0 {
		return nil, nil
	}
	// 已经是帧就原样写回。正常路径不会遇到（内存里的字段值是明文），
	// 但 Value 可能被调第二次，让它对"已经被压过的值"也稳定。
	if compress.LooksLikeFrame(plain) {
		return plain, nil
	}
	if frame := PackColumnValue(plain); frame != nil {
		return frame, nil
	}
	// 压不动（或 INV-3 没过：解回来的和原文对不上）就存明文。绝不写这帧。
	return string(plain), nil
}

// PackColumnValue 把一列的明文编成帧，压不动返回 nil。
//
// 它是"明文 → 帧"这件事的**唯一实现**，序列化器（写路径）与迁移（回填历史行）
// 共用同一份——两处各写一遍的话，"压不动就存明文"这类规则迟早会在其中一侧漂移，
// 而漂移的方向是数据损坏。
//
// 调用方看到 nil 时该做什么，两种场景不同，所以这里不替它们决定：
//   - 序列化器：退回**明文**（`string(plain)`），因为它的职责是把值写下去；
//   - 迁移：**这一列不动**，因为列里此刻的字节本来就是可读的。
//
// 空值与"已经是帧"都返回 nil。迁移的候选判据已经把这两种排除在外了，
// 但保留这两道自己判：`BeforeSave` 那条路曾经因为"假设调用方已经判过"
// 而把零值写回过（见 body.go 顶部），同一类假设不值得再赌一次。
//
// `len(frame) >= len(plain)` 这条与 `PackBodies` 里那句**逐字同源**：压完更大
// 就不压。flate 对一小段随机字节会输出比原文还长的流（光头部就十来字节），
// 不挡的话库里会凭空多出一批"越压越大"的行。压缩是纯优化，不划算就不做。
func PackColumnValue(plain []byte) []byte {
	if len(plain) == 0 || compress.LooksLikeFrame(plain) {
		return nil
	}
	frame, ok := compress.EncodeVerified(plain, compress.TypeRowFrame, compress.LevelWrite)
	if !ok || len(frame) >= len(plain) {
		return nil
	}
	return frame
}

// UnpackColumnValue 把一列此刻的字节还原成明文，**不是帧就返回 nil**。
//
// nil 的含义是"这一列不用动"，与出错区分得很开：历史明文行是正常情况，
// 调用方只有拿到 error 才该停下来。这条区分是回滚路径的安全阀——把明文
// 当帧解、或者把帧当明文写回去，两种都是静默损坏，而它们在这里各有一个
// 明确的返回值形态挡着。
func UnpackColumnValue(stored []byte) ([]byte, error) {
	plain, wasFrame, err := compress.DecompressBytes(stored)
	if err != nil {
		return nil, err
	}
	if !wasFrame {
		return nil, nil
	}
	return plain, nil
}

// Scan 把库里的字节还原成字段值。dbValue 可能是 []byte（BLOB）、
// string（TEXT，历史明文）或 nil（NULL），三种都要认。
func (compressSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue interface{}) error {
	stored, err := storedBytes(dbValue)
	if err != nil {
		return err
	}
	if stored == nil {
		field.ReflectValueOf(ctx, dst).Set(reflect.Zero(field.FieldType))
		return nil
	}
	plain, _, err := compress.DecompressBytes(stored)
	if err != nil {
		// 是我们造的帧、但解不开。必须报错——静默当明文返回就是数据损坏。
		return fmt.Errorf("compress: %s 列解压失败: %w", field.DBName, err)
	}
	return setDecoded(ctx, field, dst, plain)
}

// plainBytes 把字段值取成明文 JSON 形态。
func plainBytes(fieldValue interface{}) ([]byte, error) {
	switch v := fieldValue.(type) {
	case string:
		return []byte(v), nil
	case []string:
		if v == nil {
			// 与 GORM 内置的 json 序列化器一致：nil 切片存 NULL，而不是 "null"
			return nil, nil
		}
		return json.Marshal(v)
	default:
		return nil, fmt.Errorf("不支持的类型 %T", fieldValue)
	}
}

// storedBytes 把驱动给的值取成字节。这两个列在 SQLite 里只可能是 text / blob，
// 其余类型按 GORM 内置 json 序列化器的同款办法兜底成 JSON 文本，
// 再走一遍判帧逻辑，而不是直接失败。
func storedBytes(dbValue interface{}) ([]byte, error) {
	switch v := dbValue.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return json.Marshal(v)
	}
}

// setDecoded 把明文写回字段。字段类型不在这儿猜：Go 类型就是契约，
// 不是 []string 就该报错，而不是当字符串塞进去。
func setDecoded(ctx context.Context, field *schema.Field, dst reflect.Value, plain []byte) error {
	target := field.ReflectValueOf(ctx, dst)
	switch field.FieldType.Kind() {
	case reflect.String:
		target.SetString(string(plain))
	case reflect.Slice:
		out := reflect.New(field.FieldType)
		if len(plain) > 0 {
			if err := json.Unmarshal(plain, out.Interface()); err != nil {
				return fmt.Errorf("compress: %s 列不是合法 JSON: %w", field.DBName, err)
			}
		}
		target.Set(out.Elem())
	default:
		return fmt.Errorf("compress: %s 列的类型 %s 不受支持", field.DBName, field.FieldType)
	}
	return nil
}
