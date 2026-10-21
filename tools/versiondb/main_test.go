package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// probeSource 是「版本适配」的全部逻辑：不同代际的 Init 签名不一样，认错签名
// 就会把 v0.1.0 的库建成当前结构，整套兼容测试立刻失去意义。所以三个已知签名
// 逐个钉住，未知签名必须报错而不是猜一个模板凑合。
func TestProbeSource(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "v0.5.9 及以后：带 context",
			src:  "package models\n\nfunc Init(ctx context.Context, path string) {\n",
			want: probeWithContext,
		},
		{
			name: "v0.5.0~v0.5.8：只有 path",
			src:  "package models\n\nfunc Init(path string) {\n",
			want: probeWithoutContext,
		},
		{
			name: "v0.1.0~v0.4.15：参数叫 name",
			src:  "package models\n\nfunc Init(name string) {\n",
			want: probeWithoutContext,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := probeSource(c.src)
			if err != nil {
				t.Fatalf("应识别出签名，却报错：%v", err)
			}
			if got != c.want {
				t.Fatalf("模板选错了：拿到 %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestProbeSourceRejectsUnknownSignature(t *testing.T) {
	_, err := probeSource("package models\n\nfunc Init(conn *sql.DB) {\n")
	if err == nil {
		t.Fatal("未知签名必须报错：随便挑一个模板会静默建出错误的库")
	}
	if !strings.Contains(err.Error(), "模板") {
		t.Fatalf("报错要说清是缺模板：%v", err)
	}
}

// TestUntarDisk 里塞的是一份「恶意 tar」：绝对路径、盘符路径、.. 逃逸、符号链接
// 各一条。git archive 出的包本身是干净的，但解包代码不该只对可信输入正确——
// tag 名由 workflow 输入或 ref 决定，一条越界写入就是往仓库外写文件。
func TestUntarSkipsEscapingEntries(t *testing.T) {
	entries := []struct {
		name     string
		typeflag byte
		body     string
		link     string
	}{
		{name: "ok/keep.txt", typeflag: tar.TypeReg, body: "keep"},
		{name: "../escape.txt", typeflag: tar.TypeReg, body: "escape"},
		{name: "sub/../../escape2.txt", typeflag: tar.TypeReg, body: "escape"},
		{name: "/etc/escape3.txt", typeflag: tar.TypeReg, body: "escape"},
		{name: filepath.Join(t.TempDir(), "escape4.txt"), typeflag: tar.TypeReg, body: "escape"},
		{name: "link", typeflag: tar.TypeSymlink, link: ".."},
	}
	dst := t.TempDir()
	if err := untar(buildTar(t, entries), dst); err != nil {
		t.Fatalf("untar 返回错误：%v", err)
	}

	got := walkFiles(t, dst)
	want := []string{"ok/keep.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("解出来的文件不对：%v，期望 %v", got, want)
	}
	// 逃逸条目一个都不能落在 dst 外面：父目录里不该出现任何 escape*.txt
	outside, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatalf("读父目录：%v", err)
	}
	for _, entry := range outside {
		if strings.HasPrefix(entry.Name(), "escape") {
			t.Fatalf("有文件被写到 dst 外面：%s", entry.Name())
		}
	}
}

func TestUntarCreatesMissingDirs(t *testing.T) {
	dst := t.TempDir()
	entries := []struct {
		name       string
		typeflag   byte
		body, link string
	}{
		{name: "a/b/c.txt", typeflag: tar.TypeReg, body: "deep"},
		{name: "d/", typeflag: tar.TypeDir},
	}
	if err := untar(buildTar(t, entries), dst); err != nil {
		t.Fatalf("untar 返回错误：%v", err)
	}
	if got := walkFiles(t, dst); strings.Join(got, ",") != "a/b/c.txt" {
		t.Fatalf("嵌套目录下的文件没解出来：%v", got)
	}
	if info, err := os.Stat(filepath.Join(dst, "d")); err != nil || !info.IsDir() {
		t.Fatalf("目录条目没建出来：%v", err)
	}
}

func TestTail(t *testing.T) {
	if got := tail("short", 100); got != "short" {
		t.Fatalf("短字符串应原样返回，得到 %q", got)
	}
	long := strings.Repeat("x", 100) + "ERROR"
	got := tail(long, 10)
	if !strings.HasPrefix(got, "…") {
		t.Fatalf("截断后应带省略号：%q", got)
	}
	if !strings.HasSuffix(got, "xxxERROR") {
		t.Fatalf("截断应保留末尾内容：%q", got)
	}
	if len(got) != len("…")+10 {
		t.Fatalf("截断长度不对：%q", got)
	}
}

// buildTar 把 entries 打成一份内存 tar，供 untar 的用例使用。
func buildTar(t *testing.T, entries []struct {
	name     string
	typeflag byte
	body     string
	link     string
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		hdr := &tar.Header{
			Name:     entry.name,
			Typeflag: entry.typeflag,
			// 目录条目 Size 必须为 0，否则归档本身不合法
			Mode:     0o644,
			Linkname: entry.link,
		}
		if entry.typeflag == tar.TypeReg {
			hdr.Size = int64(len(entry.body))
		}
		if entry.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("写 tar 头 %s：%v", entry.name, err)
		}
		if entry.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatalf("写 tar 内容 %s：%v", entry.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar：%v", err)
	}
	return buf.Bytes()
}

// walkFiles 返回 dst 下所有普通文件的相对路径（/ 分隔，已排序）。
func walkFiles(t *testing.T, dst string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dst, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			rel, err := filepath.Rel(dst, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s：%v", dst, err)
	}
	sort.Strings(files)
	return files
}
