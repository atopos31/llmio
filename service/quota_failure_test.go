package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/quota"
	"gorm.io/gorm"
)

// 本文件补两类分支：
//
//  1. **读得到、写不回去** —— 磁盘满、目录只读、文件被占用都是真实会发生的运维事故。
//     它们的特点是 Load 成功而 Save 失败，因此不能靠"配置损坏"来触发。
//     造法：把配置文件本身留成合法 JSON，占住它的 `<path>.tmp` 位置。
//     SaveConfig 是"先写 .tmp 再改名"，占住 .tmp 就精确地只让写这一步失败。
//
//  2. 从上游导入时的兜底分支。
//
// 关于覆盖率口径：本项目的门禁按**单包**统计（`go test ./service/ -cover`），
// 与 S1 对 `service/stats.go` 的口径一致。跨包口径（`-coverpkg=./...`）会把
// 未被本包测试触达的标准库语句也计入分母，数字没有意义。

// newUnwritableStore 返回一个能正常读、但写不回去的 store。
func newUnwritableStore(t *testing.T) *QuotaStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quota.config.json")
	s := NewQuotaStore(path)
	if _, err := s.UpsertSource(quota.Source{
		ID: "a", Name: "A", Type: quota.TypeHTTP, URL: "https://a",
	}, false); err != nil {
		t.Fatalf("预置数据源应成功: %v", err)
	}
	if err := os.Mkdir(path+".tmp", 0o755); err != nil {
		t.Fatalf("占住 .tmp 应成功: %v", err)
	}
	return s
}

func TestStoreSaveFailuresSurface(t *testing.T) {
	t.Run("Save", func(t *testing.T) {
		err := newUnwritableStore(t).Save(&quota.Config{
			Sources: []quota.Source{{ID: "z", Type: quota.TypeHTTP, URL: "https://z"}},
		})
		if err == nil {
			t.Fatal("写盘失败应报错")
		}
	})

	t.Run("UpsertSource 更新已有项", func(t *testing.T) {
		// 走 idx >= 0 的更新分支（而非新增分支）后写盘失败
		_, err := newUnwritableStore(t).UpsertSource(quota.Source{
			ID: "a", Name: "A2", Type: quota.TypeHTTP, URL: "https://a2",
		}, false)
		if err == nil || !strings.Contains(err.Error(), "写入配额配置失败") {
			t.Fatalf("更新时写盘失败应报错，实得 %v", err)
		}
	})

	t.Run("RemoveSource 命中但写不回", func(t *testing.T) {
		// 找到了要删的源，但落盘失败：不能报删除成功，否则前端会以为已删掉
		ok, err := newUnwritableStore(t).RemoveSource("a")
		if ok || err == nil {
			t.Fatalf("写盘失败时不该报删除成功: ok=%v err=%v", ok, err)
		}
	})

	t.Run("ImportFromUpstream 写不回", func(t *testing.T) {
		_, err := newUnwritableStore(t).ImportFromUpstream(models.Provider{
			Model: gorm.Model{ID: 1}, Name: "DeepSeek",
			Config: `{"base_url":"https://api.deepseek.com","api_key":"k"}`,
		})
		if err == nil {
			t.Fatal("写盘失败应报错")
		}
	})
}

func TestStoreUpsertFirstWithEmptyIDThenAgain(t *testing.T) {
	// 新增时不给 id：由 store 生成，且生成后不能与已有项冲突
	s := newTestStore(t)
	first, err := s.UpsertSource(quota.Source{Type: quota.TypeHTTP, URL: "https://a"}, true)
	if err != nil {
		t.Fatalf("新增应成功: %v", err)
	}
	if !strings.HasPrefix(first.ID, "src-") {
		t.Fatalf("应生成 src- 前缀的 id: %q", first.ID)
	}
	// 再按生成的 id 更新应命中已有项（UpsertSource 按名字就是 upsert：
	// 找不到就新增，因此"更新"的判定标准是源数不增加）
	second, err := s.UpsertSource(quota.Source{
		ID: first.ID, Name: "改名", Type: quota.TypeHTTP, URL: "https://b",
	}, false)
	if err != nil {
		t.Fatalf("更新应成功: %v", err)
	}
	if second.Name != "改名" {
		t.Fatalf("应按 id 更新: %#v", second)
	}
	cfg, _ := s.Load()
	if len(cfg.Sources) != 1 {
		t.Fatalf("更新不该新增一项: %#v", cfg.Sources)
	}
}

func TestBuildImportedSourceUnsupportedProvider(t *testing.T) {
	// 名字与地址都不含任何已知关键词、且没有 base_url：
	// 建议出 HTTP 类型，地址推不出来，留空由用户补
	src, err := BuildImportedSource(models.Provider{Model: gorm.Model{ID: 7}, Name: ""})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if src.Type != quota.TypeHTTP || src.URL != "" {
		t.Fatalf("应给空的 HTTP 源: %#v", src)
	}
	if src.Enabled {
		t.Fatal("地址未知时应先禁用，等用户补全")
	}
}

func TestStoreImportFromUpstreamWithoutURL(t *testing.T) {
	// HTTP 类型但推导不出地址 -> 明确要求手工配置，而不是导入一个永远失败的源
	_, err := newTestStore(t).ImportFromUpstream(models.Provider{Model: gorm.Model{ID: 8}})
	if err == nil || !strings.Contains(err.Error(), "无法从上游配置推导") {
		t.Fatalf("应要求手工配置，实得 %v", err)
	}
}

func TestStoreImportFromUpstreamHTTPEnabled(t *testing.T) {
	// 未识别供应商但给了 base_url：带出占位地址，仍保持禁用
	s := newTestStore(t)
	got, err := s.ImportFromUpstream(models.Provider{
		Model: gorm.Model{ID: 9}, Name: "某个中转站",
		Config: `{"base_url":"https://relay.example.com/","api_key":"sk-x"}`,
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if got.Type != quota.TypeHTTP || got.URL != "https://relay.example.com/user/balance" {
		t.Fatalf("占位地址不符: %#v", got)
	}
	if got.Enabled {
		t.Fatal("未识别的供应商应保持禁用")
	}
}
