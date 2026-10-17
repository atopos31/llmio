package models

import (
	"testing"
)

// SkipCompress 是撞锁时的降级开关：这一笔 IO 记录退回明文，不写块表。
//
// 线上教训：并发写把库锁住时，入块表失败被当成整条记录失败，于是一次成功的
// 聊天被标成 error。压缩是优化，降级必须无损（正文逐字节保留）且不动块表。
func TestChatIO_SkipCompressStoresPlaintext(t *testing.T) {
	db := openCompressDB(t)
	before := blockCount(t, db)

	raw := []byte(bigText(2000)) // 远超 inputBlockMin，正常路径会走块表
	rec := &ChatIO{LogId: 1, Input: BodyBytes(raw), SkipCompress: true}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("写 IO 失败：%v", err)
	}

	// 库里必须是明文（typeof=text），而不是帧（blob）。
	var kind string
	if err := db.Raw("SELECT typeof(input) FROM chat_ios WHERE id = ?", rec.ID).Row().Scan(&kind); err != nil {
		t.Fatalf("读 typeof 失败：%v", err)
	}
	if kind != "text" {
		t.Fatalf("降级后应为 text，实际 %q", kind)
	}
	if got := blockCount(t, db); got != before {
		t.Fatalf("降级后不应新增块，%d → %d", before, got)
	}

	// 读回来必须逐字节一致（AfterFind 对明文原样放行）。
	var back ChatIO
	if err := db.First(&back, rec.ID).Error; err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if string(back.Input) != string(raw) {
		t.Fatalf("降级后正文不一致：%d vs %d 字节", len(back.Input), len(raw))
	}
}

// 反过来钉住默认路径：不开 SkipCompress 时同一份内容必须真的进块表，
// 否则上面那条测试可能因为"本来就压不动"而空转通过。
func TestChatIO_DefaultPathUsesBlocks(t *testing.T) {
	db := openCompressDB(t)
	before := blockCount(t, db)

	raw := []byte(bigText(2000))
	rec := &ChatIO{LogId: 2, Input: BodyBytes(raw)}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("写 IO 失败：%v", err)
	}
	if got := blockCount(t, db); got <= before {
		t.Fatalf("默认路径应写入块表，块数没有增加：%d → %d", before, got)
	}
	var kind string
	if err := db.Raw("SELECT typeof(input) FROM chat_ios WHERE id = ?", rec.ID).Row().Scan(&kind); err != nil {
		t.Fatalf("读 typeof 失败：%v", err)
	}
	if kind != "blob" {
		t.Fatalf("默认路径应落帧（blob），实际 %q", kind)
	}
}
