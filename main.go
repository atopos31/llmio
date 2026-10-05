package main

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/atopos31/llmio/handler"
	"github.com/atopos31/llmio/middleware"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/env"
	"github.com/atopos31/llmio/quota"
	"github.com/atopos31/llmio/service"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	_ "golang.org/x/crypto/x509roots/fallback"
)

func init() {
	// 沙箱子进程模式：主程序被 re-exec 成脚本沙箱时走这里。
	//
	// **必须放在 init() 的最前面**：init 会在 main() 之前跑，
	// 而沙箱子进程不该初始化数据库、不该挂日志清理调度、更不该监听端口。
	// 判据是隐藏子命令参数（quota.SandboxCommand），由 quota.RunScript 拼上。
	for _, a := range os.Args[1:] {
		if a == quota.SandboxCommand {
			os.Exit(quota.SandboxMain())
		}
	}

	ctx := context.Background()
	models.Init(ctx, demoDBPath())
	if demoSeedEnabled() {
		demoSeed(ctx)
	}
	slog.Info("TZ", "time.Local", time.Local.String())
}

func main() {
	// 存储层维护（启动期 VACUUM / auto_vacuum 转换）**放在监听端口之前**：
	// 这两件事都持排他写锁、要 2 倍库大小的空闲磁盘，放在监听之后就意味着
	// 在途请求会撞 busy_timeout 吃到 500；放在之前则请求永远看不到它。
	// 默认两个开关都是关的，所以常态下这一句只是读一次 pragma。
	service.PrepareStorage(context.Background())

	service.StartLogCleanupScheduler(context.Background())
	// 历史行的形态迁移。默认策略 Enabled=false，所以它平时只是每 30 秒
	// 空看一眼（水位≈maxID 时那两条查询扫到 0 行就返回）。
	// 它挂在这里而不是 init()：沙箱子进程不该起后台任务，
	// 而且它要读写 DB，得等 models.Init 落定（init() 里已经做了）。
	service.StartLogCompressScheduler(context.Background())
	// 空间回收的定时推进。默认策略 Enabled=false，此时它每个周期只读一次
	// freelist 计数（一次 pragma，不带锁）。与上面两个一样挂在这里而不是 init()。
	service.StartLogReclaimScheduler(context.Background())

	router := gin.Default()
	// gzip压缩
	router.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/openai", "/anthropic", "/gemini", "/v1"})))
	// 跨域
	router.Use(middleware.Cors())
	// webui
	setwebui(router)

	token := env.GetWithDefault("TOKEN", "")

	authOpenAI := middleware.AuthOpenAI(token)
	authAnthropic := middleware.AuthAnthropic(token)
	authGemini := middleware.AuthGemini(token)

	// openai
	openai := router.Group("/openai")
	{
		v1 := openai.Group("/v1", authOpenAI)
		{
			v1.GET("/models", handler.OpenAIModelsHandler)
			v1.POST("/chat/completions", handler.ChatCompletionsHandler)
			v1.POST("/responses", handler.ResponsesHandler)
		}
	}

	// anthropic
	anthropic := router.Group("/anthropic")
	{
		// claude code logging
		anthropic.POST("/api/event_logging/batch", handler.EventLogging)

		v1 := anthropic.Group("/v1", authAnthropic)
		{
			v1.GET("/models", handler.AnthropicModelsHandler)
			v1.POST("/messages", handler.Messages)
			v1.POST("/messages/count_tokens", handler.CountTokens)
		}
	}

	// gemini
	gemini := router.Group("/gemini")
	{
		v1beta := gemini.Group("/v1beta", authGemini)
		{
			v1beta.GET("/models", handler.GeminiModelsHandler)
			v1beta.POST("/models/*modelAction", handler.GeminiGenerateContentHandler)
		}
	}

	// 兼容性保留
	v1 := router.Group("/v1")
	{
		v1.GET("/models", authOpenAI, handler.OpenAIModelsHandler)
		v1.POST("/chat/completions", authOpenAI, handler.ChatCompletionsHandler)
		v1.POST("/responses", authOpenAI, handler.ResponsesHandler)
		v1.POST("/messages", authAnthropic, handler.Messages)
		v1.POST("/messages/count_tokens", authAnthropic, handler.CountTokens)
	}

	api := router.Group("/api", middleware.Auth(token))
	{
		// Analytics aggregation. 单端点覆盖时间序列、多维下钻、延迟分布与错误分析，
		// 保证一次请求对应一个时间切片（见 handler/stats.go 的说明）。
		api.GET("/metrics/stats", handler.Stats)
		api.GET("/metrics/granularities", handler.StatsGranularities)
		// Provider management
		api.GET("/providers/template", handler.GetProviderTemplates)
		api.GET("/providers", handler.GetProviders)
		api.GET("/providers/models/:id", handler.GetProviderModels)
		api.POST("/providers", handler.CreateProvider)
		api.PUT("/providers/:id", handler.UpdateProvider)
		api.DELETE("/providers/:id", handler.DeleteProvider)

		// Model management
		api.GET("/models", handler.GetModels)
		api.GET("/models/select", handler.GetModelList)
		api.POST("/models", handler.CreateModel)
		api.PATCH("/models/order", handler.UpdateModelOrder)
		api.PUT("/models/:id", handler.UpdateModel)
		api.DELETE("/models/:id", handler.DeleteModel)

		// Model-provider association management
		api.GET("/model-providers", handler.GetModelProviders)
		api.GET("/model-providers/status", handler.GetModelProviderStatus)
		api.POST("/model-providers", handler.CreateModelProvider)
		api.PUT("/model-providers/:id", handler.UpdateModelProvider)
		api.PATCH("/model-providers/:id/status", handler.UpdateModelProviderStatus)
		api.DELETE("/model-providers/:id", handler.DeleteModelProvider)

		// System status and monitoring
		api.GET("/version", handler.GetVersion)
		api.GET("/logs", handler.GetRequestLogs)
		api.GET("/logs/:id/chat-io", handler.GetChatIO)
		api.GET("/user-agents", handler.GetUserAgents)
		api.POST("/logs/cleanup", handler.CleanLogs)
		api.GET("/logs/cleanup/history", handler.GetCleanupHistory)

		// 历史行的形态迁移（数据库压缩）。
		//
		// 与 /logs/cleanup 不同，跑一轮是**分钟级**的，所以 run / rollback
		// 都是"起一个后台任务就返回"，进度看状态接口。回滚单独一个端点、
		// 还要求字面量确认：它会把整库**变大**（帧 → 明文），不该被误触。
		api.GET("/logs/compression", handler.GetCompressionStatus)
		api.PUT("/logs/compression/policy", handler.UpdateCompressionPolicy)
		api.POST("/logs/compression/run", handler.RunCompression)
		api.POST("/logs/compression/pause", handler.PauseCompression)
		api.GET("/logs/compression/decompress", handler.GetDecompressStatus)
		api.POST("/logs/compression/decompress", handler.RollbackCompression)
		// 空间回收：把 freelist 里的页还给文件系统。不是迁移的一部分
		// （它不动数据），但和迁移是同一件事的两半——见 service/storage.go。
		//
		// 回收有两档：跑一轮（默认）与持续跑到放完（body 里 continuous=true）。
		// 持续那档是"点一次就把活干完"，stop 是它的刹车——批间生效，
		// 所以停止之后可能还有一批在跑完的路上。
		api.POST("/logs/compression/reclaim", handler.ReclaimStorage)
		api.POST("/logs/compression/reclaim/stop", handler.StopReclaim)
		api.PUT("/logs/compression/reclaim/policy", handler.UpdateReclaimPolicy)
		// 重整（VACUUM）：与增量回收平级的另一条路，大空洞用它。
		api.POST("/logs/compression/vacuum", handler.VacuumStorage)

		// Auth key management
		api.GET("/auth-keys", handler.GetAuthKeys)
		api.GET("/auth-keys/list", handler.GetAuthKeysList)
		api.POST("/auth-keys", handler.CreateAuthKey)
		api.PUT("/auth-keys/:id", handler.UpdateAuthKey)
		api.PATCH("/auth-keys/:id/status", handler.ToggleAuthKeyStatus)
		api.DELETE("/auth-keys/:id", handler.DeleteAuthKey)

		// Config management
		api.GET("/config/:key", handler.GetConfigByKey)
		api.PUT("/config/:key", handler.UpdateConfigByKey)

		// Peak (time-of-day / workday) pricing.
		// 独立成一组而非并入通用 config 端点：需要结构化校验、默认值补齐，
		// 以及节假日同步这个带外网调用的动作。
		// 峰谷条款本身挂在「模型 × 上游」关联上（随 model-providers 的增改走），
		// 这里只有全局的工作日日历与"保存前预览条款"这两件事。
		api.GET("/peak-calendar", handler.GetPeakCalendar)
		api.PUT("/peak-calendar", handler.UpdatePeakCalendar)
		api.POST("/peak-calendar/preview", handler.PreviewPeakTerms)
		api.POST("/peak-calendar/holidays/sync", handler.SyncPeakHolidays)

		// 配额与余量。
		// 读端点与外层一致（TOKEN 之后）；写端点另受 LLMIO_QUOTA_ALLOW_WRITE 控制，
		// 关掉即只读（计划 §3.3）。
		api.GET("/quota/config", handler.GetQuotaConfig)
		api.PUT("/quota/config", handler.UpdateQuotaConfig)
		api.POST("/quota/sources", handler.UpsertQuotaSource)
		api.PUT("/quota/sources", handler.UpsertQuotaSource)
		api.DELETE("/quota/sources/:id", handler.DeleteQuotaSource)
		api.POST("/quota/run", handler.RunQuotaSources)
		api.POST("/quota/sources/:id/refresh", handler.RefreshQuotaSource)
		// 试跑不改变任何状态，因此只读模式下也开放
		api.POST("/quota/test", handler.TestQuotaSource)

		// Provider connectivity test
		api.GET("/test/:id", handler.ProviderTestHandler)
		api.GET("/test/react/:id", handler.TestReactHandler)
		api.GET("/test/count_tokens", handler.TestCountTokens)
	}

	router.Run(":" + listenerPort())
}

//go:embed webui/dist
var distFiles embed.FS

//go:embed webui/dist/index.html
var indexHTML []byte

func setwebui(r *gin.Engine) {
	subFS, err := fs.Sub(distFiles, "webui/dist/assets")
	if err != nil {
		panic(err)
	}

	r.StaticFS("/assets", http.FS(subFS))

	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet && !strings.HasPrefix(c.Request.URL.Path, "/api/") && !strings.HasPrefix(c.Request.URL.Path, "/v1/") {
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
			return
		}
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte("404 Not Found"))
	})
}
