package handler

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type MetricsRes struct {
	Reqs   int64 `json:"reqs"`
	Tokens int64 `json:"tokens"`
}

func Metrics(c *gin.Context) {
	days, err := strconv.Atoi(c.Param("days"))
	if err != nil {
		common.BadRequest(c, "Invalid days parameter")
		return
	}

	now := time.Now()
	year, month, day := now.Date()
	chain := gorm.G[models.ChatLog](models.DB).Where("created_at >= ?", time.Date(year, month, day, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -days))

	reqs, err := chain.Count(c.Request.Context(), "id")
	if err != nil {
		common.InternalServerError(c, "Failed to count requests: "+err.Error())
		return
	}
	var tokens sql.NullInt64
	if err := chain.Select("sum(total_tokens) as tokens").Scan(c.Request.Context(), &tokens); err != nil {
		common.InternalServerError(c, "Failed to sum tokens: "+err.Error())
		return
	}
	common.Success(c, MetricsRes{
		Reqs:   reqs,
		Tokens: tokens.Int64,
	})
}

type Count struct {
	Model string `json:"model"`
	Calls int64  `json:"calls"`
}

func Counts(c *gin.Context) {
	results := make([]Count, 0)
	if err := models.DB.Raw("SELECT name as model,COUNT(*) as calls FROM `chat_logs` WHERE `chat_logs`.`deleted_at` IS NULL  GROUP BY `name` ORDER BY `calls` DESC").Scan(&results).Error; err != nil {
		common.InternalServerError(c, err.Error())
	}
	const topN = 5
	if len(results) > topN {
		var othersCalls int64
		for _, item := range results[topN:] {
			othersCalls += item.Calls
		}
		othersCount := Count{
			Model: "others",
			Calls: othersCalls,
		}
		results = append(results[:topN], othersCount)
	}

	common.Success(c, results)
}
