package handler

import "github.com/gin-gonic/gin"

func ProviderTestHandler(c *gin.Context) {
	id := c.Param("id")
	_ = id

}
