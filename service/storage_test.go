package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 这个文件盯的是**存储层**：auto_vacuum 的开启与空间回收。两组现场必须都测，
// 因为它们走的是完全不同的代码路径，而且**老库那条路一个字节都不许动**：
//
//	新库 —— 0 页时设 pragma，ptrmap 跟着表一起长出来，**白拿**，不花任何代价。
//	老库 —— 默认什么都不做。要转换只能显式开 `DB_AUTO_VACUUM_REBUILD=on`，
//	        那是一次 55 秒的全库重写（实测 7.05 GiB），绝不能是启动副作用。
//
// 老库这条约束不是洁癖：一份 7 GiB 的老库可能只是被重启了一下，而它上面的
// 服务正跑着。

// makeOldDatabase 造一份**老库**：有页、auto_vacuum=0。
//
// 关键是绕开 models.Init——它对空库会设 pragma，那就不是老库了。
// 先用驱动自己建一张表塞几行，让 page_count > 0，再交给 models.Init。
func makeOldDatabase(t *testing.T, rows, rowBytes int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.db")

	seed, err := gorm.Open(sqlite.Open(path))
	if err != nil {
		t.Fatalf("造老库失败：%v", err)
	}
	if err := seed.Exec(`CREATE TABLE seed (id INTEGER PRIMARY KEY, body BLOB)`).Error; err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	blob := make([]byte, rowBytes)
	for i := 0; i < rows; i++ {
		// 传 string 而不是 []byte：这个纯 Go 驱动会把 []byte 参数摊成
		// 一长串数字值，报 "16384 values for 1 columns"。这里只要页数，不要类型。
		if err := seed.Exec(`INSERT INTO seed (body) VALUES (?)`, string(blob)).Error; err != nil {
			t.Fatalf("插种子行失败：%v", err)
		}
	}
	if sqlDB, err := seed.DB(); err == nil {
		_ = sqlDB.Close()
	}

	// 交回给生产那条路。它必须**认出这是老库**并原样放过。
	models.Init(context.Background(), path)
	t.Cleanup(func() {
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return path
}

func autoVacuumOf(t *testing.T, path string) int64 {
	t.Helper()
	// 另开一条连接读：读的是**文件头**，不是当前进程里某个连接的状态。
	// 这正是"转换有没有真的落下"的判据。
	db, err := gorm.Open(sqlite.Open(path))
	if err != nil {
		t.Fatalf("新连接打开失败：%v", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	var v int64
	if err := db.Raw(`PRAGMA auto_vacuum`).Row().Scan(&v); err != nil {
		t.Fatalf("读 auto_vacuum 失败：%v", err)
	}
	return v
}

func fileSizeOf(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("量文件大小失败：%v", err)
	}
	return info.Size()
}

// rowBodies 造 n 份互不相同、体积够大的正文，用来把页撑出来。
// 内容不重要，重要的是**别让它们互相去重**——那会让"占了多少页"变得不可预期。
func rowBodies(n, size int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		body := make([]byte, size)
		copy(body, fmt.Sprintf("row-%04d-", i))
		for j := 8; j < size; j++ {
			body[j] = byte('a' + i%26)
		}
		out[i] = body
	}
	return out
}

// ── 新库 ──────────────────────────────────────────────────────────────────

// 空库上白拿 ptrmap：不 VACUUM、不花时间、连一次写都不多。
func TestStorage_NewDatabaseGetsIncrementalForFree(t *testing.T) {
	setupLogCompressTestDB(t)

	if got := autoVacuumOf(t, models.DBPath); got != models.AutoVacuumIncremental {
		t.Fatalf("空库应当白拿 auto_vacuum=INCREMENTAL，实得 %d", got)
	}
}

// 新库那条路的完整闭环：删行 → 页进 freelist → 回收 → **文件真的变小**。
//
// 这一条同时是"回收到底有没有用"的正面证据。反面证据在同文件的
// TestStorage_ReclaimIsNoopWithoutAutoVacuum。
func TestStorage_ReclaimShrinksFileOnIncrementalDatabase(t *testing.T) {
	models.Init(context.Background(), filepath.Join(t.TempDir(), "new.db"))
	t.Cleanup(closeTestDB)

	ctx := context.Background()
	seedPlaintextRows(t, ctx, rowBodies(60, 16<<10), time.Hour)
	before := fileSizeOf(t, models.DBPath)

	if err := models.DB.WithContext(ctx).Exec(`DELETE FROM chat_ios`).Error; err != nil {
		t.Fatalf("删行失败：%v", err)
	}
	counters, err := readStorageCounters()
	if err != nil {
		t.Fatal(err)
	}
	if counters.freelist == 0 {
		t.Fatal("删完之后 freelist 应当是满的——删行只把页还进 freelist")
	}

	rec, err := ReclaimFreePages(ctx)
	if err != nil {
		t.Fatalf("回收失败：%v", err)
	}
	if rec.StopReason != "empty" {
		t.Fatalf("这么小的库应当一趟放完，实得 %q", rec.StopReason)
	}
	if rec.FreelistAfter != 0 {
		t.Fatalf("回收后 freelist 应当是 0，实得 %d", rec.FreelistAfter)
	}
	if rec.FileSizeAfter >= before {
		t.Fatalf("文件没缩：回收前 %d，回收后 %d", before, rec.FileSizeAfter)
	}
	if rec.FreedPages <= 0 || rec.FreedBytes <= 0 {
		t.Fatalf("放掉的页数/字节数没记上：pages=%d bytes=%d", rec.FreedPages, rec.FreedBytes)
	}
}

// ── 老库 ──────────────────────────────────────────────────────────────────

// 这是**兼容性保证**那一条：一份老库被打开，默认什么都不该发生。
func TestStorage_OldDatabaseIsLeftAloneByDefault(t *testing.T) {
	t.Setenv(models.StorageRebuildEnv, "")
	t.Setenv(models.DBVacuumEnv, "")
	path := makeOldDatabase(t, 40, 16<<10)

	if got := autoVacuumOf(t, path); got != models.AutoVacuumNone {
		t.Fatalf("老库的 auto_vacuum 被改了：%d", got)
	}
	sizeBefore := fileSizeOf(t, path)

	// 默认开关下，启动期维护整段应当是空转。
	PrepareStorage(context.Background())

	if got := autoVacuumOf(t, path); got != models.AutoVacuumNone {
		t.Fatalf("默认启动流程动了老库的 auto_vacuum：%d", got)
	}
	if after := fileSizeOf(t, path); after != sizeBefore {
		t.Fatalf("默认启动流程动了老库的文件：%d → %d", sizeBefore, after)
	}
}

// 老库没开 auto_vacuum 时，**增量回收是个空操作**（实测 0.000 秒，文件一字节不缩）。
// 不说出来的话，用户看到的是"点了回收、瞬间完成、什么都没变"。
func TestStorage_ReclaimIsNoopWithoutAutoVacuum(t *testing.T) {
	t.Setenv(models.StorageRebuildEnv, "")
	path := makeOldDatabase(t, 40, 16<<10)
	ctx := context.Background()

	// 造一个真实的 freelist：删掉种子表的一半。
	if err := models.DB.WithContext(ctx).Exec(`DELETE FROM seed WHERE id % 2 = 0`).Error; err != nil {
		t.Fatalf("删行失败：%v", err)
	}
	counters, err := readStorageCounters()
	if err != nil {
		t.Fatal(err)
	}
	if counters.freelist == 0 {
		t.Fatal("现场没造对：freelist 是空的")
	}

	rec, err := ReclaimFreePages(ctx)
	if err != nil {
		t.Fatalf("回收失败：%v", err)
	}
	if rec.StopReason != "no_auto_vacuum" {
		t.Fatalf("应当如实报 no_auto_vacuum，实得 %q", rec.StopReason)
	}
	if rec.FreedPages != 0 {
		t.Fatalf("没开 auto_vacuum 时放出页数应当是 0，实得 %d", rec.FreedPages)
	}
	if after := fileSizeOf(t, path); after != counters.fileSize {
		t.Fatalf("文件不该变：%d → %d", counters.fileSize, after)
	}
}

// 显式要求时才转换，而且转换**必须真的落进文件头**（新连接也读得到）。
func TestStorage_ConvertsOldDatabaseOnlyWhenAsked(t *testing.T) {
	path := makeOldDatabase(t, 60, 16<<10)
	t.Setenv(models.StorageRebuildEnv, "on")
	t.Setenv(models.DBVacuumEnv, "")

	PrepareStorage(context.Background())

	if got := autoVacuumOf(t, path); got != models.AutoVacuumIncremental {
		t.Fatalf("开了 on 却没转成 INCREMENTAL，实得 %d", got)
	}

	rec, err := GetLogReclaimState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rec.StopReason != "converted" {
		t.Fatalf("应当留下 converted 的记录，实得 %q", rec.StopReason)
	}
	if rec.Source != "startup" {
		t.Fatalf("来源应当是 startup，实得 %q", rec.Source)
	}
}

// 转换过之后，增量回收就真的能放页了——这正是转换的意义。
func TestStorage_ReclaimWorksAfterConversion(t *testing.T) {
	path := makeOldDatabase(t, 60, 16<<10)
	t.Setenv(models.StorageRebuildEnv, "on")
	ctx := context.Background()

	PrepareStorage(ctx)

	if err := models.DB.WithContext(ctx).Exec(`DELETE FROM seed`).Error; err != nil {
		t.Fatalf("删行失败：%v", err)
	}
	before := fileSizeOf(t, path)

	rec, err := ReclaimFreePages(ctx)
	if err != nil {
		t.Fatalf("回收失败：%v", err)
	}
	if rec.StopReason != "empty" || rec.FreedPages == 0 {
		t.Fatalf("转换之后应当放得动页：reason=%q freed=%d", rec.StopReason, rec.FreedPages)
	}
	if after := fileSizeOf(t, path); after >= before {
		t.Fatalf("转换之后回收没让文件变小：%d → %d", before, after)
	}
}

// `DB_VACUUM=true` 的老语义要保住：启动时 VACUUM 一把。
// 但不再 panic——原先是 `panic(err)`，且连磁盘够不够都没看过。
func TestStorage_StartupVacuumStillWorks(t *testing.T) {
	makeOldDatabase(t, 60, 16<<10)
	t.Setenv(models.StorageRebuildEnv, "")
	t.Setenv(models.DBVacuumEnv, "true")
	ctx := context.Background()

	if err := models.DB.WithContext(ctx).Exec(`DELETE FROM seed`).Error; err != nil {
		t.Fatalf("删行失败：%v", err)
	}
	before, _ := readStorageCounters()
	if before.freelist == 0 {
		t.Fatal("现场没造对：freelist 是空的")
	}

	PrepareStorage(ctx)

	after, _ := readStorageCounters()
	if after.freelist != 0 {
		t.Fatalf("启动期 VACUUM 没放掉 freelist：%d", after.freelist)
	}
	rec, err := GetLogReclaimState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.StopReason != "vacuumed" {
		t.Fatalf("应当留下 vacuumed 的记录，实得 %q", rec.StopReason)
	}
}

// 转换开着、VACUUM 也开着：只该做**一次**全库重写（转换本身就是 VACUUM）。
func TestStorage_ConversionSubsumesStartupVacuum(t *testing.T) {
	path := makeOldDatabase(t, 60, 16<<10)
	t.Setenv(models.StorageRebuildEnv, "on")
	t.Setenv(models.DBVacuumEnv, "true")

	PrepareStorage(context.Background())

	if got := autoVacuumOf(t, path); got != models.AutoVacuumIncremental {
		t.Fatalf("应当转成 INCREMENTAL，实得 %d", got)
	}
	rec, err := GetLogReclaimState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rec.StopReason != "converted" {
		t.Fatalf("两个开关同时开着时应当报 converted（一次重写干两件事），实得 %q", rec.StopReason)
	}
}

// 磁盘不够就**跳过**，不是失败——服务照常起。
func TestStorage_InsufficientDiskIsSkipped(t *testing.T) {
	if enoughDisk(1<<30, 1<<30) {
		t.Fatal("1 GiB 空闲放不下 1 GiB 的库，预检应当判不过")
	}
	if !enoughDisk(8<<30, 1<<30) {
		t.Fatal("8 GiB 空闲应当够 1 GiB 的库")
	}
	// 边界：2×库 + 余量，正好不够一格。
	dbSize := int64(1 << 30)
	if enoughDisk(2*dbSize+reclaimDiskMargin-1, dbSize) {
		t.Fatal("差一个字节也算不够")
	}
	if !enoughDisk(2*dbSize+reclaimDiskMargin, dbSize) {
		t.Fatal("正好等于门槛应当算够")
	}
}

// 迁移（或清理）在跑时不许插队：它们动的是同一批页。
func TestStorage_ReclaimRefusesWhenMaintenanceBusy(t *testing.T) {
	setupLogCompressTestDB(t)

	maintenanceMu.Lock()
	defer maintenanceMu.Unlock()

	if _, err := ReclaimFreePages(context.Background()); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("抢不到维护锁时应当报 ErrMaintenanceBusy，实得 %v", err)
	}
}

// 抢锁只挡得住"同时"，挡不住"交替"——两轮回收首尾相接时，第二轮的 TryLock
// 是能成功的（第一轮已经放锁了）。所以另有一道 CAS，这里钉的是那一道。
func TestStorage_ReclaimRefusesWhenAlreadyInFlight(t *testing.T) {
	setupLogCompressTestDB(t)

	reclaimInFlight.Store(true)
	t.Cleanup(func() { reclaimInFlight.Store(false) })

	if !ReclaimRunning() {
		t.Fatal("ReclaimRunning 没反映在飞的这一轮——界面据此显示「回收中」")
	}
	if _, err := ReclaimFreePages(context.Background()); !errors.Is(err, ErrReclaimRunning) {
		t.Fatalf("已有一轮在跑时应当报 ErrReclaimRunning，实得 %v", err)
	}
}

// 回收收工之后标志要放掉，否则按钮会永远停在"回收中"。
func TestStorage_ReclaimClearsInFlightAfterFinishing(t *testing.T) {
	setupLogCompressTestDB(t)

	if _, err := ReclaimFreePages(context.Background()); err != nil {
		t.Fatalf("回收失败：%v", err)
	}
	if ReclaimRunning() {
		t.Fatal("收工了却还报在跑")
	}
}

// ── 批的大小 ──────────────────────────────────────────────────────────────

// `reclaimNextBatch` 的两条分支：打满，或者只打剩下的。
//
// 这一条不是形式主义。真机上量出来的事实是**一条 `incremental_vacuum` 恰好放
// 一页**（参数被驱动忽略，见 reclaimPagesPerTx），所以"这一批打多少条"就等于
// "这一批放多少页"——多打的部分是纯粹的**空转**，而空转也发生在写事务里。
func TestReclaimNextBatch(t *testing.T) {
	cases := []struct {
		freelist int64
		want     int
	}{
		{-1, 0}, {0, 0}, {1, 1},
		{reclaimPagesPerTx - 1, reclaimPagesPerTx - 1},
		{reclaimPagesPerTx, reclaimPagesPerTx},
		{reclaimPagesPerTx + 1, reclaimPagesPerTx},
		{10 * reclaimPagesPerTx, reclaimPagesPerTx},
	}
	for _, c := range cases {
		if got := reclaimNextBatch(c.freelist); got != c.want {
			t.Errorf("reclaimNextBatch(%d) = %d，期望 %d", c.freelist, got, c.want)
		}
	}
}

// 收工理由的全部判断，包括那条**优先级**：放完了 > 停滞 > 到点。
func TestReclaimVerdict(t *testing.T) {
	cases := []struct {
		name          string
		before, after int64
		elapsed       time.Duration
		want          string
	}{
		{"放完了", 100, 0, 0, "empty"},
		{"放完了且已到点：报放完了", 100, 0, reclaimBudget, "empty"},
		{"一页没少：停滞", 100, 100, 0, "stalled"},
		{"反而变多：也是停滞", 100, 120, 0, "stalled"},
		{"停滞且已到点：停滞排在前面", 100, 100, reclaimBudget, "stalled"},
		{"还有得放：接着放", 100, 50, 0, ""},
		{"还有得放但到点了", 100, 50, reclaimBudget, "budget"},
		{"差一点到点：接着放", 100, 50, reclaimBudget - time.Millisecond, ""},
	}
	for _, c := range cases {
		if got := reclaimVerdict(c.before, c.after, c.elapsed); got != c.want {
			t.Errorf("%s：reclaimVerdict(%d, %d, %v) = %q，期望 %q",
				c.name, c.before, c.after, c.elapsed, got, c.want)
		}
	}
}

// **一条语句恰好放一页**——这条驱动行为是整个回收设计的承重事实：
// `reclaimPagesPerTx` 的解释、90 秒预算折出来的吞吐（前 9 个满轮实测约 472 MiB/轮）、
// 以及"大 freelist 该用 VACUUM 而不是增量回收"这个结论，全都建立在它上面。
//
// 它同时是一道**告警**：换驱动、或驱动升级后这个 pragma 又开始认参数了，
// 这里会红，提醒把那几个常数按新的每语句页数重新推一遍（那时
// reclaimBatch 里的恒 `1` 会保证这一批仍只放 batch 页，语义不会错，
// 只是常数不再是最优）。
func TestReclaimBatchFreesExactlyOnePagePerStatement(t *testing.T) {
	models.Init(context.Background(), filepath.Join(t.TempDir(), "batch.db"))
	t.Cleanup(closeTestDB)
	ctx := context.Background()

	seedPlaintextRows(t, ctx, rowBodies(40, 16<<10), time.Hour)
	if err := models.DB.WithContext(ctx).Exec(`DELETE FROM chat_ios`).Error; err != nil {
		t.Fatalf("删行失败：%v", err)
	}
	before, err := readStorageCounters()
	if err != nil {
		t.Fatal(err)
	}
	if before.freelist < 8 {
		t.Fatalf("现场没造对：freelist 只有 %d 页，量不出比例", before.freelist)
	}

	// 只放一半，好让"放掉的页数"和"删掉的全部页数"能分开看。
	batch := int(before.freelist / 2)
	if err := reclaimBatch(ctx, batch); err != nil {
		t.Fatalf("回收这一批失败：%v", err)
	}
	after, err := readStorageCounters()
	if err != nil {
		t.Fatal(err)
	}
	if freed := before.freelist - after.freelist; freed != int64(batch) {
		t.Fatalf("打了 %d 条语句却放掉 %d 页——一条一页这条事实变了，"+
			"reclaimPagesPerTx / reclaimBudget 都要按新的数重新推", batch, freed)
	}
}

// 一轮小库回收里，语句数和放掉的页数应当**逐一对上**（一条一页，没有空转）。
// 对不上的话，要么是上面那条事实变了，要么是循环多打了语句。
func TestStorage_ReclaimStatementsMatchFreedPages(t *testing.T) {
	models.Init(context.Background(), filepath.Join(t.TempDir(), "count.db"))
	t.Cleanup(closeTestDB)
	ctx := context.Background()

	seedPlaintextRows(t, ctx, rowBodies(60, 16<<10), time.Hour)
	if err := models.DB.WithContext(ctx).Exec(`DELETE FROM chat_ios`).Error; err != nil {
		t.Fatalf("删行失败：%v", err)
	}

	rec, err := ReclaimFreePages(ctx)
	if err != nil {
		t.Fatalf("回收失败：%v", err)
	}
	if rec.StopReason != "empty" {
		t.Fatalf("这么小的库应当一趟放完，实得 %q", rec.StopReason)
	}
	if rec.Calls != int(rec.FreedPages) {
		t.Fatalf("语句数 %d 与实际放掉的 %d 页对不上", rec.Calls, rec.FreedPages)
	}
	if rec.Calls != int(rec.FreelistBefore) {
		t.Fatalf("起始 freelist 是 %d 页，却打了 %d 条语句——多打的都是空转",
			rec.FreelistBefore, rec.Calls)
	}
}

// ── 记录 ──────────────────────────────────────────────────────────────────

// 没回收过的时候给 idle，而不是给 nil 或者零值记录。
// 界面拿 status 判"有没有跑过"，给 nil 会在前端炸出一个空指针。
func TestStorage_ReclaimStateDefaultsToIdle(t *testing.T) {
	setupLogCompressTestDB(t)

	rec, err := GetLogReclaimState(context.Background())
	if err != nil {
		t.Fatalf("没记录不该报错：%v", err)
	}
	if rec == nil || rec.Status != "idle" {
		t.Fatalf("没回收过时应当是 idle，实得 %+v", rec)
	}
}

// 手动回收跑完要落在盘上，而且来源记成 manual——与启动期那条区分开。
func TestStorage_ManualReclaimIsRecorded(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	if _, err := ReclaimFreePages(ctx); err != nil {
		t.Fatalf("回收失败：%v", err)
	}
	rec, err := GetLogReclaimState(ctx)
	if err != nil {
		t.Fatalf("读记录失败：%v", err)
	}
	if rec.Status != "done" {
		t.Fatalf("收工后状态应当是 done，实得 %q", rec.Status)
	}
	if rec.Source != "manual" {
		t.Fatalf("来源应当是 manual，实得 %q", rec.Source)
	}
	if rec.StartedAt == "" || rec.FinishedAt == "" {
		t.Fatalf("起止时刻都要记：%+v", rec)
	}
}

// 盘上那份记录坏了（被人手改、或写到一半断电）时如实报错，
// 不静默返回一份空记录——空记录在界面上是"从没回收过"，那是另一件事。
func TestStorage_ReclaimStateCorruptIsAnError(t *testing.T) {
	setupLogCompressTestDB(t)
	ctx := context.Background()

	if err := models.SaveConfigValue(ctx, models.KeyLogReclaimState, "{不是 JSON"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetLogReclaimState(ctx); err == nil {
		t.Fatal("记录坏了应当报错，而不是给一份空记录")
	}
}

func closeTestDB() {
	if sqlDB, err := models.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
