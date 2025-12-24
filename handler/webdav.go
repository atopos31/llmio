package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// WebDAVConfig WebDAV 配置结构
type WebDAVConfig struct {
	URL                string `json:"url"`
	Username           string `json:"username"`
	Password           string `json:"password"`
	EncryptionEnabled  bool   `json:"encryption_enabled"`
	EncryptionPassword string `json:"encryption_password"`
}

// WebDAVAutoSyncConfig WebDAV 自动同步配置
type WebDAVAutoSyncConfig struct {
	Enabled      bool   `json:"enabled"`
	SyncInterval int    `json:"sync_interval"` // 同步间隔（秒）
	SyncStrategy string `json:"sync_strategy"` // merge, upload_only, download_only
}

// WebDAVTestRequest 测试连接请求
type WebDAVTestRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// WebDAVUploadRequest 上传备份请求
type WebDAVUploadRequest struct {
	URL                string     `json:"url"`
	Username           string     `json:"username"`
	Password           string     `json:"password"`
	Data               BackupData `json:"data"`
	EncryptionEnabled  bool       `json:"encryption_enabled"`
	EncryptedContent   string     `json:"encrypted_content,omitempty"`
}

// WebDAVDownloadRequest 下载备份请求
type WebDAVDownloadRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// WebDAVDownloadResponse 下载备份响应
type WebDAVDownloadResponse struct {
	Data        *BackupData `json:"data,omitempty"`
	IsEncrypted bool        `json:"is_encrypted"`
	RawContent  string      `json:"raw_content,omitempty"`
}

// WebDAVSyncNowRequest 立即同步请求
type WebDAVSyncNowRequest struct {
	URL                string `json:"url"`
	Username           string `json:"username"`
	Password           string `json:"password"`
	SyncStrategy       string `json:"sync_strategy"`
	EncryptionEnabled  bool   `json:"encryption_enabled"`
	EncryptionPassword string `json:"encryption_password"`
}

const (
	WebDAVConfigKey         = "webdav_config"
	WebDAVAutoSyncConfigKey = "webdav_auto_sync_config"
	WebDAVBackupFileName    = "llmio_backup.json"
)

// GetWebDAVConfig 获取 WebDAV 配置
func GetWebDAVConfig(c *gin.Context) {
	ctx := c.Request.Context()

	config, err := gorm.G[models.Config](models.DB).Where("key = ?", WebDAVConfigKey).First(ctx)
	if err != nil {
		// 配置不存在，返回空配置
		common.Success(c, WebDAVConfig{})
		return
	}

	var webdavConfig WebDAVConfig
	if err := json.Unmarshal([]byte(config.Value), &webdavConfig); err != nil {
		common.InternalServerError(c, "Failed to parse WebDAV config: "+err.Error())
		return
	}

	common.Success(c, webdavConfig)
}

// UpdateWebDAVConfig 更新 WebDAV 配置
func UpdateWebDAVConfig(c *gin.Context) {
	var req WebDAVConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	configValue, err := json.Marshal(req)
	if err != nil {
		common.InternalServerError(c, "Failed to marshal config: "+err.Error())
		return
	}

	var existing models.Config
	if err := models.DB.Where("key = ?", WebDAVConfigKey).First(&existing).Error; err == nil {
		// 更新现有配置
		if err := models.DB.Model(&existing).Update("value", string(configValue)).Error; err != nil {
			common.InternalServerError(c, "Failed to update config: "+err.Error())
			return
		}
	} else {
		// 创建新配置
		newConfig := models.Config{
			Key:   WebDAVConfigKey,
			Value: string(configValue),
		}
		if err := models.DB.Create(&newConfig).Error; err != nil {
			common.InternalServerError(c, "Failed to create config: "+err.Error())
			return
		}
	}

	common.Success(c, req)
}

// GetWebDAVAutoSyncConfig 获取 WebDAV 自动同步配置
func GetWebDAVAutoSyncConfig(c *gin.Context) {
	ctx := c.Request.Context()

	config, err := gorm.G[models.Config](models.DB).Where("key = ?", WebDAVAutoSyncConfigKey).First(ctx)
	if err != nil {
		// 配置不存在，返回默认配置
		common.Success(c, WebDAVAutoSyncConfig{
			Enabled:      false,
			SyncInterval: 3600,
			SyncStrategy: "merge",
		})
		return
	}

	var autoSyncConfig WebDAVAutoSyncConfig
	if err := json.Unmarshal([]byte(config.Value), &autoSyncConfig); err != nil {
		common.InternalServerError(c, "Failed to parse auto sync config: "+err.Error())
		return
	}

	common.Success(c, autoSyncConfig)
}

// UpdateWebDAVAutoSyncConfig 更新 WebDAV 自动同步配置
func UpdateWebDAVAutoSyncConfig(c *gin.Context) {
	var req WebDAVAutoSyncConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	configValue, err := json.Marshal(req)
	if err != nil {
		common.InternalServerError(c, "Failed to marshal config: "+err.Error())
		return
	}

	var existing models.Config
	if err := models.DB.Where("key = ?", WebDAVAutoSyncConfigKey).First(&existing).Error; err == nil {
		if err := models.DB.Model(&existing).Update("value", string(configValue)).Error; err != nil {
			common.InternalServerError(c, "Failed to update config: "+err.Error())
			return
		}
	} else {
		newConfig := models.Config{
			Key:   WebDAVAutoSyncConfigKey,
			Value: string(configValue),
		}
		if err := models.DB.Create(&newConfig).Error; err != nil {
			common.InternalServerError(c, "Failed to create config: "+err.Error())
			return
		}
	}

	common.Success(c, req)
}

// TestWebDAVConnection 测试 WebDAV 连接
func TestWebDAVConnection(c *gin.Context) {
	var req WebDAVTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.URL == "" {
		common.BadRequest(c, "WebDAV URL is required")
		return
	}

	// 确保 URL 以 / 结尾
	url := strings.TrimSuffix(req.URL, "/") + "/"

	// 使用 PROPFIND 方法测试连接
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "PROPFIND", url, nil)
	if err != nil {
		common.InternalServerError(c, "Failed to create request: "+err.Error())
		return
	}

	httpReq.SetBasicAuth(req.Username, req.Password)
	httpReq.Header.Set("Depth", "0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		common.InternalServerError(c, "Connection failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		common.Unauthorized(c, "Authentication failed: invalid username or password")
		return
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultiStatus {
		common.InternalServerError(c, fmt.Sprintf("WebDAV server returned status: %d", resp.StatusCode))
		return
	}

	common.Success(c, map[string]interface{}{
		"message": "Connection successful",
		"status":  resp.StatusCode,
	})
}

// ensureWebDAVDirectory 确保 WebDAV 目录存在
func ensureWebDAVDirectory(ctx context.Context, dirURL, username, password string) error {
	// 使用 MKCOL 方法创建目录
	httpReq, err := http.NewRequestWithContext(ctx, "MKCOL", dirURL, nil)
	if err != nil {
		return err
	}

	httpReq.SetBasicAuth(username, password)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 201 Created - 目录创建成功
	// 405 Method Not Allowed - 目录已存在（某些服务器）
	// 301/302 - 重定向（目录已存在）
	// 2xx - 成功
	if resp.StatusCode == http.StatusCreated ||
		resp.StatusCode == http.StatusMethodNotAllowed ||
		(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}

	// 如果 URL 没有以 / 结尾，尝试添加 / 再试一次
	if !strings.HasSuffix(dirURL, "/") {
		httpReq2, err := http.NewRequestWithContext(ctx, "MKCOL", dirURL+"/", nil)
		if err != nil {
			return err
		}
		httpReq2.SetBasicAuth(username, password)

		resp2, err := client.Do(httpReq2)
		if err != nil {
			return err
		}
		defer resp2.Body.Close()

		if resp2.StatusCode == http.StatusCreated ||
			resp2.StatusCode == http.StatusMethodNotAllowed ||
			(resp2.StatusCode >= 200 && resp2.StatusCode < 300) {
			return nil
		}
	}

	// 忽略目录创建失败，继续尝试上传
	return nil
}

// UploadWebDAVBackup 上传备份到 WebDAV
func UploadWebDAVBackup(c *gin.Context) {
	var req WebDAVUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.URL == "" {
		common.BadRequest(c, "WebDAV URL is required")
		return
	}

	var backupJSON []byte
	var err error

	// 如果启用了加密且有加密内容，使用前端传来的加密内容
	if req.EncryptedContent != "" {
		backupJSON = []byte(req.EncryptedContent)
	} else if req.EncryptionEnabled {
		// 启用了加密但没有加密内容，这是一个错误
		common.BadRequest(c, "Encryption enabled but no encrypted content provided")
		return
	} else {
		// 验证备份数据不为空（至少需要有版本号或提供商数据）
		if req.Data.Version == "" && len(req.Data.Providers) == 0 && len(req.Data.Models) == 0 {
			common.BadRequest(c, "Backup data is empty or invalid")
			return
		}

		// 如果版本号为空，设置默认版本号
		if req.Data.Version == "" {
			req.Data.Version = "1.0"
		}

		// 如果导出时间为空，设置当前时间
		if req.Data.ExportedAt.IsZero() {
			req.Data.ExportedAt = time.Now()
		}

		// 序列化备份数据
		backupJSON, err = json.MarshalIndent(req.Data, "", "  ")
		if err != nil {
			common.InternalServerError(c, "Failed to serialize backup data: "+err.Error())
			return
		}
	}

	// 确保 URL 格式正确
	baseURL := strings.TrimSuffix(req.URL, "/")
	webdavURL := baseURL + "/" + WebDAVBackupFileName

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// 先尝试确保目录存在
	_ = ensureWebDAVDirectory(ctx, baseURL, req.Username, req.Password)

	// 使用 PUT 方法上传文件
	httpReq, err := http.NewRequestWithContext(ctx, "PUT", webdavURL, bytes.NewReader(backupJSON))
	if err != nil {
		common.InternalServerError(c, "Failed to create request: "+err.Error())
		return
	}

	httpReq.SetBasicAuth(req.Username, req.Password)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		common.InternalServerError(c, "Upload failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		common.Unauthorized(c, "Authentication failed")
		return
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		// 读取响应体以获取更多错误信息
		body, _ := io.ReadAll(resp.Body)
		common.InternalServerError(c, fmt.Sprintf("Upload failed with status: %d, body: %s", resp.StatusCode, string(body)))
		return
	}

	common.Success(c, map[string]interface{}{
		"message":   "Backup uploaded successfully",
		"file_name": WebDAVBackupFileName,
		"size":      len(backupJSON),
	})
}

// DownloadWebDAVBackup 从 WebDAV 下载备份
func DownloadWebDAVBackup(c *gin.Context) {
	var req WebDAVDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.URL == "" {
		common.BadRequest(c, "WebDAV URL is required")
		return
	}

	// 确保 URL 以 / 结尾
	url := strings.TrimSuffix(req.URL, "/") + "/" + WebDAVBackupFileName

	// 使用 GET 方法下载文件
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		common.InternalServerError(c, "Failed to create request: "+err.Error())
		return
	}

	httpReq.SetBasicAuth(req.Username, req.Password)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		common.InternalServerError(c, "Download failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		common.Unauthorized(c, "Authentication failed")
		return
	}

	if resp.StatusCode == http.StatusNotFound {
		common.NotFound(c, "Backup file not found on WebDAV server")
		return
	}

	if resp.StatusCode != http.StatusOK {
		common.InternalServerError(c, fmt.Sprintf("Download failed with status: %d", resp.StatusCode))
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		common.InternalServerError(c, "Failed to read response: "+err.Error())
		return
	}

	// 尝试解析为普通备份数据
	var backup BackupData
	if err := json.Unmarshal(body, &backup); err == nil && backup.Version != "" {
		// 成功解析为普通备份
		common.Success(c, WebDAVDownloadResponse{
			Data:        &backup,
			IsEncrypted: false,
		})
		return
	}

	// 检查是否是加密的备份（通过检查 type 字段）
	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err == nil {
		if typeVal, ok := envelope["type"].(string); ok && typeVal == "llmio-webdav-backup-encrypted" {
			// 是加密的备份，返回原始内容让前端解密
			common.Success(c, WebDAVDownloadResponse{
				IsEncrypted: true,
				RawContent:  string(body),
			})
			return
		}
	}

	// 无法识别的格式
	common.InternalServerError(c, "Failed to parse backup data: unrecognized format")
}

// SyncNowWebDAV 立即同步 WebDAV
func SyncNowWebDAV(c *gin.Context) {
	var req WebDAVSyncNowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.URL == "" {
		common.BadRequest(c, "WebDAV URL is required")
		return
	}

	// 根据同步策略执行不同的操作
	switch req.SyncStrategy {
	case "upload_only":
		// 仅上传：导出本地数据并上传
		common.Success(c, map[string]interface{}{
			"action":  "upload",
			"message": "请使用上传功能完成同步",
		})
	case "download_only":
		// 仅下载：下载远程数据
		common.Success(c, map[string]interface{}{
			"action":  "download",
			"message": "请使用下载功能完成同步",
		})
	case "merge":
		// 双向合并：需要先下载再合并
		common.Success(c, map[string]interface{}{
			"action":  "merge",
			"message": "请先下载远程数据，然后手动合并",
		})
	default:
		common.BadRequest(c, "Invalid sync strategy")
	}
}