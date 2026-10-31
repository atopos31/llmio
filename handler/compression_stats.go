package handler

import (
	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

// GetTransportCompressionStats 返回本次进程启动以来的传输压缩累计效果。
// 只读内存计数，不触碰开关或转发路径。
func GetTransportCompressionStats(c *gin.Context) {
	common.Success(c, service.GetCompressionStats())
}
