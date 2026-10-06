package handler

import (
	"errors"
	"strconv"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service/modelmeta"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// modelMetaManager 是取建议的那个管理器。
//
// 做成包级变量而不是直接调 modelmeta.Default()，是为了给 handler 测试留一个
// 接缝：测试里换成 NewManager(假 Source) 并 SetCatalog 装一份索引，整条
// handler 路径就不碰网络了。生产路径用的是**同一个方法**——
// 换的只是背后那份索引从哪来。
var modelMetaManager = modelmeta.Default

// GetModelProviderMetadata 查询某条「模型 × 上游」关联的模型能力与价格建议。
//
// 这是一个**只读**端点（§4.2）：它返回建议，由前端预填进关联弹窗的表单；
// 保存路径完全沿用既有的 Create/UpdateModelProvider，服务端不做任何补值。
// 因此这里不读也不改任何业务表。
//
// 上游的 base_url 从 provider 的 Config 里现取，而不是让前端传：前端的
// 关联表单里只有 provider_id，让它再传一份 base_url 等于把"两边必须一致"
// 变成一条要人维护的约定。
func GetModelProviderMetadata(c *gin.Context) {
	providerIDStr := c.Query("provider_id")
	providerModel := c.Query("provider_model")
	if providerIDStr == "" || providerModel == "" {
		common.BadRequest(c, "provider_id and provider_model query parameters are required")
		return
	}

	providerID, err := strconv.ParseUint(providerIDStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid provider_id format")
		return
	}

	ctx := c.Request.Context()
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", providerID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Provider not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve provider: "+err.Error())
		return
	}

	suggestion := modelMetaManager().Suggest(ctx, modelmeta.Query{
		ProviderType:   provider.Type,
		ProviderConfig: provider.Config,
		ProviderName:   provider.Name,
		ProviderModel:  providerModel,
	}, modelmeta.LoadPolicy(ctx))

	common.Success(c, suggestion)
}
