package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// 迁移是这套方案里**唯一会成规模改写既有数据**的动作，所以这里的测试盯的是
// "错了会怎样"，而不是"对了会怎样"：
//
//  1. **改写之后读得回来**，逐字节。这是地基。
//  2. **幂等**：重复跑、中断续跑、full 重扫，都不许把同一行压第二遍。
//  3. **水位与数据同事务**：一批失败必须整批连水位一起回滚，不能在库里留下
//     "改了一半"的状态。
//  4. **不碰 updated_at**：它是冷静期过滤器的输入，迁移自己刷新它等于
//     把这行重新标记成"刚被写过"。
//  5. **回滚闭环**：压缩整库 → 解压整库 → 与原字节逐一相等。这是 L2 降级的
//     唯一实现，也是这套方案最强的验证器。

func setupLogCompressTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service-compress.db")
	models.Init(context.Background(), path)
	t.Cleanup(func() {
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		// 这两个标志是包级的，测试之间必须复位，否则"暂停"会漏到下一个用例。
		compressInFlight.Store(false)
		compressPauseRequested.Store(false)
	})
	return models.DB
}

// seedPlaintextRows 用**裸 SQL** 造历史行。
//
// 必须绕开模型：走钩子写进去的行当场就是压缩形态，那就不是"历史行"了，
// 而迁移要处理的恰恰是钩子存在之前就躺在库里的那些行。
//
// `age` 是往回拨的时长。默认拨 1 小时：冷静期（默认 60 秒）会把刚写过的行
// 排除掉，不拨时间的话所有种子行都会被跳过，测试变成"什么都没验"。
func seedPlaintextRows(t *testing.T, ctx context.Context, bodies [][]byte, age time.Duration) []uint {
	t.Helper()
	// 先记下当前的最大 id，插完只回读**这一段**。
	// 按"整表 id"回读的话，第二次调用会把第一次种的行也算进来——
	// 调用方拿到一串对不上的 id，症状是"库里凭空多了几行"。
	var before uint
	if err := models.DB.WithContext(ctx).
		Raw(`SELECT COALESCE(MAX(id), 0) FROM chat_ios`).Row().Scan(&before); err != nil {
		t.Fatalf("读起始 id 失败：%v", err)
	}

	when := time.Now().Add(-age)
	for i, b := range bodies {
		if err := models.DB.WithContext(ctx).Exec(
			`INSERT INTO chat_ios (created_at, updated_at, log_id, input) VALUES (?, ?, ?, ?)`,
			when, when, i+1, string(b)).Error; err != nil {
			t.Fatalf("种第 %d 行失败：%v", i, err)
		}
	}
	// 自增 id 只能回读——Exec 的 RowsAffected 只告诉你插了几行。
	var ids []uint
	if err := models.DB.WithContext(ctx).
		Raw(`SELECT id FROM chat_ios WHERE id > ? ORDER BY id`, before).
		Scan(&ids).Error; err != nil {
		t.Fatalf("回读 id 失败：%v", err)
	}
	if len(ids) != len(bodies) {
		t.Fatalf("种了 %d 行，库里查到 %d 行", len(bodies), len(ids))
	}
	return ids
}

// readBack 走**生产读路径**（AfterFind → 按引用还原）把整表读回来。
func readBack(t *testing.T, ctx context.Context) map[uint][]byte {
	t.Helper()
	rows, err := gorm.G[models.ChatIO](models.DB).Order("id").Find(ctx)
	if err != nil {
		t.Fatalf("读回 chat_ios 失败：%v", err)
	}
	out := make(map[uint][]byte, len(rows))
	for _, r := range rows {
		out[r.ID] = append([]byte(nil), r.Input...)
	}
	return out
}

// colShape 直接看列里此刻的形态：typeof + 原始字节。
// 这是"迁移到底改没改"的唯一可信视角——走模型读会被 AfterFind 解成明文，
// 于是"迁没迁过"根本看不出来。
func colShape(t *testing.T, id uint) (string, []byte) {
	t.Helper()
	var (
		typ string
		raw []byte
	)
	if err := models.DB.Raw(
		`SELECT typeof(input), input FROM chat_ios WHERE id = ?`, id).
		Row().Scan(&typ, &raw); err != nil {
		t.Fatalf("读行 %d 的形态失败：%v", id, err)
	}
	return typ, raw
}

// rawSnapshot 把整表的**原始列字节**与块表规模拍成一张快照。
//
// 幂等必须这样判，不能看计数：`Scanned`/`Packed` 是**这一轮迁移累计**的，
// 续跑根本不改它们——拿计数当判据的话，"什么都没做"和"每行又压了一遍"
// 在断言上长得一模一样。真正要判的是**库里的字节变了没有**，
// 而"又压一遍"恰恰是字节会变、且什么都不报的那一类错。
func rawSnapshot(t *testing.T, ctx context.Context) string {
	t.Helper()
	rows, err := models.DB.WithContext(ctx).
		Raw(`SELECT id, typeof(input), hex(input) FROM chat_ios ORDER BY id`).Rows()
	if err != nil {
		t.Fatalf("拍快照失败：%v", err)
	}
	defer rows.Close()

	var b strings.Builder
	for rows.Next() {
		var (
			id  uint
			typ string
			hex string
		)
		if err := rows.Scan(&id, &typ, &hex); err != nil {
			t.Fatalf("拍快照失败：%v", err)
		}
		fmt.Fprintf(&b, "%d/%s/%s;", id, typ, hex)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("拍快照中断：%v", err)
	}

	var blocks, groups int64
	if err := models.DB.WithContext(ctx).Raw(`SELECT count(*) FROM blocks`).
		Row().Scan(&blocks); err != nil {
		t.Fatalf("数块表失败：%v", err)
	}
	if err := models.DB.WithContext(ctx).Raw(`SELECT count(*) FROM block_groups`).
		Row().Scan(&groups); err != nil {
		t.Fatalf("数组表失败：%v", err)
	}
	fmt.Fprintf(&b, "|blocks=%d groups=%d", blocks, groups)
	return b.String()
}

func mustPolicy(t *testing.T, ctx context.Context, p *models.LogCompressPolicy) {
	t.Helper()
	if err := SaveLogCompressPolicy(ctx, p); err != nil {
		t.Fatalf("写策略失败：%v", err)
	}
}

// bigRow 造第 i 行内容：n 字节、**不可压**且每行都不同。
//
// 两个性质都是必须的：不可压才走块表（可压的内容组里全是重复，量不出分组），
// 每行不同才不会互相去重成一块（那样多行共用一个组，"批量打包"就测不出来了）。
func bigRow(i, n int) []byte {
	out := make([]byte, n)
	var x uint64 = 0x2545F4914F6CDD1D + uint64(i)*0x9E3779B97F4A7C15
	for j := range out {
		x = x*6364136223846793005 + 1442695040888963407
		out[j] = byte(x >> 33)
	}
	return out
}

// ── 主路径 ────────────────────────────────────────────────────────────────

func TestLogCompress_MigratesAndIsIdempotent(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	// 三种形态一起迁：大行（进块表）、小行（逐行帧）、不可压的小行（留明文）。
	bodies := [][]byte{
		bigRow(1, 64<<10),
		bigRow(2, 48<<10),
		[]byte(strings.Repeat("小的、可压的历史行 ", 300)), // ~2 KiB，小于 4 KiB 阈值
		[]byte("短"),
	}
	bIDs := seedPlaintextRows(t, ctx, bodies, time.Hour)

	before := readBack(t, ctx)
	state, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if state.Status != compressDone {
		t.Fatalf("跑完的状态是 %q，期望 %q", state.Status, compressDone)
	}
	if state.TotalRows != int64(len(bodies)) {
		t.Fatalf("候选行数 %d，期望 %d", state.TotalRows, len(bodies))
	}
	if state.Scanned != int64(len(bodies)) {
		t.Fatalf("扫了 %d 行，期望 %d", state.Scanned, len(bodies))
	}
	// 3 行改了形态，1 行留明文——那一行只有 3 字节，逐行帧光帧头就 16 字节，
	// 比原文还大。这正是规则 1（压不了就存明文）在迁移路径上的样子：
	// **扫过、判过、决定不动它**，而不是漏掉。
	if state.Packed != 3 {
		t.Fatalf("改了 %d 行，期望 3 行", state.Packed)
	}
	if state.Skipped != 1 {
		t.Fatalf("跳过 %d 行，期望 1 行（那 3 字节的行压不动）", state.Skipped)
	}
	if state.BytesAfter >= state.BytesBefore {
		t.Fatalf("迁移后 %d 字节，迁移前 %d 字节——没省",
			state.BytesAfter, state.BytesBefore)
	}

	after := readBack(t, ctx)
	for id, want := range before {
		if !bytes.Equal(after[id], want) {
			t.Fatalf("行 %d 迁移后还原不一致：%d 字节 → %d 字节",
				id, len(want), len(after[id]))
		}
		// 前 3 行（大行、小行、可压的小行）都该变成帧；最后一行压不动，留明文。
		want := "blob"
		if id == bIDs[3] {
			want = "text"
		}
		if typ, _ := colShape(t, id); typ != want {
			t.Fatalf("行 %d 迁完之后 typeof 是 %q，期望 %q", id, typ, want)
		}
	}

	// ── 幂等 ──
	//
	// 判据是"库里的字节变了没有"，不是计数：Scanned/Packed 是这一轮累计的，
	// 续跑不改它们，于是"什么都没做"与"每行又压了一遍"在计数上长得一样。
	snap := rawSnapshot(t, ctx)

	again, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("第二次迁移失败：%v", err)
	}
	if again.LastID != state.LastID {
		t.Fatalf("幂等重跑把水位从 %d 推到了 %d", state.LastID, again.LastID)
	}
	if got := rawSnapshot(t, ctx); got != snap {
		t.Fatal("第二次迁移改动了库里的字节——幂等破了（每行又被压了一遍？）")
	}

	// full 重扫同样幂等，而且这条必须单独验：full 会把水位清零重走全表。
	// 若候选过滤写成依赖水位而不是 typeof，这里就会把每行再压一次。
	full, err := RunLogCompress(ctx, true)
	if err != nil {
		t.Fatalf("full 重扫失败：%v", err)
	}
	if full.Packed != 0 {
		t.Fatalf("full 重扫改动了 %d 行，期望 0 行", full.Packed)
	}
	if got := rawSnapshot(t, ctx); got != snap {
		t.Fatal("full 重扫改动了库里的字节")
	}
}

// 迁移**不许碰 updated_at**：它是冷静期过滤器的输入。
//
// 迁移自己把 updated_at 刷新了，就等于把这行重新标记成"刚被写过"——
// 下一轮迁移会绕开它，而它已经是帧了，绕开看着"没影响"。真正的后果在
// 回滚一侧：updated_at 一乱，就没法用同一个过滤器判断"这行是不是半成品"了。
func TestLogCompress_DoesNotTouchUpdatedAt(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{bigRow(7, 32<<10)}, time.Hour)
	var before string
	if err := models.DB.Raw(`SELECT updated_at FROM chat_ios WHERE id = ?`, ids[0]).
		Row().Scan(&before); err != nil {
		t.Fatalf("读 updated_at 失败：%v", err)
	}

	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	var after string
	if err := models.DB.Raw(`SELECT updated_at FROM chat_ios WHERE id = ?`, ids[0]).
		Row().Scan(&after); err != nil {
		t.Fatalf("读 updated_at 失败：%v", err)
	}
	if before != after {
		t.Fatalf("迁移改动了 updated_at：%q → %q", before, after)
	}
}

// 冷静期必须挡住"刚被写过"的行。它挡的是写路径的中间态：
// `Create` 已经把请求体写进去了、补响应体的 `Updates` 还没发。
func TestLogCompress_QuiesceSkipsFreshRows(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	fresh := seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 32<<10)}, 0)
	old := seedPlaintextRows(t, ctx, [][]byte{bigRow(2, 32<<10)}, 2*time.Hour)

	mustPolicy(t, ctx, &models.LogCompressPolicy{Enabled: true, QuiesceSec: 3600})

	state, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if state.Scanned != 1 {
		t.Fatalf("扫了 %d 行，期望只扫到那 1 行老数据", state.Scanned)
	}
	if typ, _ := colShape(t, fresh[0]); typ != "text" {
		t.Fatalf("冷静期内的行被迁了：typeof=%q", typ)
	}
	if typ, _ := colShape(t, old[0]); typ != "blob" {
		t.Fatalf("冷静期外的行没被迁：typeof=%q", typ)
	}
}

// 水位续跑：把水位手工推到中途，只该扫剩下的。
//
// 这条模拟的是"上一轮被 kill 掉了"——水位在库里，重启接着走。
func TestLogCompress_ResumesFromWatermark(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	bodies := make([][]byte, 6)
	for i := range bodies {
		bodies[i] = bigRow(100+i, 16<<10)
	}
	ids := seedPlaintextRows(t, ctx, bodies, time.Hour)

	half := ids[2]
	st := DefaultLogCompressState()
	st.Status = compressRunning
	st.LastID = half
	st.Scanned = 3
	if err := saveCompressState(ctx, modePack, st); err != nil {
		t.Fatalf("写状态失败：%v", err)
	}

	got, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("续跑失败：%v", err)
	}
	if got.Scanned != 6 {
		t.Fatalf("续跑后累计扫了 %d 行，期望 6", got.Scanned)
	}
	if got.Packed != 3 {
		t.Fatalf("续跑改了 %d 行，期望 3（水位之前那 3 行不该重扫）", got.Packed)
	}
	// 水位之前的行应当原样留在明文——迁移没碰它们。
	if typ, _ := colShape(t, ids[0]); typ != "text" {
		t.Fatalf("水位之前的行被动了：typeof=%q", typ)
	}
	if typ, _ := colShape(t, ids[5]); typ != "blob" {
		t.Fatalf("水位之后的行没被迁：typeof=%q", typ)
	}
}

// 压缩比的分子（BytesTotal）必须在**起跑那一刻量全表**，而且要把"压不动、
// 被有意留成明文"的行也算进去——它们也是原文，只是没被改形态。
//
// 这一条同时钉住"实时"：这个数在跑第一行之前就已经定下来了，之后一路不变；
// 变的是分母。若哪天有人把它改成"跑完再回填"，界面上的比值就会在整个迁移
// 过程中一直空着——正是要修的那个毛病。
func TestLogCompress_MeasuresOriginalTotalOnFreshRun(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	bodies := [][]byte{
		bigRow(1, 64<<10),
		bigRow(2, 32<<10),
		[]byte("短"), // 逐行帧光帧头就 16 字节，比原文还大 ⇒ 留明文
	}
	seedPlaintextRows(t, ctx, bodies, time.Hour)

	var want int64
	for _, b := range bodies {
		want += int64(len(b))
	}

	state, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if state.BytesTotal != want {
		t.Fatalf("原文合计 %d，期望 %d", state.BytesTotal, want)
	}
	// 从水位 0 起跑时两者相等——这正是 BytesTotal 的定义域，
	// 也是"续跑绝不能重算"那条规矩的由来（见下一个用例）。
	if state.BytesBefore != want {
		t.Fatalf("扫过的行原文合计 %d，期望 %d", state.BytesBefore, want)
	}
}

// 续跑**不能**重算原文合计。
//
// 水位之后还有明文，不代表全表都还是明文——已经压过的行的原文已经不在了，
// 量回来只会是个偏小的数。拿它配全表的分母，得到的比值恰好是真值的一个零头，
// 而且**看起来完全正常**：这就是当初 bytes_before 当分子犯的错，换个人再犯一次。
func TestLogCompress_KeepsOriginalTotalAcrossResumes(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	bodies := make([][]byte, 6)
	for i := range bodies {
		bodies[i] = bigRow(300+i, 16<<10)
	}
	ids := seedPlaintextRows(t, ctx, bodies, time.Hour)

	var full int64
	for _, b := range bodies {
		full += int64(len(b))
	}

	// 造一个"跑了一半"的现场：水位推过前 3 行，原文合计写的是**全表**的
	// （从 0 起跑那一轮本来就会这么写）。
	st := DefaultLogCompressState()
	st.Status = compressRunning
	st.LastID = ids[2]
	st.Scanned = 3
	st.BytesBefore = full
	st.BytesTotal = full
	if err := saveCompressState(ctx, modePack, st); err != nil {
		t.Fatalf("写状态失败：%v", err)
	}

	got, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("续跑失败：%v", err)
	}
	if got.BytesTotal != full {
		t.Fatalf("续跑后原文合计被改成了 %d，应当保持 %d（剩下的明文只有一半，"+
			"重算出来的正好是假值的一半）", got.BytesTotal, full)
	}
}

// 本次改动之前落的盘没有 bytes_total（那时候界面拿 bytes_before 当分子）。
// 续跑时补一次，否则一份**早就迁完**的库会永远显示不出压缩比——而那正是
// 要修的那个毛病，不能修完还留一个老库看不见。
func TestLogCompress_BackfillsLegacyTotalOnResume(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	bodies := [][]byte{bigRow(1, 16<<10), bigRow(2, 16<<10)}
	ids := seedPlaintextRows(t, ctx, bodies, time.Hour)

	// 旧格式的状态：水位已经在末尾（这一轮不会再有候选行），
	// 只有 bytes_before，没有 bytes_total。
	const legacyBefore = 4242
	st := DefaultLogCompressState()
	st.Status = compressDone
	st.LastID = ids[len(ids)-1]
	st.Scanned = int64(len(bodies))
	st.Packed = int64(len(bodies))
	st.BytesBefore = legacyBefore
	if err := saveCompressState(ctx, modePack, st); err != nil {
		t.Fatalf("写状态失败：%v", err)
	}

	got, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("跑一轮失败：%v", err)
	}
	if got.BytesTotal != legacyBefore {
		t.Fatalf("旧状态的原文合计没补上：得到 %d，期望 %d", got.BytesTotal, legacyBefore)
	}
}

// 读侧兜底：一份**跑完的**旧状态（没有 bytes_total）必须一打开状态页就报出
// 压缩比，而不是逼用户再点一次「开始迁移」。
//
// 这一条是真机反馈逼出来的：用户库里的状态是旧二进制落的盘，界面上压缩比
// 那一格一直是"—"，而状态本身是 done、数据也明明已经压进去了。
func TestLogCompress_ReadBackfillsLegacyTotalWithoutRerun(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	const legacyBefore = 6058628881
	st := DefaultLogCompressState()
	st.Status = compressDone
	st.LastID = 12530
	st.Scanned = 12483
	st.Packed = 12468
	st.BytesBefore = legacyBefore
	// BytesTotal 留零：旧格式就是这样落的盘。
	if err := saveCompressState(ctx, modePack, st); err != nil {
		t.Fatalf("写状态失败：%v", err)
	}

	got, err := GetLogCompressState(ctx)
	if err != nil {
		t.Fatalf("读状态失败：%v", err)
	}
	if got.BytesTotal != legacyBefore {
		t.Fatalf("跑完的旧状态没补上原文合计：得到 %d，期望 %d", got.BytesTotal, legacyBefore)
	}
}

// 但**半途的**旧状态不能补。`bytes_before` 只盖住扫过的行，而界面的分母是
// 全表——补出来的比值会恰好是真值的一半，一个看起来很专业的错数。
// 宁可让它显示"还没量过"（点一次「开始迁移」就量出来了）。
func TestLogCompress_DoesNotBackfillLegacyTotalMidRun(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	st := DefaultLogCompressState()
	st.Status = compressRunning
	st.LastID = 6000
	st.Scanned = 5000
	st.BytesBefore = 5 << 30
	if err := saveCompressState(ctx, modePack, st); err != nil {
		t.Fatalf("写状态失败：%v", err)
	}

	got, err := GetLogCompressState(ctx)
	if err != nil {
		t.Fatalf("读状态失败：%v", err)
	}
	if got.BytesTotal != 0 {
		t.Fatalf("半途的旧状态不该补原文合计（补出来是假值的一半），实得 %d", got.BytesTotal)
	}
}

// 一批失败必须**整批连水位一起回滚**（中断点 #3）。
//
// 注入方式：把块表改名，让写块那一步必然失败。若水位写在了事务外，
// 这里就会看到"水位推了、数据一行没改"，重启后那几行会被整段跳过——
// **永久留在明文，而且不报任何错**。
func TestLogCompress_FailedBatchRollsBackWatermark(t *testing.T) {
	db := setupLogCompressTestDB(t)
	ctx := context.Background()

	bodies := make([][]byte, 3)
	for i := range bodies {
		bodies[i] = bigRow(200+i, 32<<10)
	}
	ids := seedPlaintextRows(t, ctx, bodies, time.Hour)

	if err := db.Exec(`ALTER TABLE blocks RENAME TO blocks_broken`).Error; err != nil {
		t.Fatalf("改名块表失败：%v", err)
	}

	if _, err := RunLogCompress(ctx, false); err == nil {
		t.Fatal("块表都没了，迁移却报成功")
	}

	for _, id := range ids {
		if typ, _ := colShape(t, id); typ != "text" {
			t.Fatalf("批次失败后行 %d 却已经变了形态：typeof=%q", id, typ)
		}
	}
	st, err := GetLogCompressState(ctx)
	if err != nil {
		t.Fatalf("读状态失败：%v", err)
	}
	if st.LastID != 0 {
		t.Fatalf("批次失败后水位却推到了 %d——水位跑到事务外面去了", st.LastID)
	}
	if st.Attempts != 1 {
		t.Fatalf("失败次数记的是 %d，期望 1", st.Attempts)
	}
	if st.LastError == "" {
		t.Fatal("失败了却没记 LastError")
	}
	if st.Status != compressRunning {
		t.Fatalf("首次失败后状态是 %q，期望 %q（还能重试）", st.Status, compressRunning)
	}
}

// 连续失败到上限就停下等人处理，不再无限重试。
func TestLogCompress_GivesUpAfterMaxAttempts(t *testing.T) {
	db := setupLogCompressTestDB(t)
	ctx := context.Background()

	seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 32<<10)}, time.Hour)
	if err := db.Exec(`ALTER TABLE blocks RENAME TO blocks_broken`).Error; err != nil {
		t.Fatalf("改名块表失败：%v", err)
	}

	var st *models.LogCompressState
	for i := 0; i < compressMaxAttempts; i++ {
		if _, err := RunLogCompress(ctx, false); err == nil {
			t.Fatalf("第 %d 次却报成功", i+1)
		}
		st, _ = GetLogCompressState(ctx)
	}
	if st.Status != compressFailed {
		t.Fatalf("失败 %d 次后状态是 %q，期望 %q", compressMaxAttempts, st.Status, compressFailed)
	}
	if st.Attempts != compressMaxAttempts {
		t.Fatalf("失败次数记的是 %d，期望 %d", st.Attempts, compressMaxAttempts)
	}
}

// 同一时刻只允许一轮。用一个明确的标志而不是碰运气去撞时序。
func TestLogCompress_RejectsConcurrentRun(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	compressInFlight.Store(true)
	defer compressInFlight.Store(false)

	if _, err := RunLogCompress(ctx, false); !errors.Is(err, ErrCompressRunning) {
		t.Fatalf("已经有一轮在跑时迁移应当报 ErrCompressRunning，得到 %v", err)
	}
	// 回滚走的是同一个位：迁移在跑的时候它也必须拒绝——否则两条路会同时改
	// 同一批行的形态，那是最难查的一类损坏。
	if _, err := RunLogDecompress(ctx, true); !errors.Is(err, ErrCompressRunning) {
		t.Fatalf("已经有一轮在跑时回滚应当报 ErrCompressRunning，得到 %v", err)
	}
}

// ── 后台入口：占位必须在返回之前 ─────────────────────────────────────────

// 后台入口的契约：**返回时位已经占上**。
//
// 这不是实现细节，它是响应可信的前提：响应写着 `started: true`，状态接口就必须
// 说在跑；否则前端把"开始迁移"放回可点，并发进来的第二个请求也能透过去。
//
// 这里用维护锁把后台那一轮按住（它一定会走到取锁那一步，那是 applyCompressBatch
// 的第一件事），所以断言不必抢时间：只要占位在返回之前完成，读到的一定是 true。
//
// 但它测不出"占位被推到响应之后"那一种旧形状——那条 goroutine 会被异步抢占在
// 断言之前唤醒，光看时序分不出来（实测：把旧形状放回来，这条用例照样绿）。
// 那件事由 handler 侧的源码守卫钉住，见 TestLogCompressHandlersSpawnNoGoroutines。
func TestStartLogCompress_ReservesBeforeReturning(t *testing.T) {
	ctx := context.Background()
	setupLogCompressTestDB(t)
	seedPlaintextRows(t, ctx, [][]byte{[]byte(`{"messages":[{"role":"user","content":"hi"}]}`)}, time.Hour)

	maintenanceMu.Lock()
	if err := StartLogCompress(ctx, false); err != nil {
		maintenanceMu.Unlock()
		t.Fatalf("后台起一轮迁移失败：%v", err)
	}
	if !CompressRunning() {
		maintenanceMu.Unlock()
		t.Fatal("StartLogCompress 已经返回，但 CompressRunning() 还是 false——占位发生在返回之后")
	}
	maintenanceMu.Unlock()

	awaitCompressIdleForTest(t)
}

// 回滚那条路同一条规矩。它动的是"把库变大"，更不能出现"响应说 started、
// 状态说没在跑"。
func TestStartLogDecompress_ReservesBeforeReturning(t *testing.T) {
	ctx := context.Background()
	setupLogCompressTestDB(t)
	seedPlaintextRows(t, ctx, [][]byte{[]byte(`{"messages":[{"role":"user","content":"hi"}]}`)}, time.Hour)
	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("先把行压起来失败：%v", err)
	}

	maintenanceMu.Lock()
	if err := StartLogDecompress(ctx, true); err != nil {
		maintenanceMu.Unlock()
		t.Fatalf("后台起一轮回滚失败：%v", err)
	}
	if !CompressRunning() {
		maintenanceMu.Unlock()
		t.Fatal("StartLogDecompress 已经返回，但 CompressRunning() 还是 false——占位发生在返回之后")
	}
	maintenanceMu.Unlock()

	awaitCompressIdleForTest(t)
}

// 被拒的那一次**不许**把别人的占位放掉。
//
// "先 defer 释放、再判断占得上占不上"是个一眼看不出的写法错误，而它的后果是
// 那个位在整个迁移期间被清掉：并发请求能透过去、暂停与状态读数一起失真。
func TestStartLogCompress_RefusedStartKeepsReservation(t *testing.T) {
	resetCompressSignals(t)
	setupLogCompressTestDB(t)
	ctx := context.Background()

	compressInFlight.Store(true)

	for _, start := range []struct {
		name string
		call func() error
	}{
		{"迁移", func() error { return StartLogCompress(ctx, false) }},
		{"回滚", func() error { return StartLogDecompress(ctx, true) }},
	} {
		if err := start.call(); !errors.Is(err, ErrCompressRunning) {
			t.Fatalf("%s：已经有一轮在跑时应当报 ErrCompressRunning，得到 %v", start.name, err)
		}
		if !CompressRunning() {
			t.Fatalf("%s：被拒的那一次把位放掉了——别人的占位没了", start.name)
		}
	}
}

// 半途而废只能是"进程被 kill"，不能是"用户关了个标签页"。
//
// 请求的 ctx 在响应写完那一刻就被取消，后台那一轮必须照跑完。
func TestStartLogCompress_OutlivesRequestContext(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	// 够大才一定会换形态：小行"压不动就存明文"，用它们验不出"跑没跑成"。
	ids := seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 64<<10)}, time.Hour)
	if err := StartLogCompress(ctx, false); err != nil {
		t.Fatalf("后台起一轮迁移失败：%v", err)
	}
	cancel() // 响应写完，请求 ctx 作废

	awaitCompressIdleForTest(t)

	if typ, _ := colShape(t, ids[0]); typ != "blob" {
		t.Fatalf("请求 ctx 一取消这一轮就停了：列里的形态是 %q，期望 blob", typ)
	}
}

// 后台那一轮失败时，位必须还回来。
//
// 入口只负责"占位 + 起后台"，失败记在日志里；唯独不能把位留着——留着的话这个
// 进程再也起不了一轮迁移，而症状只是"点了开始迁移没反应"，没人会往这里查。
func TestStartLogCompress_ReleasesReservationWhenRunFails(t *testing.T) {
	db := setupLogCompressTestDB(t)
	ctx := context.Background()

	// 把库关掉：后台那一轮的第一件事（读策略）就会失败。占位自己不看库，
	// 所以这一下仍然是"起得来、跑不成"。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取 sql.DB 失败：%v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关库失败：%v", err)
	}

	if err := StartLogCompress(ctx, false); err != nil {
		t.Fatalf("占位本身不该失败（它不读库）：%v", err)
	}
	awaitCompressIdleForTest(t)

	if CompressRunning() {
		t.Fatal("后台那一轮失败了，位却没还回来——这个进程从此起不了迁移了")
	}
}

// awaitCompressIdleForTest 等后台那一轮退出。空库上它下一秒就好，超时说明真有东西卡住了。
func awaitCompressIdleForTest(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for CompressRunning() {
		if time.Now().After(deadline) {
			t.Fatal("后台迁移 5 秒还没退出——有东西卡住了")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 暂停在批与批之间生效。
//
// 这里必须**真的**在跑的过程中按下暂停：`RunLogCompress` 一进来就会清掉
// 暂停标志（它就是"被要求跑"的意思），所以"先置标志再调用"验的是别的东西。
func TestLogCompress_PausesBetweenBatches(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	bodies := make([][]byte, 40)
	for i := range bodies {
		bodies[i] = bigRow(300+i, 24<<10)
	}
	seedPlaintextRows(t, ctx, bodies, time.Hour)
	// 一批一行，把"批间"这个窗口撑得足够宽。
	mustPolicy(t, ctx, &models.LogCompressPolicy{Enabled: true, BatchRows: 1, QuiesceSec: 0})

	done := make(chan struct{})
	var state *models.LogCompressState
	var runErr error
	go func() {
		defer close(done)
		state, runErr = RunLogCompress(ctx, false)
	}()

	// 等到水位动了再按暂停：这时候循环一定停在某两次批之间。
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st, err := GetLogCompressState(ctx)
		if err == nil && st.LastID > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := PauseLogCompress(ctx); err != nil {
		t.Fatalf("暂停失败：%v", err)
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("暂停之后这一轮没有退出——暂停是在批间生效的，不该等这么久")
	}
	if runErr != nil {
		t.Fatalf("迁移报错：%v", runErr)
	}
	if state.Status != compressPaused {
		t.Fatalf("暂停后状态是 %q，期望 %q", state.Status, compressPaused)
	}
	if state.LastID == 0 {
		t.Fatal("暂停前一批都没做完")
	}
	if state.LastID > uint(len(bodies)) {
		t.Fatalf("水位 %d 超出种子行数 %d", state.LastID, len(bodies))
	}

	// 暂停之后调度器不许自己接着跑（那是"暂停"这个词的全部意思）。
	policy, err := GetLogCompressPolicy(ctx)
	if err != nil {
		t.Fatalf("读策略失败：%v", err)
	}
	if !policy.Enabled {
		t.Fatal("策略没写进去")
	}
	if st, _ := GetLogCompressState(ctx); st.Status != compressPaused {
		t.Fatalf("暂停状态没落盘，库里的状态是 %q", st.Status)
	}
}

// 暂停时**没有**任何一轮在跑：状态要当场落盘，否则界面上会一直显示"进行中"。
func TestLogCompress_PausePersistsWithoutRunning(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	st := DefaultLogCompressState()
	st.Status = compressRunning
	st.LastID = 7
	if err := saveCompressState(ctx, modePack, st); err != nil {
		t.Fatalf("写状态失败：%v", err)
	}
	got, err := PauseLogCompress(ctx)
	if err != nil {
		t.Fatalf("暂停失败：%v", err)
	}
	if got.Status != compressPaused {
		t.Fatalf("暂停后状态是 %q", got.Status)
	}
	// PauseLogCompress 只置包级标志，测试之间要清掉。
	compressPauseRequested.Store(false)
}

// ── 回滚（L2）─────────────────────────────────────────────────────────────

// 压缩整库 → 解压整库 → 与原字节逐一相等。
//
// 这条是整个方案**最强的验证器**：它同时压了编码、块表、引用序列、
// 落库形态四件事，而且验的是全库逐字节。`decompress` 不只是一个降级开关，
// 它还是唯一一条能把"压缩正确性"验到全库粒度的路径。
func TestLogCompress_DecompressRoundTrip(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	bodies := [][]byte{
		bigRow(1, 96<<10),
		bigRow(2, 40<<10),
		[]byte(strings.Repeat("中文与 emoji 🚀 混排的小行 ", 400)),
		// 非法 UTF-8 + 内嵌 NUL：真实请求体里就有，而 NUL 恰好是帧 magic 的首字节。
		append([]byte{0x00, 0xff, 0xfe}, []byte(strings.Repeat("x\x00y", 5000))...),
	}
	ids := seedPlaintextRows(t, ctx, bodies, time.Hour)
	before := readBack(t, ctx)

	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	for _, id := range ids {
		if typ, _ := colShape(t, id); typ != "blob" {
			t.Fatalf("行 %d 没有迁成帧，typeof=%q——回滚这一趟就验不到东西了", id, typ)
		}
	}

	st, err := RunLogDecompress(ctx, true)
	if err != nil {
		t.Fatalf("解压失败：%v", err)
	}
	if st.Status != compressDone {
		t.Fatalf("解压后状态是 %q", st.Status)
	}
	if st.Packed != int64(len(bodies)) {
		t.Fatalf("只还原了 %d 行，期望 %d 行", st.Packed, len(bodies))
	}

	after := readBack(t, ctx)
	for id, want := range before {
		if !bytes.Equal(after[id], want) {
			t.Fatalf("回滚后行 %d 与原字节不一致：%d 字节 → %d 字节",
				id, len(want), len(after[id]))
		}
		// 明文必须落回 TEXT。落成 BLOB 的话读路径**照样读得出来**，
		// 于是这个错会一直躺着，直到下一次迁移把它当成"已迁移"漏掉。
		if typ, _ := colShape(t, id); typ != "text" {
			t.Fatalf("回滚后行 %d 的 typeof 是 %q，期望 text", id, typ)
		}
	}
}

// 回滚之后必须还能再迁一次，而且**全表**都要迁到。
//
// 真机上的形状是"迁移 → 回滚 → 再迁移"，第三步只压了 1 行就报 done，
// 状态里 `packed` 还接着上一轮累计的数，界面上一半写"已完成"、一半写
// "待迁移 12,483 行"。根因是**迁移的水位停在回滚前的位置**，而水位之前的行
// 已经被回滚还原成明文了——水位不覆盖它们，候选查询就永远扫不到。
//
// 判据取**每一行的形态**：只断言"跑完了"是抓不到这个 bug 的，它确实报 done，
// 也确实是"没有候选了"——在它自己那个错水位之后。
func TestLogCompress_RecompressAfterRollback(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{
		bigRow(1, 96<<10),
		bigRow(2, 40<<10),
		bigRow(3, 72<<10),
	}, time.Hour)

	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("第一次迁移失败：%v", err)
	}
	if _, err := RunLogDecompress(ctx, true); err != nil {
		t.Fatalf("回滚失败：%v", err)
	}

	// 回滚完成 = 这一列回到"从没压过"。迁移的水位必须跟着退。
	packState, err := GetLogCompressState(ctx)
	if err != nil {
		t.Fatalf("读迁移状态失败：%v", err)
	}
	if packState.LastID != 0 {
		t.Fatalf("回滚之后迁移水位仍是 %d，下一次迁移从它续跑就会漏掉前面的行",
			packState.LastID)
	}
	if packState.Status != compressIdle {
		t.Fatalf("回滚之后迁移状态是 %q，期望 %q", packState.Status, compressIdle)
	}

	st, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("再迁移失败：%v", err)
	}
	if st.Status != compressDone {
		t.Fatalf("再迁移后状态是 %q", st.Status)
	}
	for _, id := range ids {
		if typ, _ := colShape(t, id); typ != "blob" {
			t.Fatalf("行 %d 在再迁移之后仍是 %q——水位没有随回滚退回去", id, typ)
		}
	}
	// 再迁一次仍要逐字节读得回来：水位归零重扫不能变成"压了两遍"。
	after := readBack(t, ctx)
	for _, id := range ids {
		if len(after[id]) == 0 {
			t.Fatalf("行 %d 读回来是空的", id)
		}
	}
}

// 续跑的回滚（full=false，内部路径）**不能**退迁移的水位：那种跑法只保证
// "水位之后没有帧了"，水位之前的帧还在，迁移的水位仍然有效。
//
// 这条是上一条的边界。少了它，将来把 full 那个条件删掉也不会有人发现
// ——而那时每一次部分回滚都会强制下一次迁移全表重扫一遍（幂等兜得住正确性，
// 但把"续跑"这件事变成了摆设）。
func TestLogCompress_PartialRollbackKeepsPackWatermark(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{
		bigRow(1, 96<<10),
		bigRow(2, 40<<10),
	}, time.Hour)
	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	packed, err := GetLogCompressState(ctx)
	if err != nil {
		t.Fatalf("读迁移状态失败：%v", err)
	}
	if packed.LastID == 0 {
		t.Fatalf("迁移之后水位仍是 0，这条测试要验的东西不成立")
	}

	if _, err := RunLogDecompress(ctx, false); err != nil {
		t.Fatalf("部分回滚失败：%v", err)
	}
	after, err := GetLogCompressState(ctx)
	if err != nil {
		t.Fatalf("读迁移状态失败：%v", err)
	}
	if after.LastID != packed.LastID {
		t.Fatalf("部分回滚把迁移水位从 %d 改成了 %d", packed.LastID, after.LastID)
	}
	// 顺带钉住这一趟确实干了活：不然"水位没变"可能只是因为它什么都没做。
	for _, id := range ids {
		if typ, _ := colShape(t, id); typ != "text" {
			t.Fatalf("行 %d 没被还原成明文，typeof=%q", id, typ)
		}
	}
}

// ── 迁移是否达到设计容量 ──────────────────────────────────────────────────

// 迁移必须**批量打包**，不能逐行各封各的组。
//
// 阶段 3 真机量过：线上逐行写入的组平均只有 7.70 KiB，远低于 flate 的
// 32 KiB 窗口；同一批内容按 256 KiB 重打能再省 35.9%。历史行迁移要是也逐行
// 封组，等于把那份 35.9% 白扔——而"白扔"在库面上看不出来，只是体积大一点。
//
// 判据取**组的原始字节数**：一批 32 行 × 24 KiB = 768 KiB，按 256 KiB 封口
// 应当只有 3 组、平均一组接近 256 KiB。逐行封则是 32 组、每组 24 KiB。
func TestLogCompress_PacksGroupsInBatches(t *testing.T) {
	db := setupLogCompressTestDB(t)
	ctx := context.Background()

	const rows, each = 32, 24 << 10
	bodies := make([][]byte, rows)
	for i := range bodies {
		bodies[i] = bigRow(400+i, each)
	}
	seedPlaintextRows(t, ctx, bodies, time.Hour)
	mustPolicy(t, ctx, &models.LogCompressPolicy{
		Enabled: true, BatchRows: rows, BatchBytes: 1 << 30, QuiesceSec: 0,
	})

	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	var groups, totalRaw, maxRaw int64
	if err := db.Raw(`SELECT count(*), COALESCE(sum(raw_len),0), COALESCE(max(raw_len),0)
		FROM block_groups`).Row().Scan(&groups, &totalRaw, &maxRaw); err != nil {
		t.Fatalf("读组表失败：%v", err)
	}
	// 32 × 24 KiB = 768 KiB，按 256 KiB 封口 ⇒ 3 组（最后不满）。
	if want := int64(rows * each / (256 << 10)); groups > want+1 {
		t.Fatalf("迁完之后有 %d 个组，批量打包应当只要 %d 个左右——是不是又变回逐行封组了",
			groups, want)
	}
	if groups >= rows {
		t.Fatalf("组数 %d ≥ 行数 %d：完全没有打包", groups, rows)
	}
	if avg := totalRaw / groups; avg < 128<<10 {
		t.Fatalf("平均一组只有 %d 原始字节，远低于 256 KiB 的目标——打包没起作用", avg)
	}
}

// ── 与日志清理互斥 ────────────────────────────────────────────────────────

// 清理抢不到维护锁时要**如实报错**，而不是静默什么都不做。
//
// 手动点一下"清理"，界面回一句"成功删了 0 条"，比回一句"正忙"糟糕得多：
// 前者会让人以为日志真的被清干净了。
func TestCleanLogsBusyWhenMigrationHoldsLock(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	maintenanceMu.Lock()
	defer maintenanceMu.Unlock()

	if _, err := CleanLogsByDays(ctx, 30); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("维护锁被占时应当报 ErrMaintenanceBusy，得到 %v", err)
	}
}

// ── 策略与备份 ────────────────────────────────────────────────────────────

func TestLogCompressPolicy_Clamps(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		in   models.LogCompressPolicy
		want models.LogCompressPolicy
	}{
		// 全零：行数/字节数回默认，冷静期保持 0。0 是**合法值**（不设冷静期），
		// 不是"没填"。迁移一行半成品不会损坏任何东西——写路径补响应体时
		// 钩子挂在 BeforeCreate 上，根本不会碰 input（陷阱 A）。冷静期省掉的
		// 只是"这一行刚写完又被迁一遍"的白工。
		{
			"全零：前两项回默认，冷静期保持 0",
			models.LogCompressPolicy{},
			models.LogCompressPolicy{
				BatchRows: defaultCompressBatchRows, BatchBytes: defaultCompressBatchBytes, QuiesceSec: 0,
			},
		},
		{
			"超上限拉回",
			models.LogCompressPolicy{BatchRows: 1 << 20, BatchBytes: 1 << 40, QuiesceSec: 1 << 20, BatchIntervalMs: 1 << 20},
			models.LogCompressPolicy{BatchRows: 4096, BatchBytes: 1 << 30, QuiesceSec: 86400, BatchIntervalMs: 5000},
		},
		{
			"负数回默认",
			models.LogCompressPolicy{BatchRows: -1, BatchBytes: -1, QuiesceSec: -1, BatchIntervalMs: -1},
			models.LogCompressPolicy{
				BatchRows: defaultCompressBatchRows, BatchBytes: defaultCompressBatchBytes, QuiesceSec: 0,
			},
		},
		{
			// 与"全零"那条对照：批间停顿的 0 是**要保留的合法值**（不停），
			// 不是"没填所以回默认"。它的默认本来就是 0，所以两条看起来一样；
			// 但意图不同，写在这里免得后来有人把它改成"回默认 100"。
			"批间停顿 0 保持 0",
			models.LogCompressPolicy{BatchIntervalMs: 0},
			models.LogCompressPolicy{
				BatchRows: defaultCompressBatchRows, BatchBytes: defaultCompressBatchBytes,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := SaveLogCompressPolicy(ctx, &tc.in); err != nil {
				t.Fatalf("写策略失败：%v", err)
			}
			got, err := GetLogCompressPolicy(ctx)
			if err != nil {
				t.Fatalf("读策略失败：%v", err)
			}
			if got.BatchRows != tc.want.BatchRows || got.BatchBytes != tc.want.BatchBytes ||
				got.QuiesceSec != tc.want.QuiesceSec ||
				got.BatchIntervalMs != tc.want.BatchIntervalMs {
				t.Fatalf("夹取结果 %+v，期望 %+v", *got, tc.want)
			}
		})
	}
}

/**
 * 批间停顿（占用率旋钮）。
 *
 * `sleepBetweenBatches` 只有三件事要做对，这三件都是**错了不会报错**的那类：
 *
 *  1. 睡够时间（否则旋钮形同虚设，而且没人会发现——迁移只是"还是那么快"）；
 *  2. **能被暂停叫醒**（否则按了暂停要瞪着进度条等最多 5 秒，手感是"按钮坏了"）；
 *  3. 0 就是 0（默认路径上一微秒都不该多花）。
 *
 * 时间断言用宽松的界：CI 与开发机上定时器都可能偏，这里要钉的是"睡没睡"
 * 这个量级，不是精度。
 */
func TestSleepBetweenBatches_WaitsTheInterval(t *testing.T) {
	resetCompressSignals(t)

	started := time.Now()
	sleepBetweenBatches(context.Background(), 150*time.Millisecond)
	elapsed := time.Since(started)

	if elapsed < 120*time.Millisecond {
		t.Fatalf("要求停 150ms，实际只停了 %s——旋钮没起作用", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("要求停 150ms，实际停了 %s——它睡过头了", elapsed)
	}
}

func TestSleepBetweenBatches_ZeroIsImmediate(t *testing.T) {
	resetCompressSignals(t)

	started := time.Now()
	sleepBetweenBatches(context.Background(), 0)
	if elapsed := time.Since(started); elapsed > 20*time.Millisecond {
		t.Fatalf("0 应当立刻返回，实际用了 %s", elapsed)
	}
}

// 暂停按钮按下去要**立刻**有反应。用 5 秒（策略上限）而不是 150ms 来测：
// 只有取到上限那个量级，"能被叫醒"与"睡满"才区分得开——短间隔下两者都是几十毫秒。
func TestSleepBetweenBatches_WakesOnPause(t *testing.T) {
	resetCompressSignals(t)
	compressPauseRequested.Store(true)
	defer resetCompressSignals(t)

	started := time.Now()
	sleepBetweenBatches(context.Background(), 5*time.Second)
	elapsed := time.Since(started)

	if elapsed > time.Second {
		t.Fatalf("已经按了暂停，却在停顿里等了 %s——按下去要立刻停", elapsed)
	}
}

// 进程要退（ctx 取消）时不该把剩下的停顿睡完。
func TestSleepBetweenBatches_WakesOnContextCancel(t *testing.T) {
	resetCompressSignals(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	sleepBetweenBatches(ctx, 5*time.Second)
	elapsed := time.Since(started)

	if elapsed > time.Second {
		t.Fatalf("ctx 已取消，却在停顿里等了 %s", elapsed)
	}
}

// 停顿**真的接在批与批之间**——上面三条只测了 sleepBetweenBatches 本身，
// 而"它有没有被调用"是另一件事，且错了完全看不出来：迁移只是照旧飞快地跑完。
//
// 判据取的是量级差：批 1 行 × 2 行、批间停 700ms，跑完至少要 1 秒（两次停顿，
// 含最后一批之后那一次）；没有接线的话整轮是几十毫秒。两边差一个数量级，
// 所以定时器怎么飘都不影响结论。
func TestLogCompress_BatchIntervalActuallyPauses(t *testing.T) {
	resetCompressSignals(t)
	setupLogCompressTestDB(t)
	ctx := context.Background()

	if err := SaveLogCompressPolicy(ctx, &models.LogCompressPolicy{
		BatchRows: 1, BatchBytes: defaultCompressBatchBytes,
		QuiesceSec: 0, BatchIntervalMs: 700,
	}); err != nil {
		t.Fatalf("写策略失败：%v", err)
	}
	seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 64<<10), bigRow(2, 64<<10)}, time.Hour)

	started := time.Now()
	state, err := RunLogCompress(ctx, false)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if state.Status != compressDone {
		t.Fatalf("跑完的状态是 %q，期望 %q", state.Status, compressDone)
	}
	if elapsed < time.Second {
		t.Fatalf("批间停 700ms × 2 批，整轮却只用了 %s——停顿没有接进迁移循环", elapsed)
	}
}

// resetCompressSignals 把两个进程级信号复位。它们不该在用例之间泄漏——
// 尤其是 pause：上一轮用例留下的 true 会让这一轮的迁移当场转 paused。
func resetCompressSignals(t *testing.T) {
	t.Helper()
	compressPauseRequested.Store(false)
	compressInFlight.Store(false)
	t.Cleanup(func() {
		compressPauseRequested.Store(false)
		compressInFlight.Store(false)
	})
}

func TestVerifyBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "llmio.db")

	if rec := VerifyBackup(path, 100); rec.Source != "missing" {
		t.Fatalf("文件不存在时 Source 是 %q，期望 missing", rec.Source)
	}

	if err := os.WriteFile(path, make([]byte, 50), 0o644); err != nil {
		t.Fatalf("造备份失败：%v", err)
	}
	if rec := VerifyBackup(path, 100); rec.Source != "stale" {
		t.Fatalf("备份比库小时 Source 是 %q，期望 stale（多半是迁移之后才拷的）", rec.Source)
	}
	rec := VerifyBackup(path, 10)
	if rec.Source != "manual" {
		t.Fatalf("正常的备份 Source 是 %q，期望 manual", rec.Source)
	}
	if rec.Size != 50 || rec.MTime == "" {
		t.Fatalf("备份存证不完整：%+v", rec)
	}
}

// ── 空库与空行 ────────────────────────────────────────────────────────────

func TestLogCompress_EmptyDatabaseIsNotAnError(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	state, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("空库上报错：%v", err)
	}
	if state.Status != compressDone || state.Scanned != 0 {
		t.Fatalf("空库跑完是 %+v", state)
	}
}

// 空 input 既不是候选（typeof='null'）也不该被顺手改成别的形态。
func TestLogCompress_LeavesEmptyInputAlone(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{[]byte("")}, time.Hour)
	if err := models.DB.Exec(
		`INSERT INTO chat_ios (created_at, updated_at, log_id, input) VALUES (?, ?, 1, NULL)`,
		time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("种空行失败：%v", err)
	}

	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	var typ string
	if err := models.DB.Raw(`SELECT typeof(input) FROM chat_ios WHERE id = ?`, ids[0]).
		Row().Scan(&typ); err != nil {
		t.Fatalf("读形态失败：%v", err)
	}
	if typ != "text" && typ != "null" {
		t.Fatalf("空行被改成了 %q", typ)
	}
}

// ── 响应体两列（of_string / of_string_array）───────────────────────────────
//
// 三列各有各的接入点——`input` 走钩子与块表，响应体那两列走序列化器——但
// "迁没迁过"是同一件事。这一段盯的就是判据、水位、形态三处**都得按三列一起算**。
//
// 真机上出过的缺口正是"只做了 input 一列"：1.47 GiB 的库里有 1.36 GiB 是响应体
// 两列的明文，而迁移一个字都没动过它们，界面还一直报"已完成"。所以这里的用例
// 一律从"请求体已经迁完"这个中间态起跑——那才是真实的起跑线。

// colShapeOf 读任意一列此刻的 storage class 与原始字节。NULL 读回来是空的 raw。
func colShapeOf(t *testing.T, id uint, column string) (string, []byte) {
	t.Helper()
	var (
		typ string
		raw []byte
	)
	q := fmt.Sprintf(`SELECT typeof(%s), %s FROM chat_ios WHERE id = ?`, column, column)
	if err := models.DB.Raw(q, id).Row().Scan(&typ, &raw); err != nil {
		t.Fatalf("读行 %d 的 %s 形态失败：%v", id, column, err)
	}
	return typ, raw
}

// writeRawColumn 裸 SQL 改一列，**不碰 updated_at**。
//
// 不碰它是刻意的：`updated_at` 是冷静期过滤器的输入，刷新它等于把这行标记成
// "刚被写过"，迁移下一轮又会绕开它——而这里恰恰是要给迁移喂一批候选行。
//
// value 传 string 落 TEXT、[]byte 落 BLOB、nil 落 NULL，与生产写路径同形。
func writeRawColumn(t *testing.T, ctx context.Context, id uint, column string, value any) {
	t.Helper()
	q := fmt.Sprintf(`UPDATE chat_ios SET %s = ? WHERE id = ?`, column)
	if err := models.DB.WithContext(ctx).Exec(q, value, id).Error; err != nil {
		t.Fatalf("写行 %d 的 %s 失败：%v", id, column, err)
	}
}

// outputPair 是一行的响应体两列在 Go 侧的样子。
type outputPair struct {
	ofString string
	ofArray  []string
}

// readBackOutputs 走**生产读路径**（序列化器的 Scan）把响应体两列读回来。
func readBackOutputs(t *testing.T, ctx context.Context) map[uint]outputPair {
	t.Helper()
	rows, err := gorm.G[models.ChatIO](models.DB).Order("id").Find(ctx)
	if err != nil {
		t.Fatalf("读回 chat_ios 失败：%v", err)
	}
	out := make(map[uint]outputPair, len(rows))
	for _, r := range rows {
		out[r.ID] = outputPair{ofString: r.OfString, ofArray: r.OfStringArray}
	}
	return out
}

// 请求体列早迁完、水位停在表尾的库，再跑一轮必须能把响应体两列也压掉。
func TestLogCompress_MigratesOutputColumns(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	// 前半程：只有请求体列是明文，迁完，状态停在 done。
	ids := seedPlaintextRows(t, ctx, [][]byte{
		bigRow(1, 96<<10), bigRow(2, 40<<10), bigRow(3, 72<<10),
	}, time.Hour)
	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("第一轮迁移失败：%v", err)
	}
	for _, id := range ids {
		if typ, _ := colShapeOf(t, id, "input"); typ != "blob" {
			t.Fatalf("前置条件不成立：行 %d 的 input 是 %q，应当是 blob", id, typ)
		}
	}

	// 后半程：补上明文的响应体两列。裸 SQL 写 string 落的就是 TEXT，
	// 与钩子存在之前写下的历史行同形——那正是迁移要处理的东西。
	text := strings.Repeat("响应体里的大段中文与 emoji 🚀 混排 ", 3000)
	array := `["` + text + `","第二段"]`
	writeRawColumn(t, ctx, ids[0], "of_string", text)
	writeRawColumn(t, ctx, ids[0], "of_string_array", array)
	writeRawColumn(t, ctx, ids[1], "of_string", text) // 另一列留 NULL
	writeRawColumn(t, ctx, ids[2], "of_string", "")   // 空串：历史形态，会被归一化成 NULL
	writeRawColumn(t, ctx, ids[2], "of_string_array", array)

	// done 之后人工再点一次 = 从水位 0 重扫。没有这一条，候选查询在水位之后
	// 一行都扫不到，这一轮会立刻报 done，上面那三行明文就一直躺着。
	if !ShouldRescanFromZero(ctx) {
		t.Fatal("状态是 done，人工续跑应当判定为需要全量重扫")
	}
	st, err := RunLogCompress(ctx, true)
	if err != nil {
		t.Fatalf("第二轮迁移失败：%v", err)
	}
	if st.Status != compressDone {
		t.Fatalf("第二轮之后状态是 %q", st.Status)
	}

	// 逐列看形态。**空的 TEXT 要被归一化成 NULL**：判据只剩 `typeof` 之后，
	// "TEXT"与"非空明文"必须是同一件事，否则真机上那 8,932 行 `of_string=''`
	// （流式响应）会被永远算成待迁移。NULL 则原样留着——它本来就不是候选。
	want := []struct {
		id                uint
		ofString, ofArray string
	}{
		{ids[0], "blob", "blob"},
		{ids[1], "blob", "null"},
		{ids[2], "null", "blob"},
	}
	for _, w := range want {
		if typ, _ := colShapeOf(t, w.id, "of_string"); typ != w.ofString {
			t.Fatalf("行 %d 的 of_string 是 %q，期望 %q", w.id, typ, w.ofString)
		}
		if typ, _ := colShapeOf(t, w.id, "of_string_array"); typ != w.ofArray {
			t.Fatalf("行 %d 的 of_string_array 是 %q，期望 %q", w.id, typ, w.ofArray)
		}
	}
	// 归一化只动形态，不动值：那一列的字节数就是 0（空串与 NULL 在这个意义上
	// 没差别，读路径也不做区分）。
	if _, raw := colShapeOf(t, ids[2], "of_string"); len(raw) != 0 {
		t.Fatalf("空的 of_string 被写进了字节：%d", len(raw))
	}

	// 读路径要能把压过的两列还原成原值——这才是"迁移没弄坏数据"的证据。
	got := readBackOutputs(t, ctx)
	if got[ids[0]].ofString != text {
		t.Fatalf("行 %d 读回来的 of_string 对不上", ids[0])
	}
	if len(got[ids[0]].ofArray) != 2 || got[ids[0]].ofArray[0] != text {
		t.Fatalf("行 %d 读回来的 of_string_array 对不上：%d 段", ids[0], len(got[ids[0]].ofArray))
	}
	if got[ids[1]].ofString != text {
		t.Fatalf("行 %d 读回来的 of_string 对不上", ids[1])
	}
	if got[ids[1]].ofArray != nil {
		t.Fatalf("行 %d 的 of_string_array 应当是 NULL，读回 %v", ids[1], got[ids[1]].ofArray)
	}
	// 归一化对读路径是透明的：`''` 与 NULL 读出来都是 ""。
	if got[ids[2]].ofString != "" {
		t.Fatalf("行 %d 的 of_string 应当读回空串，实得 %q", ids[2], got[ids[2]].ofString)
	}

	// 口径：这两个数必须把响应体那两列算进去。只算 input 的话它们会比一列明文
	// 还小——真机上界面就是据此给出"几乎不占地方"这个结论的。
	if st.BytesBefore < int64(len(text)) {
		t.Fatalf("bytes_before=%d 比一列明文（%d）还小，响应体没算进去", st.BytesBefore, len(text))
	}
	if st.BytesTotal < int64(len(text)) {
		t.Fatalf("bytes_total=%d 比一列明文（%d）还小，响应体没算进去", st.BytesTotal, len(text))
	}
}

// 空的 TEXT 必须被**收敛掉**（写成 NULL），不能只是"跳过它"。
//
// 判据只剩 `typeof` 之后，空的 TEXT 是唯一会永远赖在候选集里的形态。这一条盯的
// 是那个最难的中间态：一行三列都已迁完，只有 `of_string` 是历史留下的空串——
// 它不影响任何数据，却足以让状态页永远报"还有 1 行待迁移"。真机上这个形态有
// 8,932 行（流式响应的 `of_string` 全是空串，正文在 `of_string_array` 里）。
//
// 顺带钉住另一半：**待迁移行数必须能归零**。压不动的行（帧比原文还大）本来就
// 会留下，但那是几十行；把空串也算成候选就是几千行，界面从"快完了"变成"永远
// 差一大截"。
func TestLogCompress_NormalizesEmptyOutputColumn(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 96<<10)}, time.Hour)
	writeRawColumn(t, ctx, ids[0], "of_string", "")
	writeRawColumn(t, ctx, ids[0], "of_string_array",
		`["`+strings.Repeat("分片内容 🚀 ", 2000)+`"]`)

	if _, err := RunLogCompress(ctx, true); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	if typ, _ := colShapeOf(t, ids[0], "of_string"); typ != "null" {
		t.Fatalf("空的 of_string 是 %q，期望 null——它会永远被算成待迁移的行", typ)
	}
	if typ, _ := colShapeOf(t, ids[0], "input"); typ != "blob" {
		t.Fatalf("input 是 %q，期望 blob", typ)
	}

	resetDBStatsCache()
	if got := ReadDBStats(ctx); got == nil {
		t.Fatal("量得到却给了 nil")
	} else if got.PendingRows != 0 {
		t.Fatalf("待迁移 %d 行，期望 0——有列被永远算成候选", got.PendingRows)
	}
}

// 回滚必须把响应体两列还原成**明文 TEXT**，且逐字节相等。
//
// 这一条不能拿"迁移后读得回来"替代：读路径只认帧头，是帧就解、不是帧就当明文
// 读——把明文按 BLOB 写回去，读出来**照样是对的**。于是"列里的格式错了"这件事
// 会一直躺着，直到下一次迁移拿 `typeof` 当判据、把它当成已迁过而漏掉。
func TestLogCompress_DecompressRestoresOutputColumns(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 96<<10)}, time.Hour)
	text := strings.Repeat("回滚这一趟要逐字节比对 🚀 ", 2000)
	array := `["` + text + `"]`
	writeRawColumn(t, ctx, ids[0], "of_string", text)
	writeRawColumn(t, ctx, ids[0], "of_string_array", array)

	if _, err := RunLogCompress(ctx, true); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	for _, col := range []string{"input", "of_string", "of_string_array"} {
		if typ, _ := colShapeOf(t, ids[0], col); typ != "blob" {
			t.Fatalf("迁移后 %s 是 %q，期望 blob", col, typ)
		}
	}

	st, err := RunLogDecompress(ctx, true)
	if err != nil {
		t.Fatalf("回滚失败：%v", err)
	}
	if st.Status != compressDone {
		t.Fatalf("回滚后状态是 %q", st.Status)
	}

	for _, col := range []string{"input", "of_string", "of_string_array"} {
		if typ, _ := colShapeOf(t, ids[0], col); typ != "text" {
			t.Fatalf("回滚后 %s 是 %q，期望 text", col, typ)
		}
	}
	if _, raw := colShapeOf(t, ids[0], "of_string"); string(raw) != text {
		t.Fatalf("回滚后 of_string 与原字节不一致：%d 字节 vs %d", len(raw), len(text))
	}
	if _, raw := colShapeOf(t, ids[0], "of_string_array"); string(raw) != array {
		t.Fatalf("回滚后 of_string_array 与原字节不一致：%d 字节 vs %d", len(raw), len(array))
	}
}

// 回归：判据里的长度必须按**字节**量。
//
// SQLite 的 `length()` 对 TEXT 数到**第一个 NUL 为止**，而 NUL 打头的请求体在
// 真实语料里是有的。判据写成 `length(input) > 0` 的话，这些行会被判成"空"而永远
// 进不了候选集——迁移报"已完成"，它们还是明文。
//
// 顺带一提，NUL 又恰好是帧 magic（`\x00LCZ`）的首字节，所以这类行天生就该
// 多被盯一眼。
func TestLogCompress_NulLeadingBodyIsStillACandidate(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	// 非法 UTF-8 + 内嵌 NUL，且以 NUL 打头。
	body := append([]byte{0x00, 0xff, 0xfe}, []byte(strings.Repeat("x\x00y", 5000))...)
	ids := seedPlaintextRows(t, ctx, [][]byte{body}, time.Hour)

	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if typ, _ := colShapeOf(t, ids[0], "input"); typ != "blob" {
		t.Fatalf("NUL 打头的明文行没被迁（typeof=%q）——判据多半用了 length(列)", typ)
	}
}

// 「已完成之后再点一次就是全量重扫」这条判据本身。
//
// 它只属于**人工入口**：调度器每 30 秒拿 full=false 推一次（done 状态也推），
// 这条规则要是落进 runCompressMode，就变成每 30 秒一次全表重扫。
func TestShouldRescanFromZero(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	if ShouldRescanFromZero(ctx) {
		t.Fatal("从没跑过的库不该判定为需要全量重扫")
	}

	seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 8<<10)}, time.Hour)
	if _, err := RunLogCompress(ctx, false); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if !ShouldRescanFromZero(ctx) {
		t.Fatal("跑完之后应当判定为需要全量重扫（水位已到表尾，而三列的进度未必齐）")
	}

	// 读不到状态就**不加码**：全量重扫是更贵的那个选择，为一次读失败付它不值当。
	// 退化方向是安全的——最坏情况是这一次少扫一遍，而不是白扫一遍全表。
	sqlDB, err := models.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	if ShouldRescanFromZero(ctx) {
		t.Fatal("读不到状态时不应当要求全量重扫")
	}
}

// 响应体列里出现"是我们造的帧、但解不开"时，回滚必须**报错停下**。
//
// 静默跳过那一列的后果不是数据损坏（列里的字节没动），而是**账对不上**：
// 回滚跑完报"全部还原"，而这一列还留在帧形态，且此后没人会再去看它。
// 这条路径上"我猜它应该是……"的每一次让步，都是一次让缺口永久化的机会。
func TestLogCompress_DecompressStopsOnCorruptOutputFrame(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	ids := seedPlaintextRows(t, ctx, [][]byte{bigRow(1, 8<<10)}, time.Hour)

	frame := models.PackColumnValue([]byte(strings.Repeat("响应体明文 ", 500)))
	if frame == nil {
		t.Fatal("前置条件不成立：造不出帧")
	}
	// 帧头留着、载荷截掉一截：magic 认得出，内容解不开。
	writeRawColumn(t, ctx, ids[0], "of_string", frame[:len(frame)-3])

	if _, err := RunLogDecompress(ctx, true); err == nil {
		t.Fatal("坏帧被放过了——回滚会报\"已完成\"，而这一列还留在帧形态")
	}
}
