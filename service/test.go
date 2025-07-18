package service

import "github.com/gin-gonic/gin"

func ProviderTest(c *gin.Context) {
	id := c.Param("id")
	_ = id
}
