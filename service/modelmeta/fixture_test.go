package modelmeta

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 这一组测试用的语料是**从真端点抓下来的片段**（2026-10-06），不是手写的
// 迷你样例。理由：这个包的全部价值都建立在"真实数据的形态"上——哪一档
// 价格会缺、哪个字段名是个陷阱、哪些键带命名空间——手写样例只会把作者的
// 假设再抄一遍，等于没测。
//
// 片段本身做了裁剪（只留十来条），但每条都是原样拷贝，没有改写字段。
const (
	fixtureModelsDev = "modelsdev_fragment.json"
	fixtureLiteLLM   = "litellm_fragment.json"
)

// testFetchedAt 是固定的"抓取时刻"。用常量而不是 time.Now()：缓存新鲜度
// 判定、陈旧兜底的测试都要拿它当锚点，用 now 会让这些测试随运行时间漂。
var testFetchedAt = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读语料 %s: %v", name, err)
	}
	return raw
}

// modelsDevCatalog 直接从语料解出索引，不经过 HTTP。
func modelsDevCatalog(t *testing.T) *Catalog {
	t.Helper()
	var raw map[string]modelsDevProvider
	if err := json.Unmarshal(readFixture(t, fixtureModelsDev), &raw); err != nil {
		t.Fatalf("解 models.dev 语料: %v", err)
	}
	return parseModelsDev(raw, testFetchedAt)
}

func liteLLMRaw(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(readFixture(t, fixtureLiteLLM), &raw); err != nil {
		t.Fatalf("解 LiteLLM 语料: %v", err)
	}
	return raw
}

func liteLLMCatalog(t *testing.T) *Catalog {
	t.Helper()
	return parseLiteLLM(liteLLMRaw(t), testFetchedAt)
}

// entryOf 按"上游 id + 模型 id"取一条，取不到就让测试失败。
//
// 断言里到处要 `cat.entries[i].X`，而下标是构建顺序的产物、不该写进测试；
// 用这个取法，索引顺序变了测试不会跟着碎。
func entryOf(t *testing.T, cat *Catalog, provider, model string) Entry {
	t.Helper()
	for _, e := range cat.entries {
		if e.Provider == provider && e.Model == model {
			return e
		}
	}
	t.Fatalf("语料里没有 %s / %s", provider, model)
	return Entry{}
}

func hasEntry(cat *Catalog, provider, model string) bool {
	for _, e := range cat.entries {
		if e.Provider == provider && e.Model == model {
			return true
		}
	}
	return false
}

// stubDoer 顶替 HTTP 客户端。两个 Source 的 Fetch 都只用到 Do，
// 所以这一个桩同时服务两个源的抓取测试。
type stubDoer struct {
	status int
	body   []byte
	err    error

	gotURL     string
	gotAccept  string
	gotHeaders http.Header
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.gotURL = req.URL.String()
	s.gotAccept = req.Header.Get("Accept")
	s.gotHeaders = req.Header.Clone()
	if s.err != nil {
		return nil, s.err
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Header:     make(http.Header),
	}, nil
}

// fakeSource 是一个可控的 Source：成功时给一份装好的索引，失败时给错误。
type fakeSource struct {
	name string
	cat  *Catalog
	err  error

	fetches int
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Fetch(context.Context) (*Catalog, error) {
	f.fetches++
	if f.err != nil {
		return nil, f.err
	}
	return f.cat, nil
}

// testManager 建一个两个源都已装好索引的管理器——整条建议路径不碰网络。
//
// 装索引走的是 SetCatalog，与将来"把快照编进二进制"（§7）用的是同一个口子，
// 所以这里的测试路径与那条离线路径是同一条。
func testManager(t *testing.T, modelsDev, liteLLM *Catalog) *Manager {
	t.Helper()
	m := NewManager(
		&fakeSource{name: SourceModelsDev, cat: modelsDev},
		&fakeSource{name: SourceLiteLLM, cat: liteLLM},
	)
	m.now = func() time.Time { return testFetchedAt.Add(time.Hour) }
	if modelsDev != nil {
		m.SetCatalog(modelsDev)
	}
	if liteLLM != nil {
		m.SetCatalog(liteLLM)
	}
	return m
}

// fullTestManager 是两个源都装了真语料的管理器，最常用。
func fullTestManager(t *testing.T) *Manager {
	t.Helper()
	return testManager(t, modelsDevCatalog(t), liteLLMCatalog(t))
}

func boolValue(t *testing.T, p *bool) bool {
	t.Helper()
	if p == nil {
		t.Fatalf("期望非 nil 的 bool 指针")
	}
	return *p
}

func floatValue(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatalf("期望非 nil 的 float 指针")
	}
	return *p
}
