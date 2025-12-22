package handler

import (
	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/gin-gonic/gin"
)

func GetVersion(c *gin.Context) {
	common.Success(c, consts.Version)
}
