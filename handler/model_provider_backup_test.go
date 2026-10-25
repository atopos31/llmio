package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 备用标记的透传链路：请求体里的 backup 必须原样落到 model_with_providers.backup，
// 增改两条路都算。老客户端不传该键即 false，天然兼容，不用单独测。

func TestCreateModelProviderTransmitsBackup(t *testing.T) {
	setupWritePathDB(t)

	w := doJSON(t, CreateModelProvider, http.MethodPost, "/api/model-providers", map[string]any{
		"model_id":      1,
		"provider_id":   1,
		"provider_name": "gpt-4o",
		"weight":        1,
		"backup":        true,
	}, nil)

	if code := envelopeCode(t, w); code != http.StatusOK {
		t.Fatalf("业务码 = %d，期望 200（原文 %s）", code, w.Body.String())
	}

	row, err := gorm.G[models.ModelWithProvider](models.DB).Where("provider_model = ?", "gpt-4o").First(context.Background())
	if err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Backup == nil || !*row.Backup {
		t.Errorf("backup = %v，期望非 nil 的 true（调度分池靠它，丢了备用就不接管）", row.Backup)
	}
}

func TestUpdateModelProviderCanClearBackup(t *testing.T) {
	setupWritePathDB(t)

	existing := models.ModelWithProvider{ModelID: 1, ProviderID: 1, ProviderModel: "gpt-4o", Backup: new(true)}
	existing.ID = 9
	if err := models.DB.Create(&existing).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	w := doJSON(t, UpdateModelProvider, http.MethodPut, "/api/model-providers/9", map[string]any{
		"model_id":      1,
		"provider_id":   1,
		"provider_name": "gpt-4o",
		"weight":        1,
		"backup":        false,
	}, gin.Params{{Key: "id", Value: "9"}})

	if code := envelopeCode(t, w); code != http.StatusOK {
		t.Fatalf("业务码 = %d，期望 200（原文 %s）", code, w.Body.String())
	}

	row, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", 9).First(context.Background())
	if err != nil {
		t.Fatalf("load row: %v", err)
	}
	// 必须是**非 nil 的 false**：nil 会被调度当成"没配"，又滑回主池
	if row.Backup == nil || *row.Backup {
		t.Errorf("backup = %v，期望被显式清成非 nil 的 false", row.Backup)
	}
}
