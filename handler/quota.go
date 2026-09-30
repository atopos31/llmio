package handler

import (
	"strings"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/quota"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

// 配额端点。
//
// 写保护（计划 §3.3）：原设计是"仅回环 + 显式开启"，理由是"写权限 = 可在主机上
// 执行任意命令"。合并后 goja 沙箱替换了 OS 进程 spawn，且整个 /api 已在 TOKEN 之后，
// 那条限制的存在前提消失，因此降级为一个开关：LLMIO_QUOTA_ALLOW_WRITE=false 即只读。
// 只读模式下写端点统一返回 403，前端据此隐藏编辑入口。

// quotaStore 是进程内共享的配额 store（持有配置路径与缓存）。
//
// 做成包级单例：缓存必须跨请求存活，否则每次请求都重新打一遍上游。
var quotaStore = service.NewQuotaStore("")

// requireQuotaWrite 检查写权限，无权限时已写好响应并返回 false。
func requireQuotaWrite(c *gin.Context) bool {
	if quota.WriteAllowed() {
		return true
	}
	common.Forbidden(c, "配额写操作已被禁用（LLMIO_QUOTA_ALLOW_WRITE=false）")
	return false
}

// GetQuotaConfig 返回脱敏后的配额配置。
//
// **响应永不回传明文**：apiKey 打码；env 里键名像密钥的打码；
// scriptSource 例外——编辑器必须显示它才能编辑，且它本身不应含凭据。
func GetQuotaConfig(c *gin.Context) {
	cfg, err := quotaStore.SafeConfig()
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, gin.H{
		"config":         cfg,
		"builtins":       quota.ListBuiltins(),
		"configPath":     quotaStore.Path(),
		"writeEnabled":   quota.WriteAllowed(),
		"defaultRefresh": quota.DefaultRefreshInterval,
		"defaultWarning": quota.DefaultWarningAt,
	})
}

// UpdateQuotaConfig 改配置级字段（缓存时长、告警阈值）。
//
// 与 UpsertQuotaSource 分开的理由：那两个字段不属于任何数据源，
// 混在源的保存里会让"我只想改个阈值"变成一次数据源写操作。
func UpdateQuotaConfig(c *gin.Context) {
	if !requireQuotaWrite(c) {
		return
	}
	var body struct {
		RefreshInterval int     `json:"refreshInterval"`
		WarningAt       float64 `json:"warningAt"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		common.BadRequest(c, "请求体应形如 {\"refreshInterval\": 120, \"warningAt\": 80}")
		return
	}
	cfg, err := quotaStore.UpdateConfig(body.RefreshInterval, body.WarningAt)
	if err != nil {
		if isQuotaInputError(err) {
			common.BadRequest(c, err.Error())
			return
		}
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, cfg)
}

// UpsertQuotaSource 新增或更新一个数据源。
//
// 路由上区分新增与更新：新增用 POST（不带 id 或 id 冲突时报错），
// 更新用 PUT（按 id 找到已有项）。这样"我想改的源不存在"不会被静默变成新增。
func UpsertQuotaSource(c *gin.Context) {
	if !requireQuotaWrite(c) {
		return
	}
	var src quota.Source
	if err := c.ShouldBindJSON(&src); err != nil {
		common.BadRequest(c, "请求体不是合法的数据源配置: "+err.Error())
		return
	}

	isNew := c.Request.Method == "POST"
	if isNew && strings.TrimSpace(src.ID) == "" {
		// 新增时可以不给 id，由 store 生成
		src.ID = ""
	}
	saved, err := quotaStore.UpsertSource(src, isNew)
	if err != nil {
		// 校验类错误（缺 url、未知适配器、id 冲突）是用户输入问题 -> 400；
		// 其余（写盘失败）-> 500。
		if isQuotaInputError(err) {
			common.BadRequest(c, err.Error())
			return
		}
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, saved)
}

// DeleteQuotaSource 删除一个数据源。
func DeleteQuotaSource(c *gin.Context) {
	if !requireQuotaWrite(c) {
		return
	}
	id := c.Param("id")
	ok, err := quotaStore.RemoveSource(id)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if !ok {
		common.NotFound(c, "未找到数据源: "+id)
		return
	}
	common.Success(c, gin.H{"id": id})
}

// RunQuotaSources 跑全部启用源（或指定 id）。
//
// Query:
//
//	force=true        忽略缓存重新取数
//	ids=a,b,c         只跑这些源（其余不返回）
func RunQuotaSources(c *gin.Context) {
	force := strings.EqualFold(c.Query("force"), "true")
	ids := splitIDs(c.Query("ids"))

	res, err := quotaStore.RunAll(c.Request.Context(), force, ids)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, res)
}

// RefreshQuotaSource 只刷一个数据源，其余源走缓存。
//
// 这是"每源独立刷新"的端点（计划 §1.4 第 20 条）：面板上点某个慢源的刷新时用它，
// 不必连带重跑其他慢源。
func RefreshQuotaSource(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		common.BadRequest(c, "缺少数据源 id")
		return
	}
	res, err := quotaStore.RefreshSource(c.Request.Context(), id)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, res)
}

// TestQuotaSource 试跑一个**未保存**的数据源。
//
// 不落盘、不写缓存。这是配额功能里最有价值的作者循环：用户反复调整脚本/映射并
// 立刻看到解析结果与原始产出。因此它对只读模式**也开放**——它不改变任何状态。
func TestQuotaSource(c *gin.Context) {
	var src quota.Source
	if err := c.ShouldBindJSON(&src); err != nil {
		common.BadRequest(c, "请求体不是合法的数据源配置: "+err.Error())
		return
	}
	common.Success(c, quotaStore.TestSource(c.Request.Context(), src))
}

// DiscoverQuotaSources 列出上游 llmio 供应商并给出导入建议。
func DiscoverQuotaSources(c *gin.Context) {
	providers, err := service.GetProvidersForQuota(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	got, err := quotaStore.Discover(providers)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, got)
}

// ImportQuotaSource 从上游供应商导入一个数据源。
//
// 只接受 upstreamId，**不接受任何密钥字段**：密钥由服务端直接从上游配置取，
// 不经过浏览器（计划 §3.3）。这条在同源之后依然要显式保持。
func ImportQuotaSource(c *gin.Context) {
	if !requireQuotaWrite(c) {
		return
	}
	var body struct {
		UpstreamID uint `json:"upstreamId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		common.BadRequest(c, "请求体应形如 {\"upstreamId\": 1}")
		return
	}
	if body.UpstreamID == 0 {
		common.BadRequest(c, "缺少 upstreamId")
		return
	}

	p, err := service.FindProviderForQuota(c.Request.Context(), body.UpstreamID)
	if err != nil {
		common.NotFound(c, "未找到上游供应商")
		return
	}
	src, err := quotaStore.ImportFromUpstream(p)
	if err != nil {
		if isQuotaInputError(err) {
			common.BadRequest(c, err.Error())
			return
		}
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, src)
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// splitIDs 解析逗号分隔的 id 列表，去掉空白与空项。
func splitIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isQuotaInputError 判断一个错误是否属于"用户输入问题"（对应 400）。
//
// 用错误文本分类而不是引入哨兵错误：这些错误全部来自
// quota.ValidateSource / ValidateConfig，措辞是既定的中文提示，
// 且它们最终就是要原样展示给用户的。
func isQuotaInputError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, kw := range []string{
		"需要填写", "未知内置适配器", "未知数据源类型", "需要账号会话",
		"已存在", "已导入", "不能为负", "应在", "重复", "无法从上游配置推导",
	} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
