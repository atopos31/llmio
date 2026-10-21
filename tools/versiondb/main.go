// Command versiondb 用「某个历史版本自己的代码」建出一个该版本的数据库文件。
//
// 为什么不能只靠 testdata/upstream_schema.sql：那份快照只是 master 当前这一代结构。
// 更老的版本（v0.1.0 的 models 表还有 io_log、还没有 configs 表）建出来的库完全
// 是另一种形态，只有让那个版本自己的 models.Init 跑一遍才拿得到真实结构——手写
// DDL 永远只能证明「按我以为的旧结构能升级」。
//
// 用法（CI 里由 .github/workflows/db-compat.yml 对每个历史版本各跑一次）：
//
//	go run ./tools/versiondb -ref v0.5.0 -out /tmp/v0.5.0.db
//
// 做法：git archive 导出该 ref 的源码 → 塞一个探针包进去 → 让**那个模块自己**
// 编译并运行探针（探针只调用它的 models.Init）→ 留下建好的库文件。
// 探针不用当前代码，因此不会把新结构混进旧库。
package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	ref := flag.String("ref", "", "要建库的 git ref（通常是发布 tag，例如 v0.5.0）")
	out := flag.String("out", "", "输出库文件路径；目录不存在会自动创建")
	work := flag.String("work", "", "工作目录（默认在系统临时目录下新建）")
	keep := flag.Bool("keep", false, "保留工作目录，便于排查")
	sqlOnly := flag.Bool("sql", false, "把该版本建出的表结构（sqlite_master DDL）打到 stdout")
	flag.Parse()

	if *ref == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "用法：go run ./tools/versiondb -ref <tag> -out <db-path> [-work dir] [-keep] [-sql]")
		os.Exit(2)
	}
	if err := run(*ref, *out, *work, *keep, *sqlOnly); err != nil {
		fmt.Fprintf(os.Stderr, "versiondb: %v\n", err)
		os.Exit(1)
	}
}

func run(ref, out, work string, keep, sqlOnly bool) error {
	started := time.Now()

	// 先确认在 git 仓库里：git archive 失败时的报错信息远不如这里直白
	if _, err := gitOutput("rev-parse", "--show-toplevel"); err != nil {
		return fmt.Errorf("这里不是 git 仓库（或 git 不可用）：%w", err)
	}

	if work == "" {
		dir, err := os.MkdirTemp("", "versiondb-")
		if err != nil {
			return err
		}
		work = dir
	}
	if !keep {
		defer os.RemoveAll(work)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}

	// 1. 导出该 ref 的源码（git archive 不碰工作区，也不受 checkout 状态影响）
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	archive, err := gitOutput("archive", "--format=tar", ref)
	if err != nil {
		return fmt.Errorf("git archive %s：%w", ref, err)
	}
	if err := untar([]byte(archive), src); err != nil {
		return fmt.Errorf("解包 %s 源码：%w", ref, err)
	}

	// 2. 版本适配：Init 的签名在不同代际里不一样（v0.5.8 及更早是 Init(path)），
	//    探针按签名选用对应模板，避免「为了测旧版本先改旧版本源码」。
	initSrc, err := os.ReadFile(filepath.Join(src, "models", "init.go"))
	if err != nil {
		return fmt.Errorf("读 %s 的 models/init.go：%w", ref, err)
	}
	probe, err := probeSource(string(initSrc))
	if err != nil {
		return fmt.Errorf("%s：%w", ref, err)
	}
	probeDir := filepath.Join(src, "cmd", "dbprobe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(probe), 0o644); err != nil {
		return err
	}

	// 3. 用那个版本自己的模块跑探针：探针只 import 它自己的 models 包
	dbPath, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dbPath)
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}

	cmd := exec.Command("go", "run", "./cmd/dbprobe", dbPath)
	cmd.Dir = src
	cmd.Env = append(os.Environ(),
		"GOWORK=off",       // 别被外面的 go.work 拽到当前模块
		"GOFLAGS=-mod=mod", // 旧模块的 go.sum 可能缺条目，允许补齐
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("在 %s 上跑探针失败：%w\n%s", ref, err, tail(stderr.String(), 4000))
	}

	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("%s 的探针没有产出库文件 %s：%w", ref, dbPath, err)
	}
	if sqlOnly {
		fmt.Print(stdout.String())
	}
	stat, err := os.Stat(dbPath)
	if err != nil {
		return err
	}
	fmt.Printf("versiondb: %s → %s（%d 字节，%s）\n", ref, dbPath, stat.Size(), time.Since(started).Round(time.Millisecond))
	return nil
}

// probeSource 依据该版本 models.Init 的签名挑探针模板。
func probeSource(initSrc string) (string, error) {
	switch {
	case strings.Contains(initSrc, "func Init(ctx context.Context, path string)"):
		return probeWithContext, nil
	case strings.Contains(initSrc, "func Init(path string)"):
		return probeWithoutContext, nil
	case strings.Contains(initSrc, "func Init(name string)"):
		return probeWithoutContext, nil
	}
	return "", errors.New("models/init.go 里没找到已知的 Init 签名，需要给 tools/versiondb 补一个模板")
}

// probeWithContext / probeWithoutContext 是塞进旧版本模块里的探针源码。
//
// 只做三件事：调用那个版本的 models.Init 建库、把 sqlite_master 里的 DDL 打出来、
// 关连接。不 import 当前分支的任何代码。
const probeWithContext = `package main

import (
	"context"
	"fmt"
	"os"

	// 模块路径沿用该 tag 自己的 go.mod：本仓 151 个 tag 的 module 一直是
	// github.com/atopos31/llmio（2026-10-21 用 git grep 核过），换了要同时改这里
	"github.com/atopos31/llmio/models"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dbprobe <db-path>")
		os.Exit(2)
	}
	models.Init(context.Background(), os.Args[1])
	dump()
}

func dump() {
	var rows []struct {
		Type string
		Name string
		SQL  string
	}
	if err := models.DB.Raw("SELECT type, name, COALESCE(sql,'') AS sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY type DESC, name").Scan(&rows).Error; err != nil {
		panic(err)
	}
	for _, r := range rows {
		fmt.Printf("%s\t%s\t%s\n", r.Type, r.Name, r.SQL)
	}
	if sqlDB, err := models.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
`

const probeWithoutContext = `package main

import (
	"fmt"
	"os"

	"github.com/atopos31/llmio/models"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dbprobe <db-path>")
		os.Exit(2)
	}
	models.Init(os.Args[1])
	dump()
}

func dump() {
	var rows []struct {
		Type string
		Name string
		SQL  string
	}
	if err := models.DB.Raw("SELECT type, name, COALESCE(sql,'') AS sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY type DESC, name").Scan(&rows).Error; err != nil {
		panic(err)
	}
	for _, r := range rows {
		fmt.Printf("%s\t%s\t%s\n", r.Type, r.Name, r.SQL)
	}
	if sqlDB, err := models.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
`

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w（%s）", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func untar(data []byte, dst string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || name == ".." || filepath.IsAbs(name) ||
			strings.HasPrefix(name, "../") || strings.HasPrefix(name, "..\\") ||
			strings.HasPrefix(hdr.Name, "/") || strings.HasPrefix(hdr.Name, "\\") {
			continue
		}
		target := filepath.Join(dst, name)
		// 最后再兜一次底：不管名字长什么样，落点必须还在 dst 里面。
		// 上面按 POSIX 语义挡住了绝对路径，Windows 下 /etc/x 这种写法本身不逃逸
		// （缺盘符），但换成 C:/x 就逃逸了——这条检查不看平台，一律拦。
		if rel, err := filepath.Rel(dst, target); err != nil || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
