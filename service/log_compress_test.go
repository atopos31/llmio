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
		t.Fatalf("已经有一轮在跑时应当报 ErrCompressRunning，得到 %v", err)
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
			models.LogCompressPolicy{BatchRows: 1 << 20, BatchBytes: 1 << 40, QuiesceSec: 1 << 20},
			models.LogCompressPolicy{BatchRows: 4096, BatchBytes: 1 << 30, QuiesceSec: 86400},
		},
		{
			"负数回默认",
			models.LogCompressPolicy{BatchRows: -1, BatchBytes: -1, QuiesceSec: -1},
			models.LogCompressPolicy{
				BatchRows: defaultCompressBatchRows, BatchBytes: defaultCompressBatchBytes, QuiesceSec: 0,
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
				got.QuiesceSec != tc.want.QuiesceSec {
				t.Fatalf("夹取结果 %+v，期望 %+v", *got, tc.want)
			}
		})
	}
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
