package models

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 本文件验证**从历史发布版本自己的库升级到本分支**的路径。
//
// 为什么还需要它（migrate_test.go 已经在测上游旧库了）：testdata/upstream_schema.sql
// 只是 master 这一代的结构。比它更老的版本建出来的库形态完全不同——v0.1.0 的 models
// 表还带着 io_log 列、连 configs 表都还没有——手写一份「我以为的旧结构」只能证明
// 按那份结构能升级，证明不了按真实旧库能升级。
//
// 所以由 tools/versiondb 用每个历史 tag 自己的代码建库（见
// .github/workflows/db-compat.yml），再把库交给这里升级。断言的是契约，不是列名：
//
//  1. PRAGMA integrity_check 必须 ok；
//  2. 旧库每一行、每一个值：非 NULL 且非空的值必须原样保留（NULL/空串允许被兼容回填改写）；
//  3. 旧库的列一个都不能少（老列允许留在表上不删）；
//  4. 当前代码需要的列/索引必须全部建出来（对照同目录新建的库；多余的老列允许）；
//  5. 再 Init 一次不再改动任何数据；
//  6. 升级后的库能被当前模型写入（新列真的可用）。
//
// 手动复现某个版本：
//
//	go run ./tools/versiondb -ref v0.5.0 -out /tmp/v0.5.0.db
//	LLMIO_COMPAT_DB=/tmp/v0.5.0.db LLMIO_COMPAT_TAG=v0.5.0 go test ./models -run TestUpgradeFromReleaseDB -v
func TestUpgradeFromReleaseDB(t *testing.T) {
	src := os.Getenv("LLMIO_COMPAT_DB")
	if src == "" {
		t.Skip("未设置 LLMIO_COMPAT_DB：跳过「历史版本库升级」测试（CI 见 .github/workflows/db-compat.yml）")
	}
	tag := os.Getenv("LLMIO_COMPAT_TAG")
	if tag == "" {
		tag = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}

	work := t.TempDir()
	path := filepath.Join(work, "release.db")
	if err := compatCopyFile(src, path); err != nil {
		t.Fatalf("复制 %s 到临时目录失败：%v", src, err)
	}

	// 1. 按该版本**实际存在**的列塞两行数据：不看当前模型，只看库里的表结构。
	//    这样才能覆盖到「这个版本多出来/少掉的列」而不写死任何一代的列名。
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开历史版本库失败：%v", err)
	}
	compatSeedRows(t, legacy)
	before := compatSnapshotDB(t, legacy)
	legacySQL, err := legacy.DB()
	if err != nil {
		t.Fatalf("取历史版本库连接失败：%v", err)
	}
	if err := legacySQL.Close(); err != nil {
		t.Fatalf("关闭历史版本库连接失败：%v", err)
	}

	// 2. 用当前代码升级它。Init 会自己开新连接并写包级 DB。
	Init(context.Background(), path)
	closeCurrentDBOnCleanup(t)

	// 3. 升级结果断言
	compatAssertIntegrity(t, tag, DB)
	after := compatSnapshotDB(t, DB)
	compatAssertRowsPreserved(t, tag, before, after)
	upgraded := compatReadSchema(t, DB)

	// 4. 二次 Init 必须幂等：同一个库再启动一次不该改动任何数据
	closeCurrentDB(t)
	Init(context.Background(), path)
	again := compatSnapshotDB(t, DB)
	if !reflect.DeepEqual(after, again) {
		t.Errorf("[%s] 二次启动改动了数据（Init 必须幂等）：\n%s", tag, strings.Join(compatDiffSnapshots(after, again, 8), "\n"))
	}

	// 5. 升级后的库必须接受当前模型的写入：新列/新索引真的可用
	chatLog := ChatLog{
		Name:       "compat-" + tag,
		TraceID:    "compat-" + tag,
		Status:     "success",
		Style:      "openai",
		PeakPeriod: "peak",
		Currency:   "CNY",
	}
	if err := DB.Create(&chatLog).Error; err != nil {
		t.Fatalf("[%s] 升级后写入 ChatLog 失败：%v", tag, err)
	}
	reloaded, err := gorm.G[ChatLog](DB).Where("trace_id = ?", chatLog.TraceID).First(context.Background())
	if err != nil {
		t.Fatalf("[%s] 升级后读回 ChatLog 失败：%v", tag, err)
	}
	if reloaded.PeakPeriod != "peak" || reloaded.Currency != "CNY" {
		t.Fatalf("[%s] 升级后新列读回不一致：peak_period=%q currency=%q", tag, reloaded.PeakPeriod, reloaded.Currency)
	}

	// 6. 结构必须收敛到当前代码：对照同一个版本/同一份代码新建的空库
	closeCurrentDB(t)
	Init(context.Background(), filepath.Join(work, "fresh.db"))
	fresh := compatReadSchema(t, DB)
	compatAssertSchemaConverged(t, tag, fresh, upgraded)

	legacyColumns := compatLegacyOnlyColumns(before, fresh)
	t.Logf("[%s] 旧库 %d 张表 / %d 行 → 升级后 %d 张表 / %d 行；当前代码已没有、但仍留在库上的老列 %d 个 %v",
		tag, len(before.Tables), before.rowCount(), len(after.Tables), after.rowCount(), len(legacyColumns), legacyColumns)
}

// compatCopyFile 复制库文件（连 -wal/-shm 一起，避免复制出半截事务）。
func compatCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---------------------------------------------------------------------------
// 表结构读取
// ---------------------------------------------------------------------------

type compatColumnInfo struct {
	Name    string `gorm:"column:name"`
	Type    string `gorm:"column:type"`
	NotNull int    `gorm:"column:notnull"`
	PK      int    `gorm:"column:pk"`
}

type compatIndexInfo struct {
	Seq     int    `gorm:"column:seq"`
	Name    string `gorm:"column:name"`
	Unique  int    `gorm:"column:unique"`
	Origin  string `gorm:"column:origin"`
	Partial int    `gorm:"column:partial"`
}

type compatIndexColumn struct {
	Seqno int    `gorm:"column:seqno"`
	Cid   int    `gorm:"column:cid"`
	Name  string `gorm:"column:name"`
}

// compatQuote 给标识符加双引号。
func compatQuote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// compatTableNames 列出库里的用户表（跳过 sqlite_ 内部表）。
func compatTableNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var names []string
	if err := db.Table("sqlite_master").
		Where("type = ? AND name NOT LIKE ?", "table", "sqlite_%").
		Order("name").
		Pluck("name", &names).Error; err != nil {
		t.Fatalf("列出表名失败：%v", err)
	}
	return names
}

func compatColumns(t *testing.T, db *gorm.DB, table string) []compatColumnInfo {
	t.Helper()
	var cols []compatColumnInfo
	if err := db.Raw("PRAGMA table_info(" + compatQuote(table) + ")").Scan(&cols).Error; err != nil {
		t.Fatalf("读 %s 的列失败：%v", table, err)
	}
	return cols
}

// compatReadSchema 把库的结构（表/列/索引）读成可比对的形式。
type compatSchema struct {
	Tables  map[string]map[string]string // 表 → 列 → 声明类型
	Indexes map[string]string            // 索引签名 → 索引名
}

func compatReadSchema(t *testing.T, db *gorm.DB) compatSchema {
	t.Helper()
	schema := compatSchema{Tables: map[string]map[string]string{}, Indexes: map[string]string{}}
	for _, table := range compatTableNames(t, db) {
		columns := map[string]string{}
		for _, col := range compatColumns(t, db, table) {
			columns[col.Name] = col.Type
		}
		schema.Tables[table] = columns

		var indexes []compatIndexInfo
		if err := db.Raw("PRAGMA index_list(" + compatQuote(table) + ")").Scan(&indexes).Error; err != nil {
			t.Fatalf("读 %s 的索引失败：%v", table, err)
		}
		for _, index := range indexes {
			if index.Origin == "pk" {
				continue
			}
			var idxCols []compatIndexColumn
			if err := db.Raw("PRAGMA index_info(" + compatQuote(index.Name) + ")").Scan(&idxCols).Error; err != nil {
				t.Fatalf("读索引 %s 的列失败：%v", index.Name, err)
			}
			parts := make([]string, 0, len(idxCols))
			for _, c := range idxCols {
				parts = append(parts, c.Name)
			}
			signature := table + "(" + strings.Join(parts, ",") + ") unique=" + strconv.Itoa(index.Unique)
			schema.Indexes[signature] = index.Name
		}
	}
	return schema
}

// compatAffinity 把声明类型归到 SQLite 的类型亲和性上。
//
// 直接比声明类型太脆：varchar(255) 与 text、integer 与 bigint 都是同一类亲和性，
// 差在这上面报错只会淹没真正的信号（列根本没建出来、或者亲和性被改成另一类）。
func compatAffinity(declared string) string {
	upper := strings.ToUpper(declared)
	switch {
	case strings.Contains(upper, "INT"):
		return "INTEGER"
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		return "TEXT"
	case strings.Contains(upper, "BLOB"), upper == "":
		return "BLOB"
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"):
		return "REAL"
	default:
		return "NUMERIC"
	}
}

// ---------------------------------------------------------------------------
// 塞数据
// ---------------------------------------------------------------------------

// compatSeedRows 按库里的实际列往每张表塞两行。
//
// 值按列类型挑，不按列名挑（列名各代不同），唯一的按名特判是 deleted_at：
// 第二行必须带软删时间，用来验证升级不会把软删过的行"复活"或者删掉。
func compatSeedRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	// 必须走 database/sql 而不是 db.Exec：GORM 会把 []byte 参数当成"一组值"展开，
	// 于是 `VALUES (?, ?)` 被它写成 `VALUES (98,108,111,..., ?, ?)`，SQLite 直接报
	// "25 values for 2 columns"。BLOB 列只在 v0.8.24 及以后才出现（blocks/block_groups），
	// 所以这个坑直到那一代才炸。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取历史版本库连接失败：%v", err)
	}
	for _, table := range compatTableNames(t, db) {
		cols := compatColumns(t, db, table)
		var insertable []compatColumnInfo
		for _, col := range cols {
			if col.PK > 0 && compatAffinity(col.Type) == "INTEGER" {
				continue // INTEGER PRIMARY KEY，交给 SQLite 自增
			}
			insertable = append(insertable, col)
		}
		if len(insertable) == 0 {
			continue
		}
		names := make([]string, len(insertable))
		marks := make([]string, len(insertable))
		for i, col := range insertable {
			names[i] = compatQuote(col.Name)
			marks[i] = "?"
		}
		stmt := "INSERT INTO " + compatQuote(table) + " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
		for row := 0; row < 2; row++ {
			args := make([]any, len(insertable))
			for i, col := range insertable {
				args[i] = compatSeedValue(table, col, row)
			}
			if _, err := sqlDB.Exec(stmt, args...); err != nil {
				t.Fatalf("往历史版本库的 %s 塞第 %d 行失败：%v", table, row+1, err)
			}
		}
	}
}

func compatSeedValue(table string, col compatColumnInfo, row int) any {
	switch {
	case col.Name == "deleted_at":
		if row == 0 {
			return nil
		}
		return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	case strings.HasSuffix(col.Name, "_at"):
		return time.Date(2026, 1, 2, 3, 4, 5+row, 0, time.UTC)
	}

	// 100 起：避开 0，否则 ensureModelDisplayOrder 之类的回填会把它当"未设置"改写，
	// 而那是测试里明确允许的行为，不该混进"值被改写"的断言里。
	switch compatAffinity(col.Type) {
	case "INTEGER":
		return int64(100 + row)
	case "NUMERIC":
		// 老版本把 bool 类列（status/breaker/io_log…）声明成 numeric，存的是 1/0。
		// 只能塞 0/1：database/sql 往 bool 上转 int64 时只认这两个值，塞 100
		// 会让当前代码扫描失败——那是"塞进去的数据不像旧版本自己写的"，
		// 不是升级路径的问题。两行分别取 0 与 1，正好覆盖真假两种形态。
		return int64(row)
	case "REAL":
		return 100.25 + float64(row)
	case "BLOB":
		return []byte(fmt.Sprintf("blob-%s-%s-%d", table, col.Name, row))
	default:
		return fmt.Sprintf("%s.%s.row%d", table, col.Name, row)
	}
}

// ---------------------------------------------------------------------------
// 快照与比对
// ---------------------------------------------------------------------------

type compatTableSnapshot struct {
	Columns []string
	Rows    map[int64]map[string]string
}

type compatSnapshot struct {
	Tables map[string]*compatTableSnapshot
}

func (s compatSnapshot) rowCount() int {
	n := 0
	for _, table := range s.Tables {
		n += len(table.Rows)
	}
	return n
}

// compatSnapshotDB 把每张表的每一行按 rowid 拍下来，值转成可比较的字符串。
//
// 走 database/sql 而不是 GORM 的模型：历史版本库里有当前代码没有的列，用模型读不到；
// 而且模型读会按当前类型的解析规则重新编码一遍，掩盖掉存储层的差异。
func compatSnapshotDB(t *testing.T, db *gorm.DB) compatSnapshot {
	t.Helper()
	snap := compatSnapshot{Tables: map[string]*compatTableSnapshot{}}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取连接失败：%v", err)
	}
	for _, table := range compatTableNames(t, db) {
		cols := compatColumns(t, db, table)
		names := make([]string, 0, len(cols))
		quoted := make([]string, 0, len(cols)+1)
		for _, col := range cols {
			names = append(names, col.Name)
			quoted = append(quoted, compatQuote(col.Name))
		}
		quoted = append(quoted, compatQuote("rowid"))

		rows, err := sqlDB.Query("SELECT " + strings.Join(quoted, ", ") + " FROM " + compatQuote(table) + " ORDER BY rowid")
		if err != nil {
			t.Fatalf("快照 %s 失败：%v", table, err)
		}
		tableSnap := &compatTableSnapshot{Columns: names, Rows: map[int64]map[string]string{}}
		for rows.Next() {
			vals := make([]any, len(quoted))
			ptrs := make([]any, len(vals))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatalf("读 %s 的行失败：%v", table, err)
			}
			rowid, ok := vals[len(vals)-1].(int64)
			if !ok {
				rows.Close()
				t.Fatalf("表 %s 的 rowid 不是 int64：%T", table, vals[len(vals)-1])
			}
			values := make(map[string]string, len(names))
			for i, name := range names {
				values[name] = compatCanon(vals[i])
			}
			tableSnap.Rows[rowid] = values
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("遍历 %s 失败：%v", table, err)
		}
		rows.Close()
		snap.Tables[table] = tableSnap
	}
	return snap
}

// compatCanon 把一个值转成可比较字符串，不带类型前缀。
//
// 不区分 int64(1) 与文本 "1"：SQLite 是动态类型，表重建时同一个值在 NUMERIC 与 TEXT
// 亲和性之间可能换个 Go 类型回来，那是存储细节，不是数据损坏。真正要抓的是
// "值本身变了/行没了/列没了"。
func compatCanon(v any) string {
	switch x := v.(type) {
	case nil:
		return "<NULL>"
	case []byte:
		if utf8.Valid(x) && !strings.ContainsRune(string(x), 0) {
			return string(x)
		}
		return "\\x" + hex.EncodeToString(x)
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// compatAssertIntegrity 跑 SQLite 自带的完整性检查：结构错乱的库会在这里先炸。
func compatAssertIntegrity(t *testing.T, tag string, db *gorm.DB) {
	t.Helper()
	var result string
	if err := db.Raw("PRAGMA integrity_check").Scan(&result).Error; err != nil {
		t.Fatalf("[%s] PRAGMA integrity_check 执行失败：%v", tag, err)
	}
	if result != "ok" {
		t.Fatalf("[%s] 升级后库文件不完整：%s", tag, result)
	}
}

// compatAssertRowsPreserved 断言旧库的行与值都还在。
func compatAssertRowsPreserved(t *testing.T, tag string, before, after compatSnapshot) {
	t.Helper()
	droppedColumns := map[string]bool{}
	for _, table := range sortedCompatTables(before) {
		want := before.Tables[table]
		got, ok := after.Tables[table]
		if !ok {
			t.Errorf("[%s] 升级后表 %s 消失了", tag, table)
			continue
		}
		gotColumns := map[string]bool{}
		for _, col := range got.Columns {
			gotColumns[col] = true
		}
		for _, col := range want.Columns {
			if !gotColumns[col] && !droppedColumns[table+"."+col] {
				droppedColumns[table+"."+col] = true
				t.Errorf("[%s] 升级删掉了列 %s.%s（老列必须留在表上）", tag, table, col)
			}
		}
		for rowid, wantRow := range want.Rows {
			gotRow, ok := got.Rows[rowid]
			if !ok {
				t.Errorf("[%s] 表 %s 的行 rowid=%d 在升级后不见了", tag, table, rowid)
				continue
			}
			for _, col := range want.Columns {
				wantVal, ok := wantRow[col]
				if !ok {
					continue // 列已被删除，上面报过了
				}
				// NULL 与空串是兼容回填的合法改写对象（status NULL → true、
				// strategy '' → 默认值……），只有非空值才必须原样保留。
				if wantVal == "<NULL>" || wantVal == "" {
					continue
				}
				// updated_at 例外：兼容回填走的是 GORM 的 Update，它会给被回填的行
				// 自动盖新的 updated_at。那是"这一行被升级动过"的记账，不是数据损坏；
				// created_at 和所有业务列仍然必须逐字节保留。
				if col == "updated_at" {
					continue
				}
				if gotVal := gotRow[col]; gotVal != wantVal {
					t.Errorf("[%s] 表 %s 行 rowid=%d 列 %s 的值被改写：升级前 %q，升级后 %q", tag, table, rowid, col, wantVal, gotVal)
				}
			}
		}
	}
}

// compatAssertSchemaConverged 断言升级后的结构覆盖了当前代码需要的全部列与索引。
func compatAssertSchemaConverged(t *testing.T, tag string, fresh, upgraded compatSchema) {
	t.Helper()
	tables := make([]string, 0, len(fresh.Tables))
	for table := range fresh.Tables {
		tables = append(tables, table)
	}
	sort.Strings(tables)

	for _, table := range tables {
		wantColumns := fresh.Tables[table]
		gotColumns, ok := upgraded.Tables[table]
		if !ok {
			t.Errorf("[%s] 升级后缺表 %s", tag, table)
			continue
		}
		columns := make([]string, 0, len(wantColumns))
		for col := range wantColumns {
			columns = append(columns, col)
		}
		sort.Strings(columns)
		for _, col := range columns {
			gotType, ok := gotColumns[col]
			if !ok {
				t.Errorf("[%s] 升级后表 %s 缺列 %s（AutoMigrate 没补上）", tag, table, col)
				continue
			}
			if compatAffinity(gotType) != compatAffinity(wantColumns[col]) {
				t.Errorf("[%s] 表 %s 列 %s 的类型亲和性不对：升级后 %q，新库是 %q", tag, table, col, gotType, wantColumns[col])
			}
		}
	}

	indexes := make([]string, 0, len(fresh.Indexes))
	for signature := range fresh.Indexes {
		indexes = append(indexes, signature)
	}
	sort.Strings(indexes)
	for _, signature := range indexes {
		if _, ok := upgraded.Indexes[signature]; !ok {
			t.Errorf("[%s] 升级后缺索引 %s（期望 %s）", tag, fresh.Indexes[signature], signature)
		}
	}
}

// compatLegacyOnlyColumns 列出老库有、当前结构（新建的空库）没有的列。
//
// 它们必须被保留在升级后的表上（升级不删列），数量打进日志，便于观察某一代
// 到底留下了什么"化石"——哪天想删列时，这里就是清单。
func compatLegacyOnlyColumns(before compatSnapshot, fresh compatSchema) []string {
	var cols []string
	for tableName, table := range before.Tables {
		current, ok := fresh.Tables[tableName]
		if !ok {
			continue // 表都没了，上面已经报过
		}
		for _, col := range table.Columns {
			if _, ok := current[col]; !ok {
				cols = append(cols, tableName+"."+col)
			}
		}
	}
	sort.Strings(cols)
	return cols
}

func sortedCompatTables(snap compatSnapshot) []string {
	tables := make([]string, 0, len(snap.Tables))
	for table := range snap.Tables {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

// compatDiffSnapshots 给出两份快照前 limit 处差异，失败信息才定位得到。
func compatDiffSnapshots(want, got compatSnapshot, limit int) []string {
	var diffs []string
	for _, table := range sortedCompatTables(want) {
		if len(diffs) >= limit {
			break
		}
		wantTable := want.Tables[table]
		gotTable, ok := got.Tables[table]
		if !ok {
			diffs = append(diffs, "表 "+table+" 不见了")
			continue
		}
		for rowid, wantRow := range wantTable.Rows {
			if len(diffs) >= limit {
				break
			}
			gotRow, ok := gotTable.Rows[rowid]
			if !ok {
				diffs = append(diffs, fmt.Sprintf("表 %s 行 rowid=%d 不见了", table, rowid))
				continue
			}
			for _, col := range wantTable.Columns {
				if wantRow[col] != gotRow[col] {
					diffs = append(diffs, fmt.Sprintf("表 %s 行 rowid=%d 列 %s：%q → %q", table, rowid, col, wantRow[col], gotRow[col]))
					break
				}
			}
		}
	}
	if len(diffs) == 0 {
		diffs = append(diffs, "（未找到具体差异，可能是表的集合或列发生了变化）")
	}
	return diffs
}
