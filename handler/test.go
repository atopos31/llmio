package handler

import (
	"strconv"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ProviderTestHandler(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	// Get ModelWithProvider by ID
	modelWithProvider, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "ModelWithProvider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	// Get the Provider
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", modelWithProvider.ProviderID).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Provider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	// Create the provider instance
	providerInstance, err := providers.New(provider.Type, modelWithProvider.ProviderName, provider.Config)
	if err != nil {
		common.BadRequest(c, "Failed to create provider: "+err.Error())
		return
	}

	// Test connectivity by fetching models
	_, err = providerInstance.Models(c.Request.Context())
	if err != nil {
		common.ErrorWithHttpStatus(c, 502, 502, "Failed to connect to provider: "+err.Error())
		return
	}

	common.SuccessWithMessage(c, "Successfully connected to provider", nil)
}
