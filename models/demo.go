package models

import (
	"context"
)

// IsEmptyDB 判断这个库是不是"全新、还没人用过"。
//
// 判据取三张核心表：只要其中任何一张有行，这个库就有主人的数据，不算空。
// 之所以不只看供应商表，是因为演示数据（见 main 包的 demoSeed）要能安全地
// 幂等：若上次灌到一半、或使用者自己建了模型又重启，都不该被当成空库重来一遍。
//
// 只读，不开事务：调用点在启动路径上，此刻还没有并发者。
func IsEmptyDB(ctx context.Context) bool {
	db := DB.WithContext(ctx)
	for _, model := range []any{&Provider{}, &Model{}, &AuthKey{}} {
		var n int64
		if err := db.Model(model).Limit(1).Count(&n).Error; err != nil {
			// 数不出来时保守认为"非空"：宁可少灌一次演示数据，
			// 也不要在一个已有数据的库上再灌一遍。
			return false
		}
		if n > 0 {
			return false
		}
	}
	return true
}
