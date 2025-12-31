package handler

import (
	"net/http"

	"github.com/atopos31/llmio/common"
	"github.com/gin-gonic/gin"
)

type EventLoggingRequest struct {
	Events []EventLoggingEvent `json:"events"`
}

type EventLoggingEvent struct {
	EventType string                `json:"event_type"`
	EventData EventLoggingEventData `json:"event_data"`
}

type EventLoggingEventData struct {
	EventName          string          `json:"event_name"`
	ClientTimestamp    string          `json:"client_timestamp"`
	Model              string          `json:"model"`
	SessionID          string          `json:"session_id"`
	UserType           string          `json:"user_type"`
	Betas              string          `json:"betas"`
	Env                EventLoggingEnv `json:"env"`
	Entrypoint         string          `json:"entrypoint"`
	IsInteractive      bool            `json:"is_interactive"`
	ClientType         string          `json:"client_type"`
	AdditionalMetadata string          `json:"additional_metadata"`
	DeviceID           string          `json:"device_id"`
}

type EventLoggingEnv struct {
	Platform              string `json:"platform"`
	NodeVersion           string `json:"node_version"`
	Terminal              string `json:"terminal"`
	PackageManagers       string `json:"package_managers"`
	Runtimes              string `json:"runtimes"`
	IsRunningWithBun      bool   `json:"is_running_with_bun"`
	IsCI                  bool   `json:"is_ci"`
	IsClaubbit            bool   `json:"is_claubbit"`
	IsGithubAction        bool   `json:"is_github_action"`
	IsClaudeCodeAction    bool   `json:"is_claude_code_action"`
	IsClaudeAIAuth        bool   `json:"is_claude_ai_auth"`
	Version               string `json:"version"`
	Arch                  string `json:"arch"`
	IsClaudeCodeRemote    bool   `json:"is_claude_code_remote"`
	DeploymentEnvironment string `json:"deployment_environment"`
}

func EventLogging(c *gin.Context) {
	var req EventLoggingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"accepted_count": len(req.Events),
		"rejected_count": 0,
	})
}
