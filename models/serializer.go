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
//  2. 明文写成 string（TEXT），只有帧写成 []byte（BLOB）。
//     于是"库里的 blob 一定是帧"成了一条可审计的不变量——
//     排查时一条 `typeof(col)` 就能看出哪些行还没迁。
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
	// 空值的存储形态与压缩前完全一致：字符串列存 ''，切片列存 NULL。
	// 不为了"顺便压一下"去改变空值的类型，否则历史行的 typeof 分布会被搅乱。
	if len(plain) == 0 {
		if field.FieldType.Kind() == reflect.String {
			return "", nil
		}
		return nil, nil
	}
	// 已经是帧就原样写回。正常路径不会遇到（内存里的字段值是明文），
	// 但 Value 可能被调第二次，让它对"已经被压过的值"也稳定。
	if compress.LooksLikeFrame(plain) {
		return plain, nil
	}
	frame, ok := compress.EncodeVerified(plain, compress.TypeRowFrame, compress.LevelWrite)
	if !ok {
		// INV-3 没过：解回来的和原文对不上。退回明文，绝不写这帧。
		return string(plain), nil
	}
	return frame, nil
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
