package handler

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

// ProviderRequest represents the request body for creating/updating a provider
type ProviderRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Config  string `json:"config"`
	Console string `json:"console"`
}

// ModelRequest represents the request body for creating/updating a model
type ModelRequest struct {
	Name     string `json:"name"`
	Remark   string `json:"remark"`
	MaxRetry int    `json:"max_retry"`
	TimeOut  int    `json:"time_out"`
	IOLog    bool   `json:"io_log"`
	Strategy string `json:"strategy"`
	Breaker  bool   `json:"breaker"`
}

// ModelWithProviderRequest represents the request body for creating/updating a model-provider association
type ModelWithProviderRequest struct {
	ModelID          uint              `json:"model_id"`
	ProviderModel    string            `json:"provider_name"`
	ProviderID       uint              `json:"provider_id"`
	ToolCall         bool              `json:"tool_call"`
	StructuredOutput bool              `json:"structured_output"`
	Image            bool              `json:"image"`
	WithHeader       bool              `json:"with_header"`
	CustomerHeaders  map[string]string `json:"customer_headers"`
	Weight           int               `json:"weight"`
}

// ModelProviderStatusRequest represents the request body for updating provider status
type ModelProviderStatusRequest struct {
	Status bool `json:"status"`
}

// SystemConfigRequest represents the request body for updating system configuration
type SystemConfigRequest struct {
	EnableSmartRouting  bool    `json:"enable_smart_routing"`
	SuccessRateWeight   float64 `json:"success_rate_weight"`
	ResponseTimeWeight  float64 `json:"response_time_weight"`
	DecayThresholdHours int     `json:"decay_threshold_hours"`
	MinWeight           int     `json:"min_weight"`
}

// ConfigValueRequest represents the request body for updating config value
type ConfigValueRequest struct {
	Value string `json:"value" binding:"required"`
}

// GetProviders 获取所有提供商列表（支持名称搜索和类型筛选）
func GetProviders(c *gin.Context) {
	// 筛选参数
	name := c.Query("name")
	providerType := c.Query("type")

	// 构建查询条件
	query := models.DB.Model(&models.Provider{}).WithContext(c.Request.Context())

	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}

	if providerType != "" {
		query = query.Where("type = ?", providerType)
	}
	var providers []models.Provider
	if err := query.Find(&providers).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	common.Success(c, providers)
}

func GetProviderModels(c *gin.Context) {
	id := c.Param("id")
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	chatModel, err := providers.New(provider.Type, provider.Config)
	if err != nil {
		common.InternalServerError(c, "Failed to get models: "+err.Error())
		return
	}
	models, err := chatModel.Models(c.Request.Context())
	if err != nil {
		common.NotFound(c, "Failed to get models: "+err.Error())
		return
	}
	common.Success(c, models)
}

// CreateProvider 创建提供商
func CreateProvider(c *gin.Context) {
	var req ProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if provider exists
	count, err := gorm.G[models.Provider](models.DB).Where("name = ?", req.Name).Count(c.Request.Context(), "id")
	if err != nil {
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	if count > 0 {
		common.BadRequest(c, "Provider already exists")
		return
	}

	provider := models.Provider{
		Name:    req.Name,
		Type:    req.Type,
		Config:  req.Config,
		Console: req.Console,
	}

	if err := gorm.G[models.Provider](models.DB).Create(c.Request.Context(), &provider); err != nil {
		common.InternalServerError(c, "Failed to create provider: "+err.Error())
		return
	}

	common.Success(c, provider)
}

// UpdateProvider 更新提供商
func UpdateProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if provider exists
	if _, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context()); err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Provider not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	// Update fields
	updates := models.Provider{
		Name:    req.Name,
		Type:    req.Type,
		Config:  req.Config,
		Console: req.Console,
	}

	if _, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).Updates(c.Request.Context(), updates); err != nil {
		common.InternalServerError(c, "Failed to update provider: "+err.Error())
		return
	}

	// Get updated provider
	updatedProvider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated provider: "+err.Error())
		return
	}

	common.Success(c, updatedProvider)
}

// DeleteProvider 删除提供商
func DeleteProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete provider: "+err.Error())
		return
	}

	//删除关联
	if _, err := gorm.G[models.ModelWithProvider](models.DB).Where("provider_id = ?", id).Delete(c.Request.Context()); err != nil {
		common.InternalServerError(c, "Failed to delete provider: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Provider not found")
		return
	}

	common.Success(c, nil)
}

// GetModels 获取模型列表（支持分页与筛选）
func GetModels(c *gin.Context) {
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	query := models.DB.Model(&models.Model{})

	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ?", like)
	}

	if strategy := strings.TrimSpace(c.Query("strategy")); strategy != "" {
		switch strategy {
		case consts.BalancerLottery, consts.BalancerRotor:
			query = query.Where("strategy = ?", strategy)
		default:
			common.BadRequest(c, "invalid strategy filter")
			return
		}
	}

	if ioLog := strings.TrimSpace(c.Query("io_log")); ioLog != "" {
		switch ioLog {
		case "true", "false":
			query = query.Where("io_log = ?", ioLog == "true")
		default:
			common.BadRequest(c, "invalid io_log filter")
			return
		}
	}

	list := make([]models.Model, 0)
	total, err := common.PaginateQuery(query.Order("id DESC"), params, &list)
	if err != nil {
		common.InternalServerError(c, "Failed to query models: "+err.Error())
		return
	}

	response := common.NewPaginationResponse(list, total, params)
	common.Success(c, response)
}

// GetModelList 返回所有模型列表用于下拉选择等场景
func GetModelList(c *gin.Context) {
	modelsList, err := gorm.G[models.Model](models.DB).Order("id DESC").Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to get models: "+err.Error())
		return
	}

	common.Success(c, modelsList)
}

// CreateModel 创建模型
func CreateModel(c *gin.Context) {
	var req ModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if model exists
	count, err := gorm.G[models.Model](models.DB).Where("name = ?", req.Name).Count(c.Request.Context(), "id")
	if err != nil {
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}
	if count > 0 {
		common.BadRequest(c, fmt.Sprintf("Model: %s already exists", req.Name))
		return
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = consts.BalancerDefault
	}

	model := models.Model{
		Name:     req.Name,
		Remark:   req.Remark,
		MaxRetry: req.MaxRetry,
		TimeOut:  req.TimeOut,
		IOLog:    &req.IOLog,
		Strategy: strategy,
		Breaker:  &req.Breaker,
	}

	if err := gorm.G[models.Model](models.DB).Create(c.Request.Context(), &model); err != nil {
		common.InternalServerError(c, "Failed to create model: "+err.Error())
		return
	}

	common.Success(c, model)
}

// UpdateModel 更新模型
func UpdateModel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if model exists
	_, err = gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = consts.BalancerDefault
	}

	// Update fields
	updates := models.Model{
		Name:     req.Name,
		Remark:   req.Remark,
		MaxRetry: req.MaxRetry,
		TimeOut:  req.TimeOut,
		IOLog:    &req.IOLog,
		Strategy: strategy,
		Breaker:  &req.Breaker,
	}

	if _, err := gorm.G[models.Model](models.DB).Where("id = ?", id).Updates(c.Request.Context(), updates); err != nil {
		common.InternalServerError(c, "Failed to update model: "+err.Error())
		return
	}

	// Get updated model
	updatedModel, err := gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model: "+err.Error())
		return
	}

	common.Success(c, updatedModel)
}

// DeleteModel 删除模型
func DeleteModel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.Model](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete model: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Model not found")
		return
	}

	common.Success(c, nil)
}

type ProviderTemplate struct {
	Type     string `json:"type"`
	Template string `json:"template"`
}

var template = []ProviderTemplate{
	{
		Type: "openai",
		Template: `{
			"base_url": "https://api.openai.com/v1",
			"api_key": "YOUR_API_KEY"
		}`,
	},
	{
		Type: "openai-res",
		Template: `{
			"base_url": "https://api.openai.com/v1",
			"api_key": "YOUR_API_KEY"
		}`,
	},
	{
		Type: "anthropic",
		Template: `{
			"base_url": "https://api.anthropic.com/v1",
			"api_key": "YOUR_API_KEY",
			"version": "2023-06-01"
		}`,
	},
}

func GetProviderTemplates(c *gin.Context) {
	common.Success(c, template)
}

// GetModelProviders 获取模型的提供商关联列表
func GetModelProviders(c *gin.Context) {
	modelIDStr := c.Query("model_id")
	if modelIDStr == "" {
		common.BadRequest(c, "model_id query parameter is required")
		return
	}

	modelID, err := strconv.ParseUint(modelIDStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid model_id format")
		return
	}

	modelProviders, err := gorm.G[models.ModelWithProvider](models.DB).Where("model_id = ?", modelID).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	common.Success(c, modelProviders)
}

// GetModelProviderStatus 获取提供商状态信息
func GetModelProviderStatus(c *gin.Context) {
	providerIDStr := c.Query("provider_id")
	modelName := c.Query("model_name")
	providerModel := c.Query("provider_model")

	if providerIDStr == "" || modelName == "" || providerModel == "" {
		common.BadRequest(c, "provider_id, model_name and provider_model query parameters are required")
		return
	}

	providerID, err := strconv.ParseUint(providerIDStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid provider_id format")
		return
	}

	// 获取提供商信息
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", providerID).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve provider: "+err.Error())
		return
	}

	// 获取最近10次请求状态
	logs, err := gorm.G[models.ChatLog](models.DB).
		Where("provider_name = ?", provider.Name).
		Where("provider_model = ?", providerModel).
		Where("name = ?", modelName).
		Limit(10).
		Order("created_at DESC").
		Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve chat log: "+err.Error())
		return
	}

	status := make([]bool, 0)
	for _, log := range logs {
		status = append(status, log.Status == "success")
	}
	slices.Reverse(status)
	common.Success(c, status)
}

// CreateModelProvider 创建模型提供商关联
func CreateModelProvider(c *gin.Context) {
	var req ModelWithProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	customerHeaders := req.CustomerHeaders
	if customerHeaders == nil {
		customerHeaders = map[string]string{}
	}

	modelProvider := models.ModelWithProvider{
		ModelID:          req.ModelID,
		ProviderModel:    req.ProviderModel,
		ProviderID:       req.ProviderID,
		ToolCall:         &req.ToolCall,
		StructuredOutput: &req.StructuredOutput,
		Image:            &req.Image,
		WithHeader:       &req.WithHeader,
		CustomerHeaders:  customerHeaders,
		Weight:           req.Weight,
	}

	defaultStatus := true
	modelProvider.Status = &defaultStatus

	err := gorm.G[models.ModelWithProvider](models.DB).Create(c.Request.Context(), &modelProvider)
	if err != nil {
		common.InternalServerError(c, "Failed to create model-provider association: "+err.Error())
		return
	}

	common.Success(c, modelProvider)
}

// UpdateModelProvider 更新模型提供商关联
func UpdateModelProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelWithProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	slog.Info("UpdateModelProvider", "req", req)

	customerHeaders := req.CustomerHeaders
	if customerHeaders == nil {
		customerHeaders = map[string]string{}
	}

	// Check if model-provider association exists
	_, err = gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model-provider association not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	// Update fields
	updates := models.ModelWithProvider{
		ModelID:          req.ModelID,
		ProviderID:       req.ProviderID,
		ProviderModel:    req.ProviderModel,
		ToolCall:         &req.ToolCall,
		StructuredOutput: &req.StructuredOutput,
		Image:            &req.Image,
		WithHeader:       &req.WithHeader,
		CustomerHeaders:  customerHeaders,
		Weight:           req.Weight,
	}

	if _, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).Updates(c.Request.Context(), updates); err != nil {
		common.InternalServerError(c, "Failed to update model-provider association: "+err.Error())
		return
	}

	// Get updated model-provider association
	updatedModelProvider, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model-provider association: "+err.Error())
		return
	}

	common.Success(c, updatedModelProvider)
}

// UpdateModelProviderStatus 切换模型提供商关联启用状态
func UpdateModelProviderStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelProviderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	existing, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model-provider association not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve model-provider association: "+err.Error())
		return
	}

	status := req.Status
	updates := models.ModelWithProvider{
		Status: &status,
	}

	if _, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).Updates(c.Request.Context(), updates); err != nil {
		common.InternalServerError(c, "Failed to update status: "+err.Error())
		return
	}

	existing.Status = &status
	common.Success(c, existing)
}

// DeleteModelProvider 删除模型提供商关联
func DeleteModelProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete model-provider association: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Model-provider association not found")
		return
	}

	common.Success(c, nil)
}

type WrapLog struct {
	models.ChatLog
	KeyName string `json:"key_name"`
}

// GetRequestLogs 获取最近的请求日志（支持分页和筛选）
func GetRequestLogs(c *gin.Context) {
	// 解析分页参数
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	// 获取筛选参数
	providerName := c.Query("provider_name")
	name := c.Query("name")
	status := c.Query("status")
	style := c.Query("style")
	authKeyID := c.Query("auth_key_id")

	// 构建查询条件
	query := models.DB.Model(&models.ChatLog{})

	if providerName != "" {
		query = query.Where("provider_name = ?", providerName)
	}

	if name != "" {
		query = query.Where("name = ?", name)
	}

	if status != "" {
		query = query.Where("status = ?", status)
	}

	if style != "" {
		query = query.Where("style = ?", style)
	}

	if authKeyID != "" {
		query = query.Where("auth_key_id = ?", authKeyID)
	}

	// 执行分页查询
	var logs []models.ChatLog
	total, err := common.PaginateQuery(
		query.Order("id DESC"),
		params,
		&logs,
	)
	if err != nil {
		common.InternalServerError(c, "Failed to query logs: "+err.Error())
		return
	}

	keys, err := gorm.G[models.AuthKey](models.DB).Where("id IN ?", lo.Map(logs, func(log models.ChatLog, _ int) uint { return log.AuthKeyID })).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	keyMap := lo.KeyBy(keys, func(key models.AuthKey) uint { return key.ID })

	wrapLogs := make([]WrapLog, 0)
	for _, log := range logs {
		var keyName string
		if key, ok := keyMap[log.AuthKeyID]; ok {
			keyName = key.Name
		}
		if log.AuthKeyID == 0 {
			keyName = "admin"
		}
		wrapLogs = append(wrapLogs, WrapLog{
			ChatLog: log,
			KeyName: keyName,
		})
	}

	// 返回分页响应
	response := common.NewPaginationResponse(wrapLogs, total, params)
	common.Success(c, response)
}

// GetChatIO 查询指定日志的输入输出记录
func GetChatIO(c *gin.Context) {
	id := c.Param("id")

	chatIO, err := gorm.G[models.ChatIO](models.DB).Where("log_id = ?", id).First(c.Request.Context())
	if err != nil {
		common.NotFound(c, "ChatIO not found")
		return
	}

	common.Success(c, chatIO)
}

// GetUserAgents 获取所有不重复的用户代理种类
func GetUserAgents(c *gin.Context) {
	var userAgents []string

	// 查询所有不重复的非空用户代理
	if err := models.DB.Model(&models.ChatLog{}).
		Where("user_agent IS NOT NULL AND user_agent != ''").
		Distinct("user_agent").
		Pluck("user_agent", &userAgents).
		Error; err != nil {
		common.InternalServerError(c, "Failed to query user agents: "+err.Error())
		return
	}

	common.Success(c, userAgents)
}

// GetConfigByKey 获取特定配置
func GetConfigByKey(c *gin.Context) {
	key := c.Param("key")
	config, err := gorm.G[models.Config](models.DB).Where("key = ?", key).First(c.Request.Context())

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 配置不存在，返回空响应
			common.Success(c, map[string]string{
				"key":   key,
				"value": "",
			})
			return
		}
		common.InternalServerError(c, "Failed to get config: "+err.Error())
		return
	}

	common.Success(c, map[string]string{
		"key":   config.Key,
		"value": config.Value,
	})
}

// UpdateConfigByKey 更新配置
func UpdateConfigByKey(c *gin.Context) {
	key := c.Param("key")

	var req ConfigValueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 获取或创建配置记录
	config, err := gorm.G[models.Config](models.DB).Where("key = ?", key).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 创建新配置
			config = models.Config{
				Key:   key,
				Value: req.Value,
			}
			if err := gorm.G[models.Config](models.DB).Create(c.Request.Context(), &config); err != nil {
				common.InternalServerError(c, "Failed to create config: "+err.Error())
				return
			}
		} else {
			common.InternalServerError(c, "Failed to get config: "+err.Error())
			return
		}
	} else {
		// 更新配置值
		config.Value = req.Value
		if _, err := gorm.G[models.Config](models.DB).Where("key = ?", key).Updates(c.Request.Context(), config); err != nil {
			common.InternalServerError(c, "Failed to update config: "+err.Error())
			return
		}
	}

	common.Success(c, map[string]string{
		"key":   config.Key,
		"value": config.Value,
	})
}
