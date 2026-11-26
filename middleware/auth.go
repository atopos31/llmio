package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

// 用于系统数据操作相关鉴权
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

// bearerKey 从 Authorization 头里取出 Bearer 凭据；不是 Bearer 形态（或为空）返回空串。
//
// 抽出来是因为两个入口共用同一套解析：OpenAI 侧本来就只有这一种形态，
// Anthropic 侧除此之外还收 x-api-key（见 AuthAnthropic）。
func bearerKey(header string) string {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && parts[0] == "Bearer" {
		return parts[1]
	}
	return ""
}

// 用于OpenAI接口鉴权
func AuthOpenAI(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		checkAuthKey(c, bearerKey(c.GetHeader("Authorization")), adminToken)
	}
}

// 用于Anthropic接口鉴权
//
// 两种携带方式都收，因为客户端有两种约定：
//   - x-api-key：Anthropic 原生。Claude Code 读的是 ANTHROPIC_API_KEY；
//   - Authorization: Bearer：Claude Code 读 ANTHROPIC_AUTH_TOKEN 时发的就是它（issue #38）。
//
// 两者都在时 **x-api-key 优先**。优先级写死才可预期：否则"哪个头生效"会随客户端
// 的实现细节漂移，而 401 现场看不出是哪一半出的问题。
func AuthAnthropic(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authKey := c.GetHeader("x-api-key")
		if authKey == "" {
			authKey = bearerKey(c.GetHeader("Authorization"))
		}
		checkAuthKey(c, authKey, adminToken)
	}
}

// 用于Gemini原生接口鉴权
func AuthGemini(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("x-goog-api-key")
		checkAuthKey(c, key, adminToken)
	}
}

func checkAuthKey(c *gin.Context, key string, adminToken string) {
	ctx := c.Request.Context()
	// 如果系统中未配置Token 或者使用的是最高权限的token 则允许访问所有模型
	if adminToken == "" || key == adminToken {
		ctx = context.WithValue(ctx, consts.ContextKeyAllowAllModel, true)
		c.Request = c.Request.WithContext(ctx)
		return
	}
	// 如果key为空 则拒绝访问
	if key == "" {
		common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Authorization key is missing")
		c.Abort()
		return
	}
	authKey, err := service.GetAuthKey(ctx, key)
	if err != nil {
		// 库错误不是键错误：一律回 401 会让客户端丢掉可用的键
		//（线上：读事务挡写 → SELECT auth_keys 撞 SQLITE_BUSY → 401）。
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// 等待者自己的 ctx 断了或 lookup 预算用完：重试可解，不是键的问题
			c.Header("Retry-After", "1")
			common.ErrorWithHttpStatus(c, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "auth key lookup canceled, retry")
		case errors.Is(err, gorm.ErrRecordNotFound):
			// 键不存在/已停用——确实是无效令牌
			common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Invalid token")
		case service.IsBusyErr(err):
			// 撞锁：让客户端稍后重试，而不是丢掉钥匙
			c.Header("Retry-After", "1")
			common.ErrorWithHttpStatus(c, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "auth key lookup failed: database is busy, retry")
		default:
			slog.Error("auth key lookup failed", "error", err)
			common.ErrorWithHttpStatus(c, http.StatusInternalServerError, http.StatusInternalServerError, "auth key lookup failed")
		}
		c.Abort()
		return
	}
	// 检查是否过期
	if authKey.ExpiresAt != nil && authKey.ExpiresAt.Before(time.Now()) {
		common.ErrorWithHttpStatus(c, http.StatusUnauthorized, http.StatusUnauthorized, "Token has expired")
		c.Abort()
		return
	}
	// 异步更新使用次数
	go service.KeyUpdate(authKey.ID, time.Now())

	ctx = context.WithValue(ctx, consts.ContextKeyAuthKeyID, authKey.ID)
	ctx = context.WithValue(ctx, consts.ContextKeyAuthKeyIOLog, lo.FromPtrOr(authKey.IOLog, false))

	allowAll := lo.FromPtrOr(authKey.AllowAll, false)
	ctx = context.WithValue(ctx, consts.ContextKeyAllowAllModel, allowAll)
	// 如果不允许所有模型 则设置允许的模型列表
	if !allowAll {
		ctx = context.WithValue(ctx, consts.ContextKeyAllowModels, authKey.Models)
	}

	c.Request = c.Request.WithContext(ctx)
}
