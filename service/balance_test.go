package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

const balanceTestBody = `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`

// setupBalanceTestDB 起一个临时 SQLite 并写入包级 models.DB。
// BalanceChat 的 retryLog 收集协程会落库（SaveChatLog），DB 为 nil 会 panic。
func setupBalanceTestDB(t *testing.T) {
	t.Helper()
	models.Init(context.Background(), filepath.Join(t.TempDir(), "llmio.db"))
	t.Cleanup(func() {
		if models.DB == nil {
			return
		}
		// Windows 下文件被占用会导致 TempDir 清理失败，必须先关连接
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// newUpstream 起一个固定返回指定状态码的上游，并统计被调用次数。
// 处理器内不要调 t.Errorf：上游协程可能在测试结束后才被调用。
func newUpstream(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testOpenAIProvider(id uint, name, baseURL string) models.Provider {
	return models.Provider{
		Model:  gorm.Model{ID: id},
		Name:   name,
		Type:   consts.StyleOpenAI,
		Config: fmt.Sprintf(`{"base_url":%q,"api_key":"test-key"}`, baseURL),
	}
}

// balanceTestMeta 组装「一个主关联 + 一个备用关联」的候选池。
// 关联的 Backup 标记与 splitWeightItems 的分池结果保持一致（生产路径由同一字段驱动）。
func balanceTestMeta(primaryBaseURL, backupBaseURL string) ProvidersWithMeta {
	return ProvidersWithMeta{
		ModelWithProviderMap: map[uint]models.ModelWithProvider{
			101: {Model: gorm.Model{ID: 101}, ProviderID: 1, ProviderModel: "primary-model", Backup: new(false)},
			102: {Model: gorm.Model{ID: 102}, ProviderID: 2, ProviderModel: "backup-model", Backup: new(true)},
		},
		WeightItems:       map[uint]int{101: 1},
		BackupWeightItems: map[uint]int{102: 1},
		ProviderMap: map[uint]models.Provider{
			1: testOpenAIProvider(1, "primary-provider", primaryBaseURL),
			2: testOpenAIProvider(2, "backup-provider", backupBaseURL),
		},
		MaxRetry: 3,
		TimeOut:  5,
		Strategy: consts.BalancerDefault,
	}
}

func runBalanceChat(t *testing.T, meta ProvidersWithMeta) (*http.Response, *models.ChatLog, error) {
	t.Helper()
	before := Before{
		Model:     "test-model",
		Stream:    false,
		SessionID: "session-for-test",
		raw:       []byte(balanceTestBody),
	}
	return BalanceChat(context.Background(), time.Now(), consts.StyleOpenAI, before, meta, models.ReqMeta{
		Header:    http.Header{},
		RemoteIP:  "127.0.0.1",
		UserAgent: "llmio-test",
	}, NewBridgeNotes())
}

// 备用关联不参与日常轮询：主关联健康时必须只打主关联，备用上游一次都不能被碰。
func TestBalanceChatSkipsBackupWhenPrimaryHealthy(t *testing.T) {
	setupBalanceTestDB(t)

	primary, primaryCalls := newUpstream(t, http.StatusOK, `{"ok":true}`)
	backup, backupCalls := newUpstream(t, http.StatusOK, `{"ok":true}`)

	res, log, err := runBalanceChat(t, balanceTestMeta(primary.URL, backup.URL))
	if err != nil {
		t.Fatalf("BalanceChat() unexpected error: %v", err)
	}
	defer res.Body.Close()

	if log.ProviderName != "primary-provider" {
		t.Fatalf("命中 provider = %q, want primary-provider", log.ProviderName)
	}
	if log.Backup {
		t.Errorf("log.Backup = true, want false（主关联服务时日志不应标记为备用）")
	}
	if got := atomic.LoadInt32(primaryCalls); got != 1 {
		t.Errorf("主上游调用次数 = %d, want 1", got)
	}
	if got := atomic.LoadInt32(backupCalls); got != 0 {
		t.Errorf("备用上游调用次数 = %d, want 0（主关联健康时备用不该被触碰）", got)
	}
}

// 主关联全部失败出局后由备用接管，并正常拿到 200。
func TestBalanceChatFallsBackAfterPrimaryExhausted(t *testing.T) {
	setupBalanceTestDB(t)

	primary, primaryCalls := newUpstream(t, http.StatusInternalServerError, `{"error":"boom"}`)
	backup, backupCalls := newUpstream(t, http.StatusOK, `{"ok":true}`)

	res, log, err := runBalanceChat(t, balanceTestMeta(primary.URL, backup.URL))
	if err != nil {
		t.Fatalf("期望降级到备用后成功，实际 error: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", res.StatusCode)
	}
	if log.ProviderName != "backup-provider" || log.ProviderModel != "backup-model" {
		t.Fatalf("命中 = %s/%s, want backup-provider/backup-model", log.ProviderName, log.ProviderModel)
	}
	if !log.Backup {
		t.Error("log.Backup = false, want true（走备用关联的请求必须在日志里标出来，否则日志页看不出降级）")
	}
	if got := atomic.LoadInt32(primaryCalls); got != 1 {
		t.Errorf("主上游调用次数 = %d, want 1", got)
	}
	if got := atomic.LoadInt32(backupCalls); got != 1 {
		t.Errorf("备用上游调用次数 = %d, want 1", got)
	}

	// 失败行由后台协程落库；不等它结束，t.TempDir 的清理会撞上仍在写库的
	// 连接（CI 上表现为 directory not empty）。
	waitForFailRows(t, "test-model", 1)
}

// 连备用也挂掉时，错误信息必须标明已经用过备用，
// 否则和「主关联本身就不够用」完全无从区分。
func TestBalanceChatReportsBackupExhausted(t *testing.T) {
	setupBalanceTestDB(t)

	primary, _ := newUpstream(t, http.StatusInternalServerError, `{"error":"primary boom"}`)
	backup, _ := newUpstream(t, http.StatusInternalServerError, `{"error":"backup boom"}`)

	_, _, err := runBalanceChat(t, balanceTestMeta(primary.URL, backup.URL))
	if err == nil {
		t.Fatal("主备全挂时应返回 error")
	}
	if !strings.Contains(err.Error(), "backup providers exhausted") {
		t.Fatalf("error = %q, 期望包含 \"backup providers exhausted\"", err.Error())
	}
	if !strings.Contains(err.Error(), "backup boom") {
		t.Fatalf("error = %q, 期望保留最后一轮的真实原因 \"backup boom\"", err.Error())
	}

	waitForFailRows(t, "test-model", 2)
}

// 没配备用关联时行为与改动前一致：主关联耗尽即失败，且错误信息不带降级标记。
func TestBalanceChatWithoutBackupKeepsLegacyBehavior(t *testing.T) {
	setupBalanceTestDB(t)

	primary, primaryCalls := newUpstream(t, http.StatusInternalServerError, `{"error":"boom"}`)

	meta := balanceTestMeta(primary.URL, "")
	meta.BackupWeightItems = map[uint]int{}
	delete(meta.ModelWithProviderMap, 102)

	_, _, err := runBalanceChat(t, meta)
	if err == nil {
		t.Fatal("无备用关联且主关联失败时应返回 error")
	}
	if strings.Contains(err.Error(), "backup providers exhausted") {
		t.Fatalf("error = %q, 未配备用时不应出现降级标记", err.Error())
	}
	if got := atomic.LoadInt32(primaryCalls); got != 1 {
		t.Errorf("主上游调用次数 = %d, want 1", got)
	}

	waitForFailRows(t, "test-model", 1)
}

// 备用标记必须能活到日志页：Backup 只在 BalanceChat 里随 insert 落库，
// 之后 RecordLog 拿 processer 返回的空壳 log 做 struct Updates，
// 全靠 GORM「跳过零值字段」的语义才没被擦成 false。这条测试守的就是整条链路。
func TestRecordLogPreservesBackupFlag(t *testing.T) {
	setupBalanceTestDB(t)
	ctx := context.Background()

	logId, err := SaveChatLog(ctx, models.ChatLog{
		Name:         "test-model",
		ProviderName: "backup-provider",
		Backup:       true,
		Status:       consts.StatusRunning,
	})
	if err != nil {
		t.Fatalf("SaveChatLog() error: %v", err)
	}

	processer := func(_ context.Context, _ io.Reader, _ bool, _ time.Time) (*models.ChatLog, *models.OutputUnion, error) {
		return &models.ChatLog{FirstChunkTime: time.Second, Size: 7}, &models.OutputUnion{}, nil
	}
	RecordLog(ctx, time.Now(), io.NopCloser(strings.NewReader("data")), processer, logId, Before{}, false, NewBridgeNotes())

	got, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).First(ctx)
	if err != nil {
		t.Fatalf("回查日志失败: %v", err)
	}
	if !got.Backup {
		t.Error("RecordLog 之后 Backup 被擦成 false：struct Updates 跳过零值字段的语义已不成立（或已被改成 Save / Select）")
	}
	if got.ProviderName != "backup-provider" {
		t.Errorf("ProviderName = %q, want backup-provider（零值字段不该覆盖已落库的值）", got.ProviderName)
	}
	if got.Status != consts.StatusSuccess {
		t.Errorf("Status = %q, want %q", got.Status, consts.StatusSuccess)
	}
	if got.Size != 7 {
		t.Errorf("Size = %d, want 7（非零字段应被正常更新）", got.Size)
	}
}
