package handler

import (
	"encoding/json"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// BackupData 导出数据结构
type BackupData struct {
	Version          string                     `json:"version"`
	ExportedAt       time.Time                  `json:"exported_at"`
	Providers        []ProviderBackup           `json:"providers"`
	Models           []ModelBackup              `json:"models"`
	ModelProviders   []ModelProviderBackup      `json:"model_providers"`
	AuthKeys         []AuthKeyBackup            `json:"auth_keys"`
	Configs          []ConfigBackup             `json:"configs"`
}

// ProviderBackup 提供商备份结构
type ProviderBackup struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Config  string `json:"config"`
	Console string `json:"console"`
}

// ModelBackup 模型备份结构
type ModelBackup struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Remark   string `json:"remark"`
	MaxRetry int    `json:"max_retry"`
	TimeOut  int    `json:"time_out"`
	IOLog    bool   `json:"io_log"`
	Strategy string `json:"strategy"`
	Breaker  bool   `json:"breaker"`
}

// ModelProviderBackup 模型提供商关联备份结构
type ModelProviderBackup struct {
	ID               uint              `json:"id"`
	ModelID          uint              `json:"model_id"`
	ProviderModel    string            `json:"provider_model"`
	ProviderID       uint              `json:"provider_id"`
	ToolCall         bool              `json:"tool_call"`
	StructuredOutput bool              `json:"structured_output"`
	Image            bool              `json:"image"`
	WithHeader       bool              `json:"with_header"`
	Status           bool              `json:"status"`
	CustomerHeaders  map[string]string `json:"customer_headers"`
	Weight           int               `json:"weight"`
}

// AuthKeyBackup API Key 备份结构
type AuthKeyBackup struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Status    bool      `json:"status"`
	AllowAll  bool      `json:"allow_all"`
	Models    []string  `json:"models"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// ConfigBackup 配置备份结构
type ConfigBackup struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ExportData 导出所有数据
func ExportData(c *gin.Context) {
	ctx := c.Request.Context()

	// 获取所有提供商
	providers, err := gorm.G[models.Provider](models.DB).Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to export providers: "+err.Error())
		return
	}

	providerBackups := make([]ProviderBackup, len(providers))
	for i, p := range providers {
		providerBackups[i] = ProviderBackup{
			ID:      p.ID,
			Name:    p.Name,
			Type:    p.Type,
			Config:  p.Config,
			Console: p.Console,
		}
	}

	// 获取所有模型
	modelsList, err := gorm.G[models.Model](models.DB).Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to export models: "+err.Error())
		return
	}

	modelBackups := make([]ModelBackup, len(modelsList))
	for i, m := range modelsList {
		ioLog := false
		if m.IOLog != nil {
			ioLog = *m.IOLog
		}
		breaker := false
		if m.Breaker != nil {
			breaker = *m.Breaker
		}
		modelBackups[i] = ModelBackup{
			ID:       m.ID,
			Name:     m.Name,
			Remark:   m.Remark,
			MaxRetry: m.MaxRetry,
			TimeOut:  m.TimeOut,
			IOLog:    ioLog,
			Strategy: m.Strategy,
			Breaker:  breaker,
		}
	}

	// 获取所有模型提供商关联
	modelProviders, err := gorm.G[models.ModelWithProvider](models.DB).Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to export model providers: "+err.Error())
		return
	}

	mpBackups := make([]ModelProviderBackup, len(modelProviders))
	for i, mp := range modelProviders {
		toolCall := false
		if mp.ToolCall != nil {
			toolCall = *mp.ToolCall
		}
		structuredOutput := false
		if mp.StructuredOutput != nil {
			structuredOutput = *mp.StructuredOutput
		}
		image := false
		if mp.Image != nil {
			image = *mp.Image
		}
		withHeader := false
		if mp.WithHeader != nil {
			withHeader = *mp.WithHeader
		}
		status := false
		if mp.Status != nil {
			status = *mp.Status
		}
		customerHeaders := mp.CustomerHeaders
		if customerHeaders == nil {
			customerHeaders = map[string]string{}
		}
		mpBackups[i] = ModelProviderBackup{
			ID:               mp.ID,
			ModelID:          mp.ModelID,
			ProviderModel:    mp.ProviderModel,
			ProviderID:       mp.ProviderID,
			ToolCall:         toolCall,
			StructuredOutput: structuredOutput,
			Image:            image,
			WithHeader:       withHeader,
			Status:           status,
			CustomerHeaders:  customerHeaders,
			Weight:           mp.Weight,
		}
	}

	// 获取所有 API Keys
	authKeys, err := gorm.G[models.AuthKey](models.DB).Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to export auth keys: "+err.Error())
		return
	}

	authKeyBackups := make([]AuthKeyBackup, len(authKeys))
	for i, ak := range authKeys {
		status := false
		if ak.Status != nil {
			status = *ak.Status
		}
		allowAll := false
		if ak.AllowAll != nil {
			allowAll = *ak.AllowAll
		}
		authKeyBackups[i] = AuthKeyBackup{
			ID:        ak.ID,
			Name:      ak.Name,
			Key:       ak.Key,
			Status:    status,
			AllowAll:  allowAll,
			Models:    ak.Models,
			ExpiresAt: ak.ExpiresAt,
		}
	}

	// 获取所有配置
	configs, err := gorm.G[models.Config](models.DB).Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to export configs: "+err.Error())
		return
	}

	configBackups := make([]ConfigBackup, len(configs))
	for i, cfg := range configs {
		configBackups[i] = ConfigBackup{
			Key:   cfg.Key,
			Value: cfg.Value,
		}
	}

	backup := BackupData{
		Version:        "1.0",
		ExportedAt:     time.Now(),
		Providers:      providerBackups,
		Models:         modelBackups,
		ModelProviders: mpBackups,
		AuthKeys:       authKeyBackups,
		Configs:        configBackups,
	}

	common.Success(c, backup)
}

// ImportData 导入数据
func ImportData(c *gin.Context) {
	var backup BackupData
	if err := c.ShouldBindJSON(&backup); err != nil {
		common.BadRequest(c, "Invalid backup data: "+err.Error())
		return
	}

	// 使用事务确保数据一致性
	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 记录导入统计
	stats := map[string]int{
		"providers":       0,
		"models":          0,
		"model_providers": 0,
		"auth_keys":       0,
		"configs":         0,
	}

	// 导入提供商
	for _, p := range backup.Providers {
		provider := models.Provider{
			Name:    p.Name,
			Type:    p.Type,
			Config:  p.Config,
			Console: p.Console,
		}
		// 检查是否已存在同名提供商
		var existing models.Provider
		if err := tx.Where("name = ?", p.Name).First(&existing).Error; err == nil {
			// 更新现有记录
			if err := tx.Model(&existing).Updates(provider).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to update provider: "+err.Error())
				return
			}
		} else {
			// 创建新记录
			if err := tx.Create(&provider).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to create provider: "+err.Error())
				return
			}
		}
		stats["providers"]++
	}

	// 导入模型
	for _, m := range backup.Models {
		model := models.Model{
			Name:     m.Name,
			Remark:   m.Remark,
			MaxRetry: m.MaxRetry,
			TimeOut:  m.TimeOut,
			IOLog:    &m.IOLog,
			Strategy: m.Strategy,
			Breaker:  &m.Breaker,
		}
		var existing models.Model
		if err := tx.Where("name = ?", m.Name).First(&existing).Error; err == nil {
			if err := tx.Model(&existing).Updates(model).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to update model: "+err.Error())
				return
			}
		} else {
			if err := tx.Create(&model).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to create model: "+err.Error())
				return
			}
		}
		stats["models"]++
	}

	// 构建名称到ID的映射（用于关联导入）
	providerNameToID := make(map[string]uint)
	var allProviders []models.Provider
	if err := tx.Find(&allProviders).Error; err != nil {
		tx.Rollback()
		common.InternalServerError(c, "Failed to query providers: "+err.Error())
		return
	}
	for _, p := range allProviders {
		providerNameToID[p.Name] = p.ID
	}

	modelNameToID := make(map[string]uint)
	var allModels []models.Model
	if err := tx.Find(&allModels).Error; err != nil {
		tx.Rollback()
		common.InternalServerError(c, "Failed to query models: "+err.Error())
		return
	}
	for _, m := range allModels {
		modelNameToID[m.Name] = m.ID
	}

	// 构建旧ID到新ID的映射
	oldProviderIDToNew := make(map[uint]uint)
	for _, p := range backup.Providers {
		if newID, ok := providerNameToID[p.Name]; ok {
			oldProviderIDToNew[p.ID] = newID
		}
	}

	oldModelIDToNew := make(map[uint]uint)
	for _, m := range backup.Models {
		if newID, ok := modelNameToID[m.Name]; ok {
			oldModelIDToNew[m.ID] = newID
		}
	}

	// 导入模型提供商关联
	for _, mp := range backup.ModelProviders {
		newModelID, modelOK := oldModelIDToNew[mp.ModelID]
		newProviderID, providerOK := oldProviderIDToNew[mp.ProviderID]
		
		if !modelOK || !providerOK {
			continue // 跳过无法映射的关联
		}

		modelProvider := models.ModelWithProvider{
			ModelID:          newModelID,
			ProviderModel:    mp.ProviderModel,
			ProviderID:       newProviderID,
			ToolCall:         &mp.ToolCall,
			StructuredOutput: &mp.StructuredOutput,
			Image:            &mp.Image,
			WithHeader:       &mp.WithHeader,
			Status:           &mp.Status,
			CustomerHeaders:  mp.CustomerHeaders,
			Weight:           mp.Weight,
		}

		// 检查是否已存在相同的关联
		var existing models.ModelWithProvider
		if err := tx.Where("model_id = ? AND provider_id = ? AND provider_model = ?", 
			newModelID, newProviderID, mp.ProviderModel).First(&existing).Error; err == nil {
			if err := tx.Model(&existing).Updates(modelProvider).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to update model provider: "+err.Error())
				return
			}
		} else {
			if err := tx.Create(&modelProvider).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to create model provider: "+err.Error())
				return
			}
		}
		stats["model_providers"]++
	}

	// 导入 API Keys
	for _, ak := range backup.AuthKeys {
		authKey := models.AuthKey{
			Name:      ak.Name,
			Key:       ak.Key,
			Status:    &ak.Status,
			AllowAll:  &ak.AllowAll,
			Models:    ak.Models,
			ExpiresAt: ak.ExpiresAt,
		}
		var existing models.AuthKey
		if err := tx.Where("key = ?", ak.Key).First(&existing).Error; err == nil {
			if err := tx.Model(&existing).Updates(authKey).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to update auth key: "+err.Error())
				return
			}
		} else {
			if err := tx.Create(&authKey).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to create auth key: "+err.Error())
				return
			}
		}
		stats["auth_keys"]++
	}

	// 导入配置
	for _, cfg := range backup.Configs {
		config := models.Config{
			Key:   cfg.Key,
			Value: cfg.Value,
		}
		var existing models.Config
		if err := tx.Where("key = ?", cfg.Key).First(&existing).Error; err == nil {
			if err := tx.Model(&existing).Updates(config).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to update config: "+err.Error())
				return
			}
		} else {
			if err := tx.Create(&config).Error; err != nil {
				tx.Rollback()
				common.InternalServerError(c, "Failed to create config: "+err.Error())
				return
			}
		}
		stats["configs"]++
	}

	if err := tx.Commit().Error; err != nil {
		common.InternalServerError(c, "Failed to commit transaction: "+err.Error())
		return
	}

	common.Success(c, map[string]interface{}{
		"message": "Import completed successfully",
		"stats":   stats,
	})
}

// GetBackupInfo 获取备份信息（用于预览）
func GetBackupInfo(c *gin.Context) {
	var backup BackupData
	if err := c.ShouldBindJSON(&backup); err != nil {
		common.BadRequest(c, "Invalid backup data: "+err.Error())
		return
	}

	info := map[string]interface{}{
		"version":         backup.Version,
		"exported_at":     backup.ExportedAt,
		"providers_count": len(backup.Providers),
		"models_count":    len(backup.Models),
		"model_providers_count": len(backup.ModelProviders),
		"auth_keys_count": len(backup.AuthKeys),
		"configs_count":   len(backup.Configs),
	}

	common.Success(c, info)
}

// 用于验证 JSON 格式
func init() {
	_ = json.Marshal
}