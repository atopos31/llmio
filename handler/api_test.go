package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupTestRouter() *gin.Engine {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Initialize a mock database
	models.Init(":memory:")

	// Create a test router
	router := gin.New()

	// Register routes for providers
	router.GET("/providers", GetProviders)
	router.POST("/providers", CreateProvider)
	router.PUT("/providers/:id", UpdateProvider)
	router.DELETE("/providers/:id", DeleteProvider)

	// Register routes for models
	router.GET("/models", GetModels)
	router.POST("/models", CreateModel)
	router.PUT("/models/:id", UpdateModel)
	router.DELETE("/models/:id", DeleteModel)

	// Register routes for model-provider associations
	router.GET("/model-providers", GetModelProviders)
	router.POST("/model-providers", CreateModelProvider)
	router.PUT("/model-providers/:id", UpdateModelProvider)
	router.DELETE("/model-providers/:id", DeleteModelProvider)

	// Register routes for system operations
	router.GET("/system/status", GetSystemStatus)
	router.GET("/system/metrics", GetProviderMetrics)
	router.GET("/system/logs", GetRequestLogs)
	router.GET("/system/config", GetSystemConfig)
	router.PUT("/system/config", UpdateSystemConfig)

	return router
}

func TestProviderCRUD(t *testing.T) {
	router := setupTestRouter()

	// Test CreateProvider
	t.Run("CreateProvider", func(t *testing.T) {
		providerReq := ProviderRequest{
			Name:   "OpenAI",
			Type:   "openai",
			Config: `{"base_url": "https://api.openai.com", "api_key": "sk-..."}`,
		}

		jsonValue, _ := json.Marshal(providerReq)
		req, _ := http.NewRequest("POST", "/providers", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})

	// Test GetProviders
	t.Run("GetProviders", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/providers", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})

	// Test UpdateProvider
	t.Run("UpdateProvider", func(t *testing.T) {
		// First create a provider to update
		providerReq := ProviderRequest{
			Name:   "Anthropic",
			Type:   "anthropic",
			Config: `{"base_url": "https://api.anthropic.com", "api_key": "sk-..."}`,
		}

		jsonValue, _ := json.Marshal(providerReq)
		req, _ := http.NewRequest("POST", "/providers", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Now update the provider
		updateReq := ProviderRequest{
			Name:   "Anthropic Updated",
			Type:   "anthropic",
			Config: `{"base_url": "https://api.anthropic.com", "api_key": "sk-updated..."}`,
		}

		jsonValue, _ = json.Marshal(updateReq)
		req, _ = http.NewRequest("PUT", "/providers/2", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})

	// Test DeleteProvider
	t.Run("DeleteProvider", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/providers/1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})

	// Test DeleteNonExistentProvider
	t.Run("DeleteNonExistentProvider", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/providers/999999", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 404, response.Code)
		assert.Equal(t, "Provider not found", response.Message)
	})
}

func TestModelCRUD(t *testing.T) {
	router := setupTestRouter()

	// Test CreateModel
	t.Run("CreateModel", func(t *testing.T) {
		modelReq := ModelRequest{
			Name:   "gpt-4",
			Remark: "OpenAI's latest model",
		}

		jsonValue, _ := json.Marshal(modelReq)
		req, _ := http.NewRequest("POST", "/models", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})

	// Test GetModels
	t.Run("GetModels", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/models", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})

	// Test UpdateModel
	t.Run("UpdateModel", func(t *testing.T) {
		// First create a model to update
		modelReq := ModelRequest{
			Name:   "claude-2",
			Remark: "Anthropic's model",
		}

		jsonValue, _ := json.Marshal(modelReq)
		req, _ := http.NewRequest("POST", "/models", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Now update the model
		updateReq := ModelRequest{
			Name:   "claude-2.1",
			Remark: "Anthropic's improved model",
		}

		jsonValue, _ = json.Marshal(updateReq)
		req, _ = http.NewRequest("PUT", "/models/2", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})

	// Test DeleteModel
	t.Run("DeleteModel", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/models/1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})
}

func TestModelProviderCRUD(t *testing.T) {
	router := setupTestRouter()

	// First create a provider and a model for testing associations
	providerReq := ProviderRequest{
		Name:   "OpenAI",
		Type:   "openai",
		Config: `{"base_url": "https://api.openai.com", "api_key": "sk-..."}`,
	}

	jsonValue, _ := json.Marshal(providerReq)
	req, _ := http.NewRequest("POST", "/providers", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	modelReq := ModelRequest{
		Name:   "gpt-4",
		Remark: "OpenAI's latest model",
	}

	jsonValue, _ = json.Marshal(modelReq)
	req, _ = http.NewRequest("POST", "/models", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Test CreateModelProvider
	t.Run("CreateModelProvider", func(t *testing.T) {
		mpReq := ModelWithProviderRequest{
			ModelID:      1,
			ProviderName: "OpenAI",
			ProviderID:   1,
			Weight:       10,
		}

		jsonValue, _ := json.Marshal(mpReq)
		req, _ := http.NewRequest("POST", "/model-providers", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})

	// Test GetModelProviders
	t.Run("GetModelProviders", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/model-providers?model_id=1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})

	// Test UpdateModelProvider
	t.Run("UpdateModelProvider", func(t *testing.T) {
		// Create another provider for update test
		providerReq := ProviderRequest{
			Name:   "Anthropic",
			Type:   "anthropic",
			Config: `{"base_url": "https://api.anthropic.com", "api_key": "sk-..."}`,
		}

		jsonValue, _ := json.Marshal(providerReq)
		req, _ := http.NewRequest("POST", "/providers", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Update the model-provider association
		updateReq := ModelWithProviderRequest{
			ModelID:      1,
			ProviderName: "Anthropic",
			ProviderID:   2,
			Weight:       5,
		}

		jsonValue, _ = json.Marshal(updateReq)
		req, _ = http.NewRequest("PUT", "/model-providers/1", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})

	// Test DeleteModelProvider
	t.Run("DeleteModelProvider", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/model-providers/1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
	})
}

func TestSystemEndpoints(t *testing.T) {
	router := setupTestRouter()

	// Test GetSystemStatus
	t.Run("GetSystemStatus", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/system/status", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})

	// Test GetProviderMetrics
	t.Run("GetProviderMetrics", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/system/metrics", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})

	// Test GetRequestLogs
	t.Run("GetRequestLogs", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/system/logs", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})

	// Test GetSystemConfig
	t.Run("GetSystemConfig", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/system/config", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})

	// Test UpdateSystemConfig
	t.Run("UpdateSystemConfig", func(t *testing.T) {
		configReq := SystemConfigRequest{
			EnableSmartRouting:  false,
			SuccessRateWeight:   0.8,
			ResponseTimeWeight:  0.2,
			DecayThresholdHours: 48,
			MinWeight:           2,
		}

		jsonValue, _ := json.Marshal(configReq)
		req, _ := http.NewRequest("PUT", "/system/config", bytes.NewBuffer(jsonValue))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response common.Response
		json.Unmarshal(w.Body.Bytes(), &response)
		assert.Equal(t, 0, response.Code)
		assert.Equal(t, "success", response.Message)
		assert.NotNil(t, response.Data)
	})
}
