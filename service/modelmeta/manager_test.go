package modelmeta

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// 这一组钉的是缓存的三条规则（§3.2），它们直接决定"弹窗点开时会不会卡住"：
//
//   - 新鲜  → 直接用
//   - 陈旧  → **先返回陈旧的**，后台刷新
//   - 没有  → 起后台抓取并立刻返回 ErrCatalogPreparing
//
// 5.3 MB 的下载不能挂在一个弹窗的请求上，所以"没有缓存时立刻返回"不是
// 优化而是正确性。

// blockingSource 是一个会卡住的 Source，用来观察"抓取起了几次"。
type blockingSource struct {
	name string
	cat  *Catalog

	mu      sync.Mutex
	started int
	unblock chan struct{}
}

func (b *blockingSource) Name() string { return b.name }

func (b *blockingSource) Fetch(context.Context) (*Catalog, error) {
	b.mu.Lock()
	b.started++
	b.mu.Unlock()
	<-b.unblock
	return b.cat, nil
}

func (b *blockingSource) startedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started
}

// waitUntil 轮询一个条件，超时就失败。用于等后台 goroutine 的效果，
// 不用固定 sleep——固定 sleep 在慢机器上会假失败。
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

func TestManagerCatalogUnknownSource(t *testing.T) {
	m := NewManager(&fakeSource{name: SourceModelsDev})
	if _, err := m.Catalog(context.Background(), "no-such-source"); !errors.Is(err, ErrUnknownSource) {
		t.Errorf("err = %v，期望 %v", err, ErrUnknownSource)
	}
}

func TestManagerCatalogFreshServesCache(t *testing.T) {
	src := &fakeSource{name: SourceModelsDev, cat: modelsDevCatalog(t)}
	src.cat.FetchedAt = testFetchedAt
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt.Add(time.Hour) }
	m.SetCatalog(src.cat)

	cat, err := m.Catalog(context.Background(), SourceModelsDev)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if cat != src.cat {
		t.Error("新鲜缓存该原样返回")
	}
	// 新鲜就不该去打扰上游。
	if src.fetches != 0 {
		t.Errorf("新鲜缓存下抓取了 %d 次，期望 0 次", src.fetches)
	}
}

// TestManagerCatalogNoCacheReturnsImmediately 是这条纪律的核心：
// 没有缓存时**立刻**返回 ErrCatalogPreparing，抓取丢到后台。
func TestManagerCatalogNoCacheReturnsImmediately(t *testing.T) {
	src := &blockingSource{name: SourceModelsDev, cat: modelsDevCatalog(t), unblock: make(chan struct{})}
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt }

	done := make(chan error, 1)
	go func() {
		_, err := m.Catalog(context.Background(), SourceModelsDev)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrCatalogPreparing) {
			t.Errorf("err = %v，期望 %v", err, ErrCatalogPreparing)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有缓存时 Catalog 阻塞了——它必须立刻返回，抓取在后台跑")
	}

	close(src.unblock)
}

// TestManagerKickDeduplicates：同一次抓取不会并发跑两遍。
//
// 没有这层去重，控制台上十个页签同时打开就是十份 5.3 MB 的下载。
func TestManagerKickDeduplicates(t *testing.T) {
	src := &blockingSource{name: SourceModelsDev, cat: modelsDevCatalog(t), unblock: make(chan struct{})}
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt }

	for i := 0; i < 5; i++ {
		if _, err := m.Catalog(context.Background(), SourceModelsDev); !errors.Is(err, ErrCatalogPreparing) {
			t.Fatalf("第 %d 次调用 err = %v，期望 %v", i+1, err, ErrCatalogPreparing)
		}
	}

	// 等第一次抓取真的跑起来，再多打几次。
	waitUntil(t, "第一次抓取开始", func() bool { return src.startedCount() == 1 })
	for i := 0; i < 5; i++ {
		_, _ = m.Catalog(context.Background(), SourceModelsDev)
	}
	time.Sleep(50 * time.Millisecond)
	if got := src.startedCount(); got != 1 {
		t.Errorf("抓取起了 %d 次，期望 1 次", got)
	}

	// 放开之后缓存就填上了，之后走的是缓存。
	close(src.unblock)
	waitUntil(t, "抓取完成并写进缓存", func() bool {
		cat, err := m.Catalog(context.Background(), SourceModelsDev)
		return err == nil && cat != nil
	})
}

// TestManagerRefreshKeepsStaleOnError：抓取失败**保留**上一次成功的缓存。
//
// 这是"一次网络抖动不该让用户看到空建议"的实现处。
func TestManagerRefreshKeepsStaleOnError(t *testing.T) {
	stale := modelsDevCatalog(t)
	stale.FetchedAt = testFetchedAt.Add(-72 * time.Hour)

	src := &fakeSource{name: SourceModelsDev, err: errors.New("network down")}
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt }
	m.SetCatalog(stale)

	cat, err := m.Refresh(context.Background(), SourceModelsDev)
	if err == nil {
		t.Fatal("抓取失败该返回错误")
	}
	if cat != stale {
		t.Error("抓取失败该把旧缓存返回给调用方")
	}
	// 缓存槽里也还是旧的。
	if got, err := m.Catalog(context.Background(), SourceModelsDev); err != nil || got != stale {
		t.Errorf("Catalog = (%v, %v)，期望旧缓存", got, err)
	}
}

// TestManagerRefreshReplacesOnSuccess：抓成功就换新的，并把上次的错误清掉。
func TestManagerRefreshReplacesOnSuccess(t *testing.T) {
	old := modelsDevCatalog(t)
	old.FetchedAt = testFetchedAt.Add(-72 * time.Hour)
	fresh := modelsDevCatalog(t)
	fresh.FetchedAt = testFetchedAt
	fresh.EntryCount = 999 // 故意改一个可见的标记

	src := &fakeSource{name: SourceModelsDev, cat: fresh}
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt }
	m.SetCatalog(old)

	cat, err := m.Refresh(context.Background(), SourceModelsDev)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if cat != fresh {
		t.Fatal("成功后该换成新抓的")
	}

	m.mu.Lock()
	_, hasErr := m.lastErr[SourceModelsDev]
	m.mu.Unlock()
	if hasErr {
		t.Error("成功后该清掉上次的错误")
	}
}

func TestManagerRefreshUnknownSource(t *testing.T) {
	m := NewManager(&fakeSource{name: SourceModelsDev})
	if _, err := m.Refresh(context.Background(), "nope"); !errors.Is(err, ErrUnknownSource) {
		t.Errorf("err = %v，期望 %v", err, ErrUnknownSource)
	}
}

func TestManagerInvalidate(t *testing.T) {
	src := &fakeSource{name: SourceModelsDev, cat: modelsDevCatalog(t)}
	src.cat.FetchedAt = testFetchedAt
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt }
	m.SetCatalog(src.cat)

	m.Invalidate(SourceModelsDev)

	// 丢掉之后就该重新抓：抓取会卡在网络的 Source 上也不该阻塞这次调用。
	if _, err := m.Catalog(context.Background(), SourceModelsDev); !errors.Is(err, ErrCatalogPreparing) {
		t.Errorf("err = %v，期望 %v", err, ErrCatalogPreparing)
	}
}

// TestSetCatalogNilIsNoop：装空索引不该把已有的缓存清掉。
func TestSetCatalogNilIsNoop(t *testing.T) {
	cat := modelsDevCatalog(t)
	m := NewManager(&fakeSource{name: SourceModelsDev, cat: cat})
	m.SetCatalog(cat)
	m.SetCatalog(nil)

	got, err := m.Catalog(context.Background(), SourceModelsDev)
	if err != nil || got != cat {
		t.Errorf("Catalog = (%v, %v)，期望原有缓存还在", got, err)
	}
}

// TestNewManagerRegistersSources：按名字注册，重复名字后者覆盖前者。
func TestNewManagerRegistersSources(t *testing.T) {
	a := &fakeSource{name: SourceModelsDev}
	b := &fakeSource{name: SourceModelsDev}
	m := NewManager(a, b)
	if len(m.sources) != 1 {
		t.Errorf("注册了 %d 个源，期望 1 个（同名覆盖）", len(m.sources))
	}
}

// TestManagerConcurrentCatalog 是一个并发冒烟：多个弹窗同时打开时
// 管理器的加锁路径不能崩、不能漏。配合 -race 跑才真正有意义。
func TestManagerConcurrentCatalog(t *testing.T) {
	cat := modelsDevCatalog(t)
	cat.FetchedAt = testFetchedAt
	src := &fakeSource{name: SourceModelsDev, cat: cat}
	m := NewManager(src)
	m.now = func() time.Time { return testFetchedAt }
	m.SetCatalog(cat)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := m.Catalog(context.Background(), SourceModelsDev); err != nil {
					t.Errorf("Catalog: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
