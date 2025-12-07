package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

func Auth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 不设置token，则不进行验证
		if token == "" {
			return
		}
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Authorization header is missing")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Invalid authorization header")
			c.Abort()
			return
		}

		tokenString := parts[1]
		if tokenString != token {
			common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Invalid token")
			c.Abort()
			return
		}
	}
}

func AuthOpenAI(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if adminToken == "" {
			return
		}
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Authorization header is missing")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Invalid authorization header")
			c.Abort()
			return
		}

		tokenString := parts[1]
		checkAuthKey(c, tokenString, adminToken)
	}
}

func AuthAnthropic(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if adminToken == "" {
			return
		}
		authHeader := c.GetHeader("x-api-key")
		if authHeader == "" {
			common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "x-api-key header is missing")
			c.Abort()
			return
		}
		checkAuthKey(c, authHeader, adminToken)
	}
}

func checkAuthKey(c *gin.Context, key string, adminToken string) {
	ctx := c.Request.Context()
	// 如果使用的是最高权限的token 则允许访问所有模型
	if key == adminToken {
		ctx = context.WithValue(ctx, consts.ContextKeyAllowAllModel, true)
		c.Request = c.Request.WithContext(ctx)
		return
	}
	authKey, err := service.GetAuthKey(ctx, key)
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Invalid token")
		c.Abort()
		return
	}
	ctx = context.WithValue(ctx, consts.ContextKeyAllowAllModel, authKey.AllowAll)
	// 如果不允许所有模型 则设置允许的模型列表
	if !authKey.AllowAll {
		ctx = context.WithValue(ctx, consts.ContextKeyAllowModels, authKey.Models)
	}

	c.Request = c.Request.WithContext(ctx)
}
