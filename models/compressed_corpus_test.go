package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atopos31/llmio/pkg/compress"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 本文件守的是一类**合成语料永远发现不了的错误**：分块或编解码改了，
// 存量库里的数据就再也读不回来。
//
// 为什么合成语料不够：body_test.go / chunker_test.go 那批数据是自己生成、
// 自己消费的。写与读用的是同一份代码，**永远自洽**：改了帧头布局、换了
// codec、把引用序列从小端改成大端，它们照样全绿，而线上库里的数据已经
// 按老格式固化了。
//
// 具体能抓到什么、抓不到什么，要分清楚（这决定了它的信任边界）：
//
//	能抓到（会导致存量数据读不出来或读出错内容）：
//	  · 帧头布局 / magic / 版本号变了
//	  · codec 变了（deflate 换成别的，或改了 deflate 参数导致老载荷解不开）
//	  · 引用序列的编码变了（字节序、定长改成变长）
//	  · blocks.off / size 的语义变了（组内偏移算错一个字节就全歪）
//	  · blocks.digest 的算法变了（摘要与内容不再对应）
//	抓不到（影响的是效率与体积，不是可读性）：
//	  · BlockChunkAvg、BlockGroupTarget 变了。读路径按 blocks 表里已存的
//	    off/size 切片，**不重新分块**，所以老库照样读得出来；作废的是
//	    "新旧内容算不出同样的块"这件事，也就是跨版本去重失效。
//	    这一类回归要靠体积观测，不是靠本文件。
//
// 真机用例同样覆盖不到上面那一段。body_real_test.go 遇到源库里的帧会直接
// t.Fatalf（"这份副本跑过压缩，不是原始生产库"），serializer_real_test.go
// 的断言也写明"这份副本是没迁过的生产库"。也就是说：
// **input 已经是帧的库，读路径没有任何真机覆盖。**
//
// 语料有两级，默认跑第二级：
//
//  1. **真机全量语料**（环境变量给路径，不入库）。2026-10-27 的生产库备份实测
//     可用：69,106 行 chat_ios 中 69,080 行的 input 已是帧（引用帧 67,558 /
//     逐行帧 1,522），另有 26 行历史明文；297,707 个块的摘要与组内内容全部
//     自洽，逐行解开 0 失败。但那份库含真实用户 prompt（解压即明文），不能进
//     公开仓库。它能为这份测试提供的是**真实的长度分布与分块产物**。
//
//     $env:LLMIO_COMPRESSED_CORPUS="E:\tmp\corpus.db"
//     go test ./models/ -run CompressedCorpus -v -timeout 120m
//
//  2. **仓库内脱敏语料** testdata/cdc_corpus.db（随包分发，无需任何配置）。
//     它是从上面那份库里按明文长度分档抽 60 行、逐字节脱敏之后，再走一遍
//     生产写路径（BeforeCreate → PackBodies → 块表）生成的，约 4.7 MiB。
//     生成器见 compressed_corpus_extract_test.go。
//
// 两者的信任边界不同，别混着理解：
//   - 第 1 级验的是"真实数据长什么样"，第 2 级验的是"某一版代码写出来什么样"。
//     脱敏把字节映射进 'a'..'z'，长度保住了、熵差丢了，所以第 2 级的分块边界
//     必然与真实库不同——它**不是真实分块产物的复刻**。
//   - 但对本文件要抓的那一类错误（帧头、codec、引用编码、off/size 语义），
//     两者等价：都是一份"按老代码固化下来的库"，改了读路径就解不开。
//     真实长度分布那部分，只有第 1 级能给。
//
// 期望值文件默认是语料路径 + ".manifest.json"；也可以用
// $env:LLMIO_COMPRESSED_CORPUS_MANIFEST 指到别处。找不到时降级为
// "只验能不能解开"，日志里会明确说出来。

// corpusPath 返回本次要跑的语料路径。两级，见文件头。都没有时返回 ""。
func corpusPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("LLMIO_COMPRESSED_CORPUS"); p != "" {
		return p
	}
	// go test 把工作目录设在包目录下，所以这是 models/testdata/cdc_corpus.db。
	// 用相对路径而不是绝对路径：仓库换个位置这条不该失效。
	p := filepath.Join("testdata", "cdc_corpus.db")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// compressedCorpus 打开语料库，同时返回它的绝对路径（manifest 要按它找）。
//
// 全程只读：这份语料是基准，任何一次写入都会改变块表与组的内容，下一次比对
// 就失去意义。用 query_only 让 SQLite 自己拦下写操作，而不是靠"我们记得不要写"。
func compressedCorpus(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	path := corpusPath(t)
	if path == "" {
		// CI 上语料必须真的跑。testdata/cdc_corpus.db 是随包分发的，缺了它
		// 只可能是漏提交——那时跳过就等于"护栏静默消失"，而 CI 全绿会让人
		// 以为验过了。所以由调用环境把这条打开（见 test.yml）。
		if os.Getenv("LLMIO_REQUIRE_CORPUS") != "" {
			t.Fatal("要求跑压缩语料基准，但 testdata/cdc_corpus.db 不在：语料被漏提交了？")
		}
		t.Skip("没有可用语料：LLMIO_COMPRESSED_CORPUS 未设，testdata/cdc_corpus.db 也不在")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("路径不合法：%v", err)
	}
	// 与 serializer_real_test 同一条底线：原始数据的唯一副本在源目录的 db/ 下，
	// 不能拿它当语料跑（虽然这里开了 query_only，守的是"别指向它"这件事本身）。
	// **例外是 models/testdata**：那里放的是脱敏后重新生成的语料，是可再生产物，
	// 也正是 CI 要跑的那份。
	low := strings.ToLower(filepath.ToSlash(abs))
	if strings.HasPrefix(low, "e:/projects/") && !strings.Contains(low, "/models/testdata/") {
		t.Fatalf("拒绝在源目录上跑：%s——那里是原始数据，只能指向副本", abs)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("语料不存在：%v", err)
	}

	db, err := gorm.Open(sqlite.Open(abs+"?_pragma=query_only(1)"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开语料失败：%v", err)
	}
	closeOnCleanup(t, db)
	// 组缓存只按组 id 索引，换库必须作废——否则会读到别的库里同号的组，
	// 症状是"长度对得上、内容全错"，而且不报任何错。
	blockStore.Reset()
	return db, abs
}

// corpusStats 是遍历语料时顺手统计出来的形态分布。它只进日志，不参与判定；
// 单独拿出来是为了让失败信息能说"这份语料里有多少行是哪种形态"。
type corpusStats struct {
	total, rowFrames, refFrames, plains, empties int64
}

func (s corpusStats) String() string {
	return fmt.Sprintf("语料 %d 行：逐行帧 %d｜引用帧 %d｜历史明文 %d｜空 %d",
		s.total, s.rowFrames, s.refFrames, s.plains, s.empties)
}

// TestCompressedCorpus_EveryRowReadsBack 逐行把 input 的帧解成明文。
//
// 判据分两层：
//   - **必须解开**。解不开说明帧格式或编解码器变了（或者库坏了），最硬的失败。
//   - **有 manifest 时还要对指纹**。解开 ≠ 内容对：分块边界错位会让还原结果
//     "能解开但内容不对"，而这种错在体积和长度上都看不出来，只有哈希能发现。
func TestCompressedCorpus_EveryRowReadsBack(t *testing.T) {
	db, corpus := compressedCorpus(t)
	ctx := context.Background()
	manifest := loadCorpusManifest(t, corpus)

	rows, err := db.Raw(`SELECT id, input FROM chat_ios ORDER BY id`).Rows()
	if err != nil {
		t.Fatalf("扫描语料失败：%v", err)
	}
	defer rows.Close()

	var (
		st            corpusStats
		checked       int64
		mismatched    int64
		firstMismatch string
	)
	for rows.Next() {
		var (
			id  int64
			raw []byte
		)
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatalf("扫描失败：%v", err)
		}
		st.total++
		if len(raw) == 0 {
			st.empties++
			continue
		}

		f, ferr := compress.Unmarshal(raw)
		if errors.Is(ferr, compress.ErrNotFrame) {
			// 历史明文行：原样放行，什么都不做。2026-10-27 那份备份里有 26 行。
			st.plains++
			continue
		}
		if ferr != nil {
			t.Fatalf("行 %d 的帧头不可信：%v", id, ferr)
		}
		switch f.Type {
		case compress.TypeRowFrame:
			st.rowFrames++
		case compress.TypeBlockRefs:
			st.refFrames++
		default:
			t.Fatalf("行 %d 的 input 里装着 %s 帧，这一列不装这种", id, f.Type)
		}

		plain, err := UnpackBody(ctx, db, raw)
		if err != nil {
			t.Fatalf("行 %d（%s）解不开：%v", id, f.Type, err)
		}
		if want, ok := manifest.Rows[id]; ok {
			checked++
			if got := bodyFingerprint(plain); got != want {
				mismatched++
				if firstMismatch == "" {
					firstMismatch = fmt.Sprintf("行 %d：%d 字节，指纹 %s，期望 %s",
						id, len(plain), got, want)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历中断：%v", err)
	}

	t.Logf("%s；比对指纹 %d 行", st, checked)
	if mismatched > 0 {
		t.Fatalf("%d 行的还原内容与基准不符，首个：%s", mismatched, firstMismatch)
	}
	if checked > 0 && checked != st.rowFrames+st.refFrames {
		t.Fatalf("只比对了 %d/%d 帧行：manifest 与语料对不上（换过语料？）",
			checked, st.rowFrames+st.refFrames)
	}
}

// TestCompressedCorpus_BlockDigestsMatchContent 是这套方案内容正确性的硬判据。
//
// 块的 Digest 是 SHA-256 截断到 128 位（compress.Digest），写入时就等于内容
// 本身。所以"组里的切片重新算一遍摘要，与块表里存的比"同时证明三件事：
// 组帧没坏、偏移没漂、内容没被改。
//
// 反向的后果是设计好的：帧头、codec、引用编码或 off/size 的语义一变，
// 老库的引用就会指到不对的内容上。读路径那时未必报错（帧头仍合法），
// 只会读出**错的内容**——所以这条必须逐块核对，不能抽样。
func TestCompressedCorpus_BlockDigestsMatchContent(t *testing.T) {
	db, _ := compressedCorpus(t)

	rows, err := db.Raw(`SELECT group_id, id, digest, size, off FROM blocks ORDER BY group_id, off`).Rows()
	if err != nil {
		t.Fatalf("读块表失败：%v", err)
	}
	defer rows.Close()

	var (
		nBlocks, nGroups int64
		badDigest        int64
		badRange         int64
		badGroup         int64
		badRLen          int64
		firstBad         string
		curGID           int64 = -1
		curPlain         []byte
	)
	for rows.Next() {
		var (
			gid, bid int64
			digest   []byte
			size     int
			off      int
		)
		if err := rows.Scan(&gid, &bid, &digest, &size, &off); err != nil {
			t.Fatalf("扫描失败：%v", err)
		}
		if gid != curGID {
			var raw []byte
			var rawLen int
			if err := db.Raw(`SELECT data, raw_len FROM block_groups WHERE id = ?`, gid).
				Row().Scan(&raw, &rawLen); err != nil {
				t.Fatalf("读组 %d 失败：%v", gid, err)
			}
			nGroups++
			// 组本身就是一帧（TypeGroup），原地解开一次，该组所有块共用。
			plain, wasFrame, err := compress.DecompressBytes(raw)
			switch {
			case err != nil:
				badGroup++
				if firstBad == "" {
					firstBad = fmt.Sprintf("组 %d 解不开：%v", gid, err)
				}
				plain = nil
			case !wasFrame:
				badGroup++
				if firstBad == "" {
					firstBad = fmt.Sprintf("组 %d 存的不是帧", gid)
				}
				plain = nil
			}
			curPlain = plain
			// raw_len 是封口时记下的原始长度，必须与解出来的长度一致。
			if curPlain != nil && rawLen != len(curPlain) {
				badRLen++
				if firstBad == "" {
					firstBad = fmt.Sprintf("组 %d 的 raw_len=%d，解开是 %d 字节", gid, rawLen, len(curPlain))
				}
			}
			curGID = gid
		}
		nBlocks++

		if curPlain == nil {
			continue // 组已经报过错了，别对每个块重复刷同一条
		}
		if len(digest) != 16 {
			badDigest++
			if firstBad == "" {
				firstBad = fmt.Sprintf("块 %d 的摘要长度是 %d，应当是 16", bid, len(digest))
			}
			continue
		}
		if off < 0 || size < 0 || off+size > len(curPlain) {
			badRange++
			if firstBad == "" {
				firstBad = fmt.Sprintf("块 %d 的范围 %d..%d 超出组长 %d", bid, off, off+size, len(curPlain))
			}
			continue
		}
		want := compress.Digest(curPlain[off : off+size])
		if !bytes.Equal(want[:], digest) {
			badDigest++
			if firstBad == "" {
				firstBad = fmt.Sprintf("块 %d 的摘要与组内内容不符（%d 字节）", bid, size)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历中断：%v", err)
	}

	t.Logf("块 %d 个 / 组 %d 个；摘要不符 %d｜范围越界 %d｜组帧坏 %d｜raw_len 不符 %d",
		nBlocks, nGroups, badDigest, badRange, badGroup, badRLen)
	if nBlocks == 0 {
		t.Fatal("语料里一个块都没有，这条断言等于没跑")
	}
	if bad := badDigest + badRange + badGroup + badRLen; bad > 0 {
		t.Fatalf("%d 处不一致，首个：%s（帧格式、codec 或引用编码改过？见文件头注释）",
			bad, firstBad)
	}
}

// TestCompressedCorpus_NoDanglingRefs 每一行引用的块 id 都必须存在。
//
// 悬空引用是"块表被清理过、但引用帧没更新"的指纹。危害是"读不出来"而不是
// "读出错内容"，但它只有在真数据上才查得到——合成语料里块与引用是同一次
// 写入产生的，永远配得上。
//
// 实现上把整张块 id 拉进内存：真机 30 万个 int64 约 2.4 MiB，比一句几千个
// 占位符的 IN 干净，也不依赖 SQLite 有没有编译进 JSON1。
func TestCompressedCorpus_NoDanglingRefs(t *testing.T) {
	db, _ := compressedCorpus(t)

	known := make(map[uint32]struct{}, 1<<19)
	var liveBlocks []uint32
	if err := db.Raw(`SELECT id FROM blocks`).Scan(&liveBlocks).Error; err != nil {
		t.Fatalf("读块 id 失败：%v", err)
	}
	for _, id := range liveBlocks {
		known[id] = struct{}{}
	}
	if len(known) == 0 {
		t.Fatal("块表是空的，这条断言等于没跑")
	}

	rows, err := db.Raw(`SELECT id, input FROM chat_ios WHERE input IS NOT NULL AND length(input) > 0`).Rows()
	if err != nil {
		t.Fatalf("扫描语料失败：%v", err)
	}
	defer rows.Close()

	var (
		refRows, refsChecked, dangling int64
		firstBad                       string
	)
	for rows.Next() {
		var (
			id  int64
			raw []byte
		)
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatalf("扫描失败：%v", err)
		}
		f, err := compress.Unmarshal(raw)
		if errors.Is(err, compress.ErrNotFrame) {
			continue
		}
		// 先判错误再判类型：Unmarshal 在 ErrCorrupt 时返回零值 Frame{}，
		// Type 是 0，先比类型会把坏帧当成"不是引用帧"静默跳过。
		if err != nil {
			t.Fatalf("行 %d 的帧头不可信：%v", id, err)
		}
		if f.Type != compress.TypeBlockRefs {
			continue
		}
		refs, err := DecodeRefsFrame(f)
		if err != nil {
			t.Fatalf("行 %d 的引用序列坏了：%v", id, err)
		}
		refRows++
		if len(refs) == 0 {
			dangling++
			if firstBad == "" {
				firstBad = fmt.Sprintf("行 %d 是引用帧却一条引用都没有", id)
			}
			continue
		}
		refsChecked += int64(len(refs))
		for _, r := range refs {
			if _, ok := known[r]; !ok {
				dangling++
				if firstBad == "" {
					firstBad = fmt.Sprintf("行 %d 引用了不存在的块 id=%d", id, r)
				}
				break // 一行只报一次，别让一行刷出上万条
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历中断：%v", err)
	}

	t.Logf("引用帧 %d 行 / %d 条引用；悬空 %d 行", refRows, refsChecked, dangling)
	if refRows == 0 {
		t.Fatal("语料里没有引用帧，这条断言等于没跑")
	}
	if dangling > 0 {
		t.Fatalf("%d 行有悬空引用，首个：%s", dangling, firstBad)
	}
}

// ── 语料是否真的脱敏干净 ──────────────────────────────────────────────────

// TestCompressedCorpus_HasNoRealContent 守住"这份语料能进公开仓库"。
//
// 它是从含真实用户 prompt 的生产库抽的。脱敏一旦失效，泄露是**永久且公开**的
// ——仓库是 public，git 历史擦不掉，删了文件也没用。所以判据必须是可执行的
// 硬判据，不能是"生成脚本里写了 sanitize 所以应该没问题"。
//
// 三层，从内容到形态：
//  1. 正文（chat_ios.input / of_string / of_string_array）解开之后，每个字节
//     都必须是 'a'..'z'。sanitize 的像就是这 26 个字符，任何偏离都意味着有
//     真实字节漏了进来。
//  2. 块组的内容（block_groups.data 解开之后）同样必须是 'a'..'z'。
//     正文是通过块表间接落库的，只查 chat_ios 会漏掉这条路径。
//  3. 所有表里**实际以 TEXT 形态存在**的值必须全是 ASCII。这一层挡的是旁路
//     字段：正文脱了敏，但某个列把原文带了出来。中文 prompt 是 UTF-8 三字节，
//     一定非 ASCII，挡得住；纯英文原文则由第 1 层挡。
//
// 为什么第 3 层要加 `typeof(...) = 'text'`：正文列存的是 BLOB 帧，压缩后的
// 二进制里当然有非 ASCII 字节。不加这个限定，第 3 层会对每一帧误报。
func TestCompressedCorpus_HasNoRealContent(t *testing.T) {
	db, _ := compressedCorpus(t)
	ctx := context.Background()

	// 第 1 层：正文三列。
	rows, err := db.Raw(`SELECT id, input, of_string, of_string_array FROM chat_ios ORDER BY id`).Rows()
	if err != nil {
		t.Fatalf("读正文失败：%v", err)
	}
	var bodies, textValsChecked int64
	var sample []byte
	for rows.Next() {
		var (
			id       int64
			in       []byte
			colStr   []byte
			colArray []byte
		)
		if err := rows.Scan(&id, &in, &colStr, &colArray); err != nil {
			rows.Close()
			t.Fatalf("扫描正文失败：%v", err)
		}
		if len(in) > 0 {
			// input 可能是引用帧，必须走真正的解包（它要查块表）。
			plain, err := UnpackBody(ctx, db, in)
			if err != nil {
				rows.Close()
				t.Fatalf("行 %d 的 input 解不开（连内容都验不了，谈不上脱敏）：%v", id, err)
			}
			assertOnlyLowerAZ(t, plain, fmt.Sprintf("行 %d 的 input", id))
			bodies++
			if sample == nil {
				sample = plain
			}
		}
		for _, c := range []struct {
			name string
			raw  []byte
		}{{"of_string", colStr}, {"of_string_array", colArray}} {
			if len(c.raw) == 0 {
				continue
			}
			plain, _, err := compress.DecompressBytes(c.raw)
			if err != nil {
				rows.Close()
				t.Fatalf("行 %d 的 %s 解不开：%v", id, c.name, err)
			}
			assertOnlyLowerAZ(t, plain, fmt.Sprintf("行 %d 的 %s", id, c.name))
			bodies++
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("遍历正文中断：%v", err)
	}
	rows.Close()

	// 第 2 层：块组。正文是经块表间接落库的，这条路径必须单独走一遍。
	var groups int64
	gr, err := db.Raw(`SELECT id, data FROM block_groups ORDER BY id`).Rows()
	if err != nil {
		t.Fatalf("读块组失败：%v", err)
	}
	for gr.Next() {
		var (
			gid int64
			raw []byte
		)
		if err := gr.Scan(&gid, &raw); err != nil {
			gr.Close()
			t.Fatalf("扫描块组失败：%v", err)
		}
		plain, _, err := compress.DecompressBytes(raw)
		if err != nil {
			gr.Close()
			t.Fatalf("组 %d 解不开：%v", gid, err)
		}
		assertOnlyLowerAZ(t, plain, fmt.Sprintf("块组 %d", gid))
		groups++
	}
	if err := gr.Err(); err != nil {
		gr.Close()
		t.Fatalf("遍历块组中断：%v", err)
	}
	gr.Close()

	// 第 3 层：所有表的 TEXT 值。
	var tables []string
	if err := db.Raw(`SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`).Scan(&tables).Error; err != nil {
		t.Fatalf("读表名失败：%v", err)
	}
	for _, tbl := range tables {
		var cols []struct {
			Name string `gorm:"column:name"`
			Type string `gorm:"column:type"`
		}
		if err := db.Raw(fmt.Sprintf(`PRAGMA table_info("%s")`, tbl)).Scan(&cols).Error; err != nil {
			t.Fatalf("读 %s 的列信息失败：%v", tbl, err)
		}
		for _, col := range cols {
			// typeof 限定在 SQL 里做：BLOB 帧不进这一层。
			q := fmt.Sprintf(`SELECT "%s" FROM "%s" WHERE typeof("%s") = 'text' AND "%s" IS NOT NULL`,
				col.Name, tbl, col.Name, col.Name)
			r, err := db.Raw(q).Rows()
			if err != nil {
				t.Fatalf("读 %s.%s 失败：%v", tbl, col.Name, err)
			}
			for r.Next() {
				var v sql.NullString
				if err := r.Scan(&v); err != nil {
					r.Close()
					t.Fatalf("扫描 %s.%s 失败：%v", tbl, col.Name, err)
				}
				if !v.Valid {
					continue
				}
				for i := 0; i < len(v.String); i++ {
					if v.String[i] >= 0x80 {
						r.Close()
						t.Fatalf("%s.%s 第 %d 字节是 %#x：TEXT 列里出现非 ASCII"+
							"（中文原文的特征就是 UTF-8 三字节），脱敏没盖到这一列",
							tbl, col.Name, i, v.String[i])
					}
				}
				textValsChecked++
			}
			r.Close()
		}
	}

	// 一具空壳也能让上面三层"全过"。所以先要求它们真的扫到了东西。
	if bodies == 0 {
		t.Fatal("一条正文都没扫到：这份语料是空的，三层检查等于没跑")
	}
	if groups == 0 {
		t.Fatal("一个块组都没扫到：块表路径没被覆盖")
	}

	t.Logf("正文 %d 条、块组 %d 个、TEXT 值 %d 个：全部脱敏干净",
		bodies, groups, textValsChecked)
	if len(sample) > 0 {
		n := min(80, len(sample))
		t.Logf("正文样本（%d 字节中的前 %d）：%s", len(sample), n, sample[:n])
	}
}

// assertOnlyLowerAZ 断言 b 的每个字节都在 'a'..'z'。
//
// 这正是 sanitize 的值域（'a' + c%26）。写成独立函数是为了失败信息能指出
// 具体位置——脱敏失效时要能一眼看出漏在第几个字节。
func assertOnlyLowerAZ(t *testing.T, b []byte, what string) {
	t.Helper()
	for i, c := range b {
		if c < 'a' || c > 'z' {
			// 只报字节值，不回报上下文——万一真的漏了原文，报出来就是二次泄露。
			t.Fatalf("%s 第 %d 字节是 %#x，不在 'a'..'z'：脱敏失效，这份语料不能进公开仓库",
				what, i, c)
		}
	}
}

// ── 语料指纹 ──────────────────────────────────────────────────────────────

// corpusManifest 是语料的期望值。索引是行 id，值形如 "<sha256-hex>:<长度>"。
//
// 它刻意**不含明文**：只记"还原后的内容算出来是多少"。
//
// 注意这里的隐私差别取决于语料是哪一级：
//   - 真机语料：明文是用户 prompt，低熵内容（一句很短的提问）的哈希可以被
//     字典攻击还原。所以那份 manifest 与真机语料一样，**不入库**。
//   - 仓库内脱敏语料：明文本身就是 'a'..'z' 的确定性映射，manifest 不泄漏
//     任何东西——而且它必须跟着语料一起入库，否则 CI 只能验"解得开"，
//     验不到"解出来的内容对不对"，而那才是分块边界错位的唯一证据。
type corpusManifest struct {
	Rows map[int64]string `json:"rows"`
}

// loadCorpusManifest 读语料旁边的期望值文件。没有就降级为"只验能不能解开"，
// 并在日志里说出来——不留着让下一个人以为内容也验过了。
func loadCorpusManifest(t *testing.T, corpus string) corpusManifest {
	t.Helper()
	m := corpusManifest{Rows: map[int64]string{}}

	path := os.Getenv("LLMIO_COMPRESSED_CORPUS_MANIFEST")
	if path == "" {
		path = corpus + ".manifest.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("读 manifest 失败：%v", err)
		}
		t.Logf("没有 manifest（%s）：只验能不能解开，不验内容对不对", path)
		return m
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解析 manifest 失败：%v", err)
	}
	if m.Rows == nil {
		m.Rows = map[int64]string{}
	}
	t.Logf("manifest 覆盖 %d 行", len(m.Rows))
	return m
}

// bodyFingerprint 是"内容对不对"的判据。用 SHA-256 而不是长度：
// 分块边界错位产生的损坏，长度往往还对得上。
func bodyFingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]) + ":" + fmt.Sprint(len(b))
}
