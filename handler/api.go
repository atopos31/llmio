package handler

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

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
	Name         string `json:"name"`
	Remark       string `json:"remark"`
	MatchPattern string `json:"match_pattern"`
	MaxRetry     int    `json:"max_retry"`
	TimeOut      int    `json:"time_out"`
	IOLog        bool   `json:"io_log"`
	Strategy     string `json:"strategy"`
	Breaker      bool   `json:"breaker"`
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

// GetProviders 获取提供商列表（支持分页与筛选）
func GetProviders(c *gin.Context) {
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	query := models.DB.Model(&models.Provider{})

	// 筛选参数
	if name := strings.TrimSpace(c.Query("name")); name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}

	if providerType := strings.TrimSpace(c.Query("type")); providerType != "" {
		query = query.Where("type = ?", providerType)
	}

	list := make([]models.Provider, 0)
	total, err := common.PaginateQuery(query.Order("id DESC"), params, &list)
	if err != nil {
		common.InternalServerError(c, "Failed to query providers: "+err.Error())
		return
	}

	response := common.NewPaginationResponse(list, total, params)
	common.Success(c, response)
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
		Name:         req.Name,
		Remark:       req.Remark,
		MatchPattern: req.MatchPattern,
		MaxRetry:     req.MaxRetry,
		TimeOut:      req.TimeOut,
		IOLog:        &req.IOLog,
		Strategy:     strategy,
		Breaker:      &req.Breaker,
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
		Name:         req.Name,
		Remark:       req.Remark,
		MatchPattern: req.MatchPattern,
		MaxRetry:     req.MaxRetry,
		TimeOut:      req.TimeOut,
		IOLog:        &req.IOLog,
		Strategy:     strategy,
		Breaker:      &req.Breaker,
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
		Type: "gemini",
		Template: `{
			"base_url": "https://generativelanguage.googleapis.com/v1beta",
			"api_key": "YOUR_GEMINI_API_KEY"
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

// GetModelProviders 获取模型的提供商关联列表（支持分页）
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

	// 解析分页参数
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	query := models.DB.Model(&models.ModelWithProvider{}).Where("model_id = ?", modelID)

	list := make([]models.ModelWithProvider, 0)
	total, err := common.PaginateQuery(query.Order("id DESC"), params, &list)
	if err != nil {
		common.InternalServerError(c, "Failed to query model providers: "+err.Error())
		return
	}

	response := common.NewPaginationResponse(list, total, params)
	common.Success(c, response)
}

// BatchStatusRequest 批量状态查询请求
type BatchStatusRequest struct {
	ModelProviderIDs []uint `json:"model_provider_ids"`
}

// BatchStatusResponse 批量状态查询响应
type BatchStatusResponse struct {
	ModelProviderID uint   `json:"model_provider_id"`
	Status          []bool `json:"status"`
}

// GetModelProviderStatus 批量获取提供商状态信息
func GetModelProviderStatus(c *gin.Context) {
	var req BatchStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	if len(req.ModelProviderIDs) == 0 {
		common.BadRequest(c, "model_provider_ids cannot be empty")
		return
	}

	if len(req.ModelProviderIDs) > 100 {
		common.BadRequest(c, "Too many IDs, maximum 100 allowed")
		return
	}

	ctx := c.Request.Context()
	responses := make([]BatchStatusResponse, 0, len(req.ModelProviderIDs))

	// 批量查询 ModelWithProvider 信息
	modelProviders, err := gorm.G[models.ModelWithProvider](models.DB).
		Where("id IN ?", req.ModelProviderIDs).
		Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve model providers: "+err.Error())
		return
	}

	// 收集所有需要查询的 provider_id 和 model_id
	providerIDs := make([]uint, 0)
	modelIDs := make([]uint, 0)
	for _, mp := range modelProviders {
		providerIDs = append(providerIDs, mp.ProviderID)
		modelIDs = append(modelIDs, mp.ModelID)
	}

	// 批量查询 Provider 和 Model 信息
	providers, err := gorm.G[models.Provider](models.DB).
		Where("id IN ?", providerIDs).
		Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve providers: "+err.Error())
		return
	}

	modelList, err := gorm.G[models.Model](models.DB).
		Where("id IN ?", modelIDs).
		Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve models: "+err.Error())
		return
	}

	// 构建映射
	providerMap := make(map[uint]models.Provider)
	for _, provider := range providers {
		providerMap[provider.ID] = provider
	}

	modelMap := make(map[uint]models.Model)
	for _, model := range modelList {
		modelMap[model.ID] = model
	}

	// 为每个 ModelProvider 查询状态
	for _, mpID := range req.ModelProviderIDs {
		// 查找对应的 ModelWithProvider
		var mp *models.ModelWithProvider
		for _, modelProvider := range modelProviders {
			if modelProvider.ID == mpID {
				mp = &modelProvider
				break
			}
		}

		if mp == nil {
			// ModelProvider 不存在，返回空状态
			responses = append(responses, BatchStatusResponse{
				ModelProviderID: mpID,
				Status:          []bool{},
			})
			continue
		}

		provider, providerExists := providerMap[mp.ProviderID]
		model, modelExists := modelMap[mp.ModelID]

		if !providerExists || !modelExists {
			// Provider 或 Model 不存在，返回空状态
			responses = append(responses, BatchStatusResponse{
				ModelProviderID: mpID,
				Status:          []bool{},
			})
			continue
		}

		// 获取最近10次请求状态
		logs, err := gorm.G[models.ChatLog](models.DB).
			Where("provider_name = ?", provider.Name).
			Where("provider_model = ?", mp.ProviderModel).
			Where("name = ?", model.Name).
			Limit(10).
			Order("created_at DESC").
			Find(ctx)

		if err != nil {
			// 查询失败，返回空状态
			responses = append(responses, BatchStatusResponse{
				ModelProviderID: mpID,
				Status:          []bool{},
			})
			continue
		}

		status := make([]bool, 0, len(logs))
		for _, log := range logs {
			status = append(status, log.Status == "success")
		}
		slices.Reverse(status)

		responses = append(responses, BatchStatusResponse{
			ModelProviderID: mpID,
			Status:          status,
		})
	}

	common.Success(c, responses)
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

// CleanLogsRequest 清理日志请求
type CleanLogsRequest struct {
	Type  string `json:"type"`  // "count" 或 "days"
	Value int    `json:"value"` // 数量或天数
}

// CleanLogs 清理日志
func CleanLogs(c *gin.Context) {
	var req CleanLogsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.Value <= 0 {
		common.BadRequest(c, "Value must be greater than 0")
		return
	}

	var deletedCount int64

	switch req.Type {
	case "count":
		// 获取要保留的最小 ID
		var minID uint
		if err := models.DB.Model(&models.ChatLog{}).
			Order("id DESC").
			Limit(req.Value).
			Pluck("id", &[]uint{}).
			Scan(&minID).Error; err != nil {
			common.InternalServerError(c, "Failed to query min ID: "+err.Error())
			return
		}

		if minID == 0 {
			common.Success(c, map[string]any{"deleted_count": 0})
			return
		}

		// 先删除关联的 ChatIO
		if err := models.DB.Unscoped().Where("log_id IN (SELECT id FROM chat_logs WHERE id < ?)", minID).Delete(&models.ChatIO{}).Error; err != nil {
			common.InternalServerError(c, "Failed to delete chat IO: "+err.Error())
			return
		}

		// 删除日志
		result := models.DB.Unscoped().Where("id < ?", minID).Delete(&models.ChatLog{})
		if result.Error != nil {
			common.InternalServerError(c, "Failed to delete logs: "+result.Error.Error())
			return
		}
		deletedCount = result.RowsAffected

	case "days":
		// 计算 N 天前的时间
		cutoffTime := time.Now().AddDate(0, 0, -req.Value)

		// 先删除关联的 ChatIO
		if err := models.DB.Unscoped().Where("log_id IN (SELECT id FROM chat_logs WHERE created_at < ?)", cutoffTime).Delete(&models.ChatIO{}).Error; err != nil {
			common.InternalServerError(c, "Failed to delete chat IO: "+err.Error())
			return
		}

		// 删除日志
		result := models.DB.Unscoped().Where("created_at < ?", cutoffTime).Delete(&models.ChatLog{})
		if result.Error != nil {
			common.InternalServerError(c, "Failed to delete logs: "+result.Error.Error())
			return
		}
		deletedCount = result.RowsAffected

	default:
		common.BadRequest(c, "Invalid type: must be 'count' or 'days'")
		return
	}

	common.Success(c, map[string]interface{}{"deleted_count": deletedCount})
}
